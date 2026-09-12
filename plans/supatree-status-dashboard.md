# Supatree activity status + dashboard

## Goal

Answer, at a glance and across every supatree: what is open, what is pushed but
un-PR'd, what is approved and ready to merge, what is blocked, what has gone
stale, and what is finished and only occupying disk.

Delivered as three surfaces over one derivation: `supatree status` (plain +
JSON), `supatree dash` (full-screen TUI, opened with `D` from the sidebar), and
the `pr_status` MCP tool agents already call.

## Data

Everything needed was already collected except four facts:

| Signal | Source | Before |
| --- | --- | --- |
| worktree checked out | `Member.Exists` | had it |
| dirty | `git.IsDirty` | had it |
| branch pushed | `git.HasRemoteBranch` | had it |
| commits ahead of base | `git.CommitsAhead` | had it, unused by the UIs |
| PR number + open/draft/merged/closed | `github.Cache` | had it |
| **review verdict** | `gh pr list --json reviewDecision` | added |
| **CI checks** | `gh pr list --json statusCheckRollup` | added |
| **unpushed commits** | `git rev-list origin/<branch>..HEAD` | added (`git.UnpushedCommits`) |
| **last activity** | `git log -1 --format=%ct` | added (`git.LastCommitTime`) |

The two GitHub fields ride along in the existing `gh pr list` call — one request
either way, so the quota cost is unchanged. `PRInfo.Review` / `PRInfo.Checks`
are `omitempty`, so a pre-existing cache file decodes as "unknown" and fills in
on the next refresh.

## State model (`pkg/supatree/status.go`)

Per member:

```
absent → idle → wip → pushed → draft → open → changes | approved → merged | closed
```

`absent` = no worktree (run sync). `idle` = clean and even with the base, i.e.
this repo has nothing to ship and must not hold the supatree back. A PR, when
one exists, outranks local git: an approved PR on a dirty worktree is
`approved`, and the dirt shows up as a flag.

Per supatree: the least advanced member that still has work, plus orthogonal
flags.

| State | Meaning |
| --- | --- |
| `setup` | a member worktree is missing — run `supatree sync` |
| `new` | checked out, no work anywhere yet |
| `wip` | work exists that is not on the remote |
| `pushed` | everything pushed, a PR is still missing |
| `review` | PRs open, awaiting review |
| `approved` | every open PR approved — ready to merge |
| `done` | every PR merged or closed — safe to `supatree rm` |

Flags: `Blocked` (changes requested or failing checks), `Dirty`, `Stale` (no
commit in any member within `--stale-after`, default 7d; a `done` tree is
finished, not stale, and a tree with no commits at all falls back to its
creation time).

The derivation is pure (`memberState`, `rollup`) with the git I/O isolated in
`localState`, which is what makes it testable without a fixture repo. Git runs
across `statusWorkers` goroutines: a dozen supatrees is a few hundred processes.

## Quota discipline

The one hard constraint (see AGENTS.md): the sidebar already runs once per
Zellij tab, so a second independent poller would multiply the shared 5,000/hour
GraphQL budget again. Therefore:

- `Status` never calls GitHub. It reads the cache, so any surface may call it as
  often as it likes.
- `FetchTargets` + `FetchPRs` (`pkg/supatree/prfetch.go`) are the only fetch
  path, extracted from the sidebar so the dashboard cannot drift from it: cache
  re-read, staleness gate, skip branches with no origin ref, cross-process
  try-lock, persisted rate-limit backoff.
- Target selection and the gh calls all run inside a `tea.Cmd`. Selecting
  targets is one git process per member; on the main loop it froze the
  dashboard's first paint (found while testing) and stalled the sidebar on every
  tick.

## Surfaces

1. **`supatree status`** — plain text by default, `--json` for scripts and watch
   loops, `--refresh` to fetch first, `--all` to include finished trees'
   members, `--stale-after` to tune. Cache-only by default, so it is free.
2. **`supatree dash`** — Bubble Tea, its own tab or window. Columns: state,
   supatree, stack, PRs, review (`2✓ 1✗ 3·`), time since last commit, notes.
   `space` expands to member repos with PR numbers; `enter` focuses that
   supatree's Zellij tab; `r` forces a refresh. Sorted most-actionable first,
   finished trees last. Piped output falls back to `status`.
3. **Sidebar `D`** — opens or focuses the dashboard in a `supatree-dash` tab
   (`Workspace.OpenOrFocusCommandTab`). A tab rather than a pane under the
   sidebar: it costs no rows in every supatree tab and can be quit.
4. **MCP `pr_status`** — now reports the same rollup, and refreshes through the
   shared locked path instead of calling `gh pr list` for every member
   unconditionally.

Also: supatree sidebar member rows now carry the PR number (`◉ open #871`),
matching the workbench sidebar.

## Possible follow-ups

- Reap flow: a key in the dashboard to remove every `done` supatree.
- History: the PR cache still holds entries for removed supatrees — a
  `--history` view could show what shipped over the last N weeks.
- `workbench` sidebar could show the same derived state per worktree.
