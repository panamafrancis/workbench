# workbench — Agent Guide

Sandboxed git worktree manager. Each piece of work gets a git worktree opened as a new Zellij tab, with the chosen LLM running inside a nono security sandbox.

## Build & run

```sh
make build          # produces workbench + supatree binaries under dist/
make install        # go install both binaries
make ci             # fmt + lint + vet + test
make e2e            # build + run scripts/e2e.sh AND scripts/e2e-supatree.sh
make hooks          # enroll .githooks/ as git hooks
go test ./...
```

This repo ships **two** binaries: `workbench` (root package, `cmd/`) and
`supatree` (`cmd/supatree`, commands in `supacmd/`). supatree reuses the
workbench `pkg/*` packages; shared code (the zellij `Workspace`, the MCP
`Server` framework, `config.WithFileLock`) is parameterized rather than
duplicated. See the "Supatree" section below.

Start a Zellij session with the sidebar:
```sh
workbench start             # default session "wb-main"
workbench start client-x    # named session "wb-client-x"
```

## File layout

```
cmd/                    # Cobra commands
  root.go               # PersistentPreRunE loads config; registers all subcommands
  start.go              # workbench start — attach-or-create Zellij session
  open.go               # workbench open — open worktree in a Zellij tab
  ls.go                 # workbench ls — TUI sidebar or plain text
  add.go                # workbench add (parent for repo/worktree)
  add_repo.go           # workbench add repo
  add_worktree.go       # workbench add worktree
  rm.go                 # workbench rm (parent)
  rm_repo.go            # workbench rm repo
  rm_worktree.go        # workbench rm worktree
  rename_branch.go      # workbench rename-branch
  stats.go              # workbench stats — lifetime statistics and achievements
  init.go               # workbench init — setup wizard
  doctor.go             # workbench doctor — dependency checks
  uninstall.go          # workbench uninstall
  version.go            # workbench version (Version var stamped via ldflags)
pkg/
  config/
    config.go           # Config types, Load/Save, FindRepo, FindWorktree, CRUD helpers
    paths.go            # ConfigDir, ConfigPath, StatePath, LayoutsDir, WorktreePath
    state.go            # State (last_run_version, update check, gamification stats/achievements), LoadState/Save
  git/
    worktree.go         # DefaultBranch, FetchOrigin, CreateWorktree (returns offline bool), RemoveWorktree
    names.go            # GenerateName (city names), ValidateName, ExtractBaseCity, IsCityName
    status.go           # IsDirty, BranchName
  github/
    gh.go               # LookupPR via gh CLI
    cache.go            # PR status cache — Get/Set/Rename/Delete/IsStale
  sandbox/
    nono.go             # BuildNonoArgs(path, modelKey, cfg) → []string
  setup/
    checks.go           # RunChecks — shared check engine for init/doctor
    update.go           # CheckForUpdate — GitHub releases API with 24h cache
  tui/
    model.go            # Root Bubble Tea model — modes, update, view, footer, help
    tree.go             # Collapsible repo→worktree tree, stats, mouse, placeholder rows
    keys.go             # KeyMap + DefaultKeyMap (j/k/h/l/space/etc.)
    styles.go           # Lipgloss palette
  zellij/
    client.go           # IsInZellij, OpenTab, GoToTab, TabNames, OpenOrFocusTab
    layout.go           # WriteTabLayout — per-worktree KDL with env injection
    session.go          # ListSessions, WriteSessionLayout (go:embed), CreateBackgroundSession
    session.kdl.tmpl    # Embedded session layout template
plugin/                 # DEPRECATED — replaced by MCP server (cmd/mcp.go + pkg/mcp/)
pkg/mcp/
  server.go             # MCP stdio server — rename_branch, create_pr, docs tools + conventions prompt
pkg/docs/
  docs.go               # Documentation content organized by topic (used by MCP docs tool + CLI)
scripts/
  wb.kdl                # Standalone session layout (alternative to embedded)
  e2e.sh                # E2E test script (isolated HOME)
  ci/nono-shim          # Fallback nono shim for CI without Landlock
.githooks/
  pre-push              # Local e2e test before push
```

## Config

All state lives under `~/.workbench/`:
- `~/.workbench/config.yml` — repos, worktrees, model definitions
- `~/.workbench/state.yml` — last-run version, update check cache, gamification stats (cities_visited, worktrees_created, worktrees_merged, achievements, activity_days), and `reserved_cities` (names still occupied by a worktree or its lingering Claude history, excluded from name generation)
- `~/.workbench/worktrees/<alias>/<name>/` — default worktree location
- `~/.workbench/layouts/<name>.kdl` — generated Zellij layouts (transient)

`models` is an open map — users add arbitrary entries (`mymodel`) with any `binary`/`nono_profile`/`args`. Never hardcode model names.

`config.Load()` reads from disk every time. `config.Save()` writes atomically via temp+rename. `show_stats` (`*bool`, default true) controls the gamification stats box in the TUI sidebar.

## TUI model

`pkg/tui/model.go` is the Bubble Tea root model. Key patterns:

**Cursor skips repo headers** — `moveUp`/`moveDown` in `tree.go` consult `selectable()`, which skips `isRepo` items *except* for a collapsed repo: that header is the only row the repo has, so it must take the cursor. Without the exception, folding a repo strands the cursor in the next one and `zM` (fold all) leaves nothing selectable at all. Empty repos get a selectable placeholder row. Every fold path (`toggleCollapse`, `collapseContaining`, `expandContaining`, `setAllCollapsed`) parks the cursor via `focusRepo`, not bare `clamp`.

**Sidebar navigation and viewport** — `tree.go` mirrors `pkg/supatree/tui`: `moveBy` (used by `ctrl+d`/`ctrl+u` via `halfPage`), `gotoTop`/`gotoBottom` (`gg`/`G`), `jumpRepo` (`}`/`{`), `setAllCollapsed` (`zM`/`zR`), and the two-key sequences driven by `Model.pending` (an unrecognized second key clears the prefix and falls through, so a mistyped `g` never swallows a command). `tree.view(width, avail)` renders only the `viewport` window — `Model.View` renders `viewTail()` first and sizes `avail` from its line count. The mouse wheel calls `scrollBy`, which pans `scroll` and clears `follow`; `viewport` drags the scroll offset to the cursor only while `follow` is set, so a wheel-scrolled view survives the 60s tick. Deliberate cursor moves re-arm `follow`; `clamp` deliberately does not.

**Refresh reloads from disk** — `refreshMsg` calls `config.Load()` and replaces both `m.cfg` and `m.tree.cfg`. Don't just call `refreshDirty()` alone.

**GitHub quota discipline** — a round costs one *conditional* request per **repo**, not one per branch, and both sidebars go through the same entry point: `github.Sync(w, targets, opts)`, called inside `Cache.TryMutate`. The sidebars only decide *which branches are on screen*; everything else is Sync's job. Key properties, each of them load-bearing:

- `PollRepo` sends the stored `ETag`; an unchanged repo answers **304, which GitHub does not charge**. A quiet round therefore costs nothing. Measured cold on 14 repos / 67 branches: 15 requests; every round after that: 0.
- It uses the REST **`core`** bucket. The **GraphQL** bucket is what agents drain with `gh pr view`/`pr checks`, and it is routinely exhausted while `core` is untouched — which is why the per-branch fallback is `LookupBranchPR` (REST) and not `LookupPR` (`gh pr list`, GraphQL). Only a non-github.com remote falls back to the latter.
- **Never trust `gh api rate_limit`.** Its graphql row reported `remaining: 5000` while response headers said `used: 5001`. Read `X-RateLimit-*` off a response, or query `rateLimit{}` inside a GraphQL document.
- `gh api -i` **exits non-zero on 304 and 404**. `parsePollResponse` classifies the status from stdout and only falls back to the exit status when there is no parsable response (`errNoResponse`) — keying off the exit code alone turns every free 304 into a re-fetch.
- A 304 says *unchanged*, not *complete*: `RepoState.Truncated` is remembered across rounds, because absence from a listing only proves "no PR" when the listing covered the repo's whole history (`RepoPoll.Complete`). Otherwise the branch goes to a per-branch lookup.
- A repo that 404s for this account is recorded `Unavailable` and skipped until a forced refresh, instead of costing a request and painting an error every round.
- Rate limits pause everything until the **reported** reset: `X-RateLimit-Reset` for the primary quota, `Retry-After` for the secondary/burst limit (which trips with thousands of quota left and carries no quota headers — see `rateLimitFrom`).
- Group by repo, not by checkout: supatree gives each tree its own checkout of the same repo, and grouping by path polled `admin-frontend` six times a round.
- Every response (304s included) carries `X-RateLimit-*`, so `Budget` is observed for free and persisted in the shared cache. Conditional polls are not gated on it — they cost nothing — but the per-branch fallback lookups are: below `DefaultReserve` (500) a background round defers them and reports `Deferred`, which the sidebars show as `gh quota low`. A forced refresh drops to `ForcedReserve` (50), because the person waiting outranks the background.
- `create_pr` writes the PR it just made straight into the cache (`github.RecordCreatedPR`, parsing the URL out of `gh pr create` output). No invalidation is needed beyond that: creating a PR changes the repo's PR list, so the next poll's ETag no longer matches and the listing refreshes itself.
- The supatree MCP tools (`pr_status`, the `create_pr` dependency check) go through `syncMembers` → `github.Sync` too, so an agent's tool call costs one conditional poll per repo rather than a GraphQL request per member. Nothing outside `pkg/github` calls `LookupPR` any more.

**The PR cache has exactly one writer path** — `github.Cache.Mutate` / `TryMutate`, which flock, re-read the file, apply the callback, and write atomically. There is deliberately no exported `Save`: a whole-file write from a process-local snapshot silently reverts every entry another process wrote since it loaded, *including* the `retry_after` cooldown, which is how one `pr_status` call used to un-pause every sidebar on a drained quota. `Writable.SetRetryAfter` is monotonic — it can extend a cooldown, never shorten or clear one. Mutations must not be held across slow work: `supatree/rename.go` and `remove.go` collect their changes and apply them in a single mutation *after* the pushes and cleanup scripts, so a sidebar's round never queues behind them. `LookupPR` has a 20s timeout because the fetch round holds the lock across it.

**Local re-sync on focus/tick** — `reloadLocalState()` re-reads `config.yml` + `state.yml` and swaps `m.cfg`/`m.tree.cfg`/`m.state` on `tea.FocusMsg` and every `tickMsg`, so sidebars in different tabs stay consistent without a manual `r`. It deliberately skips the network PR fetch (that's what the full `refreshMsg` is for). It's a no-op while `m.mode != modeNormal` or a create is in flight (`m.creating`), because reloading would either shift the `pendingRepoIdx`/`pendingWorktreeIdx` slice indices under an open confirm/input mode or drop an optimistic worktree that isn't persisted yet.

**Inline input mode** — the model has an `inputMode` state machine (`modeNormal` / `modeAddRepoPath` / `modeAddRepoAlias` / `modeNewWorktree` / `modeConfirmDelete` / `modeConfirmQuit` / `modeOpenWith` / `modeHelp`). When mode is non-normal, `Update` routes `tea.KeyMsg` to `updateInput()` which handles `enter`/`esc` and passes everything else to the `textinput.Model`. Use this same pattern for any future inline prompts.

**Key bindings** — defined in `pkg/tui/keys.go`. Add new bindings to both `KeyMap` struct and `DefaultKeyMap`, then handle in `model.go`'s `Update` switch.

**Sidebar mode** — when `WORKBENCH_SIDEBAR=1` is set (injected by the layout), `q` prompts for confirmation instead of quitting immediately. The layout wraps `workbench ls` in a restart loop with backoff (`sleep 0.2` on success, `sleep 2` on failure).

**Env injection** — `WriteTabLayout` in `layout.go` accepts a `map[string]string` of env vars to inject into the agent pane's KDL `env {}` block. Keys are sorted for deterministic output. The **sidebar** pane also gets `WORKBENCH_WORKTREE_NAME=<name>` (the tab's worktree); the TUI reads it in `New()` into `tree.activeWorktree` to render the passive `▸` "you are here" marker (see `tree.go` `view`). The root session sidebar has no such env, so `activeWorktree` is empty and no marker shows.

**Pane vs tab naming** — the agent pane's *display* name is `{repo}/{worktree}` (derived from `WORKBENCH_REPO_ALIAS` in the injected env, KDL-escaped via `quoteKDL` since aliases aren't charset-validated). The Zellij *tab* name and the layout filename stay the bare worktree name — that name keys tab lookups (`OpenOrFocusTab`, `TabNames`, the TUI `openTabs` map) and is a path component, so it must remain slash-free and validated. `WriteTabLayout` validates the worktree name up front with `git.ValidateName`.

## Zellij session management

`workbench start` embeds a session layout via `go:embed` (`session.kdl.tmpl`), writes it to `~/.workbench/layouts/session-<name>.kdl`, and uses `syscall.Exec` to hand the terminal to zellij. Sessions are prefixed with `wb-`.

`pkg/zellij/session.go` provides `ListSessions`, `WriteSessionLayout`, `CreateBackgroundSession`, `DeleteSession`.

Layout/session writing hangs off a `zellij.Workspace` (`pkg/zellij/workspace.go`) that carries the tool-specific bits (`LayoutsDir`, `SidebarCommand`, `SidebarEnvVar`, `SidebarActiveEnvVar`, `SessionPrefix`, `SessionTab`). `WriteTabLayout`, `OpenTab`, `OpenOrFocusTab`, `CleanupLayout`, `CleanupStaleLayouts`, `WriteSessionLayout` are methods on it; `WorkbenchWorkspace()` / supatree's own workspace supply the values. Pure `zellij action` wrappers (`GoToTab`, `TabNames`, `ListSessions`, ...) stay free functions.

`pkg/zellij/client.go` provides tab-level operations (`OpenTab`, `GoToTab`, `OpenOrFocusTab`). These call `zellij action` subcommands and only work inside a Zellij session.

## nono sandbox

`BuildNonoArgs` returns `["run", "--profile", <profile>, "--allow", <worktreePath>, "--", <binary>, <args...>]`. The profile and binary come from the model config entry — no hardcoded mapping.

## Adding features

- **New inline TUI action**: add key to `keys.go`, add `inputMode` constants if needed, handle in `model.go` `Update` and `updateInput`.
- **New CLI command**: add file under `cmd/`, wire into `rootCmd` in `cmd/root.go` via `rootCmd.AddCommand(...)` in `init()`.
- **New MCP tool**: `pkg/mcp` is a reusable framework — `rpc.go` has the JSON-RPC `Server{Name,Version,Tools,Prompts,Gate}` + stdio loop; `workbench.go` builds the workbench tool set. Add workbench tools there; supatree tools live in `pkg/supatree/mcp.go`. Tool handlers have signature `func(args map[string]any) (text string, isError bool)`.
- **New config field**: add to structs in `pkg/config/config.go`, update `DefaultConfig()` if it needs a default.
- **Worktree creation hooks**: `copy_files` runs first (copies gitignored files from repo), then `startup_script`.
- **Change what opens in a new tab**: edit the KDL template in `pkg/zellij/layout.go`.

**Always update `README.md`** when adding or changing user-facing behavior: new config fields, new CLI flags, new keybindings, changed lifecycle behavior, or nono sandbox requirements.

## Supatree

`supatree` manages a set of worktrees — one per repo — for a single cross-repo issue. State lives under `~/.supatree/`.

**Model.** A *stack* is a git repo (`supatree scaffold`) holding `supatree.yml` (member repo aliases + `deps` edges), `AGENTS.md`, and `scripts/`. A *supatree* is a worktree of that stack repo at `~/.supatree/trees/<name>/` on branch `st/<name>`, with each member repo checked out under `repos/<alias>/` on branch `st/<slug>/<alias>` (slug starts as the city name; `rename-branch` changes it). Member repo *definitions* come from workbench's `~/.workbench/config.yml` (resolved by alias) — supatree never duplicates them.

**Discovery.** The registry (`~/.supatree/config.yml`) lists only stacks + defaults. Live supatrees are discovered by scanning the trees base for `<name>/.supatree/meta.yml` (`supatree.List`). Per-tree state: `.supatree/meta.yml` (name/slug/stack/model), `.supatree/agents.yml` (named agents + session IDs), `.supatree/info.md` (generated). All three are gitignored, as is `repos/`.

**Package layout.** `pkg/supatree/`: `paths.go`, `config.go` (registry), `spec.go` (supatree.yml), `meta.go`, `deps.go` (`TopoSort`), `instance.go` (`LoadInstance`/`List`/`Get`), `create.go`, `sync.go`, `remove.go`, `rename.go`, `agents.go`, `contextfile.go` (info.md + scaffolded AGENTS.md), `scaffold.go`, `startup.go`, `mcp.go`, `tui/` (the `supatree ls` sidebar). CLI in `supacmd/`, entry `cmd/supatree/main.go`.

**Sidebar (`pkg/supatree/tui`).** Mirrors the workbench sidebar's live-sync + PR-fetch discipline: `reloadWithSelection()` re-reads `supatree.List` on `tea.FocusMsg` and every tick (pinning the cursor by row identity), and `fetchPRCmd(force)` only hits `gh` for cache-stale branches, respects the persisted backoff (`Cache.InBackoff`), and arms `Writable.SetRetryAfter` on a rate-limit response — this is what keeps the restart loop and per-tab sidebars from exhausting the gh quota. Its `gh` calls run inside `Cache.TryMutate` exactly as the workbench sidebar's do; see "The PR cache has exactly one writer path" above. The supatree workspace sets `SidebarActiveEnvVar: "SUPATREE_ACTIVE_TREE"` (tab name → `▸` "you are here" marker), and `supatree ls` must run with `tea.WithReportFocus()` for the focus re-sync to fire. `n` prompts for a tree name (blank = auto), first asking for a stack only when >1 is registered. Trees fold with `space`/`h`/`l` (`collapsed` map, keyed by tree name — survives reloads), and `View` windows `m.rows` around the cursor (`viewport`) so long lists scroll instead of running off the pane.

**Sidebar navigation.** Vim motions live in `updateNormal`: `j`/`k`, `ctrl+d`/`ctrl+u` (`halfPage`, sized from the `viewHeight` that `viewport` records each render), `G` (`gotoBottom`), `}`/`{` (`jumpTree` — next/previous `rowTree`), plus the two-key sequences `gg`, `zM` and `zR` driven by the `pending` prefix field (an unrecognized second key clears the prefix and falls through to the normal switch, so a mistyped `g` never swallows a command). Mouse handling is in `updateMouse`: the wheel calls `scrollBy`, which pans `m.scroll` and clears `m.follow`; `viewport` only drags the scroll offset to the cursor while `follow` is set, so a wheel-scrolled view survives the 30s tick reload. Every deliberate cursor move (`moveCursor`, `gotoTop`/`gotoBottom`, `jumpTree`, `setCollapse`, `selectByRow`) re-arms `follow` — but `clampCursor`/`reloadWithSelection` deliberately do not, or background reloads would yank the view back mid-scroll.

**Agents.** Several agents share the supatree root but resume independently via session IDs (`Model.NewSessionArgs`/`ResumeSessionArgs`, `{session_id}` substituted by `sandbox.BuildAgentNonoArgs`). `supatree open [--agent <name>]` opens/resumes a root agent; `--repo <alias>` opens an agent scoped to one member (dir-based resume). Tab names: `<name>` for the `main` agent, `<name>:<agent>` otherwise.

**MCP tools** (`supatree mcp`, gated by `SUPATREE=1` except `docs`/`supatree_info`): `supatree_info`, `sync`, `rename_branches`, `create_pr`, `create_prs`, `pr_status`, `docs`. `create_pr`/`create_prs` refuse a still-city-name slug and (unless `force`) a repo whose dependencies have no PRs yet. Only `supatree init` registers this server (`claude mcp add supatree -s user`); skip it and a supatree session sees only workbench's tools. The **workbench** MCP tools (`create_pr`/`rename_branch`, gated by `WORKBENCH=1`) detect `SUPATREE=1` and redirect to these instead of dead-ending on the missing `WORKBENCH` env var. Branch slugs (`rename_branches`) share `git.ValidateName` — max 40 chars.

**Conventions.** Reuse workbench packages — never fork them. `git.CreateWorktree`/`RemoveWorktree`/`RenameBranch`/`CommitsAhead`, `repo.RunCopyFiles`/`RunStartup`/`RunCleanup`, `github.LookupPR`/`Cache`, `sandbox.BuildNonoArgs`/`BuildAgentNonoArgs`, `config.WithFileLock`. `make ci` + both e2e scripts must stay green.

## Before pushing / creating a PR

Always run `make ci` (fmt + lint + vet + test) and fix all issues before pushing. The linter (`golangci-lint`) enforces errcheck, exhaustive switch, noctx (use `exec.CommandContext`), prealloc, staticcheck, and more — do not suppress warnings, fix them.
