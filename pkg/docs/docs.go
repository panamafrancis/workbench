package docs

import "strings"

type Topic struct {
	Name    string
	Summary string
	Content string
}

var Topics = []Topic{
	{Name: "overview", Summary: "What workbench is and how it works", Content: overview},
	{Name: "commands", Summary: "CLI command reference", Content: commands},
	{Name: "config", Summary: "Configuration options and file layout", Content: configDoc},
	{Name: "tui", Summary: "TUI sidebar keybindings and modes", Content: tui},
	{Name: "worktrees", Summary: "Worktree lifecycle, naming, and branching", Content: worktrees},
	{Name: "mcp", Summary: "MCP tools and prompts for agent integration", Content: mcpDoc},
	{Name: "sandbox", Summary: "nono sandbox profiles and permissions", Content: sandbox},
	{Name: "development", Summary: "Building, testing, and contributing", Content: development},
}

func Get(topic string) (string, bool) {
	topic = strings.ToLower(strings.TrimSpace(topic))
	for _, t := range Topics {
		if t.Name == topic {
			return t.Content, true
		}
	}
	return "", false
}

func All() string {
	var sb strings.Builder
	for i, t := range Topics {
		if i > 0 {
			sb.WriteString("\n---\n\n")
		}
		sb.WriteString(t.Content)
	}
	return sb.String()
}

func ListTopics() string {
	var sb strings.Builder
	sb.WriteString("Available topics:\n")
	for _, t := range Topics {
		sb.WriteString("  ")
		sb.WriteString(t.Name)
		padding := 14 - len(t.Name)
		if padding > 0 {
			sb.WriteString(strings.Repeat(" ", padding))
		}
		sb.WriteString(t.Summary)
		sb.WriteString("\n")
	}
	return sb.String()
}

const overview = `# workbench — Overview

Sandboxed git worktree manager. Each piece of work gets its own git worktree
opened as a Zellij tab, with the chosen LLM running inside a nono security
sandbox.

## Architecture

  Terminal → Zellij session → tabs
    Tab 0: sidebar (workbench ls, always running)
    Tab N: worktree tab = sidebar pane + agent pane
      Agent pane runs: nono run --profile <p> --allow <path> -- <binary> <args>

## Key concepts

- **Repos**: registered git repositories (workbench add repo)
- **Worktrees**: git worktrees branching from a repo's default branch
- **Models**: named LLM configurations (binary + nono profile + args)
- **Sessions**: Zellij sessions prefixed with wb- (e.g. wb-main)

## Typical flow

  workbench init          # first-time setup (config, nono profile, MCP)
  workbench start         # start or resume a Zellij session
  # In the TUI sidebar:
  n                       # create a new worktree
  o / Enter               # open a worktree in a new tab
  d                       # delete a worktree

Running bare "workbench" with no arguments is equivalent to "workbench start".

## Tech stack

- Go (Cobra CLI, Bubble Tea TUI, Lipgloss styling)
- Zellij 0.43+ (programmatic tab control via zellij action)
- nono (capability-based sandbox: Seatbelt on macOS, Landlock on Linux)
- gh CLI (GitHub PR status and creation)
`

const commands = `# CLI Commands

## Session management

  workbench                           start/resume default session (wb-main)
  workbench start [name]              start or attach to a named session
  workbench start --ls                list workbench sessions
  workbench start --gc                delete dead (EXITED) sessions
  workbench start --background        create detached session (CI/scripting)

## Repo management

  workbench add repo <path> --alias=<alias>    register a repo
  workbench rm repo <alias>                    unregister a repo

## Worktree management

  workbench add worktree --repo=<alias>                     create (auto-named)
  workbench add worktree --repo=<alias> --name=<n>          create with name
  workbench add worktree --repo=<alias> --branch=<branch>   from existing branch
  workbench rm worktree <name>                              remove worktree

## Opening worktrees

  workbench open --worktree=<name>                     open in current session
  workbench open --worktree=<name> --model=<model>     open with specific model
  workbench open --worktree=<name> --session=<s>       open in named session
  workbench open --worktree=<name> --no-zellij         print raw command only

## Branch & PR

  workbench rename-branch <new> [--worktree=<name>] [--push]
      Rename branch, update config + PR cache. Use instead of git branch -m.

## Other

  workbench ls [--repo=<alias>]       TUI sidebar (or plain text when piped)
  workbench init [--non-interactive]  first-time setup wizard
  workbench doctor [--json]           dependency check
  workbench uninstall [--dry-run]     remove worktrees, sessions, config
  workbench mcp                       MCP server (stdio, used by Claude Code)
  workbench docs [topic]              show documentation
  workbench version                   print version
`

const configDoc = `# Configuration

All state lives under ~/.workbench/:

  ~/.workbench/config.yml              main config
  ~/.workbench/state.yml               last-run version, update check cache
  ~/.workbench/worktrees/<alias>/<n>/  default worktree location
  ~/.workbench/layouts/<name>.kdl      generated Zellij layouts (transient)
  ~/.workbench/cache/                  PR status cache
  ~/.workbench/logs/                   Zellij error log

## config.yml fields

  version: 1
  default_model: claude                 which model to use by default
  worktree_base: ""                     override worktree root (default ~/.workbench/worktrees/)
  default_zellij_layout: ""             override the embedded session layout
  sidebar_width: "20%"                  sidebar pane width in worktree tabs
  update_check_disabled: false          disable the GitHub release check on start

## Models

  models:
    claude:
      nono_profile: claude-code-local   nono profile name
      binary: claude                    executable name or path
      args: ["--dangerously-skip-permissions"]
      resume_args: ["--continue"]       appended when reopening existing session

Models is an open map — add any binary with any nono profile.

## Repos

  repos:
    - alias: wb
      local_path: /path/to/repo
      startup_script: ""                run before opening a worktree
      cleanup_script: ""                run before removing a worktree
      worktrees:
        - name: clear-detroit
          branch: wt/wb/clear-detroit
          path: /Users/x/.workbench/worktrees/wb/clear-detroit
          model: claude

## Environment variables injected into agent panes

  WORKBENCH=1
  WORKBENCH_WORKTREE_NAME=<name>
  WORKBENCH_REPO_ALIAS=<alias>
  WORKBENCH_BRANCH=<branch>
`

const tui = `# TUI Sidebar

The sidebar is workbench ls running inside the Zellij session layout. It shows
a tree of repos and worktrees with status indicators.

## Keybindings

  j / ↓         move down (skips repo headers)
  k / ↑         move up (skips repo headers)
  Enter / o     open selected worktree
  O             open with model picker
  Space / Tab   collapse/expand repo
  h / ←         collapse containing repo
  l / →         expand containing repo
  n             new worktree (in selected repo)
  d             delete worktree (confirms first)
  A             add repo
  r             refresh
  ?             toggle help (includes Zellij primer)
  q / Esc       quit (confirms in sidebar mode)

## Mouse

  Click repo header    collapse/expand
  Click worktree row   select

## Status indicators

  ▶  worktree has a running Zellij tab
  ✎  worktree has uncommitted changes (dirty)
  ⬆  PR is open; number shown
  ✔  PR is merged

## Sidebar mode

When WORKBENCH_SIDEBAR=1 is set (injected by the layout), q prompts for
confirmation. The layout wraps workbench ls in a restart loop so it respawns
if it crashes.

## Refresh behavior

The sidebar auto-refreshes running/dirty state when its pane gains focus.
PR status is fetched on startup and refreshed on a 60-second tick with a
15-minute max age (5 minutes for the selected worktree).
`

const worktrees = `# Worktrees

## Naming

Auto-generated names are <adjective>-<city> (e.g. bold-atlanta, clear-detroit).
Names are globally unique across all repos — they serve as Zellij tab titles.
Custom names must be lowercase alphanumeric and hyphens, 1–24 characters.

## Branch naming

Auto-created branches follow: wt/<repo-alias>/<worktree-name>
Example: wt/wb/clear-detroit

Before creating a PR, rename to something meaningful:
  workbench rename-branch wt/wb/session-launcher --push

Do NOT use bare git branch -m — it desyncs workbench config and PR cache.

## Creation flow

1. Name generated or validated (synchronous)
2. Worktree appears immediately in the TUI (optimistic update)
3. git fetch origin + git worktree add runs in background
4. On success: config saved, message shown
5. On failure: worktree removed from TUI, error shown

## Offline support

If git fetch fails, workbench falls back to the last-fetched origin/<default>
ref. The default branch is detected via git symbolic-ref refs/remotes/origin/HEAD
(falls back to main, then master).

## Opening a worktree

1. Resolve model → look up nono profile and binary
2. If tab exists with running command → focus it (no new process)
3. If tab exists but command exited → close stale tab, create fresh
4. Write layout KDL with WORKBENCH_* env vars
5. zellij action new-tab with the layout
6. Run startup_script (if configured, only on fresh tab)

## Deletion

  workbench rm worktree <name>

Runs cleanup_script, git worktree remove, deletes the wt/* branch, removes
from config, and cleans up the layout KDL file. Warns if the worktree has
a running tab or uncommitted changes.
`

const mcpDoc = `# MCP Server

workbench includes an MCP (Model Context Protocol) server for agent integration.
It runs as "workbench mcp" over stdio and is registered with Claude Code during
"workbench init".

## Registration

  claude mcp add workbench -s user -- workbench mcp

This makes the tools and prompts available in every Claude Code session.
They gate on the WORKBENCH env var — tools return an error outside workbench
sessions.

## Tools

### rename_branch

Rename the current worktree's branch and update workbench config + PR cache.

Parameters:
  new_name (string, required) — new branch name, keep wt/<alias>/ prefix
  push (boolean, optional) — push new branch and delete old remote branch

### create_pr

Push the current branch and create a pull request via gh. Refuses if the branch
still has an auto-generated name (call rename_branch first).

Parameters:
  title (string, optional) — PR title (omit to auto-fill from commits)
  body (string, optional) — PR body/description
  draft (boolean, optional) — create as draft PR

### docs

Look up workbench documentation by topic.

Parameters:
  topic (string, optional) — one of: overview, commands, config, tui,
    worktrees, mcp, sandbox, development. Omit for all documentation.

## Prompts

### workbench_conventions

Returns branch naming, scope discipline, and PR conventions. Intended to be
loaded at the start of a workbench session to guide the agent's behavior.
`

const sandbox = `# nono Sandbox

workbench sandboxes each agent process via nono. The agent pane runs:

  nono run --profile <profile> --allow <worktree-path> -- <binary> <args>

The profile and binary come from the model config entry — no hardcoded mapping.
The --allow flag grants read/write to the worktree directory only.

## Profile setup

Use "workbench init --profile" to generate a nono profile, or create one
manually at ~/.config/nono/profiles/<name>.json.

The init wizard generates claude-code-local.json by:
  - Detecting repo parent directories
  - Finding Go toolchain paths (go env GOPATH)
  - Globbing ~/.ssh/*.pub for SSH public keys
  - Including ~/.config/gh if gh is authenticated

## Key directories to allow

  ~/.workbench              config, worktree base, layouts
  ~/code/<org>              repo parent directories
  ~/code/go/pkg,bin,src     Go module cache and toolchain
  ~/.config/gh              GitHub CLI auth tokens

## SSH agent access

Git operations inside the sandbox need the SSH agent. On macOS the socket
is under /private/tmp (path changes per-boot). Allow via:

  "unix_socket_subtree": ["/private/tmp"]

## Environment passthrough

Env vars pass through the zellij → nono → agent chain intact. The WORKBENCH_*
vars, SSH_AUTH_SOCK, ZELLIJ_SESSION_NAME etc. are all available inside the
sandbox.
`

const development = `# Development

## Build & run

  make build          build ./workbench binary
  make install        copy to /usr/local/bin/workbench
  make ci             fmt + lint + vet + test (run before pushing)
  make e2e            build + run scripts/e2e.sh
  make hooks          enroll .githooks/ as git hooks
  go test ./...       run unit tests

## File layout

  cmd/                Cobra commands (one file per command)
  pkg/config/         Config types, Load/Save, CRUD helpers
  pkg/git/            Worktree creation, name generation, status
  pkg/github/         PR lookup via gh CLI, PR status cache
  pkg/sandbox/        nono argument building
  pkg/setup/          Shared check engine for init/doctor, update checker
  pkg/tui/            Bubble Tea TUI (model, tree, keys, styles)
  pkg/zellij/         Session and tab management, layout generation
  pkg/mcp/            MCP stdio server (tools + prompts)
  pkg/docs/           Documentation content (this text)
  plugin/             DEPRECATED — replaced by MCP server
  scripts/            E2E test, standalone layout, CI shim
  plans/              Design documents

## Adding features

  New CLI command:      add file under cmd/, wire into rootCmd in cmd/root.go
  New MCP tool:         add tool definition and handler in pkg/mcp/server.go
  New TUI action:       add key to keys.go, handle in model.go Update
  New config field:     add to structs in pkg/config/config.go
  New inline prompt:    add inputMode constant, handle in updateInput

## Linting

golangci-lint enforces errcheck, exhaustive switch, noctx, prealloc,
staticcheck, and more. Do not suppress warnings — fix them.

## Testing

Unit tests live alongside the code (*_test.go). The e2e test (scripts/e2e.sh)
creates an isolated HOME, runs init → doctor → add repo → add worktree →
start background → open → rm worktree.

Always run "make ci" and fix all issues before pushing.
`
