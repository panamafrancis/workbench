# workbench — Agent Guide

Sandboxed git worktree manager. Each piece of work gets a git worktree opened as a new Zellij tab, with the chosen LLM running inside a nono security sandbox.

## Build & run

```sh
make build          # produces ./workbench binary
make install        # copies to /usr/local/bin/workbench
make ci             # fmt + lint + vet + test
make e2e            # build + run scripts/e2e.sh
make hooks          # enroll .githooks/ as git hooks
go test ./...
```

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
  init.go               # workbench init — setup wizard
  doctor.go             # workbench doctor — dependency checks
  uninstall.go          # workbench uninstall
  version.go            # workbench version (Version var stamped via ldflags)
pkg/
  config/
    config.go           # Config types, Load/Save, FindRepo, FindWorktree, CRUD helpers
    paths.go            # ConfigDir, ConfigPath, StatePath, LayoutsDir, WorktreePath
    state.go            # State (last_run_version, update check cache), LoadState/Save
  git/
    worktree.go         # DefaultBranch, FetchOrigin, CreateWorktree (returns offline bool), RemoveWorktree
    names.go            # GenerateName (adj-city), ValidateName
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
plugin/                 # Claude Code plugin (slash commands + skills)
  manifest.json
  commands/             # /workbench:rename-branch, /workbench:pr
  skills/               # workbench-conventions (gated on WORKBENCH env var)
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
- `~/.workbench/state.yml` — last-run version, update check cache
- `~/.workbench/worktrees/<alias>/<name>/` — default worktree location
- `~/.workbench/layouts/<name>.kdl` — generated Zellij layouts (transient)

`models` is an open map — users add arbitrary entries (`mymodel`) with any `binary`/`nono_profile`/`args`. Never hardcode model names.

`config.Load()` reads from disk every time. `config.Save()` writes atomically via temp+rename.

## TUI model

`pkg/tui/model.go` is the Bubble Tea root model. Key patterns:

**Cursor skips repo headers** — `moveUp`/`moveDown` in `tree.go` skip `isRepo` items. The cursor only rests on worktree rows or placeholder rows. Empty repos get a selectable placeholder row.

**Refresh reloads from disk** — `refreshMsg` calls `config.Load()` and replaces both `m.cfg` and `m.tree.cfg`. Don't just call `refreshDirty()` alone.

**Inline input mode** — the model has an `inputMode` state machine (`modeNormal` / `modeAddRepoPath` / `modeAddRepoAlias` / `modeNewWorktree` / `modeConfirmDelete` / `modeConfirmQuit` / `modeOpenWith` / `modeHelp`). When mode is non-normal, `Update` routes `tea.KeyMsg` to `updateInput()` which handles `enter`/`esc` and passes everything else to the `textinput.Model`. Use this same pattern for any future inline prompts.

**Key bindings** — defined in `pkg/tui/keys.go`. Add new bindings to both `KeyMap` struct and `DefaultKeyMap`, then handle in `model.go`'s `Update` switch.

**Sidebar mode** — when `WORKBENCH_SIDEBAR=1` is set (injected by the layout), `q` prompts for confirmation instead of quitting immediately. The layout wraps `workbench ls` in a restart loop with backoff (`sleep 0.2` on success, `sleep 2` on failure).

**Env injection** — `WriteTabLayout` in `layout.go` accepts a `map[string]string` of env vars to inject into the agent pane's KDL `env {}` block. Keys are sorted for deterministic output.

## Zellij session management

`workbench start` embeds a session layout via `go:embed` (`session.kdl.tmpl`), writes it to `~/.workbench/layouts/session-<name>.kdl`, and uses `syscall.Exec` to hand the terminal to zellij. Sessions are prefixed with `wb-`.

`pkg/zellij/session.go` provides `ListSessions`, `WriteSessionLayout`, `CreateBackgroundSession`, `DeleteSession`.

`pkg/zellij/client.go` provides tab-level operations (`OpenTab`, `GoToTab`, `OpenOrFocusTab`). These call `zellij action` subcommands and only work inside a Zellij session.

## nono sandbox

`BuildNonoArgs` returns `["run", "--profile", <profile>, "--allow", <worktreePath>, "--", <binary>, <args...>]`. The profile and binary come from the model config entry — no hardcoded mapping.

## Adding features

- **New inline TUI action**: add key to `keys.go`, add `inputMode` constants if needed, handle in `model.go` `Update` and `updateInput`.
- **New CLI command**: add file under `cmd/`, wire into `rootCmd` in `cmd/root.go` via `rootCmd.AddCommand(...)` in `init()`.
- **New config field**: add to structs in `pkg/config/config.go`, update `DefaultConfig()` if it needs a default.
- **Change what opens in a new tab**: edit the KDL template in `pkg/zellij/layout.go`.

**Always update `README.md`** when adding or changing user-facing behavior: new config fields, new CLI flags, new keybindings, changed lifecycle behavior, or nono sandbox requirements.

## Before pushing / creating a PR

Always run `make ci` (fmt + lint + vet + test) and fix all issues before pushing. The linter (`golangci-lint`) enforces errcheck, exhaustive switch, noctx (use `exec.CommandContext`), prealloc, staticcheck, and more — do not suppress warnings, fix them.
