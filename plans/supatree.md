# Supatree — multi-repo worktree sets for a single issue

## Context

Workbench manages one worktree per repo per task. Some issues span multiple repos (e.g. terraform → keystone-api → admin-frontend): today that means N unrelated worktrees with no shared naming, no shared agent context, and N manual PR flows. **Supatree** fixes this: one named unit (city-named, like workbench) spanning several repos, with a single LLM agent (or several named agents) running at a shared root, and MCP tools to orchestrate per-repo PR creation, all visualised in a sidebar.

**Decisions made with the user:**
- Separate `supatree` binary in this repo, importing/reusing `pkg/*` (refactor shared code rather than duplicate).
- Supatree state under `~/.supatree/`; trees at `~/.supatree/trees/<name>/`.
- **A stack is a git repo ("stack repo"); a supatree is a worktree of it.** The stack repo holds `supatree.yml` (repo selection + dependency edges), `AGENTS.md` (agent instructions), and `scripts/`. Each supatree is a branch `st/<name>` of the stack repo, so AGENTS.md and the repo set are branch-local, editable mid-issue, and mergeable back.
- Repo *definitions* (alias → local_path, copy_files, scripts) stay in workbench's `~/.workbench/config.yml`; `supatree.yml` references workbench aliases.
- Agents: all run with cwd = supatree root under `nono --allow <root>`. Multiple named agents per supatree, each individually resumable via cached session IDs. Sidebar shows two sections per supatree: **agents** and **repositories**.

## Architecture

### Stack repo (scaffolded by `supatree scaffold <dir> [--repos a,b,c]`)
```
supatree.yml      # members: [alias...], deps: {alias: [depends-on...]}, default model
AGENTS.md         # tracked agent instructions — hand-edited, never machine-written
scripts/          # startup / test-env scripts (run at supatree root, SUPATREE_* env)
.gitignore        # repos/ agents/ .supatree/
```
Registered in `~/.supatree/config.yml`: `stacks: [{alias, path}]` + defaults (model, sidebar width). The live supatree list is derived from `git worktree list` on each registered stack repo (no duplicated instance list; a small cache is allowed for sidebar speed).

### A supatree = worktree of the stack repo
```
~/.supatree/trees/canberra/        # branch st/canberra of the stack repo
  AGENTS.md  supatree.yml  scripts/   # tracked, branch-local
  repos/terraform/                    # member worktree, branch st/<slug>/terraform
  repos/keystone-api/                 # (gitignored; nested repos have own .git file)
  agents/                             # gitignored optional per-agent scratch
  .supatree/agents.yml                # gitignored: [{name, model, session_id, created_at}]
  .supatree/info.md                   # gitignored generated member/branch/merge-order table
```
- Branches: members use `st/<slug>/<alias>`; `slug` starts as the city name. `supatree rename-branch <slug>` renames all member branches + updates `.supatree/`; the stack-repo branch `st/<name>` and dir keep the stable city name. PR creation refuses while slug is still a city name (mirrors pkg/mcp/server.go:275).
- Dependencies: `deps` in `supatree.yml`, topo-sorted (Kahn, alphabetical tie-break, cycle error) → creation order, merge order in info.md, `create_prs` order.
- **sync**: `supatree sync [name]` reconciles `supatree.yml` ↔ `repos/` — creates missing member worktrees, regenerates info.md, reports (never deletes without `--prune`). This makes the repo set per-supatree editable: edit the file (agent or human), run sync. Also an MCP tool.

### Agents & resume
- Extend workbench `Model` config (pkg/config/config.go:40) with optional `new_session_args` / `resume_session_args` containing a `{session_id}` placeholder (claude: `["--session-id","{session_id}"]` / `["--resume","{session_id}"]`).
- New agent: generate UUID, substitute into launch args, append `{name, model, session_id}` to `.supatree/agents.yml`, open Zellij tab `<name>:<agent>` with cwd = root, `sandbox.BuildNonoArgs(root, model, wbCfg)`.
- Resume: same tab name via `ws.OpenOrFocusTab`; if tab dead, relaunch with `resume_session_args`.
- Models without session-ID args: single `main` agent using existing dir-based `hasPriorSession` → `resume_args` (`--continue`) machinery.
- Env per agent: `SUPATREE=1`, `SUPATREE_NAME`, `SUPATREE_ROOT`, `SUPATREE_MEMBERS`, `SUPATREE_BRANCH_SLUG`, `SUPATREE_AGENT=<agent-name>`.

## Phase 0 — refactors in workbench (no behavior change, lands separately)

1. **Export flock helper** — pkg/config/lock.go: add `WithFileLock(lockPath string, fn func() error) error`; `withConfigLock` wraps it with `ConfigPath()+".lock"`.
2. **Parameterize pkg/zellij on a `Workspace`** — `{LayoutsDir, LogsDir, SidebarCommand, SidebarEnvVar, SessionPrefix, SessionTab}`; `WorkbenchWorkspace()` keeps current values. Convert layout/session/log functions to methods; templatize the hardcoded sidebar line (pkg/zellij/layout.go:50) and `session.kdl.tmpl`. ~9 call sites: cmd/open.go, cmd/start.go, cmd/rm_worktree.go, cmd/uninstall.go, pkg/tui/model.go.
3. **Extract MCP framework** — split pkg/mcp/server.go into `rpc.go` (exported `Server{Name, Version, Tools, Prompts, Gate}` + stdio loop) and `workbench.go` (existing tools + `WORKBENCH=1` gate). cmd/mcp.go unchanged externally.
4. **Model session-arg fields** — add `new_session_args`/`resume_session_args` to `Model`, defaulted for claude in `DefaultConfig()`; workbench behavior unchanged.

`make ci` + `make e2e` green; zero user-visible change.

## Phase 1 — core (scaffold / new / sync / open / rm / rename)

```
cmd/supatree/main.go     # package main → supacmd.Execute()
supacmd/                 # root, scaffold, new, sync, ls (plain), open, rm, start,
                         #   rename_branch, agent, init, mcp, version
pkg/supatree/            # paths.go, config.go, stack.go, deps.go, create.go,
                         #   sync.go, remove.go, agents.go, contextfile.go, mcp.go
pkg/supatree/tui/        # Phase 3
```

- **paths.go**: `Dir()`=~/.supatree, `ConfigPath()`, `TreesDir()`, `TreeRoot(name)`, `LayoutsDir()`, `LogsDir()`, `PRCachePath()`.
- **config.go**: registry `{Version, Stacks []Stack{Alias, Path}, DefaultModel, TreesBase}`; Load/Save mirror pkg/config/config.go:104-137; mutations via `config.WithFileLock`. **stack.go**: parse `supatree.yml` from a worktree; resolve aliases via workbench `config.Load()`+`FindRepo` (missing alias → "run: workbench add repo …"); enumerate supatrees via `git worktree list --porcelain` on each stack repo.
- **create.go** — `supatree new [--stack s] [--name n] [--model m]`:
  1. Name via `git.GenerateName` (pkg/git/names.go:60), existing = supatree names ∪ workbench `AllWorktreeNames()`.
  2. `git.CreateWorktree(stackPath, TreeRoot(name), "st/<name>")` (pkg/git/worktree.go:51) — the meta-worktree.
  3. Read its `supatree.yml`; topo-sort; per member: `git.CreateWorktree(repo.LocalPath, <root>/repos/<alias>, "st/<name>/<alias>")` + `repo.RunCopyFiles` (pkg/config/config.go:265).
  4. Write `.supatree/info.md` + empty `agents.yml`.
  5. Partial failure: roll back members in reverse + remove meta-worktree/branch; `--keep-partial` to skip. `rm` tolerates half-created trees.
- **sync.go**: reconcile as described; shared by `new` (step 3), CLI, MCP.
- **contextfile.go**: `.supatree/info.md` from text/template (golden-tested): members table (alias, `./repos/<alias>/`, branch, source repo, default branch), dep edges + merge order, MCP tool crib. Scaffolded AGENTS.md references `@.supatree/info.md` and the supatree conventions; AGENTS.md itself is never regenerated.
- **agents.go**: agents.yml CRUD, UUID generation, arg substitution, fallback logic.
- **open**: `supatree open <name> [--agent a] [--model m]` → ensure agent entry, tab `<name>:<agent>` via `SupatreeWorkspace().OpenOrFocusTab`, run stack `scripts/startup` on first open. `supatree start [session]` mirrors cmd/start.go with the `st-` workspace (separate session avoids tab-name collisions with `wb-`).
- **rm**: reverse topo per member: `repo.RunCleanup`, `git.RemoveWorktree`, delete branch (warn-not-fail, cmd/rm_worktree.go:52), `sandbox.ClearSessionCache`; then meta-worktree: if AGENTS.md/supatree.yml differ from stack default branch, warn and offer `--push` (push `st/<name>` before deleting locally); remove meta-worktree + branch, `os.RemoveAll` leftovers, PR-cache deletes, `ws.CleanupLayout` per tab.
- **rename-branch**: per member `git branch -m st/<newslug>/<alias>`; `github.Cache.Rename` (pkg/github/cache.go:80); regen info.md; `--push` mirrors cmd/rename_branch.go:112.
- Makefile: add `./cmd/supatree` to build/install/e2e (same LDFLAGS, shared pkg/version).

## Phase 2 — MCP server (PR orchestration + self-service)

`supatree mcp` on the Phase-0 framework; gate: everything except `docs`/`supatree_info` requires `SUPATREE=1`. `supatree init` registers: `claude mcp add supatree -s user -- <path> mcp` (mirror cmd/init.go:193).

| Tool | Args | Behavior |
|---|---|---|
| `supatree_info` | — | members, paths, branches, deps, merge order, agents for `SUPATREE_NAME` |
| `sync` | `prune` | shell to `supatree sync` — agent adds a repo by editing supatree.yml then calling this |
| `rename_branches` | `new_slug`, `push` | shell to `supatree rename-branch` (pattern: pkg/mcp/server.go:241) |
| `create_pr` | `repo`, `title`, `body`, `draft`, `force` | refuse city-name slug; warn if repo's deps lack PRs (override `force`); push + `gh pr create` with `cmd.Dir` = member path |
| `create_prs` | `title`, `body`, `draft`, `per_repo` | topo order; skip members with no commits ahead of base; cross-link sibling PR URLs (backfill via `gh pr edit`); per-repo result table, partial failure reported |
| `pr_status` | — | `github.LookupPR` (pkg/github/gh.go:44) per member; cache at `PRCachePath()`; aggregate |
| `docs` | `topic` | supatree doc topics |

## Phase 3 — sidebar TUI (`pkg/supatree/tui/`)

Purpose-built Bubble Tea model (do **not** generalize pkg/tui/model.go; copy its documented patterns — `inputMode` state machine, `refreshMsg` full reload, background poll `tea.Cmd`s — and the glyphs/palette from pkg/tui/tree.go:345 / styles.go). Reuse `github.Cache`, `git.IsDirty`, `zellij.TabNames`.

```
▾ canberra                    2/3 PRs
    agents
      ● main (claude)         # ● tab running, ○ resumable
      ○ reviewer (claude)
    repositories
      terraform     * ◐       # dirty + PR icon per member branch
      keystone-api    ✓
```
Keys: `enter` open/resume agent (on agent row) ; `a` new agent (name + model prompt); `n` new supatree (stack picker; ad-hoc = pick stack then edit supatree.yml + sync); `d` delete w/ confirm; `s` sync; `q` sidebar-guarded quit (`SUPATREE_SIDEBAR=1`). `supatree ls` plain when non-interactive (copy `isInteractive()` from cmd/ls.go:43).

## Phase 4 — later

Dependency-aware merge orchestration (`merge_prs` topo order waiting on checks), test-env startup hooks, clone-on-missing repos (stack repo carries remote URLs), stack-repo PR flow helpers for merging AGENTS.md improvements back.

## Verification

- **Unit**: deps (topo/cycles/determinism), registry + supatree.yml parsing round-trip, agents.yml CRUD + arg substitution, info.md golden file, zellij Workspace tests for both workspaces.
- **E2E**: `scripts/e2e-supatree.sh` modeled on scripts/e2e.sh (isolated HOME): init 2 fixture repos + a fixture stack repo (supatree.yml with a dep edge) → `workbench add repo` ×2 → `supatree scaffold`/register → `supatree new --stack` → assert meta-worktree branch `st/<name>`, member dirs/branches under repos/, info.md content/order, gitignore keeps meta-worktree clean (`git status --porcelain` empty) → edit supatree.yml to add repo 3 → `supatree sync` → assert new member → `supatree rename-branch myfeature` → assert member branches renamed, meta branch unchanged → `echo y | supatree rm` → assert full cleanup incl. pruned worktrees in source + stack repos. Wire into `make e2e`.
- **Every phase**: `make ci`; update README.md + AGENTS.md file-layout (AGENTS.md mandates).
- **Manual smoke**: `supatree start` → create → root agent sees AGENTS.md/info.md → spawn second agent → quit + resume both individually → `create_prs` against scratch repos.

## Risks

1. Phase-0 zellij Workspace refactor touches ~9 call sites — mechanical, land alone, verify with `make e2e`.
2. Session-ID resume depends on the model CLI supporting `--session-id`/`--resume <id>` (claude does); fallback is single dir-resumed agent — keep that path tested.
3. Nested worktrees: member worktrees inside the meta-worktree are fine for git, but `rm` ordering matters (members before meta) and e2e must cover half-created trees.
4. Stack-repo divergence on delete: never silently drop edited AGENTS.md/supatree.yml — diff against default branch and offer `--push`.
5. Resist reusing pkg/tui.Model — fresh small model is cheaper than parameterizing 911 coupled lines.
