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

A retired name isn't handed back out immediately. When a worktree is removed, its name is held in a reserved-cities cache (`reserved_cities` in `state.yml`) and stays excluded from generation until both the worktree is gone **and** its Claude session history has been cleaned up. This prevents a fresh worktree from colliding with a stale Zellij tab or resuming an unrelated Claude session of the same name. Deleting through workbench clears the history, so those names free up on the next generation; a name whose transcript lingers stays reserved until it's gone. (Explicitly passing `--name`/typing a name bypasses the reservation.)

### Branch renaming

Auto-created branches carry the worktree name (`wt/<alias>/<name>`). Before creating a PR, rename to something meaningful:

```sh
workbench rename-branch wt/wb/session-launcher        # renames + updates config + PR cache
workbench rename-branch wt/wb/session-launcher --push  # also pushes and deletes old remote branch
```

Do not use bare `git branch -m` — it desyncs workbench config and the PR cache.

A PR only follows the rename if it is still open when the new branch is pushed. One that merged or closed first keeps the old head ref on GitHub forever; workbench tracks it by PR number from then on, so it still reports `merged`.

### TUI key bindings

| Key | Action |
|-----|--------|
| `j` / `↓` | Move down (skips repo headers) |
| `k` / `↑` | Move up (skips repo headers) |
| `Ctrl+d` / `Ctrl+u` | Half-page down/up |
| `gg` / `G` | Jump to the first / last row |
| `}` / `{` (or `]` / `[`) | Jump to the next / previous repo |
| `Enter` / `o` | Open selected worktree |
| `O` | Open with model picker |
| `Space` / `Tab` | Collapse/expand repo |
| `h` / `←` | Collapse containing repo |
| `l` / `→` | Expand containing repo |
| `zM` / `zR` | Collapse / expand **every** repo |
| `n` | New worktree |
| `d` | Delete worktree |
| `A` | Add repo |
| `r` | Refresh dirty status |
| `?` | Toggle help (includes zellij primer) |
| `q` / `Esc` | Quit (confirms in sidebar mode) |

The list scrolls when it is taller than the pane. The mouse wheel scrolls it and a click selects a row (clicking an expanded repo header folds it); wheel scrolling pans the view without moving the cursor, and the view returns to the cursor on the next movement key.

The cursor normally skips repo headers, but it does rest on a **collapsed** repo — that row is all there is to select. This is also what keeps folding a repo from pushing the cursor into a neighbouring one, and what keeps `zM` navigable.

The sidebar shows a gamification stats box (cities visited, lifetime counters, streak, latest achievement) and a stats line at the bottom (repo count, worktree count, running/dirty/PR indicators) with context-sensitive key hints. Hide the stats box with `show_stats: false` in config.

Each worktree tab has its own sidebar, and the worktree that tab belongs to is marked with a `▸` in the gutter ("you are here"). This marker is independent of the cursor selection (the highlighted row), so it keeps pointing at the current worktree even as you navigate the list. The root session sidebar (from `workbench start`) isn't tied to a worktree, so it shows no marker.

Mouse: click a repo header to collapse/expand; click a worktree row to select.

The sidebar auto-restarts if it crashes or is accidentally quit — the layout wraps `workbench ls` in a restart loop. It waits 2 seconds between restarts (5 seconds if `workbench ls` exits with an error). Restarts reuse the on-disk PR status cache rather than re-querying GitHub, so a churning sidebar doesn't hammer the API.

The sidebar re-reads the shared on-disk state (`config.yml` + `state.yml`) when its pane gains focus (e.g. switching back from a worktree tab) and periodically on its tick, so the worktree list, gamification stats, and `▶` running indicators stay consistent across tabs without pressing `r`. This local re-sync skips the network PR lookup that a full refresh (`r`) performs, so it's cheap enough to run on every focus change — long-lived sidebars no longer show a stale snapshot that another tab has since changed.

PR status is fetched via the `gh` CLI (GitHub's GraphQL API) and cached on disk. Because there is one sidebar per Zellij tab, several things keep the shared 5,000/hour GraphQL quota from draining:

- **Only one sidebar fetches per round.** The `gh` calls run under a cross-process try-lock (`~/.workbench/cache/pr-status.json.lock`); the tabs that lose the lock cede the round and pick up the cache the winner writes, so ten open tabs cost the same quota as one.
- **Every sidebar re-reads the cache before fetching**, so a status another tab just looked up is reused instead of re-queried.
- **Unpushed branches are never queried.** A branch with no `origin/<branch>` ref cannot have a PR, so no request is made for it. (A branch whose PR is already cached keeps refreshing either way, in case it was pushed from a clone this repo has never fetched.)
- **Merged and closed PRs are cached for 24 hours.** Those states are final; only open/draft/no-PR branches refresh on the ordinary staleness window.
- **A renamed branch is re-verified once.** `rename-branch` moves the cached entry to the new branch name but marks it unverified, so the next round asks GitHub instead of trusting a status recorded under a name GitHub has never seen. If the branch then names no PR — because the PR merged or closed before the rename, which freezes its head ref on the old name — the cached PR *number* resolves it (`gh pr view <n>`), so the worktree keeps showing `merged #485` instead of silently dropping to no-PR.

If GitHub rate-limits the account anyway, the sidebar shows a `gh rate limited` hint and pauses all PR fetches for 15 minutes before retrying. The pause is persisted in the cache, so it survives sidebar restarts and applies to every tab, not just the one that hit the limit.

Note that `gh api rate_limit` reports the **REST** (`core`) bucket, which is usually untouched. `gh pr list` spends the separate **GraphQL** bucket — check that one with `gh api graphql -f query='{rateLimit{used remaining resetAt}}'`.

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

## supatree — multi-repo worktrees for one issue

`supatree` is a companion binary (built and installed alongside `workbench`) for work that spans several repos at once — e.g. a change to `terraform`, then `keystone-api`, then `admin-frontend`. It creates one worktree per repo under a shared root so a single agent can run at the top and see every repo, and it orchestrates per-repo PR creation.

### Model

- A **stack** is a git repo holding the repo selection (`supatree.yml`), an agent guide (`AGENTS.md`), and `scripts/`. Create one with `supatree scaffold`.
- A **supatree** is a worktree of that stack repo at `~/.supatree/trees/<name>/`, with each member repo checked out under `repos/<alias>/`. It is named with a city name, like workbench worktrees.
- Member repos are referenced by their **workbench alias** — supatree reuses workbench's registered repo definitions (path, `copy_files`, scripts). Register repos with `workbench add repo` first.
- Member branches are `st/<slug>/<alias>` (the slug starts as the city name; rename it before opening PRs). The stack worktree itself is on `st/<name>`.
- A **review tree** (`supatree review`) is the same thing pointed at someone else's work: each member is checked out at a pull request's head on a tree-local `review/<slug>/<alias>` branch, and the tree records the PRs rather than deriving them from the branch name. The authoring commands (`rename-branch`, `create_pr`, `create_prs`) refuse there — the branches belong to the PRs' authors — and `.supatree/info.md` carries review instructions instead. The `docs` MCP tool with `topic: review` explains how to review in one. A review tree finishes as `reviewed` rather than `done` — the author's merge is their milestone, not work you shipped, and `history` counts the two separately.
- **Review tooling.** `review_refresh` (MCP, or `supatree review refresh`) re-fetches the PR heads when an author pushes; `review_post` submits one batched review with inline comments anchored to the checked-out commit, and refuses if the head has moved since. Posting publishes in your name, so it requires the `outward` permission (off by default at every autonomy level). `supatree review fork` converts a review tree into an authoring one whose PRs target the authors' branches.
- Teardown (`supatree rm`, and `sync --prune`) deletes only the branch the tree itself created. A member left on some other branch — after a manual `gh pr checkout`, say — is reported and left alone, because the delete is `git branch -D`.
- Dependencies between repos (`deps:` in `supatree.yml`) drive creation order, the merge order shown in `.supatree/info.md`, and `create_prs` ordering.

### Quickstart

```sh
supatree init                                   # set up ~/.supatree, register MCP
workbench add repo ~/code/terraform --alias=terraform
workbench add repo ~/code/keystone  --alias=keystone
supatree scaffold fraud                         # pick repos interactively; stack at ~/.supatree/stacks/fraud
# (or non-interactively: supatree scaffold fraud --repos=terraform,keystone)
# edit ~/.supatree/stacks/fraud/supatree.yml to add deps, commit it
supatree start                                  # start the st-main Zellij session
supatree new --stack=fraud                      # create a city-named supatree
supatree open <name>                            # open the root agent (sees all repos)
supatree open <name> --agent=reviewer           # a second, independently-resumable agent
supatree ls                                     # list supatrees + member PR status
supatree status                                 # activity of every supatree (open/pushed/approved/stale/done)
supatree dash                                   # full-screen dashboard of the same
supatree watch                                  # background poller: activity ledger + desktop notifications
supatree comments <tree> <repo>                 # what reviewers said, with unresolved threads first
supatree pm                                     # the PM agent — sees every supatree at once
supatree request "…"                            # queue something for the PM
supatree schedule                               # recurring PM work (standups, triage, reminders)
supatree message <tree> <agent> "…"             # leave a message in an agent's mailbox
supatree inbox <tree> <agent>                   # what is waiting for it
supatree sync <name>                            # reconcile after editing supatree.yml
supatree rename-branch <slug> <name>            # rename all member branches (slug: max 40 chars)
supatree rm <name>                              # tear down all member worktrees

# Reviewing someone else's cross-repo change
supatree review <pr-url> <pr-url> ...           # review tree: each repo at its PR head
supatree review fraud-zero/api#600 fraud-zero/web#988   # or owner/repo#number
supatree review refresh <name>                  # authors pushed — re-fetch the heads
supatree review fork <name>                     # turn the review into a proposal
```

`rename-branch` renames every member or none: if a member fails, the renames already made are rolled back, so the tree's recorded slug never points at a name only some members are on. With `--push` the pushes run after every local rename has landed, and a push failure is reported per repo without undoing the rename.

### Sidebar

`supatree ls` (the sidebar in each supatree tab, and `supatree start`'s pane) is a TUI listing every supatree with its agents and member repos.

| Key | Action |
| --- | --- |
| `j` / `k` (or `↓` / `↑`) | Move down/up (skips subheaders) |
| `Ctrl+d` / `Ctrl+u` | Half-page down/up |
| `gg` / `G` | Jump to the first / last row |
| `}` / `{` (or `]` / `[`) | Jump to the next / previous supatree |
| `Space` | Fold/unfold the innermost section — the repositories list on a repo row, otherwise the supatree |
| `h` / `l` (or `←` / `→`) | Collapse / expand; `h` closes the repositories section first, then the supatree |
| `zM` / `zR` | Fold / unfold **every** supatree |
| `Enter` / `o` | Open the selected agent, or a shell in the selected member repo |
| `a` | Add an agent to the selected supatree — on a member row, open that repo's agent |
| `n` | New supatree |
| `s` | Sync the selected supatree |
| `d` | Delete the selected supatree |
| `D` | Open (or focus) the dashboard tab |
| `r` | Refresh (forces a PR status fetch) |
| `?` | Keybinding reference (any key closes it) |
| `q` | Quit (confirms in sidebar mode) |

The mouse works too: the wheel scrolls the list and a click selects a row. Wheel scrolling pans the view without moving the cursor, so you can read further down the list and it stays put — the view snaps back to the cursor as soon as you press a movement key. When the list is taller than the pane it scrolls to keep the cursor in view.

Pressing `n` prompts for a **name** (leave it blank to auto-generate a city name). If more than one stack is registered you first pick which stack from a list (`↑`/`↓` or `j`/`k` to move, `enter` to select, `esc` to cancel), then the name. The name is checked as you type — a name that would be rejected (`feature-v1.1`, or one already taken by a supatree or a workbench worktree) shows the reason under the field and `enter` leaves the prompt open so you can fix it in place; the warning clears with the character that caused it. After creation the cursor lands on the new supatree so it scrolls into view.

Each supatree's **repositories section starts folded**, so a long list of supatrees stays readable. Its header carries a coloured count per PR status instead (`◌1 ◉2 ✓1 ✕1 ·3` — draft, open, merged, closed, and members with no PR yet); the same summary moves up onto the supatree row when the whole supatree is folded. Unfold the section (`Space`, `l` or `enter` on the `repositories` row) to see the member rows, which show each repo's PR state and number in full (`◉ open #871`).

Folds are shared: they live in `~/.supatree/ui.yml` rather than in each sidebar process, so folding a supatree in one tab folds it in every other tab's sidebar on its next reload (focus or the 30s tick) instead of leaving each tab with its own shape of the same list.

The two sections answer different questions, so `enter` does different things in them. Agent rows are processes (the `●`/`○` dot is liveness), and `enter` opens or focuses that agent's tab. Member rows are places reporting state (branch, dirty mark, PR), so `enter` (or `o`) stands in one: a shell pane rooted at `repos/<alias>/`, opened beside the agent in the tab's main area rather than under the sidebar. That shell is your own — it is **not** inside the nono sandbox, unlike every agent workbench and supatree launch — and it is disposable, closing when you exit it. To get an agent scoped to a single member repo instead (nono allows only that repo, not the whole tree), press `a` on the member row; `a` on a supatree or agent row still prompts for a new root agent's name. The footer hint tracks the cursor (`enter shell` vs `enter open`) so you can see which you'll get. A member that isn't checked out yet says so and points at `supatree sync`.

Like the workbench sidebar, each supatree tab's sidebar marks the supatree that tab belongs to with a `▸` in the gutter ("you are here"), independent of the cursor. It re-reads live state when the pane regains focus and on its periodic tick, so newly created or removed supatrees appear across tabs without pressing `r`. PR status is fetched via `gh` and cached on disk under the same quota discipline as the workbench sidebar (single-fetcher try-lock, cache re-read before fetching, no request for unpushed branches, 24h cache for merged/closed PRs, and a persisted 15-minute pause after a rate-limit response), so a churning or multi-tab sidebar doesn't drain the API quota.

### Status and dashboard

`supatree status` answers "where is everything?" across all supatrees at once. It derives each member repo's place in the ship lifecycle from local git plus the cached PR status:

```
absent → idle → wip → pushed → draft → open → changes | approved → merged | closed
```

and rolls that up per supatree — `setup` (a member worktree is missing), `new`, `wip`, `pushed`, `review`, `approved` (every open PR approved, ready to merge), or `done` (everything merged or closed, so the tree is only occupying disk — the cue to `supatree rm` it). Alongside the state it flags supatrees that are **blocked** (a PR has changes requested or failing checks), **dirty** (uncommitted work), and **stale** (no commit in any member for `--stale-after`, default 7 days).

```sh
supatree status              # human-readable, cache only — costs no GitHub quota
supatree status --refresh    # fetch PR status from GitHub first
supatree status --json       # the whole summary, for scripts and watch loops
supatree status --all        # include the members of finished supatrees
```

`supatree dash` is the same data as a full-screen TUI, meant to live in its own window or Zellij tab: one row per supatree with its state, open/total PRs, review verdicts (`2✓ 1✗ 3·` — approved, changes requested, waiting), time since the last commit, and what needs attention. `space` expands a supatree to its member repos with PR numbers and per-repo state; `enter` focuses that supatree's Zellij tab. The most actionable supatrees sort first (blocked, then ready to merge, then in review) and finished ones sink to the bottom.

Press `D` in the supatree sidebar to open or focus the dashboard in a `supatree-dash` tab, or run `supatree dash` in any terminal. Both the dashboard and the sidebar read the same on-disk PR cache and fetch under the same staleness gate, cross-process lock and rate-limit backoff, so running a dashboard alongside a screenful of sidebars adds no extra GitHub API load. Non-interactive (piped) output falls back to `supatree status`.

### Review comments

`supatree comments <tree> <repo>` shows what reviewers have said on a member repo's pull request: top-level comments, review verdicts, and line-anchored threads with their **resolved state**. Unresolved threads lead the output, because they are the only part still waiting on an answer; `--all` covers every member, `--json` is for scripts, `--force` re-fetches.

Unlike `supatree status`, this one costs API quota. Thread resolution exists only in GitHub's GraphQL API, so it is a second query shape per PR on top of the one the sidebar already spends. It is therefore fetched **only when asked for, never on a timer**, and cached until the PR itself changes — so asking twice about an unchanged PR is free. Agents get the same thing as the `pr_comments` MCP tool.

### The PM agent

`supatree pm` (or `P` in the sidebar) opens a standing agent rooted at `~/.supatree/pm` that can see every supatree at once: what is blocked, what reviewers said, who is working where. It is **optional** — nothing else depends on it running, and without it supatree behaves exactly as it does today.

It is not rooted in a supatree, because one that manages many cannot live inside one of them. That also means it gets its own MCP gate: `SUPATREE_PM=1` unlocks the cross-tree tools (`requests`, `list_trees`, `events`, `notify`), while `SUPATREE` stays *unset* so the tree-scoped tools stay hidden rather than resolving nothing.

**Its sandbox is a different shape, not a bigger one.** It allows its own state, each tree's `.supatree/`, and the stack repos — and nothing under `repos/`. The invariant is not "read-only on trees" but *the PM may write supatree's own state and never a member repo's working tree*. Set `pm_model` in `~/.supatree/config.yml` to point it at a `models` entry with its own `nono_profile`: the PM executes no third-party code but reads text other people wrote and holds credentials that reach off the machine, so the profile it wants is narrow on egress rather than wide on the filesystem.

```yaml
# ~/.supatree/config.yml
pm_model: claude-pm     # a models entry whose nono_profile scopes the gh credential
```

**Reaching it.** Anything that can append to a file can queue a request — `supatree request`, the sidebar, the watcher — and the PM reads the queue at the top of each turn. That indirection is the point: a Go process cannot use a message bus, but every Go process can append a line. The queue is read from a **stored offset**, so a PM that has been closed for a day catches up on the backlog instead of losing it, and the offset is only committed after the requests have been handed over.

**Two rules it is given up front.** It never opens a Zellij tab unprompted — opening focuses the tab and yanks the terminal away from whoever is using it, so it creates trees and *reports*, and you press enter yourself. And it treats fetched text as data rather than instructions: a PR comment is written by anyone who can comment on the repository.

It also cannot notify you directly — nothing inside the sandbox can — so its `notify` tool queues through the watcher, which applies the same tiering and deduping as its own events.

### Autonomy

What the PM may do unasked is explicit and per supatree, in `.supatree/meta.yml`, with a workspace default in `~/.supatree/config.yml`:

| Level | Unasked, the PM may | If you ask it to |
| --- | --- | --- |
| `off` | report only | report only |
| `nudge` *(default)* | message agents | create, reap, push |
| `auto` | create supatrees, open PRs, reap finished ones | as unasked |

**The level governs what the PM does unasked**, which is the only thing about it worth being careful over. Ask it to create a supatree and it creates one: the mutating tools take an `asked` flag, the PM sets it when the request came from you in that turn, and below `auto` that is the difference between doing the thing and reporting that it could. `off` is the exception — report-only means report-only, and asking does not lift it — and a scheduled turn cannot carry the flag at all, because there is nobody in one to have asked. It is the same assertion `remove_tree`'s `force` has always rested on, trusted the same way: autonomy is a consent boundary and the sandbox is the security one, and consent is exactly what an agent is in a position to report.

**Outward-facing actions are a separate axis** (`outward`, off everywhere by default). "Message a local agent" and "comment on a PR" are different kinds of risk — one is private and recoverable, the other is published and permanent — so wanting the PM to create supatrees unattended does not also grant it a public voice.

The workspace default is not just convenience: autonomy lives per tree, so without it nothing would govern `new_tree`, which has no tree yet to carry a level. **A scheduled turn caps at `nudge`** however the tree is configured, unless its schedule entry opts in — nobody is watching one of those.

### The board

`.supatree/board.md` is what the PM maintains per supatree: what each agent is on, what is blocked, what is waiting on you. Status must not mean "read the PM's chat log" — scrollback is a terrible status display and people stop reading it by day three. Chat is where you negotiate; the board is where you check. `b` in the dashboard shows the selected tree's board.

### Memory

Durable notes live in `notes/` **in the stack repo**, scaffolded by `supatree scaffold`. That is a deliberate choice over a vector store: at the volume this produces — tens to low hundreds of finished supatrees a year — grep beats embedding retrieval on precision and on being debuggable, and a git directory is diffable, blameable, reviewable and shared with the team. Curation arrives as a pull request rather than a migration.

Three tools: `remember` writes a note, `recall` searches them, and `history` answers what shipped from the event ledger. The split matters — **`history` is exact and `recall` is not**, so the PM is told never to answer a status question from memory. Git and the ledger are authoritative for facts; notes are for judgement.

Two things keep it from rotting. `info.md` names only the few most recent notes, with everything else behind `recall`: storage was never the hard problem, what loads into every session is. And every `recall` logs its query and whether it hit, so *"a store nothing has read in 30 days gets deleted, not debugged"* is a measurable claim rather than a hope.

`supatree rm` no longer throws away agent history either: transcripts are moved to `~/.supatree/archive/<tree>/` before the session cache is cleared. Archiving is deterministic and cheap, which is what makes it safe on the removal path — distilling one into something worth keeping is a judgement call, and blocking a removal on an agent round-trip would be worse than the leak.

### Scheduled work

`~/.supatree/schedule.yml` (`supatree schedule init` writes an example) runs recurring PM work: a morning standup, hourly triage of new review comments, a Friday reap proposal, or a one-shot reminder.

The scheduler lives in the **watcher**, not in the PM. An agent cannot be trusted to hold a timer — it is mid-turn, blocked on a tool call, or was restarted an hour ago — and a schedule that silently drops jobs is worse than none. Firing a job is an append to the same request queue the sidebar's `m` uses, so the PM needs no timer and no new channel.

```yaml
jobs:
  - id: standup
    at: "09:00"
    days: [mon, tue, wed, thu, fri]
    when: events_since_last     # the default: stay silent when nothing moved
    prompt: "Summarise what moved since yesterday."
```

Three rules do the real work. A job seen for the first time is **seeded, not fired** — otherwise writing the file fires every entry at once. A job that missed fifteen hourly windows overnight fires **once**, not fifteen times. And `when: events_since_last` is the default because a standup that reports "nothing changed" every morning is notification fatigue wearing a suit. Jobs read the cache and never fetch, so a timetable costs no API quota.

`supatree schedule run <id>` fires one now for testing, deliberately without touching the fire times.

### Talking between agents

Several agents can share a supatree, and they can now reach each other. Each is launched with a stable address — `st-<tree>-<agent>` — recorded in `.supatree/agents.yml` and passed to the CLI via the model's `agent_name_args` (`["--name", "{agent_name}"]` for claude). Without it every agent in a tree would derive its name from the shared tree root and they would all collide.

Three MCP tools: `agents` lists who is here with their addresses and unread counts, `message_agent` leaves one a message, and `inbox` reads and clears your own. All three take an optional `tree`, so the PM — which lives in no supatree — can use them by naming one; inside a supatree you can omit it and mean your own.

**Delivery is always by mailbox** — a file under `.supatree/mail/<agent>/`, read on the recipient's next turn. That is the contract, and it works for every model, whether or not the recipient is running. A message bus, where the CLI has one, only makes the same message arrive sooner; `agents` reports per agent whether it is reachable that way (`bus st-canberra-main`) or by mailbox alone (`mailbox (next turn)`), rather than implying parity.

```yaml
# ~/.workbench/config.yml — a model with no message bus simply omits this
models:
  claude:
    agent_name_args: ["--name", "{agent_name}"]
```

### Notifications

`supatree watch` is the single background poller. Each round it refreshes PR status on the shared staleness gate, derives the same summary `supatree status` shows, diffs it against the previous round, appends what changed to `~/.supatree/events.jsonl`, and delivers the few events that warrant interrupting you as desktop notifications.

`supatree start` spawns one automatically and it exits when the last supatree Zellij session closes. It is a singleton enforced by a file lock, so a second one — a stray `supatree watch`, or a cron entry firing while a session is open — exits quietly rather than doubling the GitHub API load. That makes a scheduled `supatree watch --once` safe to add if you want the hours when no session is running covered too.

Events are a diff, not a report. Nothing that has no previously observed state is announced, so a fresh install and a newly created supatree both stay quiet instead of telling you about everything they can see.

Only two kinds interrupt you: **changes requested** and **checks failing** — the two that mean a human is now waiting on you. Merges, approvals and finished trees are recorded but silent, and pushes and opened PRs only change a sidebar glyph. Repeats of the same news stay quiet for a cooldown (two hours for failing checks, which flap as CI re-runs), and notifications for the supatree whose tab you are currently looking at are suppressed, since you can already see it.

```yaml
# ~/.supatree/config.yml
notify_command: ["notify-send", "{title}", "{text}"]   # default: osascript on macOS
watch_interval: 30s                                    # how often to re-derive; the gh fetch behind it stays gated
```

`{title}` and `{text}` are substituted; the values are stripped of quotes and control characters, so a PR title cannot break out of the notifier's own quoting.

In the sidebar, a supatree holding news you have not looked at is marked `!` next to its name; opening it clears the mark. In the dashboard, `e` toggles a recent-activity feed of the same ledger.

### Agents

Opening a root agent also marks the tree root as a trusted folder in `~/.claude.json`, so Claude does not ask "Do you trust the files in this folder?" on every launch. It has to be seeded rather than simply answered once: several agents share the tree root, each rewrites that file wholesale from what it read at startup, and an agent that started before you accepted puts the unaccepted answer back. Only the `hasTrustDialogAccepted` flag for the tree root is touched, only when it is not already set.

All agents run at the supatree root under a nono sandbox that allows the whole tree. Multiple named agents (`--agent`) share the directory but resume independently via cached session IDs. `--repo <alias>` opens an agent scoped to a single member repo instead (the same thing `a` does on a member row in the sidebar).

### MCP tools

`supatree init` registers an MCP server (`claude mcp add supatree -s user -- supatree mcp`) — **run it before your first supatree**, otherwise the supatree PR tools won't appear and you'll only see workbench's own tools. Inside a supatree, the agent gets: `supatree_info`, `sync`, `rename_branches`, `create_pr`, `create_prs` (dependency-ordered), `pr_status`, and `docs`. PR tools refuse to run while the branch slug is still an auto-generated city name, and (unless forced) while a repo's dependencies have no PRs yet. Tools gate on the `SUPATREE` env var, so global registration is safe. New branch slugs (`rename_branches` / `rename-branch`) are lowercase alphanumeric and hyphens, max 40 chars.

The **workbench** MCP tools (`create_pr`, `rename_branch`) are for plain workbench worktrees, not supatrees: inside a supatree they detect `SUPATREE=1` and redirect you to the supatree tools above rather than acting on the wrong branch.

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
