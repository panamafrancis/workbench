# workbench — Implementation Plan

## Context

A sandboxed git worktree manager for developers. Each piece of work (PR/feature) gets its own git worktree running in a nono security sandbox. A persistent Zellij pane shows the TUI sidebar; selecting a worktree opens a new Zellij tab with the chosen LLM (claude, codex, opencode, dirac) sandboxed via nono.

Inspired by Conductor.build but with real process isolation rather than yolo execution.

**Why Zellij over Ghostty:** Ghostty has no programmatic tab API on macOS. Zellij exposes `zellij action new-tab --layout <kdl>` which opens a new tab running any command — clean and zero shell-hook hackery required.

---

## Tech Stack

| Layer | Choice | Rationale |
|---|---|---|
| Language | Go 1.26.2 | Already installed |
| TUI | Bubble Tea + Lipgloss + Bubbles | Best mouse+keyboard+tree support in Go |
| CLI | Cobra | Standard Go CLI framework |
| Config | gopkg.in/yaml.v3 | Human-readable YAML; simpler than TOML for nested model config |
| Sandbox | nono (`claude-code` profile) | Pre-configured for Anthropic; at `/opt/homebrew/bin/nono` |
| Multiplexer | Zellij 0.43.1 | Programmatic tab/pane control via `zellij action` |

---

## CLI Commands

```
workbench add repo <local-path> --alias=ss         # register repo with alias
workbench rm  repo <alias>                          # unregister repo + remove worktrees from config

workbench add worktree --repo=ss [--name=atlanta]  # create worktree (auto-names if omitted)
workbench rm  worktree <name>                       # remove worktree + git cleanup

workbench ls [--repo=ss]                            # list worktrees (plain text or TUI)

workbench open --repo=ss --worktree=atlanta [--model=claude]  # open in new Zellij tab
```

`add` and `rm` are Cobra command groups; `repo` and `worktree` are their subcommands.

---

## Project Structure

```
workbench/
├── go.mod                          # module github.com/panamafrancis/workbench
├── main.go
├── cmd/
│   ├── root.go                     # cobra root; config loading into context
│   ├── add.go                      # workbench add (parent, no-op)
│   ├── add_repo.go                 # workbench add repo
│   ├── add_worktree.go             # workbench add worktree
│   ├── rm.go                       # workbench rm (parent, no-op)
│   ├── rm_repo.go                  # workbench rm repo
│   ├── rm_worktree.go              # workbench rm worktree
│   ├── ls.go                       # workbench ls → launches Bubble Tea TUI
│   └── open.go                     # workbench open → writes KDL + zellij action new-tab
├── pkg/
│   ├── config/
│   │   ├── config.go               # Config types; Load/Save; CRUD helpers
│   │   └── paths.go                # ConfigPath(); DefaultWorktreeBase(); WorktreePath()
│   ├── git/
│   │   ├── worktree.go             # CreateWorktree, RemoveWorktree, ListGitWorktrees
│   │   ├── status.go               # BranchName, IsDirty, HeadSHA
│   │   └── names.go                # GenerateName (adjective+city), ValidateName
│   ├── sandbox/
│   │   └── nono.go                 # BuildNonoArgs(worktreePath, modelKey, cfg) []string
│   ├── zellij/
│   │   ├── client.go               # IsInZellij(); OpenTab(); GoToTab()
│   │   └── layout.go               # WriteTabLayout(name, cwd, nonoArgs) (path, error)
│   └── tui/
│       ├── model.go                # Root Bubble Tea model
│       ├── tree.go                 # Collapsible repo→worktree tree component
│       ├── keys.go                 # KeyMap
│       └── styles.go               # Lipgloss color palette
├── scripts/
│   └── wb.kdl                      # Zellij layout: sidebar pane running `workbench ls`
└── plans/
    └── workbench.md                 # this file
```

**Go dependencies:** `spf13/cobra`, `charmbracelet/bubbletea`, `charmbracelet/bubbles`, `charmbracelet/lipgloss`, `gopkg.in/yaml.v3`, `golang.org/x/sync`

---

## Config (`~/.workbench/config.yml`)

All workbench state lives under `~/.workbench/`:
- `~/.workbench/config.yml` — main config
- `~/.workbench/worktrees/{alias}/{name}/` — default worktree location
- `~/.workbench/layouts/{name}.kdl` — generated Zellij layouts (transient)

### Example config

```yaml
version: 1
default_model: claude
worktree_base: ""          # empty = ~/.workbench/worktrees/
default_zellij_layout: ""  # empty = no layout, plain zellij session

models:
  claude:
    nono_profile: claude-code
    binary: claude
    args: []
  codex:
    nono_profile: default
    binary: codex
    args: []
  opencode:
    nono_profile: default
    binary: opencode
    args: []
  dirac:
    nono_profile: default
    binary: dirac
    args: []
  shell:
    nono_profile: default
    binary: bash
    args: []

repos:
  - alias: ss
    local_path: /Users/stefan/code/fraud-zero/scoring-service
    startup_script: ""         # path to script run before opening a worktree in this repo
    cleanup_script: ""         # path to script run when removing a worktree
    startup_instructions: ""   # freeform text; passed to the LLM as initial context (future)
    worktrees:
      - name: atlanta
        branch: wt/ss/atlanta
        path: /Users/stefan/.workbench/worktrees/ss/atlanta
        created_at: 2026-01-16T09:00:00Z
        model: claude
```

### Go types (`pkg/config/config.go`)

```go
type Config struct {
    Version           int                `yaml:"version"`
    DefaultModel      string             `yaml:"default_model"`
    WorktreeBase      string             `yaml:"worktree_base"`
    DefaultZellijLayout string           `yaml:"default_zellij_layout"`
    Models            map[string]Model   `yaml:"models"`
    Repos             []Repo             `yaml:"repos"`
}

type Model struct {
    NonoProfile string   `yaml:"nono_profile"`
    Binary      string   `yaml:"binary"`
    Args        []string `yaml:"args"`
}

type Repo struct {
    Alias                string     `yaml:"alias"`
    LocalPath            string     `yaml:"local_path"`
    StartupScript        string     `yaml:"startup_script"`
    CleanupScript        string     `yaml:"cleanup_script"`
    StartupInstructions  string     `yaml:"startup_instructions"`
    Worktrees            []Worktree `yaml:"worktrees"`
}

type Worktree struct {
    Name      string    `yaml:"name"`
    Branch    string    `yaml:"branch"`
    Path      string    `yaml:"path"`
    CreatedAt time.Time `yaml:"created_at"`
    Model     string    `yaml:"model"`
}
```

### `pkg/config/paths.go`

```go
func ConfigDir() string          // ~/.workbench/
func ConfigPath() string         // ~/.workbench/config.yml
func DefaultWorktreeBase() string // ~/.workbench/worktrees/
func WorktreePath(base, alias, name string) string
func LayoutsDir() string         // ~/.workbench/layouts/
```

No XDG — `~/.workbench/` is the single source of truth.

---

## Worktree Storage

Default tree: `~/.workbench/worktrees/<repo-alias>/<worktree-name>`

```
~/.workbench/worktrees/ss/atlanta/
~/.workbench/worktrees/ss/oslo/
~/.workbench/worktrees/pay/kyoto/
```

Override with `worktree_base` in config.

---

## Worktree Naming

`workbench add worktree --repo=ss` (no `--name`): auto-generates `<adjective>-<city>` (e.g. `swift-atlanta`). Names are unique globally across all repos — they serve as Zellij tab titles and must be unambiguous in `workbench open`.

`workbench add worktree --repo=ss --name=atlanta`: validates lowercase alphanumeric + hyphens, max 24 chars, globally unique.

Default branch: `wt/<alias>/<name>` (e.g. `wt/ss/atlanta`). Override with `--branch`.

---

## nono Sandboxing (`internal/sandbox/nono.go`)

Model config drives nono — no hardcoded profile map.

```go
// Returns args slice for: nono <args...>
// e.g. ["run", "--profile", "claude-code", "--allow", "/path", "--", "claude"]
func BuildNonoArgs(worktreePath string, modelKey string, cfg *config.Config) ([]string, error) {
    m, ok := cfg.Models[modelKey]
    if !ok {
        return nil, fmt.Errorf("unknown model %q", modelKey)
    }
    args := []string{"run", "--profile", m.NonoProfile, "--allow", worktreePath, "--"}
    args = append(args, m.Binary)
    args = append(args, m.Args...)
    return args, nil
}
```

---

## Startup / Cleanup Scripts

When `workbench open` is called, if the repo has a `startup_script`, it is run in the worktree directory before launching nono. If `workbench rm worktree` is called and the repo has a `cleanup_script`, it runs before the git worktree is removed.

Scripts receive the worktree path as `$WORKBENCH_WORKTREE_PATH` and the worktree name as `$WORKBENCH_WORKTREE_NAME`.

```go
// internal/config helpers
func (r *Repo) RunStartup(worktreePath, worktreeName string) error
func (r *Repo) RunCleanup(worktreePath, worktreeName string) error
```

---

## Zellij Integration

### How it works

```
workbench open --repo=ss --worktree=atlanta --model=claude
  1. BuildNonoArgs(path, "claude", cfg) → ["run", "--profile", "claude-code", "--allow", "/path", "--", "claude"]
  2. repo.RunStartup(path, "atlanta")
  3. WriteTabLayout → writes ~/.workbench/layouts/atlanta.kdl
  4. zellij action new-tab --name "atlanta" --cwd "/path" --layout ~/.workbench/layouts/atlanta.kdl
```

### `pkg/zellij/layout.go`

```kdl
layout {
    pane name="atlanta" cwd="/Users/stefan/.workbench/worktrees/ss/atlanta" {
        command "nono"
        args "run" "--profile" "claude-code" "--allow" "/path" "--" "claude"
    }
}
```

### `pkg/zellij/client.go`

```go
func IsInZellij() bool
func OpenTab(name, cwd string, nonoArgs []string) error
func GoToTab(name string) error
func CloseTab() error
```

Error when not in Zellij:
```
workbench: not running inside a Zellij session.
Start a session with: zellij --layout ~/.config/zellij/layouts/wb.kdl
```

### Sidebar layout (`scripts/wb.kdl`)

```kdl
layout {
    tab name="workbench" focus=true {
        pane split_direction="horizontal" {
            pane size="32" name="sidebar" {
                command "workbench"
                args "ls"
            }
            pane name="shell" focus=true
        }
    }
}
```

If `default_zellij_layout` is set in config, `workbench open` passes `--layout` to the initial session too.

---

## TUI Layout

```
┌─────────────────────────────────────────────────┐
│ workbench                            [?] help    │
├─────────────────────────────────────────────────┤
│ ▼ scoring-service (ss)                          │
│   ● atlanta    wt/ss/atlanta-rules  [claude]    │
│   ● oslo       wt/ss/oslo-widget    [codex]     │
│ ▶ payments-api (pay)   [2 worktrees]            │
│                                                 │
├─────────────────────────────────────────────────┤
│ [n]ew  [o]pen  [d]el  [r]efresh  [q]uit        │
└─────────────────────────────────────────────────┘
```

No PR/CI badges — GitHub integration removed.

### Key bindings

| Key | Action |
|---|---|
| j / ↑ | Move up |
| k / ↓ | Move down |
| Enter / o | Open selected worktree (last-used or default model) |
| O | Open with model picker overlay |
| Space / Tab | Collapse/expand repo |
| n | New worktree (inline prompt for alias + name) |
| d | Delete worktree (confirmation required) |
| r | Refresh dirty status |
| ? | Toggle help |
| q / Esc | Quit |

Mouse: click repo header to collapse/expand; click worktree row to select.

### Bubble Tea model structure

```go
type Model struct {
    config   *config.Config
    tree     TreeModel
    dirty    map[string]bool  // key = worktree name
    width    int
    height   int
    keys     KeyMap
    err      error
}
```

---

## Implementation Phases

### Phase 1: Skeleton + Config
`workbench add repo`, `workbench ls` (plain text), config load/save.
- `go mod init github.com/panamafrancis/workbench`
- Add cobra, gopkg.in/yaml.v3, golang.org/x/sync
- `internal/config/` — all types, Load/Save (atomic write via temp+rename), CRUD helpers
- `cmd/root.go`, `cmd/add.go`, `cmd/add_repo.go`, `cmd/ls.go` (fmt.Println tree)
- ✓ `workbench add repo /path/to/repo --alias=ss && workbench ls` prints the tree

### Phase 2: Git Operations
`workbench add worktree`, `workbench rm worktree` with real git.
- `internal/git/worktree.go` — CreateWorktree, RemoveWorktree
- `internal/git/names.go` — GenerateName with adjective+city word list
- `cmd/add_worktree.go` — validate flags, compute paths, create worktree, run startup script, update config
- `cmd/rm_worktree.go`, `cmd/rm_repo.go` — confirm, run cleanup script, remove worktree, prune config
- ✓ `workbench add worktree --repo=ss` creates real git worktree at `~/.workbench/worktrees/ss/<name>`

### Phase 3: Zellij Integration
`workbench open --repo=ss --worktree=atlanta` opens a new Zellij tab running nono.
- `internal/sandbox/nono.go` — BuildNonoArgs from config model entry
- `internal/zellij/layout.go` — WriteTabLayout writes KDL to `~/.workbench/layouts/`
- `internal/zellij/client.go` — IsInZellij, OpenTab, GoToTab, CloseTab
- `cmd/open.go` — look up worktree, run startup script, build nono args, call zellij.OpenTab
- `scripts/wb.kdl` — sidebar layout template
- ✓ `workbench open --repo=ss --worktree=atlanta --model=claude` opens new Zellij tab

### Phase 4: Bubble Tea TUI
`workbench ls` launches full TUI (no GitHub status).
- `internal/tui/styles.go`, `keys.go`, `tree.go`, `statusbar.go`, `model.go`
- Launch: `tea.NewProgram(tui.New(cfg), tea.WithAltScreen(), tea.WithMouseCellMotion())`
- Enter on a worktree calls the same open logic as `cmd/open.go`
- ✓ Arrow keys nav; Space collapses repos; Enter opens worktree in new Zellij tab

### Phase 5: Polish
- `Makefile` with `install` target (copies binary to `/usr/local/bin/workbench`)
- `workbench completion zsh` via cobra built-in completion
- `workbench open --no-zellij` flag: prints the nono command to run manually
- README: install, `wb.kdl` setup, nono profile note, model config examples

---

## Verification Checklist

1. `workbench add repo /path --alias=ss` → check `~/.workbench/config.yml`
2. `workbench add worktree --repo=ss` → `~/.workbench/worktrees/ss/<name>/` exists; `git worktree list` shows it
3. `workbench rm worktree <name>` → worktree path gone; config entry removed; cleanup script ran if configured
4. `workbench open --repo=ss --worktree=<name> --model=claude` (from inside Zellij) → new tab; nono + claude start in worktree dir; startup script ran if configured
5. `workbench ls` → TUI shows tree; Enter opens worktree
6. Mouse click on repo header → collapses/expands
7. `$ZELLIJ` unset → `workbench open` prints clear error
8. Custom model in config (`models.mymodel`) → `--model=mymodel` picks it up

---

## Key Decisions

- **`workbench` not `wb`**: Subcommand groups (`add repo`, `add worktree`) are clear and self-documenting; no need for a short alias to save typing.
- **YAML not TOML**: Nested `models:` map is cleaner in YAML; `gopkg.in/yaml.v3` is the standard Go choice.
- **`~/.workbench/` as home**: Single location for config, worktrees, and layouts. No XDG — no ambiguity.
- **Model config is open-ended**: `models` is a `map[string]Model` not an enum; users can define `mymodel` with any binary/profile/args without a code change.
- **No GitHub integration**: Removed. No `gh` dependency, no CI/PR badges, no polling. Keep the tool focused on worktree + sandbox management.
- **Scripts over hooks**: Startup/cleanup are plain shell scripts configured per repo. Simple, debuggable, no DSL.
- **Zellij for tab control**: `zellij action new-tab --layout <kdl>` replaces the entire Ghostty approach.
- **Dynamic KDL per open**: One temp layout file per `workbench open`. Files live in `~/.workbench/layouts/` for easy debugging.
