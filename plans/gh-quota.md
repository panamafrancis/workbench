# GitHub quota: one delta sync, one safe cache

> **Status (2026-09-10): implemented, with one layer dropped.** Layers 0, 1 and 3
> landed in `d8dc4e7`, `f7c89b7` and `24a9a1f`, along with the budget ledger.
> **Layer 2 (the batched GraphQL sweep) was dropped on measurement** — see
> "Layer 2: dropped" below. Measured on the real setup (14 repos / 67 branches):
> a cold round costs 15 requests, every round after that costs **0**.

Measured on 2026-09-09 against the live API. Every number below is observed, not estimated.

## What I measured

| probe | result |
|---|---|
| `gh api graphql -f query='{viewer{login}}' -i` while "rate limited" | `X-Ratelimit-Resource: graphql`, `Used: 5001`, `Remaining: 0`, `Reset: 17:18:55` |
| `gh api rate_limit` at that same instant | `graphql: {used: 0, remaining: 5000}` — **wrong** |
| `gh pr list --head <b> --state all` (`GH_DEBUG=api`) | one `POST /graphql`, **1 point** |
| aliased batch: 9 branches across 3 repos, one request | **1 point**, definitive `NO PR` answers included |
| `search(query:"org:fraud-zero is:pr updated:>=… sort:updated-desc", first:100)` | **1 point**, 43 PRs with `headRefName` + `repository.nameWithOwner` |
| same search scoped `repo:a/b repo:c/d` (multi-owner OR) | **1 point**, exact scoping |
| `GET /repos/{o}/{r}/pulls?state=all&sort=updated&per_page=100` | **1 core point**, 100 PRs with `head.ref`, `state`, `draft` |
| same request with `If-None-Match` (×3) | **0 points** — `304`, `X-Ratelimit-Used` never moved |

Two conclusions that reshape the design:

1. **`gh api rate_limit` cannot be used for anything.** Its GraphQL row said "5000 remaining"
   while the bucket was at `used: 5001`. The only truthful sources are the `rateLimit { }`
   field inside a query and the `X-Ratelimit-*` headers on the response (note: a
   GraphQL rate-limit rejection arrives as **HTTP 200** with an `errors[].type: "RATE_LIMIT"`
   body).
2. **The answer to "what is the PR status of my 205 branches?" costs 1 point, not 142.**
   Per-branch polling is not a thing we need to optimise; it is a thing we need to delete.

Current footprint: 105 supatree + 100 workbench cache entries, 142 of them non-terminal
→ one `gh pr list` process each per round → **~142 points per round, ~850/hour**, of which
125 branches are `none` (pushed, no PR) and never change.

## Root cause 1 — the cache is a last-writer-wins blob, so the cooldown never holds

This, not volume, is why the sidebars sit in a rate-limited state and stay there.

`Cache.Save()` marshals the writer's **entire in-memory snapshot** over the file. A gopls
census of the call sites (`findReferences` on `Cache.Save`) — 8 production sites, only 3
of them under `TryFileLock`:

| site | holds the lock? | window between `Load` and `Save` |
|---|---|---|
| `pkg/tui/model.go:1065/1071/1079` (fetch round) | yes | — |
| `pkg/supatree/tui/update.go:474/480/488` (fetch round) | yes | — |
| `pkg/tui/model.go:1128` (`fetchIfUncached`, on cursor move) | **no** | one `gh` call |
| `pkg/supatree/mcp.go:256` (`pr_status`) | **no** | N `gh` calls, seconds |
| `pkg/supatree/rename.go:51` | **no** | git rename + push per member |
| `pkg/supatree/remove.go:58` | **no** | cleanup scripts + worktree removal |
| `cmd/rename_branch.go:101` | **no** | short |

Every unlocked `Save` silently reverts whatever another process wrote since that process
last called `Load` — **including `retry_after`**. So:

- An agent running `supatree pr_status` loads the cache, spends N failing `gh` calls,
  and saves — erasing the cooldown a sidebar just armed. All 26 tabs immediately resume
  hammering a bucket that is at zero. `handlePRStatus` doesn't even check
  `IsRateLimited`, so it never arms one itself.
- Moving the cursor in any sidebar can do the same thing.
- Reverted entries look uncached, so the next round re-asks GitHub for them. The loss
  compounds with tab count.

The irony is that `withConfigLock` in `pkg/config/lock.go` documents this exact hazard
("the later Save resurrects an entry the other removed") — the discipline exists for
`config.yml` and was never applied to the PR cache. The type's API is the bug: it hands
callers `Load`/`Set`/`Save` and *requires* them to remember an external flock.

## Root cause 2 — per-branch granularity

142 requests to answer a question that one request answers. Everything built to soften
that (`HasRemoteBranch`'s per-branch `git rev-parse`, `KnowsPR`, `TerminalMaxAge`,
`activeMaxAge`/`visibleMaxAge` tiers, fetch-on-cursor-move, the try-lock leader election
per round) is compensation for the granularity, and most of it can be deleted.

## Root cause 3 — no budget, no timeout, string-matched errors

- The bucket is shared with every agent in every worktree (`gh pr view`, `gh pr checks`,
  MCP `create_pr`) and the interactive shell. The sidebars are a background nicety, yet
  nothing stops them from spending the last point out from under an agent opening a PR.
- `github.LookupPR` runs `exec.CommandContext(context.Background(), …)` — **no timeout** —
  *inside* the flock. One hung `gh` blocks every other tab's fetch round indefinitely and
  pins `m.fetching` forever. The repo already has both idioms to fix this:
  `pkg/zellij/client.go`'s 30s `cmdTimeout` + circuit breaker, and `mcp.ToolContext()`.
- Rate-limit detection is `strings.Contains(stderr, "rate limit")`. Structured detection
  (`errors[].type == "RATE_LIMIT"`) plus the exact `X-Ratelimit-Reset` is available and
  free.
- `rateLimitCooldown` is a flat 15-minute guess when the response carries the real reset.

## Design

Four layers, cheapest first. Each is useful alone; together the steady state costs nothing.

### Layer 0 — one cache, one safe mutation API (the live bug)

Merge the two cache files into one — the buckets are per-token, not per-tool — and key
entries by `owner/repo#branch` (branch-only keys are a latent cross-repo collision, and
the poll below hands us the repo anyway).

Collapse the API to what cannot be misused:

```go
func Open(path string) (*Cache, error)          // read-only snapshot, no lock
func (c *Cache) Get(key Key) *PRInfo            // render path — free, never blocks
func (c *Cache) Budget() Budget                 // last observed quota state

// The only writer. Flocks, re-reads from disk, applies fn to the fresh state,
// merges per entry, writes atomically, releases.
func Mutate(path string, fn func(*Writable) error) error
```

`Save()` leaves the exported surface, so lost updates become unrepresentable instead of a
rule call sites must remember. Merge is per entry (newest `FetchedAt` wins).
`retry_after`, the ETag map and the budget observation are **monotonic** — a stale process
physically cannot un-arm a cooldown.

### Layer 1 — conditional per-repo polling: free while nothing changes

This is the primary mechanism, and it is the one thing that makes the cost structurally
zero rather than merely small.

```
GET /repos/{owner}/{repo}/pulls?state=all&sort=updated&direction=desc&per_page=100
If-None-Match: <stored etag>
```

- One request per **repo**, not per branch: ~10 requests covers all 205 branches.
- Returns `number`, `state`, `draft`, `head.ref`, `title`, `html_url`, `updated_at` —
  everything the sidebar renders — for every branch in that repo, so absence is
  authoritative too.
- Unchanged → `304 Not Modified`, **cost 0** (measured: three repeats, `X-Ratelimit-Used`
  never moved). A quiet round costs literally nothing.
- Changed → 1 point, and only for the repo that changed.
- It spends from the **core** bucket. The GraphQL bucket that agents drain with
  `gh pr view` / `gh pr checks` / `gh pr list` is no longer touched by background
  polling at all, so the two workloads stop competing for the same 5,000.

ETags live in the shared cache file next to the entries, so whichever process is leader
for a round revalidates with the ETag its predecessor stored. Page-1-of-100 sorted by
`updated` covers weeks of activity in these repos; a full page walk happens only on a cold
cache, and terminal statuses are already retained permanently.

### Layer 2 — batched GraphQL, for cold start and new branches

Keep the aliased query as the *reconcile* path, not the steady-state path:

```graphql
r0: repository(owner: "…", name: "…") {
  b0: pullRequests(headRefName: "…", first: 1, orderBy: {field: UPDATED_AT, direction: DESC}) { nodes { … } }
  b1: …   # aliases generated; branch names live in arguments
}
```

Measured **1 point for 9 branches across 3 repos** (cost is `ceil(nodes/100)`, so all 142
≈ 2 points), chunked at ~50 aliases. Runs on a cold cache, for a brand-new branch, and on
a forced refresh. The org-wide delta search (`search(query:"…is:pr updated:>=<watermark>…")`,
also **1 point**, 43 PRs with `headRefName`) stays available as a single-request fallback
when per-repo ETags are useless — e.g. after a long sidebar outage.

### Layer 3 — stop asking questions we already know the answer to

- **Write-through on creation.** `create_pr` / `create_prs` already parse `gh pr create`
  output; write the resulting number and URL straight into the cache. This kills the
  polling burst at the exact moment it is hottest — the user watching the sidebar for the
  badge to appear.
- **Push-triggered invalidation.** We own the pushes (`create_pr`, `sync`,
  `rename_branches`). Mark those branches for revalidation instead of sweeping everything.
- **Bound the working set.** Poll only repos with a worktree or tree touched in the last N
  days. Most of the 205 branches are dead history.

### Render/sync split and the budget ledger

Every sidebar renders from the cache file on its own tick — free, never blocks, no
network. Syncing is one leader per host per round (existing `TryFileLock`), adaptive
cadence (60s while things change, backing off to ~5 min when rounds come back 304), reset
by any user action. One `github.Sync(ctx, targets, mode)` entry point in `pkg/github`
replaces the ~90-line fetch loop duplicated in `pkg/tui/model.go` and
`pkg/supatree/tui/update.go` (which AGENTS.md currently tells us to keep mirrored by
hand). `gh` invocation gets a timeout and the `pkg/zellij` breaker idiom.

The budget ledger tracks **both** buckets from `X-Ratelimit-*` / `rateLimit{}` (never from
`gh api rate_limit`, which lies) and gates callers:

| caller | proceeds while |
|---|---|
| background poll / sweep | `remaining > backgroundReserve` (default 1000) |
| forced refresh, MCP `pr_status`, `create_pr*` | `remaining > forcedReserve` (default 100) |

Free 304s are not gated — there is nothing to spend.

## Alternatives considered

**Centralising the fetcher.** Adopted, but as centralised *state and policy*, not a new
process: leader-election per round plus a shared ETag/budget store. A real daemon
(`workbench sync --daemon` under launchd) would additionally let us honour
`X-Poll-Interval` and hold ETags in memory, but it buys no quota saving over the
flock-elected leader and adds lifecycle, staleness and cleanup problems — the same reason
we don't ship one today. Revisit only if we ever want push-style latency.

**Only query on demand (user presses a key).** Rejected as the *primary* mechanism, kept
as an escape hatch. The sidebar's whole value is the passive at-a-glance PR colour across
26 tabs; make it on-demand and it is stale by default, which is how people stop trusting
it. On-demand is the right answer only for the operations that genuinely cost something —
the cold-start sweep and forced refresh — and with Layer 1 at zero cost there is no quota
argument left for it. Worth adding regardless: a "refresh this row now" key, which is one
conditional request.

**Batching.** Adopted as Layer 2, and note Layer 1 is itself batching done better —
batched by repo, with a free-when-unchanged property that per-branch batching cannot have.

**A second identity for background polling.** The `panamafrancis` account has its own
5,000/hr buckets and legitimate access to these repos, and `GH_TOKEN` scoping is already
the pattern in this setup. Scoping the sync path to it would isolate background polling
from interactive work completely. Offered, not defaulted — after Layer 1 the background
path costs ~0, so this is insurance rather than a fix, and it doubles the auth surface.

**Webhooks / event-driven.** No public endpoint on a laptop, so out. The notifications
API is also conditional-and-free and carries `X-Poll-Interval`, but per-repo `pulls`
ETags already give us the change signal more directly.

## What gets deleted

`HasRemoteBranch`'s per-round `git rev-parse` per branch (142 processes), `KnowsPR`,
`TerminalMaxAge`, the `activeMaxAge`/`visibleMaxAge` tiers, `fetchIfUncached`,
`fetchStaleCmd`, the duplicated fetch loop in both TUIs, and the second cache file.

## Cost, before and after

| | now | after |
|---|---|---|
| requests per quiet round | 142 (all charged) | ~10 (all 304, **free**) |
| points per quiet round | 142 | **0** |
| points per round with activity | 142 | 1 per changed repo |
| background points/hour | ~850 | ~0–60 |
| bucket used | graphql (shared with agents) | core (agents untouched) |
| scales with worktree count | yes | no |
| cooldown survives a concurrent writer | no (blob-save wipes it) | yes (monotonic, merged) |
| can background polling starve an agent | yes | no |
| recovery after a limit | flat 15m guess | exact `X-Ratelimit-Reset` |
| a hung `gh` wedges all sidebars | yes | no (timeout + breaker) |

## Correctness fixes this surfaces

- `depsWithoutPRs` (`pkg/supatree/mcp.go:269`) treats **any** error as "no PR":
  `if err != nil || info == nil || info.Status == PRNone`. A rate-limit error therefore
  reads as "this dependency has no PR yet", so `create_pr` refuses to stack — or with
  `force`, reports a dependency graph that isn't real. Absence and failure must be
  distinct.
- `handlePRStatus` ignores the cache, never detects a rate limit, never arms a cooldown,
  and blob-saves over one.
- `fetchIfUncached` does unlocked network I/O on cursor movement; it disappears once
  Layer 1 makes every answer authoritative.
- `LookupPR` has no timeout and runs inside the flock.

## Implementation order

1. **Cache integrity** — `Mutate`-only API, per-entry merge, monotonic
   `retry_after`/ETags/budget, `Save` unexported; convert the 5 unlocked call sites.
   Tests: concurrent `Mutate` loses nothing; a stale writer cannot clear a cooldown.
   This is the live bug and lands independently of everything below.
2. **`ghREST`/`ghGraphQL` wrappers** — timeout, breaker, structured `RATE_LIMIT`
   detection, `X-Ratelimit-*` → `Budget`, 304 handling. Tests over recorded responses.
3. **Layer 1** — per-repo conditional poll, ETag store, response → cache deltas,
   authoritative absence. Tests over recorded 200 + 304 pairs.
4. **Budget ledger + `Allow(mode)`**, exact-reset cooldown.
5. **Layer 2** — aliased sweep (golden document, chunking) + delta-search fallback.
6. **Layer 3** — write-through on `create_pr`, push-triggered invalidation, working-set
   bound.
7. **`github.Sync`** + adaptive leader cadence; rewrite both TUIs to render-from-cache and
   delete their fetch loops; move `pr_status` / `create_prs` / `depsWithoutPRs` onto it.
8. `make ci`, both e2e scripts, then AGENTS.md ("GitHub quota discipline" — replace the
   "mirror this exactly" instruction) and README.md (`gh_background_reserve`, single cache
   path).

Cache, ETags and watermark are derived data, so no migration: delete the old files and let
the first round repopulate.

## Verification

- Per-round debug log of bucket, cost and status: a quiet round must show all 304s and
  zero points.
- Watch `X-Ratelimit-Used` on both buckets for an hour with all tabs open plus agent
  activity; graphql consumption from the sidebars should be zero.
- Race test: run `supatree pr_status` while a sidebar holds an armed cooldown and assert
  it is still armed afterwards — the exact failure happening now.
- Set `remaining` low in the ledger and confirm sidebars keep rendering, show the reserve
  hint, and still allow a forced refresh down to the hard floor.

## Layer 2: dropped

The batched aliased GraphQL query was meant to answer "which of my branches have
*no* PR" cheaply — 1 point for ~50 branches, against one point *per branch* for
`gh pr list`. Two things learned while building Layer 1 removed its reason to
exist:

1. **Absence is usually free already.** A repo whose listing is not truncated
   proves absence for every branch in it as a side effect of the poll, at no
   cost. Only truncated repos need to ask about a specific branch.
2. **The per-branch fallback moved to REST.** `LookupBranchPR`
   (`GET /pulls?head=owner:branch`) answers definitively for one core point, and
   in the real setup a cold round needed **2 of them** — most branches were
   either covered by the listing or unpushed, and an unpushed branch is never
   asked about at all.

So Layer 2 would spend the *contended* GraphQL bucket — the one that is
routinely exhausted while core sits at 5,000 — to save a couple of requests from
the bucket we have in abundance. That is the wrong trade, and it is worth
recording that the measurement, not the design, decided it.

If a cold start ever does need to resolve hundreds of branches at once, the
cheaper move is to page the repo listing to completion (core bucket, ETag-able)
rather than to reach for GraphQL.

## What the implementation added beyond the plan

Things the design did not anticipate, each found by measuring or by a test:

- **Group by repo, not by checkout.** Supatree gives every tree its own checkout
  of the same repo, so the first implementation polled `admin-frontend` six
  times a round: 61 requests instead of 13, and 50-second rounds. The extra ones
  were 304s and cost no quota, but they were still round trips.
- **A 304 says "unchanged", not "complete".** `RepoState.Truncated` has to
  persist across rounds, or a not-modified poll reads as proof that a branch has
  no PR.
- **`gh api -i` exits non-zero on 304 and 404.** The status has to be classified
  from stdout, with the exit code only as a fallback (`errNoResponse`).
  Otherwise every free 304 becomes a re-fetch — the exact opposite of the point.
- **Secondary rate limits look nothing like primary ones.** They arrive with
  `Retry-After` and a message, no quota headers, and thousands of quota left.
- **Repos the account cannot see.** `PiwikPRO/fraud0_api_contract` 404s for this
  token; it now gets recorded `Unavailable` and skipped instead of costing a
  request and painting an error every round.
