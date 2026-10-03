# workbench — Agent Guide

Sandboxed git worktree manager. Each piece of work gets a git worktree opened as a new Zellij tab, with the chosen LLM running inside a nono security sandbox.

## Build & run

```sh
make build          # produces workbench binaries under dist/
make install        # go install workbench
make ci             # fmt + lint + vet + test
make e2e            # build + run scripts/e2e.sh
make hooks          # enroll .githooks/ as git hooks
go test ./...
```

This repo ships the `workbench` binary (root package, `cmd/`) and the shared
`pkg/*` packages that supatree (a separate repo) also builds on. Shared code
(the zellij `Workspace`, the MCP `Server` framework, `config.WithFileLock`) is
parameterized rather than duplicated. See the "Supatree" section below.

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
  version.go            # workbench version (pkg/version.Version: ldflags, else the go install module version)
pkg/
  xdg/
    xdg.go              # ConfigHome/StateHome/CacheHome — env first, XDG defaults on every OS
  testutil/
    home.go             # IsolateHome(t) / IsolateProcess() — pin HOME and all three XDG vars
  config/
    config.go           # Config types, Load/Save, FindRepo, FindWorktree, CRUD helpers
    paths.go            # ConfigDir, StateDir, CacheDir, AgentCacheDir, LayoutsDir, OldLayout
    state.go            # State (last_run_version, update check, gamification stats/achievements), LoadState/Save
  git/
    worktree.go         # DefaultBranch, FetchOrigin, CreateWorktree (returns offline bool), RemoveWorktree
    names.go            # GenerateName (city names), ValidateName, ExtractBaseCity, IsCityName
    status.go           # IsDirty, BranchName
  github/
    gh.go               # LookupPR (by head) / LookupPRByNumber, head-then-number resolution policy
    cache.go            # PR status cache — read snapshot + Mutate/TryMutate (the only writer)
    poll.go             # PollRepo (conditional REST poll per repo), RepoRefFromRemote, rate-limit parsing
    sync.go             # Sync — one fetch round: polls, fallback lookups, open-PR detail refresh
    create.go           # RecordCreatedPR — cache a PR straight from `gh pr create` output
  sandbox/
    nono.go             # BuildNonoArgs(path, config.Model) → []string
  setup/
    checks.go           # RunChecks — shared check engine for init/doctor
    profile.go          # Profile — nono profile generator shared by both init commands
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

Paths follow XDG on every platform (`pkg/xdg`: the env var when set to an absolute path, else the spec default — never `os.UserConfigDir`, which is `~/Library/Application Support` on darwin). Every path is built in `pkg/config/paths.go` (workbench) (supatree builds its own in its repo); don't join a path to `$HOME` anywhere else. The two tools share **no** file on disk.
- `~/.config/workbench/config.yml` — repos, worktrees, model definitions
- `~/.local/state/workbench/state.yml` — last-run version, update check cache, gamification stats (cities_visited, worktrees_created, worktrees_merged, achievements, activity_days), and `reserved_cities` (names still occupied by a worktree or its lingering Claude history, excluded from name generation); `logs/` and `config.lock` beside it
- `~/.cache/workbench/agent/pr-status.json` — the PR cache, in the one cache subdir an agent's profile is granted (`AgentCacheDir`)
- `~/.cache/workbench/layouts/<name>.kdl` — generated Zellij layouts (transient). Never grant these to a sandbox: zellij runs them unsandboxed.
- `~/workbench/<alias>/<name>/` — default worktree location

**Old layout.** `config.OldLayout()` (`~/.workbench` exists, XDG config does not) makes every command except `migrate`/`version`/`doctor`/`help` exit with `OldLayoutMessage`; the MCP server refuses per tool call instead (`mcp.Server.WithPrecondition`), so an agent's session survives.

**Tests must isolate all of HOME and XDG.** Use `testutil.IsolateHome(t)`, never `t.Setenv("HOME", …)` alone — a developer's exported `XDG_CONFIG_HOME` would win and the test would write into the real `~/.config`. Every test package also has a `TestMain` calling `testutil.IsolateProcess()` as a backstop, and both e2e scripts export all three XDG vars.

`models` is an open map — users add arbitrary entries (`mymodel`) with any `binary`/`nono_profile`/`args`. Never hardcode model names.

`config.Load()` reads from disk every time. `config.Save()` writes atomically via temp+rename. `show_stats` (`*bool`, default true) controls the gamification stats box in the TUI sidebar.

## TUI model

`pkg/tui/model.go` is the Bubble Tea root model. Key patterns:

**Cursor skips repo headers** — `moveUp`/`moveDown` in `tree.go` consult `selectable()`, which skips `isRepo` items *except* for a collapsed repo: that header is the only row the repo has, so it must take the cursor. Without the exception, folding a repo strands the cursor in the next one and `zM` (fold all) leaves nothing selectable at all. Empty repos get a selectable placeholder row. Every fold path (`toggleCollapse`, `collapseContaining`, `expandContaining`, `setAllCollapsed`) parks the cursor via `focusRepo`, not bare `clamp`.

**Sidebar navigation and viewport** — `tree.go` mirrors supatree's sidebar (`pkg/supatree/tui` in its repo): `moveBy` (used by `ctrl+d`/`ctrl+u` via `halfPage`), `gotoTop`/`gotoBottom` (`gg`/`G`), `jumpRepo` (`}`/`{`), `setAllCollapsed` (`zM`/`zR`), and the two-key sequences driven by `Model.pending` (an unrecognized second key clears the prefix and falls through, so a mistyped `g` never swallows a command). `tree.view(width, avail)` renders only the `viewport` window — `Model.View` renders `viewTail()` first and sizes `avail` from its line count. The mouse wheel calls `scrollBy`, which pans `scroll` and clears `follow`; `viewport` drags the scroll offset to the cursor only while `follow` is set, so a wheel-scrolled view survives the 60s tick. Deliberate cursor moves re-arm `follow`; `clamp` deliberately does not.

**Refresh reloads from disk** — `refreshMsg` calls `config.Load()` and replaces both `m.cfg` and `m.tree.cfg`. Don't just call `refreshDirty()` alone.

**GitHub quota discipline** — a round costs one *conditional* request per **repo**, not one per branch, and both sidebars go through the same entry point: `github.Sync(w, targets, opts)`, called inside `Cache.TryMutate`. The sidebars only decide *which branches are on screen*; everything else is Sync's job. Key properties, each of them load-bearing:

- `PollRepo` sends the stored `ETag`; an unchanged repo answers **304, which GitHub does not charge**. A quiet round therefore costs nothing. Measured cold on 14 repos / 67 branches: 15 requests; every round after that: 0.
- It uses the REST **`core`** bucket. The **GraphQL** bucket is what agents drain with `gh pr view`/`pr checks`, and it is routinely exhausted while `core` is untouched — which is why the per-branch fallback is `LookupBranchPR` (REST) and not `LookupPR` (`gh pr list`, GraphQL). Only a non-github.com remote falls back to the latter.
- **Never trust `gh api rate_limit`.** Its graphql row reported `remaining: 5000` while response headers said `used: 5001`. Read `X-RateLimit-*` off a response, or query `rateLimit{}` inside a GraphQL document.
- `gh api -i` **exits non-zero on 304 and 404**. `parsePollResponse` classifies the status from stdout and only falls back to the exit status when there is no parsable response (`errNoResponse`) — keying off the exit code alone turns every free 304 into a re-fetch.
- A 304 says *unchanged*, not *complete*: `RepoState.Truncated` is remembered across rounds, because absence from a listing only proves "no PR" when the listing covered the repo's whole history (`RepoPoll.Complete`). Otherwise the branch goes to a per-branch lookup.
- A repo gh cannot see (a 404 from the poll, or `ErrRepoNotFound` — "Could not resolve to a Repository" — from a fallback lookup: moved, renamed, or no access for the active account) is backed off as a whole via `RepoState.UnavailableUntil` for `UnreachableBackoff` (6h), persisted so every poller honours it; a forced refresh still tries. It must not abort the round, and it must not be retried every round either — that once filled `watch.log` with 2,800 identical lines and spent ~700 GraphQL calls an hour. Non-GitHub remotes are grouped (and backed off) per checkout path.
- Rate limits pause until the **reported** reset — `X-RateLimit-Reset` for the primary quota, `Retry-After` for the secondary/burst limit (which trips with thousands of quota left and carries no quota headers — see `rateLimitFrom`) — and they pause **only the bucket that ran out**. `Cache.RetryAfter` is keyed by resource: an exhausted `graphql` bucket must not stop the conditional `core` polls, which cost nothing. `ResourceAll` is the key for the burst limit, which really does apply to everything. Getting this wrong is not theoretical: it shipped that way, and one GraphQL exhaustion paused fifteen minutes of free polling across every repo.
- Resolve a repo's owner/name with `github.RepoRefFromRemote`, never `ParseRemoteURL` alone. Remotes routinely go through an ssh config alias (`git@github-work:owner/repo.git`), which is not literally github.com but resolves to it; `ssh -G <host>` is ssh's own parser, runs locally and costs nothing (hand-reading `~/.ssh/config` would miss Include, wildcards and Match). A remote that fails to resolve silently falls back to the per-branch GraphQL path — the expensive one — so this is worth getting right.
- Group by repo, not by checkout: supatree gives each tree its own checkout of the same repo, and grouping by path polled `admin-frontend` six times a round.
- Every response (304s included) carries `X-RateLimit-*`, so `Budget` is observed for free and persisted in the shared cache. Conditional polls are not gated on it — they cost nothing — but the per-branch fallback lookups are: below `DefaultReserve` (500) a background round defers them and reports `Deferred`, which the sidebars show as `gh quota low`. A forced refresh drops to `ForcedReserve` (50), because the person waiting outranks the background.
- `create_pr` writes the PR it just made straight into the cache (`github.RecordCreatedPR`, parsing the URL out of `gh pr create` output). No invalidation is needed beyond that: creating a PR changes the repo's PR list, so the next poll's ETag no longer matches and the listing refreshes itself.
- **Review and checks are the one GraphQL cost left.** The REST listing carries no `reviewDecision` or check rollup, and a check run finishing does not even change its ETag. So a poll carries `Review`/`Checks` over from the previous entry for the same PR (`mergePolled` — blanking them would flap badges and fire spurious check events), and `refreshDetails` re-reads them with one `gh pr view` per **open** PR when `PRInfo.DetailedAt` is older than `MaxAge`, or at once when a poll saw the head or `updated_at` move. Merged, closed and PR-less branches never reach it. It is gated on the `graphql` backoff and a rate limit there pauses only `graphql`.
- The supatree MCP tools (`pr_status`, the `create_pr` dependency check) go through `syncMembers` → `github.Sync` too, so an agent's tool call costs one conditional poll per repo rather than a GraphQL request per member. Nothing outside `pkg/github` calls `LookupPR` any more.

**A PR is identified by number, not by branch.** A head lookup only finds a PR whose head ref is *currently* that branch, and a rename retargets the head only for a PR still open when the new branch is pushed — one that merged or closed first is frozen on the old name, as is one whose push failed or never ran. So `Writable.Rename` carries the entry's `Number` but zeroes `FetchedAt` (a status verified under the old key must not be trusted, nor held for `TerminalMaxAge`), and `Sync` matches a polled page by head first — a branch may have picked up a *new* PR — then by the cached number (`matchPolled`). A per-branch fallback does the same through `resolvePR`: head lookup, then `gh pr view <n>` when that comes back empty and a number is known. A number is checked against the cached URL's repo before it is trusted — the cache is keyed on branch name alone, so two repos with the same branch name share one entry. An entry that was never verified under its key (zero `FetchedAt`: renamed, or seeded by a review tree) is never "confirmed" by a poll that lacks it; it is looked up.

**The PR cache has exactly one writer path** — `github.Cache.Mutate` / `TryMutate`, which flock, re-read the file, apply the callback, and write atomically. There is deliberately no exported `Save`: a whole-file write from a process-local snapshot silently reverts every entry another process wrote since it loaded, *including* the `retry_after` cooldown, which is how one `pr_status` call used to un-pause every sidebar on a drained quota. `Writable.SetRetryAfter` is monotonic — it can extend a cooldown, never shorten or clear one. Mutations must not be held across slow work: `supatree/rename.go` and `remove.go` collect their changes and apply them in a single mutation *after* the pushes and cleanup scripts, so a sidebar's round never queues behind them. `LookupPR` has a 20s timeout because the fetch round holds the lock across it.

**Local re-sync on focus/tick** — `reloadLocalState()` re-reads `config.yml` + `state.yml` and swaps `m.cfg`/`m.tree.cfg`/`m.state` on `tea.FocusMsg` and every `tickMsg`, so sidebars in different tabs stay consistent without a manual `r`. It deliberately skips the network PR fetch (that's what the full `refreshMsg` is for). It's a no-op while `m.mode != modeNormal` or a create is in flight (`m.creating`), because reloading would either shift the `pendingRepoIdx`/`pendingWorktreeIdx` slice indices under an open confirm/input mode or drop an optimistic worktree that isn't persisted yet.

**Inline input mode** — the model has an `inputMode` state machine (`modeNormal` / `modeAddRepoPath` / `modeAddRepoAlias` / `modeNewWorktree` / `modeConfirmDelete` / `modeConfirmQuit` / `modeOpenWith` / `modeHelp`). When mode is non-normal, `Update` routes `tea.KeyMsg` to `updateInput()` which handles `enter`/`esc` and passes everything else to the `textinput.Model`. Use this same pattern for any future inline prompts.

**Key bindings** — defined in `pkg/tui/keys.go`. Add new bindings to both `KeyMap` struct and `DefaultKeyMap`, then handle in `model.go`'s `Update` switch.

**Sidebar mode** — when `WORKBENCH_SIDEBAR=1` is set (injected by the layout), `q` prompts for confirmation instead of quitting immediately. The layout wraps `workbench ls` in a restart loop with backoff (`sleep 0.2` on success, `sleep 2` on failure).

**Env injection** — `WriteTabLayout` in `layout.go` accepts a `map[string]string` of env vars to inject into the agent pane's KDL `env {}` block. Keys are sorted for deterministic output. The **sidebar** pane also gets `WORKBENCH_WORKTREE_NAME=<name>` (the tab's worktree); the TUI reads it in `New()` into `tree.activeWorktree` to render the passive `▸` "you are here" marker (see `tree.go` `view`). The root session sidebar has no such env, so `activeWorktree` is empty and no marker shows.

**Pane vs tab naming** — the agent pane's *display* name is `{repo}/{worktree}` (derived from `WORKBENCH_REPO_ALIAS` in the injected env, KDL-escaped via `quoteKDL` since aliases aren't charset-validated). The Zellij *tab* name and the layout filename stay the bare worktree name — that name keys tab lookups (`OpenOrFocusTab`, `TabNames`, the TUI `openTabs` map) and is a path component, so it must remain slash-free and validated. `WriteTabLayout` validates the worktree name up front with `git.ValidateName`.

## Zellij session management

`workbench start` embeds a session layout via `go:embed` (`session.kdl.tmpl`), writes it to `~/.cache/workbench/layouts/session-<name>.kdl`, and uses `syscall.Exec` to hand the terminal to zellij. Sessions are prefixed with `wb-`.

`pkg/zellij/session.go` provides `ListSessions`, `WriteSessionLayout`, `CreateBackgroundSession`, `DeleteSession`.

Layout/session writing hangs off a `zellij.Workspace` (`pkg/zellij/workspace.go`) that carries the tool-specific bits (`LayoutsDir`, `SidebarCommand`, `SidebarEnvVar`, `SidebarActiveEnvVar`, `SessionPrefix`, `SessionTab`). `WriteTabLayout`, `OpenTab`, `OpenOrFocusTab`, `CleanupLayout`, `CleanupStaleLayouts`, `WriteSessionLayout` are methods on it; `WorkbenchWorkspace()` / supatree's own workspace supply the values. Pure `zellij action` wrappers (`GoToTab`, `TabNames`, `ListSessions`, `NewPane`, ...) stay free functions. `NewPane` is the one pane-level call: it opens an unsandboxed shell at a cwd in the caller's current tab, with no layout and no tab name, for "take me to this directory" actions that must not claim an identity in the tab namespace agents are keyed on. Two zellij quirks are baked into it: `new-pane` splits the *focused* pane — which is the sidebar that asked — so it steps the focus right first (best effort) and splits `--direction right`, landing the shell in the main area beside the agent instead of stacked under the sidebar; and `--cwd` is honoured only for a pane that runs a command, so the shell (`$SHELL`, else `bash`) is passed explicitly with `--close-on-exit`. A bare `new-pane --cwd <dir>` silently opens in the session's cwd.

`pkg/zellij/client.go` provides tab-level operations (`OpenTab`, `GoToTab`, `OpenOrFocusTab`). These call `zellij action` subcommands and only work inside a Zellij session.

**Never close a tab by focusing it.** `zellij action close-tab` closes the *client's current tab*, and closing a tab kills every process in it — including the sidebar that asked for the close. That is why `OpenOrFocusTab` replaces a tab whose agent has exited by creating the replacement **first** and only then closing the husk via `close-tab-by-id` (ids come from `TabIDs`): the caller may well be that tab's own sidebar, and it dies on the close, so nothing may be left to do afterwards. The old close-then-create order silently did nothing at all when an agent was reopened from its own tab's sidebar. `closeTab` (by focus) survives only as the fallback for when an id cannot be resolved.

Note that `zellij action dump-layout` reports a tab as *empty* once its `close_on_exit=true` command pane has exited, even though the sidebar pane is still running — so `tabHasCommandPane` answers "no agent here", which is what triggers the replacement path.

## nono sandbox

`BuildNonoArgs` returns `["run", "--profile", <profile>, "--allow", <worktreePath>, "--", <binary>, <args...>]`. The profile and binary come from the model config entry — no hardcoded mapping. Every `pkg/sandbox` builder takes a resolved `config.Model`, never a whole `Config`: each tool resolves the key against its own config (`config.Config.Model`, `supatree.Config.Model`), so the shared package never knows whose config a model came from. The built-in entries live in `config.DefaultModels()`, which both tools seed from.

`workbench init` writes the `claude-code-local` profile through `setup.Profile` (`pkg/setup/profile.go`), which also adds the toolchain grants (`WithToolchain`). Each tool writes only its own profile.

## Adding features

- **New inline TUI action**: add key to `keys.go`, add `inputMode` constants if needed, handle in `model.go` `Update` and `updateInput`.
- **New CLI command**: add file under `cmd/`, wire into `rootCmd` in `cmd/root.go` via `rootCmd.AddCommand(...)` in `init()`.
- **New MCP tool**: `pkg/mcp` is a reusable framework — `rpc.go` has the JSON-RPC `Server{Name,Version,Tools,Prompts,Gate}` + stdio loop; `workbench.go` builds the workbench tool set. Add workbench tools there; supatree builds its own tool set in its repo. Input schemas are built with the shared helpers in `schema.go` (`ObjectSchema`/`StringProp`/`BoolProp`/`EnumProp`/`EmptyObject`) — both tool sets use them, so don't hand-roll the map literals. Tool handlers have signature `func(args map[string]any) (text string, isError bool)`.
- **New config field**: add to structs in `pkg/config/config.go`, update `DefaultConfig()` if it needs a default.
- **Worktree creation**: `config.CopyFiles(repo.LocalPath, wt, repo.CopyFiles)` copies gitignored files (a `.env`, mostly) from the clone's checkout; a missing file is returned, warned about, and skipped. There are no per-repo startup or cleanup hooks.
- **Change what opens in a new tab**: edit the KDL template in `pkg/zellij/layout.go`.

**Always update `README.md`** when adding or changing user-facing behavior: new config fields, new CLI flags, new keybindings, changed lifecycle behavior, or nono sandbox requirements.

## Supatree

Supatree lives in its own repo, github.com/panamafrancis/supatree, and imports
these `pkg/*` packages; nothing here imports it. **The shared packages are not
a stable public API, but supatree is their one outside consumer**: a change to
one is a two-PR change — this repo first, then supatree's `go get
github.com/panamafrancis/workbench@<sha>` bump that adapts to it. For local
development across both, use a `go.work` that `use`s both checkouts.

## Before pushing / creating a PR

Always run `make ci` (fmt + lint + vet + test) and fix all issues before pushing. The linter (`golangci-lint`) enforces errcheck, exhaustive switch, noctx (use `exec.CommandContext`), prealloc, staticcheck, and more — do not suppress warnings, fix them.
