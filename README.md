# workbench

A sandboxed git worktree manager. Each piece of work gets its own git worktree running inside a [nono](https://nono.sh) security sandbox. A persistent Zellij sidebar shows your worktrees; selecting one opens a new Zellij tab with your chosen LLM sandboxed via nono.

![workbench](docs/screenshot.png)

## Requirements

- Go 1.22+
- [nono](https://nono.sh) — capability-based sandbox (macOS via Seatbelt, Linux via Landlock)
- [Zellij](https://zellij.dev) 0.43+
- git

## Install

```sh
go install github.com/panamafrancis/workbench@latest
```

Or build from source:

```sh
git clone https://github.com/panamafrancis/workbench
cd workbench
go build -o /usr/local/bin/workbench .
```

## Setup

```sh
workbench init       # interactive wizard: config, nono profile, gh auth, first repo
workbench doctor     # verify all dependencies are installed and configured
workbench start      # launch a Zellij session with the sidebar
```

`workbench init` creates `~/.workbench/config.yml`, optionally generates a nono profile (globbing your `~/.ssh/*.pub` keys), and offers to run `gh auth login`.

`workbench doctor` checks: zellij, nono, git, gh auth, config, nono profiles, SSH agent, and registered repos.

### Register a repo

```sh
workbench add repo /path/to/your/repo --alias=myrepo
```

## Usage

```
workbench start [session-name]                start or attach to a Zellij session
workbench start --ls                          list workbench sessions
workbench start --gc                          delete dead sessions

workbench add repo <path> --alias=<alias>     register a repo
workbench rm  repo <alias>                    unregister a repo

workbench add worktree --repo=<alias>         create a worktree (auto-named)
workbench add worktree --repo=<alias> --name=<name> [--branch=<branch>]
workbench rm  worktree <name>                 remove a worktree

workbench ls [--repo=<alias>]                 open TUI (or plain text when piped)
workbench open --worktree=<name> [--model=<model>] [--repo=<alias>] [--no-zellij]
workbench open --worktree=<name> --session=<session>   target a specific session

workbench rename-branch <new-branch> [--worktree=<name>] [--push]
workbench init [--non-interactive] [--profile]
workbench doctor [--json]
workbench uninstall [--dry-run] [--keep-config] [--force]
workbench version
```

### Session management

`workbench start` replaces the old `zellij --layout ...` workflow. It embeds the session layout, manages `wb-`-prefixed Zellij sessions, and uses `syscall.Exec` so workbench doesn't linger as a wrapper process.

Multiple sessions are supported — `workbench start work` and `workbench start client-x` run independently. Two terminals can attach to the same session (Zellij multi-client).

### Worktree names

Auto-generated names are `<adjective>-<city>` (e.g. `bold-atlanta`). Names are globally unique across all repos — they serve as Zellij tab titles. Custom names must be lowercase alphanumeric and hyphens, 1–24 characters.

### Branch renaming

Auto-created branches carry the worktree name (`wt/<alias>/<name>`). Before creating a PR, rename to something meaningful:

```sh
workbench rename-branch wt/wb/session-launcher        # renames + updates config + PR cache
workbench rename-branch wt/wb/session-launcher --push  # also pushes and deletes old remote branch
```

Do not use bare `git branch -m` — it desyncs workbench config and the PR cache.

### TUI key bindings

| Key | Action |
|-----|--------|
| `j` / `↓` | Move down (skips repo headers) |
| `k` / `↑` | Move up (skips repo headers) |
| `Enter` / `o` | Open selected worktree |
| `O` | Open with model picker |
| `Space` / `Tab` | Collapse/expand repo |
| `h` / `←` | Collapse containing repo |
| `l` / `→` | Expand containing repo |
| `n` | New worktree |
| `d` | Delete worktree |
| `A` | Add repo |
| `r` | Refresh dirty status |
| `?` | Toggle help (includes zellij primer) |
| `q` / `Esc` | Quit (confirms in sidebar mode) |

The sidebar shows a stats line at the bottom (repo count, worktree count, running/dirty/PR indicators) and context-sensitive key hints.

Mouse: click a repo header to collapse/expand; click a worktree row to select.

The sidebar auto-restarts if it crashes or is accidentally quit — the layout wraps `workbench ls` in a restart loop. If `workbench ls` exits with an error, it waits 2 seconds before retrying.

The sidebar refreshes automatically when its pane gains focus (e.g. switching back from a worktree tab), so the `▶` running indicators stay up to date without pressing `r`.

### Offline support

Worktree creation works offline — if `git fetch` fails, workbench falls back to the last-fetched `origin/<default>` ref and prints a warning. The default branch is auto-detected via `git symbolic-ref refs/remotes/origin/HEAD` (falls back to `main`, then `master`).

## Configuration

All state lives under `~/.workbench/`:

| Path | Purpose |
|------|---------|
| `~/.workbench/config.yml` | Main config |
| `~/.workbench/state.yml` | Last-run version, update check cache |
| `~/.workbench/worktrees/<alias>/<name>/` | Default worktree location |
| `~/.workbench/layouts/<name>.kdl` | Generated Zellij layouts (transient) |
| `~/.workbench/cache/` | PR status cache |
| `~/.workbench/logs/` | Zellij error log |

### Example config

```yaml
version: 1
default_model: claude
worktree_base: ""          # empty = ~/.workbench/worktrees/
default_zellij_layout: ""  # override the embedded session layout
sidebar_width: "15%"       # sidebar pane width in new worktree tabs
update_check_disabled: false  # set true to disable the update check on start

models:
  claude:
    nono_profile: claude-code
    binary: claude
    args: []
    resume_args: ["--continue"]  # appended when reopening an existing session
  codex:
    nono_profile: default
    binary: codex
    args: []
  shell:
    nono_profile: default
    binary: bash
    args: []

repos:
  - alias: ss
    local_path: /path/to/scoring-service
    startup_script: ""     # run before opening a worktree
    cleanup_script: ""     # run before removing a worktree
    worktrees:
      - name: atlanta
        branch: wt/ss/atlanta
        path: /Users/you/.workbench/worktrees/ss/atlanta
        model: claude
```

### Custom models

`models` is an open map — add any binary with any nono profile:

```yaml
models:
  mymodel:
    nono_profile: default
    binary: /path/to/my-llm
    args: ["--some-flag"]
```

Then use it with `workbench open --model=mymodel` or set it as `default_model`.

### Startup and cleanup scripts

Scripts are run as `bash -- <script>` with two environment variables:

| Variable | Value |
|----------|-------|
| `WORKBENCH_WORKTREE_PATH` | Absolute path to the worktree |
| `WORKBENCH_WORKTREE_NAME` | Worktree name |

```yaml
repos:
  - alias: ss
    startup_script: /path/to/setup.sh    # runs on workbench open
    cleanup_script: /path/to/teardown.sh # runs on workbench rm worktree
```

### Sidebar width

Set `sidebar_width` to control the sidebar pane width in new worktree tabs (default `"15%"`). Already-open tabs are not affected — this is a Zellij limitation.

```yaml
sidebar_width: "20%"
```

## How `open` works

```
workbench open --repo=ss --worktree=atlanta --model=claude
  1. Resolve model → look up nono profile and binary from config
  2. If a tab with the same name exists but its command has exited, close it
  3. Run startup_script (if configured)
  4. Write ~/.workbench/layouts/atlanta.kdl (with WORKBENCH_* env vars)
  5. zellij action new-tab --name atlanta --layout ~/.workbench/layouts/atlanta.kdl
```

The agent pane receives these environment variables:

| Variable | Value |
|----------|-------|
| `WORKBENCH` | `1` |
| `WORKBENCH_WORKTREE_NAME` | Worktree name |
| `WORKBENCH_REPO_ALIAS` | Repo alias |
| `WORKBENCH_BRANCH` | Branch name |

Outside Zellij, use `--no-zellij` to print the raw command instead:

```sh
workbench open --worktree=atlanta --no-zellij
# cd /path/to/worktree && nono "run" "--profile" "claude-code" ...
```

Or target a specific session from outside Zellij:

```sh
workbench open --worktree=atlanta --session=wb-main
```

## Session lifecycle

When a worktree's command exits (e.g. typing `exit` in a claude session), the pane auto-closes (`close_on_exit`). If you later press `o` on that worktree in the sidebar, workbench detects the stale tab (sidebar-only, no running command) and recreates it with a fresh session. If the session is still running, `o` focuses the existing tab.

## Update checking

`workbench start` checks for newer releases via the GitHub API (cached for 24 hours, silent on network failure). Disable with `update_check_disabled: true` in config.

## Uninstalling

```sh
workbench uninstall              # interactive: lists what will be removed, confirms
workbench uninstall --dry-run    # preview only
workbench uninstall --keep-config  # remove worktrees/sessions but keep ~/.workbench
workbench uninstall --force      # also remove dirty worktrees
```

Uninstall does **not** touch: your git repos, nono profiles (`~/.config/nono/`), gh auth, or the workbench binary itself.

## Claude Code plugin

The `plugin/` directory contains a Claude Code plugin with slash commands and skills:

- `/workbench:rename-branch` — rename the worktree branch to a meaningful name
- `/workbench:pr` — create a PR (renames branch first if still auto-generated)
- `workbench-conventions` skill — branch naming and scope conventions, gated on `WORKBENCH_WORKTREE_NAME`

The plugin is designed to be installed globally — slash commands are namespaced and skills gate on the `WORKBENCH` env var, so they're inert outside workbench sessions.

## nono sandbox

workbench passes `--allow <worktree-path>` to nono so the sandboxed process can read and write only its own worktree. The profile name comes from the model config entry (`nono_profile`). The built-in `claude` model uses the `claude-code` profile; everything else defaults to `default`.

### Profile setup

Use `workbench init --profile` to generate a nono profile, or create one manually.

The init wizard generates `~/.config/nono/profiles/claude-code-local.json` by:
- Detecting your repo parent directories
- Finding your Go toolchain paths (`go env GOPATH`)
- Globbing `~/.ssh/*.pub` for SSH public keys
- Including `~/.config/gh` if gh is authenticated

Example generated profile:

```json
{
  "extends": ["claude-code"],
  "meta": {
    "name": "claude-code-local",
    "description": "claude-code with project repos, toolchain, and SSH agent"
  },
  "filesystem": {
    "allow": [
      "$HOME/.workbench",
      "$HOME/code/myorg",
      "$HOME/code/go/pkg",
      "$HOME/code/go/bin",
      "$HOME/code/go/src",
      "$HOME/.config/gh"
    ],
    "read_file": [
      "$HOME/.ssh/config",
      "$HOME/.ssh/id_ed25519.pub"
    ],
    "allow_file": [
      "$HOME/.ssh/known_hosts"
    ],
    "unix_socket_subtree": [
      "/private/tmp"
    ],
    "bypass_protection": [
      "$HOME/.ssh/config",
      "$HOME/.ssh/known_hosts",
      "$HOME/.ssh/id_ed25519.pub"
    ]
  }
}
```

Then reference it in your workbench config:

```yaml
models:
  claude:
    nono_profile: claude-code-local
    binary: claude
    args: ["--dangerously-skip-permissions"]
    resume_args: ["--continue"]
```

### Key directories to allow

| Path | Why |
|------|-----|
| `$HOME/.workbench` | workbench config, worktree base, generated layouts |
| `$HOME/code/<org>` | Your repo parent directories (worktrees live under `~/.workbench/worktrees/` but the bare repo is here) |
| `$HOME/code/go/pkg`, `bin`, `src` | Go module cache and toolchain (adjust for your `GOPATH`) |
| `$HOME/.config/gh` | GitHub CLI auth tokens (needed for `gh` commands and PR lookups) |

### SSH agent access

Git operations inside the sandbox (push, fetch) need access to your SSH agent. The macOS SSH agent uses a Unix socket under `/private/tmp` (the path changes per-boot, e.g. `/private/tmp/com.apple.launchd.xyz/Listeners`). To allow this:

```json
"unix_socket_subtree": ["/private/tmp"]
```

The `workbench init --profile` wizard handles SSH key discovery automatically by globbing `~/.ssh/*.pub`.
