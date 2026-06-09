# workbench — Agent Guide

Sandboxed git worktree manager. Each piece of work gets a git worktree opened as a new Zellij tab, with the chosen LLM running inside a nono security sandbox.

## Build & run

```sh
make build          # produces ./workbench binary
make install        # copies to /usr/local/bin/workbench
make setup          # copies scripts/wb.kdl to ~/.config/zellij/layouts/wb.kdl
make ci             # fmt + vet + test + build
go test ./...
```

Start the Zellij session with the sidebar:
```sh
zellij --layout ~/.config/zellij/layouts/wb.kdl
```

## Real file layout

The plan in `plans/workbench.md` references `internal/` but the code uses `pkg/`:

```
cmd/                    # Cobra commands (root, add, rm, ls, open)
pkg/
  config/
    config.go           # Config types, Load/Save, FindRepo, FindWorktree, CRUD helpers
    paths.go            # ConfigDir, ConfigPath, DefaultWorktreeBase, LayoutsDir, WorktreePath
  git/
    worktree.go         # CreateWorktree, RemoveWorktree
    names.go            # GenerateName (adj-city), ValidateName
    status.go           # IsDirty, BranchName
  sandbox/
    nono.go             # BuildNonoArgs(path, modelKey, cfg) → []string
  tui/
    model.go            # Root Bubble Tea model — start here
    tree.go             # Collapsible repo→worktree tree component
    keys.go             # KeyMap + DefaultKeyMap
    styles.go           # Lipgloss palette (styleMuted, styleDirty, styleSelected, ...)
  zellij/
    client.go           # IsInZellij, OpenTab, GoToTab
    layout.go           # WriteTabLayout → writes ~/.workbench/layouts/<name>.kdl
scripts/
  wb.kdl                # Zellij session layout (sidebar + empty shell pane)
```

## Config

All state lives under `~/.workbench/`:
- `~/.workbench/config.yml` — repos, worktrees, model definitions
- `~/.workbench/worktrees/<alias>/<name>/` — default worktree location
- `~/.workbench/layouts/<name>.kdl` — generated per-worktree Zellij layouts (transient)

`models` is an open map — users add arbitrary entries (`mymodel`) with any `binary`/`nono_profile`/`args`. Never hardcode model names.

`config.Load()` reads from disk every time. `config.Save()` writes atomically via temp+rename.

## TUI model

`pkg/tui/model.go` is the Bubble Tea root model. Key patterns:

**Refresh reloads from disk** — `refreshMsg` calls `config.Load()` and replaces both `m.cfg` and `m.tree.cfg`. Don't just call `refreshDirty()` alone.

**Inline input mode** — the model has an `inputMode` state machine (`modeNormal` / `modeAddRepoPath` / `modeAddRepoAlias`). When mode is non-normal, `Update` routes `tea.KeyMsg` to `updateInput()` which handles `enter`/`esc` and passes everything else to the `textinput.Model`. Use this same pattern for any future inline prompts (e.g. new worktree name, delete confirm).

**Key bindings** — defined in `pkg/tui/keys.go`. Add new bindings to both `KeyMap` struct and `DefaultKeyMap`, then handle in `model.go`'s `Update` switch.

Current bindings: `j/k` nav, `enter/o` open, `space/tab` collapse, `r` refresh, `A` add repo, `n` new worktree, `d` delete, `q/esc` quit.

## Zellij tab layout

`WriteTabLayout` in `pkg/zellij/layout.go` generates a KDL file for each opened worktree. The layout is a horizontal split with:
- left: 36-char `workbench ls` sidebar (stays sticky across all tabs)
- right: `nono <args>` pane focused on the worktree cwd

Adding or changing what appears in the worktree tab means editing the KDL template in `WriteTabLayout`.

`OpenTab` calls `zellij action new-tab --name <name> --cwd <path> --layout <kdl>`. This only works inside a Zellij session (`IsInZellij()` checks `$ZELLIJ`).

## nono sandbox

`BuildNonoArgs` returns `["run", "--profile", <profile>, "--allow", <worktreePath>, "--", <binary>, <args...>]`. The profile and binary come from the model config entry — no hardcoded mapping.

## Adding features

- **New inline TUI action**: add key to `keys.go`, add `inputMode` constants if needed, handle in `model.go` `Update` and `updateInput`.
- **New CLI command**: add file under `cmd/`, wire into `rootCmd` in `cmd/root.go` via `rootCmd.AddCommand(...)` in `init()`.
- **New config field**: add to structs in `pkg/config/config.go`, update `DefaultConfig()` if it needs a default.
- **Change what opens in a new tab**: edit the KDL template in `pkg/zellij/layout.go`.
