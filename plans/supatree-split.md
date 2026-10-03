# Supatree split: two repos, self-describing stacks, XDG paths

Status: proposed (2026-09-30, revised 2026-10-02)

## Goal

Promote supatree without losing workbench. Workbench stays the tool for
single-repo work (monorepos like cv, ghostcard); supatree is the tool for one
issue across many repos. Two named repos, one-directional build-time
dependency:

- `panamafrancis/workbench` — shared `pkg/*` + the `workbench` binary.
- `panamafrancis/supatree` — `pkg/supatree`, `supacmd`, `cmd/supatree`;
  depends on `github.com/panamafrancis/workbench/pkg/...`.

The seam already exists: supatree imports `config`, `github`, `zellij`, `git`,
`sandbox`, `mcp`, `version`; nothing outside `pkg/supatree`, `supacmd` and
`cmd/supatree` imports them (the only reverse touch is the `SUPATREE=1` gate in
`pkg/mcp/workbench.go`). No third "core" repo — workbench *is* the core.

The two tools are **fully independent at runtime**: either installs and runs
without the other, and nothing on disk is shared — no common config, state,
cache, registry or nono profile, so no file ever has two writers and no
cross-binary version skew exists. The only link is build-time: supatree's Go
module imports workbench's `pkg/*`, compiled into the supatree binary.

**Supatree owns its clones.** Every member repo is cloned into supatree's own
repo cache, at a path derived from its URL. Supatree never adopts a clone it
did not make (`~/code/…`, workbench's repos). Someone using both tools has two
clones of a repo they use in both; that is accepted.

Creating a tree or worktree must never need a human: no unlock, no prompt, no
network call beyond git. The PM creates trees on its own (`new_tree`) from
inside the sandbox, where nothing can prompt anyway.

Single user today, so migration is allowed to be blunt: existing trees and
worktrees are closed before migrating, not carried across.

## 1. Independent config, XDG paths

### Roots

Each tool resolves its own roots from the XDG env vars, falling back to the
XDG defaults on every platform, macOS included (as `gh` and `git` do — not
`~/Library`, and not `os.UserConfigDir`, which returns `~/Library/Application
Support` on darwin):

| Kind | Env var | Default | supatree contents | workbench contents |
|---|---|---|---|---|
| config | `XDG_CONFIG_HOME` | `~/.config/<tool>` | `config.yml` (stacks, `models`, `repos` overrides) | `config.yml` (repos, models) |
| state | `XDG_STATE_HOME` | `~/.local/state/<tool>` | `ui.yml` (sidebar folds), `trees/<name>/` (per-tree state), `events.jsonl*`, `requests.jsonl`, `notify.jsonl`, `pm/`, `archive/`, `logs/`, locks, `watch.lock` | `state.yml`, `logs/`, locks |
| cache | `XDG_CACHE_HOME` | `~/.cache/<tool>` | `pr-status.json`, `last-status.json`, `notify-state.json`, `layouts/` | `pr-status.json`, `layouts/` |
| workspaces | — (config: `trees_base`, `worktree_base`) | `~/supatree/`, `~/workbench/` | `trees/`, `stacks/`, `repos/<host>/<owner>/<repo>/` | `<alias>/<name>/` |

Workspaces are visible because people `cd` into them; everything else is
machinery and stays hidden. Nothing in one tool's column is read by the other.
Config is what a person edits; `ui.yml` is rewritten by every sidebar tab, so
it is state.

- Everything goes through `pkg/config/paths.go` / `pkg/supatree/paths.go`, on
  top of a small `pkg/xdg` (ConfigHome/StateHome/CacheHome, env-first) in the
  workbench module. Paths built elsewhere today and to fold in:
  `cmd/init.go` (`~/.workbench` nono grant), `pkg/setup/checks.go`,
  `pkg/sandbox/nono.go`.
- **Tests must pin all three XDG vars.** 17 Go tests isolate with
  `t.Setenv("HOME", …)` and both e2e scripts with `export HOME=$(mktemp -d)`;
  once paths come from `XDG_*`, a value set in the developer's shell wins over
  the fake HOME and tests write into the real `~/.config`. One test helper
  (`testutil.IsolateHome(t)`) and the e2e preambles set `HOME`,
  `XDG_CONFIG_HOME`, `XDG_STATE_HOME` and `XDG_CACHE_HOME` together, and land
  before any path moves.
- **Per-tree state moves out of the tree**, to `<state>/trees/<name>/`
  (`meta.yml`, `agents.yml`, `info.md`, `board.md`, `mail/`). The tree root
  keeps `.supatree` as a gitignored symlink to it, so every path agents and
  docs use (`.supatree/info.md`) is unchanged, and `StateDir(root)` is the one
  function that changes. This is what lets the PM be granted *all* tree state
  with one directory (supatree#3, below), without carving `repos/` out of a
  grant on the trees base — which nono can only do on macOS (see the
  supatree#3 row). Discovery (`supatree.List`) scans
  `<state>/trees/*/meta.yml` instead of the trees base; `meta.yml` records the
  root, so a tree is found wherever `trees_base` put it.

### Cutting supatree's runtime ties to workbench config

Today `supacmd/root.go` loads `~/.workbench/config.yml` into `wbCfg` and
threads it through `pkg/supatree`. Every use goes:

| Use today | Replacement |
|---|---|
| `wb.FindRepo(alias)` ×6, `wbCfg.Repos` (instance, startup, remove, sync, review) | `ResolveMember` over the repo cache (§2) |
| `wb.ResolveModel`, `Config.ResolveModel(_, wb)`, `PMModel(wb)` | a `models` map in supatree's `config.yml`; `default_model`/`pm_model` already live there |
| `sandbox.Build*NonoArgs(…, cfg *config.Config)`, `AppendPrompt`, `SupportsSessions`, `BuildGrantedNonoArgs` | take a resolved `config.Model` instead of the whole workbench `Config` — the shared package stops knowing whose config it came from |
| `wb.AllWorktreeNames()` in `create.go` `existingNames` and the sidebar's `validateTreeName` | dropped. It only kept generated city names unique across the two tools, and they run in separate Zellij sessions with separate workspace dirs and branch prefixes (`st/…` vs `wt/…`) |
| `config.Repo.RunCopyFiles` | a free function `config.CopyFiles(srcDir, dstDir, patterns)`, called by both tools on their own types |

**Supatree's on-disk formats are supatree's types.** It does not reuse
`config.Repo` (which carries workbench's `Worktrees` and fields supatree does
not have) for anything it writes, so a workbench change cannot silently change
a supatree file. `config.Model` is reused — it is pure data, and the default
definitions move to a shared `config.DefaultModels()` so the built-in
`claude`/`codex`/… entries are not duplicated in source — but a workbench bump
that changes `Model` changes supatree's `config.yml`; check for that on bumps.

### The repo cache

```
~/supatree/repos/github.com/fraud-zero/keystone/
```

- One clone per remote, at `repos/<host>/<owner>/<repo>`. The path *is* the
  registry: there is no alias table, no duplicate-remote case, and no way to
  clone a repo twice.
- The key comes from a new `github.RemoteRef{Host, Owner, Name}` parser.
  `RepoRefFromRemote` cannot supply it: `RepoRef` is `{Owner, Name}` only and
  the function is GitHub-only. `RemoteRef` reuses its `ssh -G` host
  resolution (so `git@github-work:fraud-zero/keystone.git` and the
  `github.com` URL land on the same clone) and also parses non-GitHub ssh and
  https remotes; `RepoRefFromRemote` becomes a thin wrapper over it.
- Cloned with the URL as the stack gives it. A personal ssh alias (needed for
  a second GitHub account) belongs in git's `url.<base>.insteadOf`, not in the
  shared spec.
- The clone's own checkout is never worked in; it exists to hold worktrees.
  It is also where personal untracked files live (below).
- Created on demand by `ResolveMember` (§2); that clone is the one network
  call tree creation may make, and it is git.

Per-repo personal settings, keyed the same way, in `config.yml`:

```yaml
repos:
  github.com/fraud-zero/keystone:
    copy_files: [.env.local]
```

### Dropped repo fields

`startup_script`, `cleanup_script` and `startup_instructions` go, from both
tools (`RunStartup`/`RunCleanup` and their callers in `cmd/open.go`,
`cmd/rm_*.go`, `cmd/uninstall.go`, `pkg/tui`, `pkg/supatree/startup.go`,
`remove.go`). No configured repo sets any of them, and a private per-repo hook
is exactly what makes a stack unshareable.

`copy_files` **stays**, as a personal, local setting (supatree's `repos`
overrides, workbench's `config.yml`): on member-worktree creation it copies
the listed gitignored files (today's `.env` credentials) from the base clone's
checkout — for supatree, the cache clone — into the worktree. You put
`.env.local` into `~/supatree/repos/github.com/fraud-zero/keystone/` once;
every tree gets a copy. A listed file that is missing warns and continues. It
never goes in `supatree.yml`. Environment secrets get a proper answer later.

### nono profile

Supatree agents run today under `nono_profile: claude-code-local`, a profile
that **`workbench init` generates** (`cmd/init.go`, `buildProfileJSON`) in
nono's own config dir, granting `~/.workbench` and each workbench repo's
parent dir. After the split supatree could not create it (the generator is in
workbench's `cmd` package), and two tools writing one profile is a two-writer
file again.

- Move the generator into `pkg/setup`.
- `supatree init` writes its own profile, `supatree-agent`, and the default
  `models.claude` entry in supatree's config points at it. The profile holds
  only what is the same for every launch: toolchain, `gh`, the ssh agent, and
  supatree's config / state / cache dirs. Anything specific to a tree is a
  per-launch flag (below), because a static file cannot know where a given
  tree's stack lives.
- Workbench's `claude-code-local` stops granting anything of supatree's.

### Sandbox grants

Both layers are nono's and both are kernel-enforced: the **profile** (static,
written once by `init`) and **per-launch flags** (`--allow` / `--read`,
repeatable, chosen each time supatree starts nono). The per-launch path
already exists — `sandbox.Grants{Allow, Read}` and `BuildGrantedNonoArgs` are
how the PM gets `PMGrants` — but a coding agent today gets exactly one flag,
`--allow <tree root>` in `BuildNamedAgentNonoArgs`. It gains a
`TreeGrants(inst)` list the same way.

The coding agent's sandbox includes the `supatree mcp` server its model
spawns, and that server writes: `pr_status` refreshes through `FetchPRs` →
`Cache.Mutate`, `create_pr` calls `RecordCreatedPR`. So:

| Path | Layer | Coding agent (incl. its `supatree mcp`) | PM | Watcher, sidebars (unsandboxed) |
|---|---|---|---|---|
| config (`config.yml`) | profile | read | read | rw |
| tree root | flag | rw (its own tree) | none | rw |
| per-tree state `<state>/trees/` | flag | rw — its own tree's dir only (the `.supatree` symlink's target; nono checks the real path) | rw — the whole dir, one grant, so a new tree needs no relaunch | rw |
| the tree's stack git dir | flag | rw — the tree is a worktree of the stack, so committing at the root writes there; found with `git -C <root> rev-parse --git-common-dir`, wherever the stack lives | rw (stacks, as today) | rw |
| each member's base clone git dir | flag | rw — this tree's members only, not the whole cache | read | rw |
| state: `notify.jsonl`, `requests.jsonl` (+ locks) | profile, per file (`allow_file`) | rw, used append-only | rw, used append-only | rw |
| state: `ui.yml` | profile | none | none | rw (sidebars run unsandboxed) |
| state: `pm/` (incl. `launch.jsonl`) | flag (`PMGrants`) | none | rw | rw |
| state: `events.jsonl` | profile, per file (`read_file`) | read (`events` tool) | read | rw |
| cache (`pr-status.json` + lock) | profile | rw | rw | rw |

**The PM's tree-mutating tools must leave its sandbox.** `new_tree`,
`remove_tree` and the review-tree tools run *inside* the PM's MCP server
(`mcp.go`: `New`, `Remove`, `NewReview`), so they create and delete worktrees
under `trees/<name>/repos/`, write base clones' `.git` and the stack's `.git`,
and run `copy_files` and `scripts/setup`. `PMGrants` grants none of that; the
tools work today only because the profile grants all of `~/.supatree`. Any
narrowing of the PM — this table, or a `repos/` deny — breaks them. So they
move to the unsandboxed watcher, the same way agent launches already did:
the tool appends a request to a queue in `pm/` (`create.jsonl` beside
`launch.jsonl`), the watcher performs it and writes the result back for the
tool to return (the tool waits on it with a timeout, since a PM asking for a
tree wants to know it exists). The PM's grants then really are just `pm/` +
tree state + stacks, and `remove_tree` through the watcher can also stop the
tree's agents and close its tabs, which the PM cannot (supatree#2).

nono has no append-only mode, and granting the state *directory* would hand
coding agents `pm/` with it — so state is granted file by file, and `pm/`
only by the PM's own flag. Per-file grants need a file to point at, so
`init` and `migrate` create those files and their `.lock` siblings up front.
The cache is granted as a directory because `Cache.Mutate` writes by temp
file + rename.

Grants are fixed when nono starts. A member that `sync` adds to a running
tree is not writable by that tree's agents until they restart; `sync` says so
("restart the agent to commit in <alias>"). Same caveat `PMGrants` documents.
Granting all of `~/supatree/repos/` in the profile would avoid it, at the cost
of every agent being able to write every cached clone — not worth it for a
rare operation.

**Today's effective grants are far wider than the generated flags** —
checked 2026-10-02 with `nono why --self` from inside a coding agent in a
tree:

| Path | Result | Granted by |
|---|---|---|
| `~/.supatree` (incl. `pm/`, `cache/`) | rw | profile: `~/.supatree` |
| `~/.workbench` | rw | profile: `~/.workbench` |
| `~/code/panamafrancis/…` | rw | profile: `~/code/panamafrancis` (repo parent dir) |
| the tree root | rw | user flag: `--allow <root>` |
| `~/.config/nono` | denied | — |

So `claude-code-local` grants all of `~/.supatree`, which makes `PMGrants`'
care decorative and explains supatree#3's "the PM could write a tree not in
its allow list". It is also a **live escalation path**: any coding agent can
append to `pm/launch.jsonl`, which the unsandboxed watcher executes as
`supatree open`, and can write the PM's `requests.jsonl` cursor and every
other tree's state. The `supatree-agent` profile must not grant the state
root; the table above is the target. Until it ships, consider dropping
`~/.supatree` from `claude-code-local` and granting it per file.

Verify with `nono profile show <name>` (the fully resolved profile) and
`nono why --self --path <p> --op write`, and add an e2e case that launches the
generated nono command and asserts writes to `pm/` and to another tree's
state fail — skipped under the CI nono shim, which does not enforce.

No supatree process is granted anything under workbench's dirs, and vice
versa. Update `PMGrants`, add `TreeGrants`, update both init commands,
and their tests.

### Migration

**An explicit command, not "on first run".** `supatree` runs unattended in
three places — every agent's MCP server (`supatree mcp`), the sidebar's
restart loop (`supatree ls`, every 0.2s) and the watcher — so a binary that
migrates or refuses on first run would break every open tree the moment it
was installed. Instead:

- On the old layout (`~/.supatree` present, new config absent), every command
  except `migrate`, `version` and `doctor` exits non-zero with one line:
  "run `supatree migrate`". The MCP server returns that as its tool error
  rather than crashing the agent's session.
- The sequence is: close every tree with `supatree rm` (not by deleting
  folders — `rm` runs `ArchiveSessionCache`, which keeps the agents'
  transcripts for the PM to distil), quit every `st-*` Zellij session (which
  stops the watcher, sidebars, PM and any agent `rm` left running), **then**
  install, then `supatree migrate`. Developing this from inside a supatree (krakow) means the
  final install + migrate happen after that tree is closed, from a plain
  checkout.

`supatree migrate`:

1. Refuses while any supatree exists, `watch.lock` is held, any `st-*`
   Zellij session is alive, or any `supatree` process other than itself is
   running — listing each. "No trees" alone is not enough: `supatree rm`
   leaves the tree's tab and agent running (supatree#2), and retried
   `supatree pm` leaves pane-less PM processes behind (supatree#1); every one
   of them holds an old-binary MCP server that would keep writing the old
   paths. `migrate` runs unsandboxed, so it can see the process table. No tree
   survives, so no path-keyed state (Claude session history, folder trust,
   `.git` worktree pointers) can be orphaned.
2. Takes the old locks, copies config / state / cache files to their XDG
   homes — except `layouts/`, which is regenerated on demand and today holds
   ~20 stale `.kdl` files for removed trees (supatree#2).
3. **One-way import** from workbench's config (old path or new XDG one), if
   present, read-only: the `models` map, and for each repo a registered stack
   references, its `copy_files` list keyed by host/owner/repo. Absent
   workbench config → `config.DefaultModels()`.
4. For each stack member, derives the **canonical** URL from the old clone's
   origin: `RemoteRef` resolves an ssh host alias to its real host, so
   `git@github-panamafrancis:panamafrancis/workbench.git` becomes
   `git@github.com:panamafrancis/workbench.git`. When the origin used an
   alias, migrate adds the matching `url.<alias-base>.insteadOf <canonical-base>`
   to the user's global git config (printing it first), so the canonical URL
   still authenticates with the right key. Then it clones into the repo cache
   and copies that repo's `copy_files` from the old local clone into the cache
   clone, so the first tree still has its `.env`.
5. Rewrites each stack's `supatree.yml` to the URL form (§2) using the
   canonical URLs — never a personal alias, since the spec is shared — as a
   commit in the stack repo. A stack repo with uncommitted changes is skipped
   with a warning, not committed over.
6. Moves stacks that sit under the old `~/.supatree/stacks/` to
   `~/supatree/stacks/`; stacks registered elsewhere (e.g.
   `~/code/panamafrancis/supatree-workbench-stack`) stay where they are. The
   registry is updated.
7. Renames `~/.supatree` to `~/.supatree.pre-xdg` rather than deleting it —
   the way back if something was missed. Delete it by hand later.

`workbench migrate` is the same shape for workbench's own files, including the
old-layout guard (workbench's sidebar also runs in a restart loop): refuse
while any worktree is recorded; copy to XDG homes; drop the removed fields; rename
`~/.workbench` to `~/.workbench.pre-xdg`. The two can run in either order.

The PM's `pm/` directory moves with state, which starts the PM with a fresh
Claude history. Accepted: its durable memory is in the stack notes and
`memory`, not the transcript.

## 2. Self-describing stacks

Today `supatree.yml` lists members by workbench alias, resolved through
workbench's private `~/.workbench/config.yml`. A cloned stack is meaningless
to a teammate, and supatree cannot run without workbench's config.

New `supatree.yml` — members keyed by stack-local alias, carrying only the
remote:

```yaml
members:
  keystone: git@github.com:fraud-zero/keystone.git
  admin-frontend: git@github.com:fraud-zero/admin-frontend.git
deps:
  admin-frontend: [keystone]
```

- One format. The list-of-aliases form is rewritten once by `migrate` and
  then removed; no dual-form `UnmarshalYAML`.
- No per-member hooks. Team-shared setup is the stack's `scripts/setup` (§3);
  personal files are `copy_files` (§1).
- The alias is stack-local: it names `repos/<alias>/` in the tree and the
  `st/<slug>/<alias>` branch. Two stacks may alias the same repo differently.
- `ResolveMember(alias, url)` returns the cache clone for `url`, cloning it
  first if absent. It replaces every `wb.FindRepo(alias)` in `pkg/supatree`
  (instance, startup, remove, sync, review ×2) and `aliasForRepo` in
  `review.go`, which today shells out to each clone's origin to match a PR's
  repo — with URLs in the spec it matches on the spec directly.

## 3. Stack setup hook, never interactive

Today `RunStartupScripts` runs the stack's `scripts/startup` *after*
`OpenOrFocusTab` and on *every* tab creation (including after a Zellij
restart) — so it races the agent and is not "once" as its comment says. No
registered stack has one.

Replace it with `scripts/setup`: run once per tree, at creation, after the
members exist and `copy_files` has run, before any agent tab opens; stdin
closed so it cannot prompt; recorded in `meta.yml` so it is not re-run;
failure warns and leaves the tree usable. Same env as today
(`SUPATREE_NAME`, `SUPATREE_ROOT`, `SUPATREE_MEMBERS`).

`sync` adding a member later runs that member's `copy_files` but not
`scripts/setup` again: setup is per tree, `copy_files` per member. It also
tells you to restart the tree's agents before they commit in the new member
(§1, sandbox grants).

## 4. Stack and repo commands

```sh
supatree stack new <stack> [--from ~/code/dir] [--from-org fraud-zero]
supatree stack add <stack> <owner/repo | url> [--as <alias>]
supatree stack rm  <stack> <alias>
supatree stack dep <stack> <from> <to>
supatree stack clone <git-url>        # teammate onboarding: register the stack, fill the cache

supatree repo ls                      # the cache: clones, size, trees using each
supatree repo rm <owner/repo>         # refuses while a tree has a worktree of it
```

- Members are named by URL (or `owner/repo`, expanded to the GitHub ssh URL);
  never by a local path. `--from` scans a directory only to *read* the
  `origin` of each git repo in it → multi-select picker; the chosen URLs are
  cloned into the cache, the scanned checkouts are not used. `--from-org`
  lists via `gh`, skipping archived repos and forks (one-off, fine for
  quota).
- `stack add` and `stack clone` clone into the cache up front, so the first
  tree does not pay for it.
- `scaffold` becomes an alias of `stack new`.
- `stack clone` prints the stack's `scripts/setup` if it has one, since setup
  runs on every tree made from it.
- Sidebar `S` runs the same flow; `stack_add`/`stack_rm`/`stack_dep` become PM
  MCP tools (gated on mutation autonomy).
- `supatree init` covers models, the `supatree-agent` nono profile and nono
  checks (reusing `pkg/setup`), and writes only supatree's own files —
  onboarding never touches workbench.

## 5. The repo split

1. `git filter-repo` supatree's paths (`pkg/supatree`, `supacmd`,
   `cmd/supatree`, `scripts/e2e-supatree.sh`, supatree plans/docs) into
   `panamafrancis/supatree`, keeping history.
2. Workbench repo deletes them; module path unchanged. Point the `SUPATREE=1`
   gate's wording at the supatree repo.
3. Supatree `go.mod` requires workbench; local dev uses a `go.work` with both
   checkouts. Shared `pkg/*` is not a stable public API — say so in the
   workbench README. Day-to-day bumps use pseudo-versions
   (`go get github.com/panamafrancis/workbench@<sha>` on a merged main
   commit), so a shared-code change never waits on a workbench release; tags
   are cut only for workbench's own releases. A cross-cutting change is two
   PRs in order: workbench first, then the supatree bump that adapts to it.
4. Install paths and release checks:
   - `pkg/setup/update.go:91` and `README.md` suggest
     `go install …/workbench/cmd/supatree@latest`, which stops resolving.
     Workbench's update check stops mentioning supatree; supatree gets its own
     check against its own releases.
   - Makefile `dist` / `install` / `e2e-supatree` targets move to the supatree
     repo.
   - `supatree version` reports its own version and the workbench module
     version it was built against (from `debug.ReadBuildInfo`).
5. Split AGENTS.md: shared-package discipline (quota, cache single writer)
   stays in workbench; supatree sections move. Rewrite what the split
   invalidates: "member repo definitions come from workbench's config —
   supatree never duplicates them" (supatree owns its clones now), and the
   Conventions line listing `repo.RunCopyFiles`/`RunStartup`/`RunCleanup`.
   Check `plugin/` for supatree content. Each README cross-links: "supatree is
   workbench for multi-repo changes".
6. Release: separate Homebrew formulas / install commands; CI in each repo;
   supatree CI pins workbench and runs both e2e suites against it.

With both tools open on the same repos, each polls with its own cache.
Unchanged polls are 304s and free, but the GraphQL review/check refresh runs
once per tool. Accepted.

## Open issues in panamafrancis/supatree

The issue tracker has already moved; the code has not. Until step 5, fixes for
these land in this repo and reference `panamafrancis/supatree#N`.

| Issue | Relation to this plan | Where it is handled |
|---|---|---|
| #3 PM relaunched mid-session; per-tree allow list | The plan's per-launch grants would make it worse (more per-tree flags to go stale). Its own proposal — allow the trees base, deny `trees/*/repos/**` — works only on macOS. `filesystem.deny` is a profile field (no CLI flag); on macOS a deny glob compiles to a Seatbelt rule that also covers trees created after launch. On Linux, Landlock is allow-only: a deny overlapping an allow makes nono **refuse to start**, and the only workaround (`capability_elevation`) prompts interactively, which the PM cannot answer (nono 0.79.0 profile guide). | **Tested 2026-10-02 on nono 0.79.0 / macOS** (`allow trees/`, `deny trees/*/repos` + `trees/*/repos/**`): tree state writable, existing *and* later-created `repos/` denied — even `mkdir` of a new tree's `repos/`. So on macOS the proposal works as a backstop. **In the plan:** per-tree state moves to `<state>/trees/`, granted to the PM as one dir, so a new tree never needs a relaunch on either platform (§1); on macOS the profile adds the `repos/` deny on top. Either way, the PM's `new_tree`/`remove_tree` must first move to the watcher (§1), or the deny blocks the PM from creating trees at all. The sandbox audit (§1) answers its "allow list isn't what's enforced" finding. Out of scope: logging relaunch reasons, deferring relaunch until the PM is idle. |
| #2 `rm` leaves tab, agent and layouts behind | Migration's "close every tree" relies on `rm` actually stopping a tree. | **Before step 3:** fix `rm` (stop agents, close the tab and `<tree>:<repo>` sub-tabs, delete layouts, drop mail/bus registration). Every agent runs under nono, so `nono ps --json` / `nono stop` may let `rm` (unsandboxed) find and stop a tree's agents — including pane-less ones — without supatree tracking PIDs itself; verify `nono ps` exposes enough (cwd or args) to map a session to a tree. The same would serve #1's orphan cleanup. Migration also checks for live processes itself (§1) rather than trusting `rm`, and skips `layouts/`. `supatree gc` for existing orphans can follow. |
| #1 launch storm; retried `supatree pm` orphans PMs | Orphaned PMs are pane-less old-binary processes that would survive "close every tree". | **In the plan:** migration's process check (§1). Out of scope: launch throttling and a real PM singleton — independent of the split, and they touch `launch.go`/`pm.go` alongside step 1, so land them before or after steps 1–3, not interleaved. |

## Order

Steps 1–3 merge one at a time and **ship as one release**, because
`supatree migrate` needs all of them: it imports (1), moves paths (2) and
rewrites specs to a format only (3) can read. Main stays installable on the
old layout throughout: step 1 keeps a **temporary fallback** that reads
workbench's models and repos while supatree's own config has none, and step 3
deletes it in the same change that adds `migrate`.

1. Cut the runtime ties: `models` and `repos` overrides in supatree's
   `config.yml`, supatree's own types, `config.CopyFiles`, `sandbox` taking a
   `config.Model`, nono profile generator in `pkg/setup`, drop
   `AllWorktreeNames` from supatree, confine `wbCfg` to the temporary
   fallback; drop the repo hook fields from both tools (`copy_files` stays).
2. XDG-pinning test helper and e2e preambles first; the sandbox audit and its
   enforcement e2e case; then `pkg/xdg`, path moves (per-tree state to
   `<state>/trees/` with the `.supatree` symlink), `TreeGrants` and the grant
   matrix, PM tree create/remove delegated to the watcher (before any PM
   grant is narrowed), the old-layout guard on every command (both tools).
3. The supatree#2 `rm` fix lands first. Then `RemoteRef`, repo cache, URL-form `Spec` + `ResolveMember`,
   `scripts/setup`, then `supatree migrate` / `workbench migrate` on top,
   deleting the step-1 fallback and `wbCfg`. Update the generated and
   user-facing text that describes members by alias — `contextfile.go`
   (info.md, the scaffolded AGENTS.md), the `docs` topics, the README. e2e:
   supatree runs with no workbench config at all, and `migrate` runs against a
   fixture old layout, including an ssh-alias origin.
4. `stack` and `repo` commands, standalone `supatree init`, sidebar/MCP entry
   points.
5. Repo split.

1–4 land in this repo; 5 is mechanical once they are in.
