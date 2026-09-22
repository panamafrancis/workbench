# Supatree PM agent — notifications, roster, and cross-tree orchestration

## Goal

A standing "PM" that can manage supatrees on your behalf: create new ones, read
what reviewers said, talk to the agents already working in a tree, and tell you
only the things worth interrupting you for.

Delivered as eight milestones, with the PM itself as an **optional
consumer** — if it is not running, supatree is exactly what it is today plus
better notifications.

The numbers are *not* the build order, and pretending otherwise will cost a
refactor. M1 and M4 stand alone. M7's transport seam must exist before M2 writes
its first call site. M8 needs M3 (something to consume a request) and wants M7
(headless delivery when no PM is running). So: **M1 → M7 seam → M2 → M3 → M5 →
M8**, with M4 and M6 wherever they fit.

## What already exists (do not rebuild it)

Claude Code ships a local cross-session message bus. Every session exports
`CLAUDE_CODE_MESSAGING_SOCKET=/tmp/cc-socks/<pid>.sock`, discovers its peers,
and messages them by name. Verified on this machine (claude 2.1.261,
2026-09-20): a sandboxed session inside a workbench worktree listed and could
address the seven other agents running in unrelated worktrees. **It works from
inside nono.**

So "communicate with agents already active in a supatree" is not a protocol
problem. It is a *naming* problem: bus names are derived from the session's cwd,
and every agent in one supatree shares `~/.supatree/trees/<name>/`, so they all
collide. Supatree's contribution is to make agents addressable, and nothing
more — never parse the socket, never speak the wire protocol. It is
undocumented, version-coupled, and absent for non-Claude models.

The counterpart constraint, which shapes the whole design:

> **A Go process cannot use the bus.** The sidebar, the dashboard and the
> watcher are Go. Only a Claude session can send a bus message.

Hence three channels rather than one:

| Direction | Channel | Why |
| --- | --- | --- |
| anything → PM | `~/.supatree/requests.jsonl`, read from a stored offset each turn; `Monitor` where it exists | Go can append to a file; Claude also gets woken per line |
| PM → agent | mailbox file, with the bus as a wake-up where available (M7) | portable by default; Claude gets lower latency |
| PM / watcher → you | PM's own tab, `board.md`, desktop notification | the only three things a human reads |

## Non-goals

- No second GitHub poller. Everything goes through `FetchTargets`/`FetchPRs`
  (see Quota, below).
- The PM never writes member code. That is the agents' job, and it keeps the
  blast radius of a confused PM to its own state directory.
- No PM TUI. The PM is a chat; the sidebar and dash stay views.
- No reimplementation of the message bus in Go.
- No vector or graph database. At the volume this system produces (tens to low
  hundreds of closed trees a year), an index file and grep beat embedding
  retrieval on precision and debuggability alike. See M6 for the kill criterion
  that would justify revisiting it.

---

## M1 — Events, watcher, notifications

Standalone: ships real value with no agent involvement at all.

`Status()` is already a pure function from on-disk state to a `Summary`, with
JSON tags throughout. So events come from diffing consecutive summaries rather
than from any new data collection.

**`pkg/supatree/events.go`** (new)

```go
type EventKind string // pr_opened, changes_requested, checks_failed,
                      // approved, merged, tree_done, stale, conflict
type Event struct {
    At     time.Time `json:"at"`
    Kind   EventKind `json:"kind"`
    Tree   string    `json:"tree"`
    Member string    `json:"member,omitempty"`
    PR     int       `json:"pr,omitempty"`
    Text   string    `json:"text"`
}

func Diff(prev, cur Summary) []Event   // pure, table-testable
func AppendEvents(evs []Event) error   // O_APPEND under EventsLockPath()
func ReadEvents(since time.Time) ([]Event, error)
```

Keep `Diff` pure over two `Summary` values — same discipline as `memberState`
and `rollup`, and the reason those are the parts with tests.

**`pkg/supatree/paths.go`**: `EventsPath()` (`~/.supatree/events.jsonl`),
`LastSummaryPath()` (`~/.supatree/cache/last-status.json`), `WatchLockPath()`,
`RequestsPath()`, `NotifyPath()` — plus `SchedulePath()` and
`ScheduleStatePath()` when M8 lands.

**`supacmd/watch.go`** (new) — `supatree watch`

- Singleton via `config.TryFileLock(WatchLockPath())` — the same primitive that
  keeps per-tab sidebars from stampeding gh. Note the lock is held for the whole
  of `fn`, so the daemon holds it for its entire life: a `--once` run (cron, e2e)
  gets `ErrLockBusy` while the daemon is up and **must exit 0 quietly**. That is
  the desired behaviour — it is what stops a cron entry double-fetching — but it
  has to be deliberate, not an error path.
- Loop: `FetchTargets` → `FetchPRs` → `Status` → `Diff` against
  `last-status.json` → `AppendEvents` → notify → persist the new summary.
- `--once` for cron and for the e2e script.
- **First run seeds the baseline silently.** With no `last-status.json`, `Diff`
  against the zero `Summary` emits a full set of events for every tree and
  notifies about all of them. Write the baseline and emit nothing.
- **Flappable states need dwell time.** `checks_failed` oscillates as CI
  re-runs, and `stale` flips back and forth across its threshold. The dedupe
  rule below suppresses *repeats*, not `fail → pass → fail`; require N
  consecutive observations before notifying on those two kinds.

**Launch and lifetime** — the fiddly part, and the reason this gets its own
paragraph. `supacmd/start.go` ends in `syscall.Exec`, which *replaces* the
process with zellij: nothing written after that line ever runs. So the watcher
must be spawned **before** the exec, detached (`Setsid`), with its stdio
redirected to `LogsDir()`. And because its parent is then gone, it needs its own
exit condition: poll `zellij.ListSessions()` and exit when no `st-` session
remains. Without that it outlives every session and polls GitHub forever.

That makes the daemon **session-scoped**, which is right for polling — nobody
needs PR status refreshed for a screen that is not open — but it is exactly what
M8's overnight jobs must not depend on. Unattended firing is therefore the
`--once` path under launchd or cron, not a longer-lived daemon: the singleton
lock already makes that entry a no-op while a session is up, and the real thing
when none is. `--once` must run the scheduler tick as well as the poll.

It runs *outside* nono — it is supatree's own code and needs `osascript`, which
the sandbox denies — and therefore also **outside any zellij session**. That
matters for the focused-tab suppression below: bare `zellij action` has no
session to target from there, so the watcher must record the session name at
spawn and call `zellij --session <name> action dump-layout`.

**Notification policy.** The tier table is the deliverable here — getting it
wrong is what makes people turn the feature off.

| Tier | Events | Surface |
| --- | --- | --- |
| Glyph | pushed, pr_opened, checks passed, agent idle | sidebar row only |
| Board | task finished, approved, tree_done | `board.md`, no interrupt |
| Desktop | changes_requested, checks_failed, conflict, agent blocked on a human decision | OS notification |
| Never | per-commit, per-poll, anything the previous round already said | — |

Two rules carry more weight than the tiers:

1. **Dedupe on `(tree, member, kind)` with a cooldown.** Note *why*, because
   the obvious reason is wrong: a persisted baseline makes `Diff` edge-triggered,
   so a check that is still failing emits nothing on the next tick. Dedupe is
   there for the two cases that defeat that — a watcher restart against a missing
   or stale `last-status.json`, and genuine flapping. Implement it as a cooldown
   on top of edge-triggering, not as a substitute for it.
2. **Suppress the desktop tier for the currently focused tab's tree.** You are
   already looking at it. Best effort via `zellij --session <name> action
   dump-layout` (`focus=true`) — note the explicit `--session`, per the launch
   paragraph above. When the focused tab cannot be resolved, do **not**
   suppress: a missed suppression is a minor annoyance, a missed notification is
   a bug.

Notification delivery is a config knob (`notify_command` in
`~/.supatree/config.yml`), defaulting to `osascript -e 'display notification'`,
so Linux can point it at `notify-send` without a code change.

**The watcher is the only process that can notify**, because it is the only one
outside nono (see Risks). So anything else that wants to reach the desktop — the
PM summarising a standup, most of M8 — needs a route, and it is not a second
notifier. The watcher's notify stage drains `~/.supatree/notify.jsonl` alongside
its own diffed events, and MCP `notify(text, tier)` is how the PM appends to it.
One delivery point, one tier table. Crucially the PM's messages go through the
*same* dedupe and focus suppression: an outbox that bypasses the policy is a
notification bypass, and it will be used as one.

**Views**: sidebar tree rows gain an attention marker for unacted events
(alongside the existing `prCounts` badge, and lifted onto the folded tree row
the same way); `pkg/supatree/dash` gains `e` to toggle an events feed pane.

Also: `README.md` (new command, new config key), `AGENTS.md` (the watcher is a
new singleton process with a lock — it belongs in the quota section),
`scripts/e2e-supatree.sh` (`watch --once` over a fixture).

---

## M2 — Addressable agents

The prerequisite for anything talking to anything.

**`pkg/config/config.go`**: `Model` gains

```go
AgentNameArgs []string `yaml:"agent_name_args,omitempty"` // claude: ["--name","{agent_name}"]
```

**`pkg/sandbox/nono.go`**: generalize `substituteSession` into a token
substituter over `{session_id}` and `{agent_name}`; `BuildAgentNonoArgs` takes
the agent name and appends `AgentNameArgs`. Existing callers pass `""` and are
unaffected.

**`pkg/supatree/agents.go`**: `Agent` gains `Address string`. `EnsureAgent`
computes it as `st-<tree>-<agent>` — deliberately *not* the `<tree>:<agent>`
zellij tab identity, because a colon in a bus name is asking for trouble and the
two namespaces should be free to diverge.

✅ **Verified 2026-09-20** (claude 2.1.261). `--name` sets the *bus* address, not
merely a display name, and it is used **verbatim**: `st-probe` appeared in
`ListAgents` as exactly `st-probe`, while every directory-derived name in the
same listing carried a two-character suffix (`agentmode-8c`, `calgary-1d`).

That kills the rule this section used to carry. **Addresses match exactly; there
is no prefix correlation to do.** The suffix is how the bus disambiguates names
it derived itself, and an explicit name opts out of it — so `st-<tree>-<agent>`
is both the stored address and the live one, and the "two trees with a `main`"
collision is solved by the address scheme rather than by fuzzy matching. Storing
a prefix and correlating it would have been strictly worse: `st-canberra-main`
is a prefix of `st-canberra-main-2`.

The address is persisted on the `Agent` rather than derived at read time,
because a supatree renamed after an agent launched cannot rename the running
process — the stored value is what that agent is actually answering to.

> **Build M7's transport seam before this, not after.** The bus is one
> implementation of "deliver a message to an agent", and it is the Claude-only
> one. Writing `SendMessage` directly into the PM's instructions here means
> retrofitting the abstraction later, through every call site.

---

## M3 — The PM agent

**Home.** `~/.supatree/pm/` — its own root, not a tree, because a PM managing
many supatrees cannot be rooted in one of them. Tab name `supatree-pm`, reserved
the way `DashTab` is (`pkg/supatree/paths.go`), so it can never collide with a
tree name.

**Sandbox — a different profile, not a bigger one.** The PM's threat model is
close to the inverse of a coding agent's, so it gets its own nono profile. That
costs no code: `nono_profile` hangs off the `models` entry
(`sandbox.BuildNonoArgs`), and `models` is an open map, so a `claude-pm` entry
pointing the same binary at a `supatree-pm` profile is the intended extension
point.

| | coding agent | PM |
| --- | --- | --- |
| executes | untrusted third-party code | nothing — MCP calls and file I/O |
| input | mostly your own repo | **every PR comment** — attacker-authored |
| credentials | gh, scoped to its own branch | gh, spanning every repo and every tree |
| blast radius | a worktree; `git checkout -- .` | other people's inboxes; a public comment you cannot unsay |
| needs | registries, arbitrary fetches | a fixed handful of API endpoints |

Read that table twice before concluding the PM is the safer of the two. It is
the one holding the lethal trifecta — private data, untrusted content, and a way
to send — and the coding agent mostly lacks the middle term: a third-party
library is untrusted *code*, but it is not composing sentences aimed at your
agent. `gh` alone supplies the third leg; a PR comment is published, and a closed
PR is visible to everyone watching the repo. What *is* true is that the PM's risk
sits in network and credentials rather than in execution, so that is where the
profile must be strict, and it is exactly where nono's filesystem-shaped controls
do nothing for you.

**Scope the credentials, not the binary.** The high-value control is
`--credential <service>`, which injects the token through nono's reverse proxy so
it never enters the agent's environment, combined with `--allow-endpoint
SERVICE:METHOD:/path` to pin it to the calls the PM actually makes:

```
--credential github  --allow-endpoint 'github:GET:/repos/*/*/pulls/**'
--allow-domain 'https://api.github.com/repos/<org>/**'
```

Which is enough, because **the PM as specified is a GitHub reader.** Every tool
in the table below is local — worktrees, `agents.yml`, the event ledger — and
M4's `pr_comments` is a GET. Nothing in M1–M4 needs a token that can write. So
pin it to GET now, while that is true and costs nothing, rather than discovering
later that the convenient token was always a write token.

That also gives M5's autonomy levels something better than good intentions
behind them: **`auto` is a different credential scope, not just a different
config value.** The one level that opens PRs and pushes branches gets a profile
whose allowlist includes those endpoints; `off` and `nudge` run under a
GET-only one. A prompt-injected PM at `nudge` then cannot comment on a PR
however convinced it is that it should — the proxy refuses the call.

Note what none of this relies on: `--allow-command`/`--block-command` are
deprecated and explicitly not child-process enforced, so you cannot stop the PM
shelling out to `curl`. You do not need to. Design for *code runs but reaches
nothing*, not for *code cannot run*.

**Filesystem.** The PM needs a wider grant than any agent today, and a *narrower*
write grant. The naive version — `--allow ~/.supatree/pm --read
~/.supatree/trees` — is wrong: the PM also has to write `board.md` into each
tree (M5) and retrospectives into the stack repo (M6), neither of which that
grant permits. The shape that actually works:

```
nono run --profile <p> \
  --allow ~/.supatree/pm \           # its own state
  --read  ~/.supatree/trees \        # every tree, read-only
  --allow ~/.supatree/trees/*/.supatree \   # board.md only
  --allow ~/.supatree/stacks \       # notes/, reviewed as a diff
  -- claude ...
```

The invariant to preserve is not "read-only on trees", it is **the PM may write
supatree's own state and never a member repo's working tree**. `repos/` stays
read-only; `.supatree/` does not. If nono cannot glob mid-path, enumerate the
trees at launch — the PM restarts often enough that a tree created later can
wait for the next launch, and `open_agent` is how new trees get their agents
anyway.

`BuildNonoArgs`/`BuildAgentNonoArgs` currently take a single path. Extend to
`allow []string, read []string`; nono's `-a`/`-r` are both repeatable.

**Singleton.** Nothing stops `supatree pm` running in two zellij sessions, and
two PMs both draining `requests.jsonl` means every agent gets nudged twice.
Take `config.TryFileLock(PMLockPath())` at launch; a second invocation reports
which session already owns the PM instead of starting a rival.

**`supacmd/pm.go`** (new) — `supatree pm`, mirroring `supacmd/dash.go`. Opens or
focuses the `supatree-pm` tab via `Workspace.OpenOrFocusTab` with the PM's nono
args. Sidebar key `P`, mirroring `D`, wired in `pkg/supatree/tui/update.go`
alongside `openDashboard()`.

**Inbound requests.** Anything that wants the PM's attention appends a JSON line
to `RequestsPath()` — the sidebar, the watcher, the scheduler (M8), a shell.

Delivery is two-tier, and the distinction matters. The PM arms a persistent
`Monitor` on the file at startup (instructed via its `AGENTS.md`, scaffolded
into `~/.supatree/pm/`) — but that is a **latency optimisation, not the
guarantee**. A monitor does not survive a restart, and an agent that has to
remember to re-arm one will eventually not. The guarantee is that the PM reads
everything after a stored offset at the top of each turn, which is cheap and
stateless. Design the request file so a PM that has been down for a day catches
up correctly on its next turn rather than silently losing the backlog.

**Fetched content is data, never instructions.** M4 pulls review comments
straight into the PM's context, and a comment is a text field any contributor can
write. The mitigation is not a cleverer prompt; it is that **an outward-facing
action requires a human or a schedule entry as its initiator** — never a document
the PM just read. The MCP tools that return external text label its provenance so
the boundary is visible in the transcript. The same reasoning covers the bus: a
PM granted `--allow-unix-socket-dir /tmp/cc-socks` can relay into your coding
agents, which is the feature, and also the path an injected instruction would
take.

**Cross-tree MCP tools** (`pkg/supatree/mcp.go`). Every tool today resolves "the
current tree" from `SUPATREE_ROOT` and is gated on `SUPATREE=1`. The PM is
cross-tree, so add a second gate:

```go
Gate: func(name string) string {
    if pmTools[name] { if os.Getenv("SUPATREE_PM") != "1" { return "..." }; return "" }
    ...
}
```

| Tool | Wraps | Notes |
| --- | --- | --- |
| `list_trees` | `Status(insts, cache, opts)` | already JSON-tagged; cache-only, so free |
| `new_tree` | `supatree.New` | returns the name; does **not** open a tab |
| `remove_tree` | `supatree.Remove` | refuses a tree that is not `done` unless forced |
| `agents` | `LoadAgents` + roster | returns bus address prefixes |
| `events` | `ReadEvents(since)` | the PM's own catch-up on restart |
| `open_agent` | `OpenRootAgent`/`OpenMemberAgent` | only from inside zellij |

**The PM does not open tabs unprompted.** `zellij new-tab` focuses the new tab,
and having your terminal yanked away by a background agent is intolerable. The
PM creates trees and seeds agents, then *reports*; the sidebar surfaces the new
tree on its next tick and you press enter yourself. Opening is a human verb.

---

## M4 — Review comments

**`pkg/github/comments.go`** (new): `PRComments(path string, number int)`
returning issue comments, review bodies, and **unresolved review threads**. The
resolution state only exists in GraphQL (`reviewThreads { isResolved }`), so
this is a second query shape, not another field on the existing `gh pr list`.

That makes it the single riskiest thing in this plan for the shared 5,000/hour
GraphQL budget, so:

- Comments are fetched **on demand only** — when the PM or a human asks. Never
  on the watcher's tick.
- Cached beside the PR status under the same lock, keyed on
  `(branch, pr, updated_at)`, so repeated asks about an unchanged PR are free.
- `IsRateLimited` arms the same persisted `SetRetryAfter` cooldown every other
  fetch path observes.

Surfaced as MCP `pr_comments(tree, repo)` and `supatree comments <tree> <repo>`.

---

## M5 — Board and autonomy

**The board.** Status must not mean "read the PM's chat log" — scrollback is a
terrible status display and people stop reading it by day three. The PM
maintains `.supatree/board.md` per tree: what each agent is on, what is blocked,
what is waiting on you. The dashboard renders it in a pane. Chat is where you
negotiate; the board is where you check.

`board.md` is gitignored per-tree state like `info.md`, and follows
`contextfile.go`'s convention — generated, never hand-edited.

**Autonomy.** An agent that silently pokes your other agents is unsettling the
first time it happens. Make it explicit, per-tree, in `meta.yml`:

| Level | The PM may |
| --- | --- |
| `off` | report only |
| `nudge` *(default)* | message agents; never create, delete, or push |
| `auto` | create trees, open PRs, reap done trees |

These are one axis and they need two. "Message a local agent" and "comment on a
PR" are different kinds of risk — one is private and recoverable, the other is
published and permanent — yet `nudge` permits the first while `auto` bundles the
second in with tree creation, so wanting the PM to open PRs unattended also
grants it a public voice. Split outward-facing actions into their own setting,
defaulting to off, and map each level onto a credential scope (M3) so the config
value is not the only thing standing in the way.

Rendered next to the tree in the sidebar, so you always know what it is allowed
to do without having to ask it.

**The `m` seam.** On any sidebar row, `m` appends a request carrying that row's
context (tree, member, branch, PR number) and focuses the PM tab — you land in
its chat with it already reading "about canberra/keystone-api #412…". One verb,
every row kind, which is what makes it learnable. The footer hint per row kind
and `?` carry the discovery, following the `enter shell` / `enter open`
convention.

---

## M6 — Durable memory

A PM that re-derives the same context every session is a PM you stop trusting.
But the fix is not a store — it is noticing that **supatree already has a scope
hierarchy, and memory maps onto it exactly**:

| Scope | Lifetime | Store | Holds |
| --- | --- | --- | --- |
| agent | one session | claude transcript | working context |
| tree | until reaped | `.supatree/` | board, info, notes |
| stack | forever, team-shared | **the stack repo (git)** | conventions, retrospectives |
| repo | forever, cross-tool | workbench `Repo` config | per-repo lessons |

The stack repo is the important one and it already exists: branch-local,
editable mid-issue, mergeable back, and holding `AGENTS.md` today. Memory that
lives there is diffable, blameable and reviewable — properties no embedding
store has. Three layers, in order.

### Layer 0 — harvest the store that is already running

Claude Code keeps per-project file memory under
`~/.claude/projects/<encoded-path>/memory/` with a `MEMORY.md` index loaded at
session start. **This is already accumulating on this machine** — verified
2026-09-20: `-Users-stefan--supatree-stacks-keyrepos/memory/` holds 16 facts,
and the fraud0 member repos have their own.

The resolution rule is the interesting part, and it is not the obvious one:

| Artefact | Keyed on |
| --- | --- |
| transcripts | the session's **cwd** |
| memory | the **main repo** (git common dir), not the worktree |

So a supatree *root* agent — cwd `trees/<name>/`, a worktree of the stack
repo — writes memory into the **stack repo's** dir, shared by every tree cut
from that stack. A *member* agent in `repos/<alias>/` writes into that repo's
main dir, shared with every workbench worktree of the same repo. **Per-stack and
per-repo memory already work, for free, and have been filling up for months.**

That reframes this layer from "build a store" to "use the one you have, and add
the half it cannot do". Two properties auto-memory lacks: it is private to
Claude on one machine, and it is invisible to the Go tooling. So:

- **Harvest, don't duplicate.** Before writing any new store, read what is
  already in those directories. It is the cheapest possible evidence about what
  the PM would want to remember.
- **`notes/` in the stack repo** — scaffolded by `pkg/supatree/scaffold.go`
  alongside `AGENTS.md` — covers what auto-memory cannot: shared with the team,
  reviewed as a diff, readable by `supatree` itself. Deliberate and curated,
  where auto-memory is automatic and private. Do not mirror one into the other;
  they are different stores because they have different readers.
- **The PM's own memory** lands at `~/.supatree/pm/` (not a git repo, so it is
  keyed on that path directly). Zero infrastructure; it works the moment M3
  lands.

### Layer 1 — make the ledger queryable

The status half of "memory" is structured and exact, so it must not become a
search problem. `events.jsonl` (M1) + `meta.yml` + git + the PR cache already
hold every fact; they just need an interface.

**MCP `history(repo:, tree:, since:)`** in `pkg/supatree/mcp.go` — which trees
touched a repo, what shipped, what was reverted, how long things took. Exact
answers from structured data, no new dependency. Note that the PR cache already
retains entries for removed supatrees, so history survives a reap for free.

**Rule: the PM never answers a status question from a memory store.** Git and
the ledger are authoritative; memory is for judgement, not for facts.

### Layer 2 — capture what would otherwise be lost

Two hooks, both on code paths that already exist:

**Retrospective on `TreeDone`.** `status.go` already computes it. When a tree
finishes, the PM writes `notes/<slug>.md` into the stack repo: what shipped,
which repos, what the review caught, what was harder than expected. High signal,
naturally bounded, and permanently true — a fact about the past does not go
stale the way a fact about code does.

**Session salvage on reap.** This is the biggest memory leak in the system
today: `supatree rm` calls `sandbox.ClearSessionCache`, which deletes the
agent's entire transcript. Everything it learned dies with the worktree.

**Archive, do not distill.** `remove.go` is Go — it cannot summarise a
transcript, and blocking a removal on a PM round-trip would be worse than the
leak. So the hook is deterministic and cheap: move the transcripts to
`~/.supatree/archive/<tree>/` before the clear, and let the PM distill them into
a retrospective on its own schedule. Best effort — a failed archive may not
block a removal. Note that auto-memory (Layer 0) is keyed on the stack repo, not
the tree, so it *already* survives a reap; it is the transcript that does not.

Two consequences worth deciding up front: raw transcripts are large and contain
whatever was pasted into them, so the archive needs a retention policy; and once
the PM has written the retrospective, the archived transcript should go.

**Provenance.** `meta.yml` gains `Intent string` — the issue or prompt the tree
was created for, captured at `supatree new`. The rename flow deliberately
discards the city name for a slug, so without this there is nothing left three
weeks later that says what `canberra` was *for*. It is also the seed the
retrospective is written against.

### The part that actually decides whether this works

**Injection budget.** Storage is not the hard problem; deciding what loads at
session start without drowning the context is. `contextfile.go` already writes
`info.md` and is already injected. So: `info.md` gains a short memory section —
a hard cap of N items, most-relevant-first — and everything else sits behind an
MCP `recall(query)` the agent calls when it wants more. Write-time is cheap;
read-time is the budget.

**Instrumentation and a kill criterion.** Every memory system dies the same way:
things get written, nothing gets read, and nobody notices for months. So log
`recall()` hits and misses from day one. **A store that nothing has read in 30
days gets deleted, not debugged.** If the query log later shows a pattern that
grep over `notes/` genuinely cannot serve, *that* is when to revisit a semantic
index — with a real corpus and a real eval set, rather than on spec.

**Decay.** Notes accumulate and rot. A periodic PM job merges, dedupes and
expires them — and because they live in the stack repo, it arrives as a PR you
review. Curation is a diff, not a migration.

---

## M7 — Cross-agent support (Claude and codex)

`models` is an open map and AGENTS.md is explicit that model names are never
hardcoded. This plan quietly violates that: it is written around one vendor's
message bus. Here is the audit, and the seam that fixes it.

| Mechanism | Claude | codex | Verdict |
| --- | --- | --- | --- |
| supatree MCP tools | ✓ | ✓ via `~/.codex/config.toml`, stdio | **portable — the real interop layer** |
| `AGENTS.md` context injection | ✓ | ✓ (codex's own convention) | portable, already used |
| `requests.jsonl`, mailbox, board, events | ✓ | ✓ | portable — they are files |
| session pinning via `{session_id}` | ✓ | ✓ ids exist | portable, already config-driven |
| watcher, notifications, sidebar, dash | Go | Go | agent-agnostic already |
| **cross-session bus** | ✓ | ✗ | **not portable** |
| `Monitor` (wake on file change) | ✓ | ✗ | not portable (already demoted in M3) |
| auto-memory (M6 Layer 0) | ✓ | ✗ own store | not portable |
| `sandbox.TrustDir` | ✓ | n/a | already Claude-only, correctly |

Most of it survives, and it survives because it went through MCP and files
rather than through vendor features. Two real gaps: **waking an idle agent**,
and **memory**.

### The transport seam

Invert the M2/M3 design so the vendor-specific thing is the optimisation:

```go
type Transport interface {
    Deliver(a Agent, msg string) error // append to .supatree/mail/<agent>/ — universal
    Wake(a Agent) error                // best effort, capability-dependent
}
```

`Deliver` always writes the mailbox, for every model. `Wake` is the bus for
Claude and a no-op for codex — a delivered message is read at the agent's next
turn instead of interrupting it. The mailbox is the contract; the bus is
latency.

**Report reachability honestly rather than faking parity.** The roster and the
sidebar say `bus` or `mailbox (next turn)` per agent, so you know whether a nudge
lands now or later. The tempting fake is `zellij action write-chars`, which
works for any agent — and writes into whatever is in that pane's input buffer at
that moment, corrupting a half-typed prompt. Not worth it.

### Delegation as the uniform third mode

For "go read these review comments and fix them", messaging a live agent is not
actually the best primitive — spawning a worker is, and *that* is uniform:

```yaml
models:
  claude: { exec_args: ["-p"] }
  codex:  { exec_args: ["exec"] }
```

`Model.ExecArgs` gives the PM a headless one-shot that works identically for
both, with no bus and no idle agent required. Codex CLI can additionally run *as*
an MCP server, so a second route is registering it as a tool the PM calls
directly — worth evaluating once `ExecArgs` exists, not before.

### A pre-existing bug this surfaces

`pkg/sandbox/nono.go` hardcodes `~/.claude/projects/...` in `SessionExists`,
`HasPriorSession` and `ClearSessionCache`. So today a codex agent with
`resume_args` set **never resumes** — the check looks in Claude's directory,
finds nothing, and silently starts fresh. It is a live bug, not a new one, and
M6 makes it worse: salvage-on-reap would archive Claude transcripts only.

Fix with config, not a model check: `Model` gains `session_dir` (default
`~/.claude/projects` for the claude entry) and the three functions take it as a
parameter. That also makes them testable without a fake `$HOME`.

### Consequence for memory

Auto-memory is Claude-only, which means M6 Layer 0's harvest is a *bonus* for
Claude agents, not the substrate. The cross-agent store is `notes/` in the stack
repo, referenced from `AGENTS.md` — which both read. That strengthens the case
already made there: invest in the reviewed, git-versioned store, and treat
auto-memory as a private accelerator that happens to be free.

### The PM need not be Claude

Everything the PM does is MCP calls and file I/O except `Wake`. A codex PM works,
with mailbox-only delivery. Worth keeping true — it is the cheapest possible
check that the abstraction is real.

⚠️ Codex was not installed on this machine, so its flags (`exec`, session ids,
`config.toml` shape) are from documentation rather than verified locally.
Confirm before implementing `ExecArgs`.

---

## M8 — Scheduled work

"Fetch PRs on a schedule" is already M1 — the watcher loop *is* a timer, and it
must stay the only one. What is missing is the other two kinds of scheduled, and
they are the interesting ones:

| Kind | Example | Status |
| --- | --- | --- |
| **polling** | re-derive status on a 30s tick, re-fetch behind `PRStaleAge` (10m) | M1, done, and capped by the quota rule |
| **recurring judgement** | 09:00 standup; hourly triage of new review comments; Friday reap proposal | new |
| **deferred one-shot** | "remind me about #412 in two hours if nothing has changed"; snooze | new, and the one you will use most |

So: a separate milestone, but a small one. The mechanism is roughly a hundred
lines because it reuses two things that already exist; all of the real work is
in the firing *policy*.

### Time lives in Go, not in the agent

The scheduler goes in the **watcher**, not the PM. The watcher already has a
loop, already holds a singleton lock, and already runs whether or not a PM tab
is open. Adding a due-check to its existing tick costs one function call and no
new daemon, no new lock, no new poller.

Firing an entry then means **appending a line to `requests.jsonl`** — which is
already how the sidebar, the watcher and a shell reach the PM (M3). The PM needs
no timer, no scheduling ability, and no new channel; a scheduled job and a
pressed `m` arrive identically.

That split is not just convenience. An agent cannot be trusted to hold a timer:
it is mid-turn, it is blocked on a tool call, it was restarted an hour ago, its
monitor did not survive. Every one of those silently drops a job, and a schedule
that silently drops jobs is worse than none. Claude Code's own cron tools are
the same trap plus vendor lock-in (M7) — and they are invisible to `supatree`,
so nothing else can see or edit the timetable.

### The timetable

`~/.supatree/schedule.yml` — hand-editable, diffable, and inspectable with
`supatree schedule` (list, with next and last fire times):

```yaml
jobs:
  - id: standup
    at: "09:00"           # local time, weekdays only
    days: [mon,tue,wed,thu,fri]
    when: events_since_last
    prompt: "Summarise what moved since yesterday. Board + desktop, no chat."
  - id: triage
    every: 1h
    when: events_since_last
    kinds: [changes_requested, checks_failed]
    prompt: "Fetch comments for the affected PRs and brief the tree's agent."
  - id: reap
    at: "17:00"
    days: [fri]
    when: always
    prompt: "List every done tree and propose reaping it. Do not remove anything."
```

`every: <duration>` and `at: HH:MM` + `days:` are parseable with `time.ParseDuration`
and `strings.Split`; full five-field cron syntax would mean a new dependency for
expressiveness nobody needs at this granularity. The escape hatch for anything
weirder already exists: system cron or launchd calling `supatree watch --once`,
which no-ops cleanly while the daemon holds the lock (M1).

### Firing policy — the part that is actually hard

**Seed on first sight, like the baseline.** Last-fire times live in
`~/.supatree/cache/schedule-state.json`, written under the watch lock beside
`last-status.json`. A job id that is not in that file has never fired, and the
naive reading of "never fired" is "overdue" — so a freshly written `schedule.yml`
fires every entry at once, on a Tuesday afternoon. Seed an unseen id with *now*
and emit nothing, exactly as M1 does for `last-status.json`. Same bug, same fix,
and it will be rediscovered separately if it is not written down here.

**Coalesce missed fires; never replay them.** Close the laptop at 18:00, open it
at 09:00, and a naive hourly entry is fifteen jobs deep. On wake, an entry that
is due fires **once**, regardless of how many intervals elapsed — anacron's rule,
not cron's. This is the single most likely way a first implementation embarrasses
itself.

**A job that has nothing to say must not run.** `when: events_since_last` is the
default and it is load-bearing: a 09:00 standup that says "nothing changed" every
morning is notification fatigue wearing a suit, and people mute it inside a week.
`ReadEvents(since)` (M1) already answers the question; `kinds:` narrows it
further. `when: always` exists, and should be rare.

**TTL.** A scheduled request carries a deadline; past it, drop rather than run
late. A 09:00 standup executed at 16:00 because the PM was down is noise, and
worse, noise that looks like a bug.

**Autonomy is checked at fire time, against the tree.** M5's per-tree level gates
scheduled actions exactly as it gates interactive ones — and scheduled work is
where `auto` gets genuinely uncomfortable, because nobody is watching. The
default stance: **a scheduled job caps at `nudge` even in an `auto` tree**,
unless the entry opts in with `autonomy: auto`. Unattended tree creation should
be a thing you typed, once, deliberately.

Which exposes a gap in M5 rather than in M8: autonomy lives in per-tree
`meta.yml`, so **nothing governs an action that has no tree yet** — `new_tree`
most obviously, and any cross-tree sweep. M5 needs a workspace-level default in
`~/.supatree/config.yml` that `meta.yml` overrides per tree, or the most
dangerous verb in the tool is the one verb the permission model does not cover.

### Quota

Scheduled jobs read the cache. `Status`, `Diff` and `ReadEvents` never touch the
network, so N entries across M trees cost nothing — which is the whole reason the
firing side is a request append rather than a fetch.

The one exception is the job you asked for: **fetching comments on a schedule**.
M4 makes comments on-demand precisely because they are a second GraphQL query
shape per PR, and "every hour, for every open PR" is exactly what the shared
5,000/hour budget cannot absorb. The rule that makes it safe is to gate on events
rather than on trees:

> A scheduled comment fetch covers only PRs with a `changes_requested` or
> `checks_failed` event since that entry's last fire — never "all open PRs".

Which is usually zero, occasionally one or two, and bounded by review activity
rather than by tree count. It still goes through the M4 cache and the one
persisted backoff.

### When the PM is not running

The request sits in the file and the PM reads it on its next start — correct, but
often useless: a standup delivered at 16:00 is the TTL case above.

The better answer costs nothing extra, because M7 already builds it. Headless
delegation via `Model.ExecArgs` (`claude -p`, `codex exec`) runs the job with no
tab and no live agent, leaving its output in `board.md` and a notification.
Scheduled work is in fact the clearest justification for `ExecArgs` existing:
overnight jobs should not require a terminal to have been left open.

### Snooze

The deferred one-shot falls out of the same machinery for almost free, and it is
what turns notifications from a feed into a workflow. MCP `schedule_add(when,
prompt, once: true)` lets the PM defer its own follow-ups — "check back on #412
in two hours". One-shot entries live in the same file and are removed after they
fire. (A sidebar key for "hand this row to the PM *later*" is the obvious
companion to `m`, but it needs a free key and a picker for the delay; it is a
follow-up, not part of this milestone.)

### Surfaces and testing

`supacmd/schedule.go` — `supatree schedule` (list with next/last fire),
`schedule run <id>` (fire one now, for testing the prompt without waiting until
Friday — it appends the request but must *not* touch the last-fire state, or
testing a job silently skips its next real run). No new key; this is
configuration, not a live view.

The pure seam, and the only part that needs real tests:

```go
func Due(jobs []Job, state map[string]time.Time, evs []Event, now time.Time) []Job
```

Table-driven over: a missed overnight window coalescing to one fire, `at:` across
a weekday boundary, `events_since_last` with an empty event slice, TTL expiry,
and a clock that moved backwards. DST is the trap — an `at: 09:00` entry must not
fire twice on the day the clock goes back.

---

## Keys

| Key | Where | Action |
| --- | --- | --- |
| `P` | sidebar | open/focus the PM tab |
| `m` | sidebar, any row | hand this row to the PM |
| `e` | dash | toggle the events feed |

All three are free in `updateNormal` today. Add to `helpView()` in
`pkg/supatree/tui/view.go` in the same pass.

## Worked flow

Watcher sees `changes_requested` on `canberra/keystone-api`. Sidebar glyph flips
(nearly free — `MemberChanges` already exists). Desktop notification fires. The
PM's monitor wakes it; it calls `pr_comments`, then `SendMessage` to
`st-canberra-main` with the unresolved threads. That agent is idle in its tab
and wakes up. The board updates. You find out when you next look — nobody had to
be at the keyboard.

## Quota

The one hard constraint (AGENTS.md). A PM sweeping N trees on a timer is exactly
what the shared budget cannot absorb. Therefore: the watcher is the **only**
poller, singleton-enforced by a file lock; it uses `FetchTargets`/`FetchPRs`
unchanged; `Status` and `Diff` never touch the network; comments are on-demand
and cached; every path observes the one persisted backoff. Scheduled jobs (M8)
fire by appending a request and read the cache, so they add no GitHub load at
all — except a scheduled comment fetch, which is gated on events rather than on
trees for exactly that reason.

## Risks

- **Bus dependency.** Undocumented, version-coupled, Claude-only. M7's transport
  seam is the containment: the mailbox is the contract, the bus only makes
  delivery faster, so losing it degrades latency rather than breaking messaging.
  Build the seam before the bus call sites, not after.
- **Injection through fetched content.** The PM's whole input surface is text
  other people wrote, and it holds credentials that reach outside the machine.
  This is the risk that does not exist for a coding agent, and prompt wording is
  not a control for it: the containment is M3's scoped credentials, read-only
  where possible, plus a human or schedule entry as the initiator of anything
  outward-facing.
- **Desktop notifications from inside nono are blocked.** The watcher must stay
  outside the sandbox; it is our code, not an agent's.
- **Notification fatigue** is the most likely way this fails in practice, and it
  fails quietly — people mute it rather than report it. The dedupe and the
  focused-tab suppression are not polish.
- **PM confidently wrong.** `nudge` as the default, read-only on trees, and
  never opening tabs are the three things that keep a bad PM turn cheap. A
  scheduled turn (M8) is the same risk with nobody watching, which is why it caps
  at `nudge` even in an `auto` tree unless the entry says otherwise.

## Testing

Most of this plan touches things that cannot be tested in CI — a message bus, a
desktop notification, an agent's judgement. That is an argument for keeping the
testable core large and the untestable shell thin, the same split `status.go`
already makes between `localState` (I/O) and `memberState`/`rollup` (pure).

The pure seams to extract deliberately, because they are where the bugs will be:

| Function | Shape | Covers |
| --- | --- | --- |
| `Diff(prev, cur Summary) []Event` | pure | every state transition, first-run baseline |
| `shouldNotify(ev, state) Tier` | pure | tiering, dedupe cooldown, dwell time, focus suppression |
| `AgentAddress(tree, agent)` | pure | exactness, two trees with a `main` |
| `Due(jobs, state, evs, now) []Job` | pure | first-sight seeding, missed-window coalescing, TTL, DST, weekday `at:` |

Everything else — spawning the watcher, `osascript`, the bus, the `Monitor` —
is a thin shell over those, exercised by `scripts/e2e-supatree.sh` with
`watch --once` against a fixture and a stub `notify_command` that appends to a
file. The flappable-state and dedupe rules in particular must be unit tests, not
things you discover by being notified forty times.

## Definition of done (each milestone)

`make ci` green, both e2e scripts green, `README.md` updated for user-facing
behaviour (new commands, keys, config keys), `AGENTS.md` updated where a new
invariant lands (the watcher singleton, the PM gate, the address-by-prefix
rule).

## Possible follow-ups

- Reap flow: the PM proposes removing every `done` tree; one keypress confirms.
- `events.jsonl` retention/rotation, and a `--history` view over what shipped.
- The workbench sidebar could consume the same event stream for its worktrees.
- Schedule entries scoped to one tree, rather than the whole workspace.
- A sidebar key for deferring a row to the PM — snooze from where you are.
- Slack or similar as a notification surface. Deliberately out of scope: it adds
  a second untrusted input channel and an outward-facing credential, and M3's
  profile is the thing that would have to absorb both.
