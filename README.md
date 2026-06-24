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
workbench stats                                show lifetime statistics and achievements
workbench init [--non-interactive] [--profile]
workbench doctor [--json]
workbench uninstall [--dry-run] [--keep-config] [--force]
workbench docs [topic]                        show documentation (topics: overview, commands, config, ...)
workbench mcp                                 MCP server (stdio, used by Claude Code)
workbench version
```

### Session management

Running bare `workbench` with no arguments auto-starts (or resumes) the default session — equivalent to `workbench start`. Inside Zellij it shows help; with no config it hints at `workbench init`.

`workbench start` replaces the old `zellij --layout ...` workflow. It embeds the session layout, manages `wb-`-prefixed Zellij sessions, and uses `syscall.Exec` so workbench doesn't linger as a wrapper process.

Multiple sessions are supported — `workbench start work` and `workbench start client-x` run independently. Two terminals can attach to the same session (Zellij multi-client).

### Worktree names

Auto-generated names are city names (e.g. `tokyo`, `nairobi`). On collision, a numeric suffix is added (`tokyo-2`, `tokyo-3`). ~200 cities are available. Names are globally unique across all repos — they serve as Zellij tab titles. Custom names must be lowercase alphanumeric and hyphens, 1–24 characters.

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

The sidebar shows a gamification stats box (cities visited, lifetime counters, streak, latest achievement) and a stats line at the bottom (repo count, worktree count, running/dirty/PR indicators) with context-sensitive key hints. Hide the stats box with `show_stats: false` in config.

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
| `~/.workbench/state.yml` | Last-run version, update check cache, gamification stats |
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
sidebar_width: "20%"       # sidebar pane width in new worktree tabs
update_check_disabled: false  # set true to disable the update check on start
show_stats: true           # show gamification stats box in the sidebar (default true)

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
    copy_files: [".claude", ".env"]  # copied from repo to new worktrees
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

Scripts are run as `bash -- <script>` with these environment variables:

| Variable | Value |
|----------|-------|
| `WORKBENCH_REPO_BASE_PATH` | Absolute path to the repo (the `local_path` from config) |
| `WORKBENCH_WORKTREE_PATH` | Absolute path to the worktree |
| `WORKBENCH_WORKTREE_NAME` | Worktree name |

```yaml
repos:
  - alias: ss
    startup_script: /path/to/setup.sh    # runs on workbench open
    cleanup_script: /path/to/teardown.sh # runs on workbench rm worktree
```

### Copying files to new worktrees

Git worktrees only contain tracked files. To automatically copy gitignored files (like `.env` or `.claude/`) from the repo into each new worktree, use `copy_files`:

```yaml
repos:
  - alias: ss
    copy_files:
      - .claude
      - .env
```

Paths are relative to the repo root. Both files and directories are supported. Directories are copied recursively. The copy runs after `git worktree add` and before any startup script.

### Sidebar width

Set `sidebar_width` to control the sidebar pane width in new worktree tabs (default `"20%"`). Already-open tabs are not affected — this is a Zellij limitation.

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

### Deleting worktrees

Deleting a worktree (`d` in the sidebar or `workbench rm worktree <name>`) runs the repo's cleanup script, removes the git worktree directory (`git worktree remove --force`), deletes the auto-created `wt/<alias>/<name>` branch, removes the config entry, and cleans up the generated Zellij layout. The sidebar and the CLI perform the same steps.

It also clears the agent's cached session transcripts for that path (e.g. `~/.claude/projects/<encoded-path>/`). This prevents a future worktree created at the same path from being silently resumed via `resume_args` (`--continue`) into an unrelated session. Config writes for create and delete are done as read-modify-write against the on-disk config, so an action in one process or sidebar instance never resurrects a worktree another deleted.

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

## MCP server

Workbench includes an MCP server that integrates with Claude Code (and any MCP-compatible agent). It provides tools and conventions to the agent running inside a workbench session.

### Registration

`workbench init` offers to register the MCP server automatically. To register manually:

```sh
claude mcp add workbench -s user -- workbench mcp
```

### Tools

- **`rename_branch`** — rename the worktree branch and update workbench config + PR cache (replaces bare `git branch -m`)
- **`create_pr`** — push branch and create a PR via `gh` (refuses if the branch still has an auto-generated name)
- **`docs`** — look up workbench documentation by topic (overview, commands, config, tui, worktrees, mcp, sandbox, development)

### Prompts

- **`workbench_conventions`** — branch naming, scope discipline, and PR conventions

The MCP server gates on the `WORKBENCH` env var — tools return an error outside workbench sessions, so registration is safe globally.

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
