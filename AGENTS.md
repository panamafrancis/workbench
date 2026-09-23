# workbench — Agent Guide

Sandboxed git worktree manager. Each piece of work gets a git worktree opened as a new Zellij tab, with the chosen LLM running inside a nono security sandbox.

## Build & run

```sh
make build          # produces workbench + supatree binaries under dist/
make install        # go install both binaries
make ci             # fmt + lint + vet + test
make e2e            # build + run scripts/e2e.sh AND scripts/e2e-supatree.sh
make hooks          # enroll .githooks/ as git hooks
go test ./...
```

This repo ships **two** binaries: `workbench` (root package, `cmd/`) and
`supatree` (`cmd/supatree`, commands in `supacmd/`). supatree reuses the
workbench `pkg/*` packages; shared code (the zellij `Workspace`, the MCP
`Server` framework, `config.WithFileLock`) is parameterized rather than
duplicated. See the "Supatree" section below.

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
  version.go            # workbench version (Version var stamped via ldflags)
pkg/
  config/
    config.go           # Config types, Load/Save, FindRepo, FindWorktree, CRUD helpers
    paths.go            # ConfigDir, ConfigPath, StatePath, LayoutsDir, WorktreePath
    state.go            # State (last_run_version, update check, gamification stats/achievements), LoadState/Save
  git/
    worktree.go         # DefaultBranch, FetchOrigin, CreateWorktree (returns offline bool), RemoveWorktree
    names.go            # GenerateName (city names), ValidateName, ExtractBaseCity, IsCityName
    status.go           # IsDirty, BranchName
  github/
    gh.go               # LookupPR (by head) / LookupPRByNumber / ResolvePR via gh CLI
    cache.go            # PR status cache — Get/Set/Rename/Delete/IsStale/Ref
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

All state lives under `~/.workbench/`:
- `~/.workbench/config.yml` — repos, worktrees, model definitions
- `~/.workbench/state.yml` — last-run version, update check cache, gamification stats (cities_visited, worktrees_created, worktrees_merged, achievements, activity_days), and `reserved_cities` (names still occupied by a worktree or its lingering Claude history, excluded from name generation)
- `~/.workbench/worktrees/<alias>/<name>/` — default worktree location
- `~/.workbench/layouts/<name>.kdl` — generated Zellij layouts (transient)

`models` is an open map — users add arbitrary entries (`mymodel`) with any `binary`/`nono_profile`/`args`. Never hardcode model names.

`config.Load()` reads from disk every time. `config.Save()` writes atomically via temp+rename. `show_stats` (`*bool`, default true) controls the gamification stats box in the TUI sidebar.

## TUI model

`pkg/tui/model.go` is the Bubble Tea root model. Key patterns:

**Cursor skips repo headers** — `moveUp`/`moveDown` in `tree.go` consult `selectable()`, which skips `isRepo` items *except* for a collapsed repo: that header is the only row the repo has, so it must take the cursor. Without the exception, folding a repo strands the cursor in the next one and `zM` (fold all) leaves nothing selectable at all. Empty repos get a selectable placeholder row. Every fold path (`toggleCollapse`, `collapseContaining`, `expandContaining`, `setAllCollapsed`) parks the cursor via `focusRepo`, not bare `clamp`.

**Sidebar navigation and viewport** — `tree.go` mirrors `pkg/supatree/tui`: `moveBy` (used by `ctrl+d`/`ctrl+u` via `halfPage`), `gotoTop`/`gotoBottom` (`gg`/`G`), `jumpRepo` (`}`/`{`), `setAllCollapsed` (`zM`/`zR`), and the two-key sequences driven by `Model.pending` (an unrecognized second key clears the prefix and falls through, so a mistyped `g` never swallows a command). `tree.view(width, avail)` renders only the `viewport` window — `Model.View` renders `viewTail()` first and sizes `avail` from its line count. The mouse wheel calls `scrollBy`, which pans `scroll` and clears `follow`; `viewport` drags the scroll offset to the cursor only while `follow` is set, so a wheel-scrolled view survives the 60s tick. Deliberate cursor moves re-arm `follow`; `clamp` deliberately does not.

**Refresh reloads from disk** — `refreshMsg` calls `config.Load()` and replaces both `m.cfg` and `m.tree.cfg`. Don't just call `refreshDirty()` alone.

**GitHub quota discipline** — the sidebar runs once per Zellij tab, so a naive per-process poll multiplies GitHub's 5,000/hour **GraphQL** quota (the bucket `gh pr list` spends — not the `core` bucket `gh api rate_limit` reports) by the tab count. `fetchVisibleCmd` therefore: re-reads the on-disk cache before building targets; skips branches with no `origin/<branch>` ref (`git.HasRemoteBranch` — an unpushed branch cannot have a PR); trusts merged/closed statuses for `github.TerminalMaxAge` (24h, enforced inside `Cache.IsStale`, bypassed by a forced refresh); and runs the actual `gh` calls under `config.TryFileLock(config.PRCacheLockPath())` so only one tab fetches per round while the rest emit `prSkippedMsg` and pick up the cache it writes. A rate-limit response arms a persisted `SetRetryAfter` cooldown that every tab observes. A repository gh cannot see (`ErrRepoNotFound` — "Could not resolve to a Repository": moved, renamed, or no access for the active account) is per-branch, not per-round: it must not abort the batch, and it must not be retried every round either, which is what once filled `watch.log` with 2,800 identical lines and spent ~700 GraphQL calls an hour. `Cache.MarkUnreachable` backs that key off for `UnreachableBackoff` (6h), persisted in the cache file so every poller honours it; `IsStale` reports it fresh meanwhile, `Set` clears it, and a forced refresh still tries. `pkg/supatree/tui` mirrors this exactly — keep the two in step.

**A PR is identified by number, not by branch.** `gh pr list --head <branch>` only finds a PR whose head ref is *currently* that branch, and a rename retargets the head only for a PR still open when the new branch is pushed — one that merged or closed first is frozen on the old name, as is one whose push failed or never ran. So `Cache.Rename` carries the entry's `Number` but zeroes `FetchedAt` (a status verified under the old key must not be trusted, nor held for `TerminalMaxAge`), and every fetcher goes through `github.ResolvePR(path, branch, cache.Ref(branch))`: head lookup first — a branch may have picked up a *new* PR — then `gh pr view <n>` when that comes back empty and a number is known. The ref carries the cached URL as well as the number, and a by-number result from a different repo is rejected — the cache is keyed on branch name alone, so two repos with the same branch name share one entry and the number would otherwise resolve an unrelated PR. Never call `LookupPR` directly from a fetch path.

**Local re-sync on focus/tick** — `reloadLocalState()` re-reads `config.yml` + `state.yml` and swaps `m.cfg`/`m.tree.cfg`/`m.state` on `tea.FocusMsg` and every `tickMsg`, so sidebars in different tabs stay consistent without a manual `r`. It deliberately skips the network PR fetch (that's what the full `refreshMsg` is for). It's a no-op while `m.mode != modeNormal` or a create is in flight (`m.creating`), because reloading would either shift the `pendingRepoIdx`/`pendingWorktreeIdx` slice indices under an open confirm/input mode or drop an optimistic worktree that isn't persisted yet.

**Inline input mode** — the model has an `inputMode` state machine (`modeNormal` / `modeAddRepoPath` / `modeAddRepoAlias` / `modeNewWorktree` / `modeConfirmDelete` / `modeConfirmQuit` / `modeOpenWith` / `modeHelp`). When mode is non-normal, `Update` routes `tea.KeyMsg` to `updateInput()` which handles `enter`/`esc` and passes everything else to the `textinput.Model`. Use this same pattern for any future inline prompts.

**Key bindings** — defined in `pkg/tui/keys.go`. Add new bindings to both `KeyMap` struct and `DefaultKeyMap`, then handle in `model.go`'s `Update` switch.

**Sidebar mode** — when `WORKBENCH_SIDEBAR=1` is set (injected by the layout), `q` prompts for confirmation instead of quitting immediately. The layout wraps `workbench ls` in a restart loop with backoff (`sleep 0.2` on success, `sleep 2` on failure).

**Env injection** — `WriteTabLayout` in `layout.go` accepts a `map[string]string` of env vars to inject into the agent pane's KDL `env {}` block. Keys are sorted for deterministic output. The **sidebar** pane also gets `WORKBENCH_WORKTREE_NAME=<name>` (the tab's worktree); the TUI reads it in `New()` into `tree.activeWorktree` to render the passive `▸` "you are here" marker (see `tree.go` `view`). The root session sidebar has no such env, so `activeWorktree` is empty and no marker shows.

**Pane vs tab naming** — the agent pane's *display* name is `{repo}/{worktree}` (derived from `WORKBENCH_REPO_ALIAS` in the injected env, KDL-escaped via `quoteKDL` since aliases aren't charset-validated). The Zellij *tab* name and the layout filename stay the bare worktree name — that name keys tab lookups (`OpenOrFocusTab`, `TabNames`, the TUI `openTabs` map) and is a path component, so it must remain slash-free and validated. `WriteTabLayout` validates the worktree name up front with `git.ValidateName`.

## Zellij session management

`workbench start` embeds a session layout via `go:embed` (`session.kdl.tmpl`), writes it to `~/.workbench/layouts/session-<name>.kdl`, and uses `syscall.Exec` to hand the terminal to zellij. Sessions are prefixed with `wb-`.

`pkg/zellij/session.go` provides `ListSessions`, `WriteSessionLayout`, `CreateBackgroundSession`, `DeleteSession`.

Layout/session writing hangs off a `zellij.Workspace` (`pkg/zellij/workspace.go`) that carries the tool-specific bits (`LayoutsDir`, `SidebarCommand`, `SidebarEnvVar`, `SidebarActiveEnvVar`, `SessionPrefix`, `SessionTab`). `WriteTabLayout`, `OpenTab`, `OpenOrFocusTab`, `CleanupLayout`, `CleanupStaleLayouts`, `WriteSessionLayout` are methods on it; `WorkbenchWorkspace()` / supatree's own workspace supply the values. Pure `zellij action` wrappers (`GoToTab`, `TabNames`, `ListSessions`, `NewPane`, ...) stay free functions. `NewPane` is the one pane-level call: it opens an unsandboxed shell at a cwd in the caller's current tab, with no layout and no tab name, for "take me to this directory" actions that must not claim an identity in the tab namespace agents are keyed on. Two zellij quirks are baked into it: `new-pane` splits the *focused* pane — which is the sidebar that asked — so it steps the focus right first (best effort) and splits `--direction right`, landing the shell in the main area beside the agent instead of stacked under the sidebar; and `--cwd` is honoured only for a pane that runs a command, so the shell (`$SHELL`, else `bash`) is passed explicitly with `--close-on-exit`. A bare `new-pane --cwd <dir>` silently opens in the session's cwd.

`pkg/zellij/client.go` provides tab-level operations (`OpenTab`, `GoToTab`, `OpenOrFocusTab`). These call `zellij action` subcommands and only work inside a Zellij session.

**Never close a tab by focusing it.** `zellij action close-tab` closes the *client's current tab*, and closing a tab kills every process in it — including the sidebar that asked for the close. That is why `OpenOrFocusTab` replaces a tab whose agent has exited by creating the replacement **first** and only then closing the husk via `close-tab-by-id` (ids come from `TabIDs`): the caller may well be that tab's own sidebar, and it dies on the close, so nothing may be left to do afterwards. The old close-then-create order silently did nothing at all when an agent was reopened from its own tab's sidebar. `closeTab` (by focus) survives only as the fallback for when an id cannot be resolved.

Note that `zellij action dump-layout` reports a tab as *empty* once its `close_on_exit=true` command pane has exited, even though the sidebar pane is still running — so `tabHasCommandPane` answers "no agent here", which is what triggers the replacement path.

## nono sandbox

`BuildNonoArgs` returns `["run", "--profile", <profile>, "--allow", <worktreePath>, "--", <binary>, <args...>]`. The profile and binary come from the model config entry — no hardcoded mapping.

## Adding features

- **New inline TUI action**: add key to `keys.go`, add `inputMode` constants if needed, handle in `model.go` `Update` and `updateInput`.
- **New CLI command**: add file under `cmd/`, wire into `rootCmd` in `cmd/root.go` via `rootCmd.AddCommand(...)` in `init()`.
- **New MCP tool**: `pkg/mcp` is a reusable framework — `rpc.go` has the JSON-RPC `Server{Name,Version,Tools,Prompts,Gate}` + stdio loop; `workbench.go` builds the workbench tool set. Add workbench tools there; supatree tools live in `pkg/supatree/mcp.go`. Input schemas are built with the shared helpers in `schema.go` (`ObjectSchema`/`StringProp`/`BoolProp`/`EnumProp`/`EmptyObject`) — both tool sets use them, so don't hand-roll the map literals. Tool handlers have signature `func(args map[string]any) (text string, isError bool)`.
- **New config field**: add to structs in `pkg/config/config.go`, update `DefaultConfig()` if it needs a default.
- **Worktree creation hooks**: `copy_files` runs first (copies gitignored files from repo), then `startup_script`.
- **Change what opens in a new tab**: edit the KDL template in `pkg/zellij/layout.go`.

**Always update `README.md`** when adding or changing user-facing behavior: new config fields, new CLI flags, new keybindings, changed lifecycle behavior, or nono sandbox requirements.

## Supatree

`supatree` manages a set of worktrees — one per repo — for a single cross-repo issue. State lives under `~/.supatree/`.

**Model.** A *stack* is a git repo (`supatree scaffold`) holding `supatree.yml` (member repo aliases + `deps` edges), `AGENTS.md`, and `scripts/`. A *supatree* is a worktree of that stack repo at `~/.supatree/trees/<name>/` on branch `st/<name>`, with each member repo checked out under `repos/<alias>/` on branch `st/<slug>/<alias>` (slug starts as the city name; `rename-branch` changes it). Member repo *definitions* come from workbench's `~/.workbench/config.yml` (resolved by alias) — supatree never duplicates them.

**Discovery.** The registry (`~/.supatree/config.yml`) lists only stacks + defaults; `~/.supatree/ui.yml` holds the sidebar's fold state (`uistate.go`). Live supatrees are discovered by scanning the trees base for `<name>/.supatree/meta.yml` (`supatree.List`). Per-tree state: `.supatree/meta.yml` (name/slug/stack/model), `.supatree/agents.yml` (named agents + session IDs), `.supatree/info.md` (generated). All three are gitignored, as is `repos/`.

**Package layout.** `pkg/supatree/`: `paths.go`, `config.go` (registry), `spec.go` (supatree.yml), `meta.go`, `deps.go` (`TopoSort`), `instance.go` (`LoadInstance`/`List`/`Get`), `create.go`, `sync.go`, `remove.go`, `rename.go`, `agents.go`, `contextfile.go` (info.md + scaffolded AGENTS.md), `scaffold.go`, `startup.go`, `status.go` (the activity state model), `uistate.go` (sidebar fold state), `prfetch.go` (shared PR fetch), `review.go` + `post.go` (review mode), `mcp.go`, `tui/` (the `supatree ls` sidebar), `dash/` (the `supatree dash` dashboard). CLI in `supacmd/`, entry `cmd/supatree/main.go`.

**Status model (`status.go`).** `Status(insts, cache, opts)` derives a `Summary` from local git plus the on-disk PR cache — it never calls GitHub, so any UI may call it freely. Keep the derivation pure and testable: `localState` does the git I/O, `memberState` and `rollup` are pure functions over it and are what the tests cover. Member states run `absent → idle → wip → pushed → draft → open → changes|approved → merged|closed`; the tree rollup is the least advanced member that still has work, with `Blocked`/`Dirty`/`Stale` as orthogonal flags and `TreeDone` meaning "reap this tree". Two states sit off that ladder: `foreign` (the branch checked out is not the one the tree derives — every other number would be measured against a branch the worktree is not on, so `localState` stops collecting them and `rollup` sets `Foreign` instead of ranking it) and `in-review` (a review tree's member, ranked alongside an open PR). `memberState` takes `tracked`, because a review tree holds every member of its stack and the repos the change does not touch must fall out as `idle` — left `in-review` they would pin the rollup below `reviewed` forever. Local git runs concurrently (`statusWorkers`) because a dozen supatrees is a few hundred git processes.

**Review mode (`review.go`, `post.go`, `supacmd/review.go`).** `supatree review <pr-urls…>` builds a *review tree*: `Meta.Mode = reviewing`, `Meta.Review` maps each member alias to the `ReviewRef` (repo, number, URL, head SHA, head ref, base) it is checked out at, and `createMember` starts it from `refs/pull/<n>/head` — the only ref that reaches a PR opened from a fork — on a tree-local `review/<slug>/<alias>` branch. The branch prefix is not cosmetic: the author's branch name cannot be checked out twice in one clone, a detached HEAD would make `CurrentBranch` report the literal `HEAD`, and a tree-local ref is unambiguously supatree's to delete.

**A review tree is keyed on the pull request, not the branch.** `Member.CacheKey()` returns `pr:<repo>#<n>` there — the PR cache is one flat map across every repo and tree, which is safe for `st/<slug>/<alias>` only because that name embeds the alias, while two members of one cross-repo change routinely share a branch name. `FetchTargets` therefore exempts review members from the unpushed-branch skip (their branch has no origin ref *by design*) and resolves them with `github.ResolvePRByNumber`, since `gh pr list --head` on a tree-local branch is a guaranteed-empty request. `NewReview` seeds the cache with number+URL and no status so the first round has something to resolve. **`LoadInstance` attaches `Member.Review` only while the mode is still `reviewing`** — `ForkReview` keeps `Meta.Review` as provenance, and carrying it past the fork would key the forked tree's members on the *author's* PR forever.

**The authoring commands refuse** (`refuseAuthoring`, `ErrReviewTree`): `rename_branches`, `create_pr`, `create_prs`, `supatree rename-branch`. The refusal names the alternative, because the agent that reaches it was told by its own instructions to rename before opening a PR. `contextfile.go` renders `reviewTmpl` instead of `infoTmpl`, and the review *craft* lives behind `docs topic: review` rather than in the generated info.md — it is generic, long, and identical in every tree, so it must not load into every context by default.

**`review_post` is the one outward-facing tool.** It needs the `outward` permission — off at every level, `auto` included, **except in a review tree**, where `Resolve` defaults it on (`Config.ReviewOutward`, `review_outward: false` to withhold; a tree's own `outward` still wins). Posting the review, APPROVE included, is what a review tree is for, and every other authoring verb there is already refused and `outwardDenied` refuses rather than falls through when the config cannot be read. It refuses again when `headMoved` — cached `PRInfo.HeadOID` against the recorded `ReviewRef.Head` — says the author has pushed, since GitHub marks an inline comment outdated the moment it lands on a passed commit. `review_refresh` / `supatree review refresh` re-fetches each head and `git reset --hard`s the worktree onto it, **never** over uncommitted work (review notes are not disposable). `supatree review fork` renames every member onto `st/<slug>/<alias>` (phased with rollback, like `RenameBranchSlug`) and records each PR's head ref in `Meta.Base`, so `createOnePR` passes `--base` and the change arrives as a proposal on the author's PR rather than a rival against main.

**A review tree's merge is the author's milestone.** `Event.Mode` is stamped on every event so the append-only ledger can never be reinterpreted; `History` counts those on a `Reviewed` axis and refuses to add them to `Merged`. Review trees are never `Blocked` (failing checks are the author's problem, requested changes are your own review landing), finish as `TreeReviewed` rather than `TreeDone`, and report `author_pushed` — the one transition that invalidates a review in progress, and the only one diffed outside the lifecycle state.

**Teardown deletes only the branch the tree created.** `removeMember` takes the branch it *owns* and runs `git branch -D` on that alone; a member found on anything else is reported and left in place, because a `gh pr checkout` in a member worktree would otherwise have the author's branch destroyed by an ordinary `supatree rm`. There is an e2e case for exactly this.

**PR fetching (`prfetch.go`).** `FetchTargets` + `FetchPRs` are the single implementation of the GitHub quota discipline — cache re-read, staleness gate, skip unpushed branches, cross-process try-lock, persisted rate-limit backoff. The sidebar and the dashboard both call them; do not write a third copy, and do not add a poller that bypasses them. Both callers run the whole thing inside a `tea.Cmd`: selecting targets is one git process per member, which visibly stalls a UI if done on the main loop.

**Autonomy (`autonomy.go`).** `Config.Resolve(meta)` folds the workspace default and the tree override into a `Permission`. The workspace default is not convenience — autonomy lives in per-tree `meta.yml`, so without it nothing governs `new_tree`, whose tree does not exist yet to carry a level. **Outward-facing is a second axis, not a higher level**: `auto` grants mutation and deliberately not a public voice, and there is a test asserting that. **The level gates the unasked case only** — `AsAsked` carries the PM's assertion that the human asked for this action in this turn, and `AllowsMutation` honours it below `auto`, because the word in `meta.yml` has always meant "unasked" and a gate that also refuses a direct request turns the default level into a wall in front of the commonest interactive verb there is. It does not lift `off` (report-only is not a level you talk your way past) and it cannot hold on a `Scheduled` turn (nobody is in one to have asked) — both have tests. Trust it the way `remove_tree`'s `force` is already trusted: this is a consent boundary, the sandbox is the security boundary, and the PM is the only thing that knows who asked. `AsScheduled` caps a scheduled turn at `nudge` (nobody is watching) unless the job opts in, and returns a copy rather than mutating. `Deny` must name the setting a human would change; a bare "not permitted" is useless.

**Memory (`memory.go`).** `notes/` in the *stack repo*, not a store of our own: diffable, blameable, reviewable, team-shared, and readable by supatree itself. `Recall` is substring search and `History` is exact derivation from the ledger — keep that split, and keep the PM instructed never to answer a status question from `recall`. `isNote` excludes the scaffolded README, which otherwise matches every query with its own examples (found by a test). `MemorySummary` is the injection budget: a hard cap in `info.md` with everything else behind `recall`, because read-time is what has to be rationed. `logRecall` records hits *and* misses from day one — without it the 30-day kill criterion is unmeasurable. `sandbox.ArchiveSessionCache` runs before `ClearSessionCache` on reap: move now, distil later, and never block a removal on it.

**Schedule (`schedule.go`).** `Due(jobs, state, evs, now)` is pure and carries three rules that are each a table test: seed an unseen job id rather than treating "never fired" as overdue; coalesce a missed window into **one** fire (anacron, not cron); and suppress a job with nothing to say (`when: events_since_last` is the default). A *suppressed* fire still advances `LastFired`, or the job is due on every tick until something happens and then fires at the wrong time. `At:` compares against today's occurrence rather than counting elapsed time, which is what keeps the clock going back from manufacturing a second 09:00. The scheduler runs inside the watcher — same loop, same singleton lock, no new poller — and fires by appending to `requests.jsonl`, so a scheduled job and a pressed `m` arrive identically.

**Three MCP gates, not two.** `recallTools` are ungated (memory only one caller can read is memory half the system cannot use); `dualTools` (`agents`, `message_agent`, `inbox`, `pr_comments`) accept `SUPATREE=1` *or* `SUPATREE_PM=1` and resolve their supatree from an optional `tree` argument, falling back to the environment; `pmTools` require `SUPATREE_PM=1`. The dual set exists because the PM is rooted in no supatree — without it the PM could see every tree and talk to none of them, which is most of the point of having one. `messagingDenied` enforces `AllowsMessaging` for the PM only: an agent messaging its own siblings is collaboration, not autonomous action.

**The ledger is bounded and degrades softly.** `AppendEvents` rotates *before* appending — rotating afterwards renames away the file the new events just landed in — keeping exactly one generation (`events.jsonl.1`), which `ReadEvents` reads first so the result stays a timeline. A torn JSON line costs that line; a line longer than the scan buffer ends that file's read and returns what came before. Both matter because four readers depend on this one call (sidebar, dashboard feed, schedule gate, `History`) and a hard error would take all four down together. The sidebar reads it in a `tea.Cmd`, never on the main loop: one sidebar runs per Zellij tab.

**PM agent (`pm.go`, `requests.go`, `supacmd/pm.go`).** Rooted at `~/.supatree/pm`, tab `PMTab` (reserved like `DashTab`). **Singleton by tab name, not by file lock** — `OpenOrFocusTab` focuses the live tab rather than opening a second one, and nothing in the CLI process stays alive to hold a lock on the agent's behalf, so a lock here would be theatre. Gated by a *second* MCP gate: `pmTools` require `SUPATREE_PM=1`, and the PM deliberately does **not** set `SUPATREE`, so the tree-scoped tools stay hidden rather than resolving nothing and failing confusingly.

`PMGrants` is the security boundary and has tests asserting it: allow `~/.supatree/pm`, each tree's `.supatree/`, and the stack repos; read the trees base; never anything under `repos/`. Trees are enumerated at launch because nono cannot glob mid-path. `pm_model` selects the models entry, which is how the PM gets a *different* nono profile from the coding agents — its risk is in network and credentials, not execution.

`requests.jsonl` is the inbound queue: anything that can append to a file can reach the PM, which is what lets Go processes talk to an agent at all. The read cursor is a **byte offset**, not a timestamp — two requests in the same nanosecond are two entries and a timestamp cursor would silently drop one — stored with the file size it was valid for, so a truncated queue is re-read from the start rather than seeked past the end. `CommitRequests` is called *after* the requests are handed over, so a PM that dies mid-turn re-reads rather than loses.

**Hands-off launches (`launch.go`, `supacmd/watch.go`, `supacmd/open.go`).** The PM cannot open a tab — it runs inside nono — so `new_tree start:true` and `start_agent` go through `startAgent`: register the agent (`EnsureAgent`, so the launch resumes by a known session id and `message_agent` can find it), `Deliver` the brief to its mailbox (a review tree with no brief gets `DefaultReviewBrief`), then `QueueLaunch` onto `~/.supatree/pm/launch.jsonl` — in `PMDir` because that is the one place the PM's sandbox is guaranteed to write — stamped with the PM's `ZELLIJ_SESSION_NAME`. **Brief before launch, always:** `OpenRootAgent` appends `KickoffPrompt` (via the model's `prompt_args`, `{prompt}` substituted by `sandbox.AppendPrompt`) only when `HasMail`, so the order is what makes the agent start working instead of sitting at an empty prompt. The kickoff is fixed text by design — the brief rides in the mailbox, so nothing the PM wrote (or relayed from a PR) reaches a command line or a KDL layout. The watcher drains the queue on its own 2s tick (`launchPollInterval`; one `stat`, no GitHub) and runs each launch as a child `supatree open --session <s> --background`, reusing the ordinary open path; `--background` records `FocusedTab` first and `GoToTab`s back, so a launch nobody at the keyboard asked for does not take the terminal away. Replies go the other way: a tree agent's `message_agent agent=pm` lands in *its own tree's* mailbox (the only one it can write), and `ForwardPMMail` — run every watcher round — moves it into `requests.jsonl`, deleting each message only after it is queued.

**Agent addressing and mail (`agents.go`, `mail.go`).** `AgentAddress(tree, agent)` is `st-<tree>-<agent>`, deliberately not the `<tree>:<agent>` zellij tab identity — a colon has no business in a bus name and the namespaces should be free to diverge. It is **persisted** on the `Agent`, not derived at read time: a supatree renamed after an agent launched cannot rename the running process, so the stored value is what that agent actually answers to. Addresses match **exactly** — verified against claude 2.1.261 (2026-09-20), where `--name st-probe` appeared on the bus as exactly `st-probe` while directory-derived names all carried a two-character suffix. Do not reintroduce prefix matching: `st-canberra-main` is a prefix of `st-canberra-main-2`.

`Model.AgentNameArgs` carries the flag (`{agent_name}` substituted by `sandbox.BuildNamedAgentNonoArgs`); a model without it gets no name flag invented for it. `substituteTokens` replaces tokens anywhere in an argument, so `--name={agent_name}` works as well as a separate argument.

**The mailbox is the contract; the bus is an optimisation.** `Deliver`/`Mail`/`Drain` write one file per message under `.supatree/mail/<agent>/` — one file rather than an appended log because delivery and consumption are separate processes, and deleting exactly the file you read needs no read-modify-write of a shared log. A torn message is dropped by a *consuming* read only, so one bad file cannot wedge an inbox forever. Go can write a mailbox; Go cannot use the bus, and neither can a non-Claude model — so anything that treats the bus as the primary channel is wrong and will break the first time either is absent.

**Review comments (`pkg/github/comments.go`, `pkg/supatree/comments.go`).** `PRComments` is the one path that spends a *second* GraphQL query shape per PR — thread resolution (`reviewThreads { isResolved }`) exists nowhere else — so it is on-demand only and must never be called from a poll. Comments and reviews ride along in the same query rather than costing two more calls, and the `last:` windows are bounded so one pathological PR cannot eat the budget. `RepoSlug` reads owner/name from the local origin remote rather than `gh repo view`, because every API route to the same answer spends from the bucket this package exists to conserve. `supatree.Comments` caches on `(branch, number)` and validates against the PR's `updatedAt` — GitHub moves it on any activity, so an unmoved timestamp proves nothing has been said. It takes the *blocking* `WithFileLock` rather than the pollers' try-lock: somebody explicitly asked, so waiting out another round beats returning nothing. A rate-limit response arms the same persisted `SetRetryAfter` cooldown every other fetch path observes.

**Events and the watcher (`events.go`, `notify.go`, `supacmd/watch.go`).** `supatree watch` is the only background poller and a singleton by `config.TryFileLock(WatchLockPath())` — the lock is held for the daemon's whole life, so a concurrent `--once` gets `ErrLockBusy` and **must exit 0 quietly**; that is what stops a cron entry double-fetching, not an error path. It fetches through `FetchTargets`/`FetchPRs` like everything else, so it adds no new GitHub load.

`Diff(prev, cur Summary) []Event` is pure and takes its timestamps from `cur.At` rather than a clock. **An entity absent from `prev` is not diffed at all** — that single rule is what keeps a first run, and a freshly created supatree, from announcing their initial state as news; there is deliberately no "seed the baseline" special case in the caller. `Decide(evs, state, opts)` is the equally pure notification policy: the `eventTier` table, a per-key cooldown, and focused-tab suppression. A *suppressed* event must not arm the cooldown, or looking away once silences the notification you were owed. Note the cooldown is not defending against a still-failing check repeating — a persisted baseline makes `Diff` edge-triggered, so that emits nothing — it covers a restart against a missing `last-status.json` and genuine flapping.

The watcher is spawned by `supacmd/start.go` **before** its `syscall.Exec`, since nothing after that line runs. It is `Setsid`-detached with stdio to `LogsDir()`, and its own exit condition is "no `st-` session remains" — without that it outlives every session and polls forever. That makes it session-scoped, so covering the hours when nothing is open is a launchd/cron `--once` entry rather than a longer-lived daemon.

It runs outside nono (it needs `osascript`) and therefore outside any zellij session, so focused-tab suppression must name the session explicitly: `zellij.FocusedTab(session)` → `zellij --session <name> action dump-layout`. Guard it with `zellij.SessionAlive` first: `dump-layout` sent to a server that is shutting down makes zellij panic, and the watcher only notices a dead session on its next tick. The watcher writes its own `watch.log` through a writer that rotates at 1 MiB to one `.1` generation, opened only by the election winner (a loser rotating would move the live watcher's file away); `spawnWatcher` sends raw stdio to `watch.out`. **The watcher is the only process outside the sandbox**, so it is the only one that can notify; anything sandboxed appends to `NotifyPath()` and the watcher drains it through the same `AppendEvents` + `Decide` path as its own diff — an outbox that bypassed the tier policy would be used as a way around it. Drain **before** `AppendEvents`: a board-tier event exists to be written down rather than delivered, so an outbox drained after the ledger write is an event the PM was told had been recorded and that nothing recorded.

**Dashboard (`dash/`).** `supatree dash` is a read-mostly Bubble Tea view over `Status`, opened in its own tab (`supatree.DashTab`, via `Workspace.OpenOrFocusCommandTab`) by `D` in the sidebar. Row text is built plain and styled last — `truncate` slices runes, so it must never run over a string that already carries ANSI escapes.

**Sidebar (`pkg/supatree/tui`).** Mirrors the workbench sidebar's live-sync + PR-fetch discipline: `reloadWithSelection()` re-reads `supatree.List` on `tea.FocusMsg` and every tick (pinning the cursor by row identity), and `fetchPRCmd(force)` only hits `gh` for cache-stale branches, respects the persisted backoff (`Cache.InBackoff`), and arms `SetRetryAfter` on a rate-limit response — this is what keeps the restart loop and per-tab sidebars from exhausting the gh quota. The supatree workspace sets `SidebarActiveEnvVar: "SUPATREE_ACTIVE_TREE"` (tab name → `▸` "you are here" marker), and `supatree ls` must run with `tea.WithReportFocus()` for the focus re-sync to fire. `n` prompts for a tree name (blank = auto), first asking for a stack only when >1 is registered. **The PM section leads `m.rows`**: `rebuildRows` always emits `rowPM` + `rowDivider` (`pmSectionRows`) before the first tree, even with no supatrees, so the empty state renders them above its hint. `rowDivider` joins `rowSubheader` as non-`selectable()`; the tree-scoped keys go through `selectedInTree()`, which is nil on the PM row, so fold/sync/delete/`a`/`m` never act on a tree named `""`. Its `✉N` badge is `PendingRequests` counted in a `tea.Cmd` (`refreshPMPendingCmd`) alongside the other background refreshes. `View` windows `m.rows` around the cursor (`viewport`) so long lists scroll instead of running off the pane, and `?` swaps the rows for `helpView()` (any key dismisses it).

**Folds are on disk, not in the model.** `supatree.UIState` (`uistate.go`, `~/.supatree/ui.yml`) carries both fold maps, because one sidebar runs per Zellij tab and an in-memory map left every tab rendering a different shape of the same list. Both maps default to their zero value — a supatree is expanded unless listed collapsed, its repositories section collapsed unless listed expanded — so a name nobody has touched gets the intended default with no entry. Every change goes through `Model.persistUI`, which applies the mutation to the *on-disk* state under `UIStateLockPath()` (a peer tab's fold made in the meantime survives) and adopts the result; `reload()` re-reads it, which is how a fold made elsewhere arrives. Because `persistUI` adopts what it read back, a fold poked straight into `m.ui` is dropped by the next one — tests fold through `persistUI` too.

Two levels fold: `space` shuts the innermost section the cursor is in (the repositories list on a repo or section row, the supatree anywhere else), `h` shuts the repositories section first and only steps out to the supatree once it is already shut. The repositories header is its own `rowRepos` kind — selectable, unlike the `agents` subheader — and carries `prCounts`, one coloured glyph-and-count per PR status across the members. That badge is lifted onto the supatree row only while the whole supatree is folded, so it is never printed twice. `prGlyph`/`prStyle` are the single mapping from `github.PRStatus` to symbol and colour, shared with the per-member `prIcon`.

**The footer is not a log.** `updateNormal` clears `m.err`/`m.msg` on every key, mirroring the workbench sidebar: an `actionDoneMsg` error otherwise outlived the moment it described (a rejected supatree name sat under the next create prompt). The new-tree prompt validates as you type — `validateTreeName` checks `git.ValidateName` against the names already in `m.insts` plus `wbCfg.AllWorktreeNames()` rather than re-scanning the trees base, since it runs per keystroke, and `enter` on an invalid name keeps the prompt open instead of firing a create that would only fail. `supatree.New` still re-validates against disk.

**Enter is row-kind contextual.** The row sections carry different meanings and `openSelected` respects that: an agent row is a *process* (its `●`/`○` is liveness) so `enter` opens or focuses its tab via `OpenRootAgent`; a member row is a *place* reporting state (branch, dirty, PR) so `enter` calls `OpenMemberShell` — a plain `zellij.NewPane` at `repos/<alias>/`, outside nono because it is the user's own shell, and a pane rather than a tab because `<tree>:<alias>` is already the member *agent*'s tab name and a disposable shell needs no identity there. The member-scoped agent (`OpenMemberAgent`, nono allowing only that repo) moved to `a`, which reads as "give me an agent here" on every row — the tree-level name prompt on tree/agent rows, the repo-scoped agent on a member row. Don't collapse these back onto one key: the footer hint (`enter shell` / `enter open`, built from `m.selected()`) is what makes the split discoverable, so a new row kind needs its hint case too.

**Sidebar navigation.** Vim motions live in `updateNormal`: `j`/`k`, `ctrl+d`/`ctrl+u` (`halfPage`, sized from the `viewHeight` that `viewport` records each render), `G` (`gotoBottom`), `}`/`{` (`jumpTree` — next/previous `rowTree`), plus the two-key sequences `gg`, `zM` and `zR` driven by the `pending` prefix field (an unrecognized second key clears the prefix and falls through to the normal switch, so a mistyped `g` never swallows a command). Mouse handling is in `updateMouse`: the wheel calls `scrollBy`, which pans `m.scroll` and clears `m.follow`; `viewport` only drags the scroll offset to the cursor while `follow` is set, so a wheel-scrolled view survives the 30s tick reload. Every deliberate cursor move (`moveCursor`, `gotoTop`/`gotoBottom`, `jumpTree`, `setCollapse`, `selectByRow`) re-arms `follow` — but `clampCursor`/`reloadWithSelection` deliberately do not, or background reloads would yank the view back mid-scroll.

**Claude folder trust.** `OpenRootAgent` seeds `projects["<root>"].hasTrustDialogAccepted` in `~/.claude.json` via `sandbox.TrustDir` before launching. Claude asks "Do you trust the files in this folder?" once per directory and records the answer there — but a supatree root is shared by several agents, and each rewrites that whole file from the snapshot it read at startup, so an agent that started before the dialog was accepted writes the unaccepted entry back and the prompt returns forever. Seeding it before any agent starts means every process reads an already-trusted entry and writes it back unchanged. It is best effort (a failure is at most a warning), writes only when the flag is not already set, and resolves the `~/.claude.json` symlink so an atomic write lands on the real file instead of replacing the link. Workbench worktrees are one-agent-per-directory and do not need it.

**Agents.** Several agents share the supatree root but resume independently via session IDs (`Model.NewSessionArgs`/`ResumeSessionArgs`, `{session_id}` substituted by `sandbox.BuildAgentNonoArgs`). `supatree open [--agent <name>]` opens/resumes a root agent; `--repo <alias>` opens an agent scoped to one member (dir-based resume). Tab names: `<name>` for the `main` agent, `<name>:<agent>` otherwise.

**MCP tools** (`supatree mcp`, gated by `SUPATREE=1` except `docs`/`supatree_info`): `supatree_info`, `sync`, `rename_branches`, `create_pr`, `create_prs`, `pr_status`, `docs`. `pr_status` reports the `Status` rollup and refreshes through `FetchPRs` (forced past the staleness gate — an agent asking wants a current answer — but still under the lock and backoff). `create_pr`/`create_prs` refuse a still-city-name slug and (unless `force`) a repo whose dependencies have no PRs yet. Only `supatree init` registers this server (`claude mcp add supatree -s user`); skip it and a supatree session sees only workbench's tools. The **workbench** MCP tools (`create_pr`/`rename_branch`, gated by `WORKBENCH=1`) detect `SUPATREE=1` and redirect to these instead of dead-ending on the missing `WORKBENCH` env var. Branch slugs (`rename_branches`) share `git.ValidateName` — max 40 chars.

**Rename (`rename.go`).** `RenameBranchSlug` is phased so the tree is never half-renamed: every local `git branch -m` runs first and a failure part-way rolls the earlier ones back (`meta.Slug` derives `Member.Branch`, so a member left on the other slug falls outside its own tree and loses its PR and status); only then are meta + info written; only then does `--push` run, collecting per-member failures rather than aborting, since the rename has already been committed to meta.

**Conventions.** Reuse workbench packages — never fork them. `git.CreateWorktree`/`RemoveWorktree`/`RenameBranch`/`CommitsAhead`, `repo.RunCopyFiles`/`RunStartup`/`RunCleanup`, `github.ResolvePR`/`Cache`, `sandbox.BuildNonoArgs`/`BuildAgentNonoArgs`, `config.WithFileLock`. `make ci` + both e2e scripts must stay green.

**e2e scripts: never pipe into `grep -q`.** Under `set -o pipefail`, `-q` exits on the first match, the Go process writing to the pipe dies of SIGPIPE (exit 141), and the pipeline fails even though the assertion passed. It fires on roughly 3% of calls, which reads as flakiness rather than a bug. Use plain `grep … >/dev/null`, which consumes its input.

## Before pushing / creating a PR

Always run `make ci` (fmt + lint + vet + test) and fix all issues before pushing. The linter (`golangci-lint`) enforces errcheck, exhaustive switch, noctx (use `exec.CommandContext`), prealloc, staticcheck, and more — do not suppress warnings, fix them.
