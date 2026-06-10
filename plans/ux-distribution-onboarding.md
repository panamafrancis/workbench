# UX, Distribution & Onboarding

Covers: session launch UX, sidebar polish, first-run setup, agent skills distribution, e2e install testing, lifecycle (update/uninstall), and a polish backlog. Homebrew is captured but deferred. Ordering is deliberate — the session launcher (1) removes the manual layout install, which simplifies the setup wizard (4) and the e2e test (6).

---

## 1. Session launch: `workbench start`

### Problem

Starting a session today is `zellij --layout ~/.config/zellij/layouts/wb.kdl` — the user must have copied `scripts/wb.kdl` into Zellij's layout directory first (`make setup`), and the command is unmemorable. The binary should own this.

### Design

New command: **`workbench start [session-name]`** (and make bare `workbench` with no args suggest it, or alias to it — see discussion).

```
workbench start              # start/attach session "workbench"
workbench start client-x     # named session → multiple concurrent sessions
workbench start --ls         # list workbench sessions (wraps zellij list-sessions)
```

Behavior:

1. **Refuse if already inside Zellij** (`zellij.IsInZellij()`): print "already in a Zellij session" and the tab-opening hint instead of nesting.
2. **Generate the session layout at runtime.** Embed `scripts/wb.kdl` via `go:embed` as a template, render it (sidebar width from `cfg.ResolveSidebarWidth()`) and write to `~/.workbench/layouts/session-<name>.kdl`. This kills the "copy the kdl file" install step entirely — the binary always uses a layout consistent with its own version and config. If `default_zellij_layout` is set in config, use that file instead (escape hatch for users with customized session layouts).
3. **Attach or create:**
   - `zellij list-sessions --short` (and the non-short form to detect `EXITED`) to see what exists.
   - Session exists and is live → `exec zellij attach <name>`.
   - Session exists but exited → attach resurrects it (Zellij session resurrection), or offer `zellij delete-session <name>` if the user wants a fresh start. Needs verification against Zellij 0.43 behavior — resurrected sessions re-run pane commands, which is what we want (sidebar comes back).
   - No session → `exec zellij --session <name> --new-session-with-layout <path>` (verify exact flag spelling on 0.43; older invocation is `zellij --session <name> --layout <path>`).
4. **`exec`, not subprocess.** Use `syscall.Exec` so workbench replaces itself and Zellij owns the terminal directly. No wrapper process lingering, signals behave normally.

Session naming: prefix-namespace workbench sessions (`wb-<name>`, default `wb-main` or just `workbench`) so `--ls` can filter out non-workbench Zellij sessions.

Dead-session housekeeping: zellij accumulates EXITED sessions indefinitely (a real machine showed ~19 dead auto-named sessions). `start --ls` should show dead `wb-*` sessions distinctly, and `start` could offer `zellij delete-session` for them — or a `workbench start --gc` to sweep all dead `wb-*` sessions.

### Multiple sessions and multiple terminals

Three scenarios, all supported by Zellij — `workbench start` just needs to not get in the way:

1. **Different sessions in different terminals** (`workbench start work` in one, `workbench start client-x` in another): fully independent — separate Zellij server instances per session, separate tab sets, separate sidebars. This is the primary multi-session use case and falls straight out of the attach-or-create design above.
2. **Two terminals attached to the *same* session** (`workbench start work` twice): Zellij supports multiple clients per session, tmux-style. By default each client has its own focus (the `mirror_session` config option, default `false`, controls whether clients instead mirror each other's view). Nothing breaks: tabs and their processes exist once in the session, both clients see them. So "re-attach" is not a limitation — same-name `start` from a second terminal simply joins as a second client. Worth one manual check on 0.43 that `attach` to an already-attached session doesn't prompt or force-detach the first client (older zellij versions differed here; there's a `--force-run-commands` / detach nuance on resurrected sessions, not live ones).
3. **Same worktree opened from two different sessions**: this is the only sharp edge. Worktree names are globally unique (they're tab titles), so each session can hold at most one tab per worktree — but two *sessions* can each open the same worktree, giving two sandboxed agent processes in one worktree directory. The per-session running indicator (`zellij action query-tab-names`) won't show session A's ▶ in session B's sidebar.

For (3): `zellij action` has no `--session` flag, but it targets the session named in `$ZELLIJ_SESSION_NAME` (verified on 0.43.1: `ZELLIJ_SESSION_NAME=<other> zellij action query-tab-names` works from anywhere). So the sidebar could aggregate running-state across all `wb-*` sessions and render a distinct marker (e.g. `▷ (other session)`), and `open` could warn before launching a second agent into the same worktree. Cross-session tab *focus* isn't possible (a client can't jump sessions), so warn-and-confirm is the ceiling. This is a polish-backlog item, not a blocker — the failure mode (two agents in one dir) already exists today and is the user's call.

### Files touched

- `cmd/start.go` (new), wire into `cmd/root.go`
- `pkg/zellij/client.go`: `ListSessions()`, `AttachSession()`, `StartSession()` — these are *not* `zellij action` subcommands, so they need a sibling to `runZellij()` that calls `zellij` top-level
- `pkg/zellij/layout.go`: `WriteSessionLayout(name, sidebarWidth)` + `//go:embed` of the template (move `scripts/wb.kdl` to `pkg/zellij/session.kdl.tmpl` or embed from scripts/)
- `cmd/open.go:53`: update the "not inside Zellij" hint to say `workbench start`
- README: Setup section shrinks to "run `workbench start`"; `make setup` can be deleted

---

## 2. Sidebar UX polish

### Problems

1. `j/k` walks over both repo headers and worktrees; repo headers are mostly noise to navigate over and selecting one makes half the keybindings invalid ("select a worktree to delete/open").
2. Command surface is three sets blended together: worktree commands (`o`, `O`, `d`), repo commands (`n`, collapse), and globals (`A`, `r`, `q`) — the footer (`model.go:532`) shows them all flat regardless of what's selected.
3. Cosmetic: repo header renders alias twice — `tree.go:149` does `fmt.Sprintf("%s %s (%s)…", icon, r.Alias, r.Alias, …)`; the parenthesized part was presumably meant to be the repo path (or basename of `LocalPath`).

### Design

**Cursor lives only on worktrees.** `items()` keeps emitting headers for rendering, but `moveUp`/`moveDown` skip `isRepo` items. Consequences to handle:

- **Collapse**: `space` collapses the repo *containing* the selected worktree. Optionally `h`/`l` (or `←`/`→`) for collapse/expand vim-style.
- **Mouse: README is wrong today.** `cmd/ls.go:23` enables `tea.WithMouseCellMotion()`, but no `tea.MouseMsg` case exists anywhere in `pkg/tui` — clicks are silently dropped, despite the README documenting click-to-collapse and click-to-select. Either implement mouse handling as part of this work (click header → toggle collapse, click worktree row → select; modest `MouseMsg` switch mapping y-coordinate to `items()` index) or strike the README section. Recommend implementing — it pairs naturally with the cursor-skip change since clicking is then the *only* pointer-style way to interact with headers.
- **`n` (new worktree)**: targets the repo of the selected worktree. When a repo is collapsed it has no selectable rows — acceptable, since collapsed repos are deliberately out of the way.
- **Empty repos**: a repo with zero worktrees has no selectable row, so you can't `n` into it. Render a selectable placeholder row under empty repos: `  (no worktrees — press n)`. The placeholder is a pseudo-item that only supports `n`. This also fixes the current empty-state where a fresh repo is a dead end in the TUI.
- **Empty config**: cursor on nothing; footer shows only `A`/`q` (already mostly the case).

**Context-sensitive footer.** Replace the flat status line with two groups:

```
worktree: [enter]open [O]open-with [n]ew [d]el [space]fold     global: [A]dd-repo [r]efresh [?]help [q]uit
```

Only render the worktree group when a worktree (or placeholder) is selected; width-permitting, truncate the global group first. The `?` help overlay groups bindings the same way (worktree / global) instead of the current flat list.

**Repo removal** stays CLI-only (`workbench rm repo`) — it's rare, destructive-ish, and not worth a key. Document in help overlay footer: "manage repos: workbench add/rm repo".

**Layout rework: stats + instructions at the bottom, width-aware.** The sidebar pane is ~36 columns; the current header hint line (`model.go:510`) overflows and gets hard-clipped. New vertical layout:

```
workbench                      ← title only (+ session name when multi-session lands)
────────────────────────
▼ wb (~/code/workbench) [2]
  ● clear-detroit …
  ● great-oslo …
                               ← tree fills remaining height
────────────────────────
2 repos · 5 wt · 1▶ · 2* · 3⬆  ← stats line (NEW)
[n]ew [o]pen [d]el  [?]more    ← key hints, moved DOWN, wrapped to width
```

- **Stats line**: all inputs are already in memory — repo/worktree counts from `cfg`, running from `tree.openTabs`, dirty from `tree.dirty`, open PRs from `prCache`. Pure rendering, no new fetches.
- **Key hints**: move from header to footer; render with lipgloss width-aware wrapping (`lipgloss.NewStyle().Width(m.width)`) so they wrap instead of clipping; collapse to just `[?] help` below a width threshold.
- **Zellij primer**: new users land in zellij not knowing how to move. Add a second section to the `?` help overlay with the zellij defaults that matter here: `Ctrl+t` then `←/→` (or mouse) to switch tabs, `Alt+←/→/hjkl` to move between panes (sidebar ↔ agent), `Ctrl+o d` detach, `Ctrl+q` quit session. Caveat in the text that these are zellij *defaults* and may be rebound. One of these (`Alt+←/→ switch panes`) can also live permanently in the footer rotation.

**Sidebar must survive `q` (accidental quit).** Two layers:

1. *Prevent*: the layouts set `WORKBENCH_SIDEBAR=1` in the sidebar pane env; when present, `q` prompts `quit sidebar? [y/n]` instead of quitting instantly (plain `workbench ls` in a normal terminal keeps instant `q`).
2. *Recover*: wrap the sidebar command in both layout templates (`scripts/wb.kdl` session layout and `WriteTabLayout`) in a restart loop — `command "bash"` / `args "-c" "while true; do workbench ls; sleep 0.2; done"` — so even a crash or confirmed quit just respawns it. Closing the *pane* via zellij remains the deliberate way to get rid of it. (Without this, a quit leaves zellij's "EXITED — press enter to re-run" banner, which is exactly the accidental state being reported.)

**Smaller polish while in there:**

- Fix the duplicated alias in repo header → `▼ alias (~/path/basename) [n]`.
- `O` model picker: instead of free-text input, cycle through `cfg.Models` keys with a small selection list (the keys are already enumerated for the prompt at `model.go:551-556`). Free text stops being needed because models is a closed set at runtime.
- Breadcrumb (`tree.breadcrumb()`) becomes less useful once repo headers aren't selectable — keep it; it still shows repo › worktree context.

### Files touched

- `pkg/tui/tree.go`: `moveUp`/`moveDown` skip logic, placeholder item kind, header render fix, `MouseMsg` hit-testing
- `pkg/tui/model.go`: footer/stats rendering, collapse-on-containing-repo, `n` on placeholder, model picker mode, sidebar quit-confirm, zellij primer in help
- `pkg/tui/keys.go`: optional `h`/`l`; regroup help text
- `scripts/wb.kdl` + `pkg/zellij/layout.go`: sidebar restart loop, `WORKBENCH_SIDEBAR=1`
- README keybinding table + strike-or-implement mouse section

---

## 3. Homebrew distribution — DEFERRED

**Decision: skip for now.** `go install` + the init wizard (section 4) covers onboarding well enough until there are external users. Kept below as reference for when it's revisited. One piece is pulled forward regardless: the `--version` flag (ldflags-stamped), which the e2e tests (section 6) and `doctor` want anyway.

### Recommendation when revisited: personal tap + GoReleaser

homebrew-core is out of reach for now (notability bar: ~75 stars, maintainer review); a personal tap is the standard path and takes an afternoon:

1. **Create repo `panamafrancis/homebrew-tap`.** Users then do:
   ```sh
   brew install panamafrancis/tap/workbench
   ```
2. **Switch the release pipeline to GoReleaser.** It replaces the hand-rolled cross-compile in the Makefile + `gh release` script in `.github/workflows/release.yml` and automates the formula:
   - `.goreleaser.yml`: builds (darwin/arm64, darwin/amd64, linux/amd64 — note the Makefile currently skips darwin/amd64), archives, checksums, GitHub release, and a `brews:` section that renders `Formula/workbench.rb` and pushes it to the tap repo on every tag.
   - Needs a `TAP_GITHUB_TOKEN` repo secret (fine-grained PAT with write on homebrew-tap) since `GITHUB_TOKEN` can't push cross-repo.
   - Release workflow becomes ~10 lines (`goreleaser/goreleaser-action`). The existing `make release-patch/minor/major` tagging flow stays as the trigger — nothing changes for the human.
3. **Formula details:**
   - Binary formula from release artifacts (no Go toolchain needed on user machines). GoReleaser handles per-arch sha256s.
   - `depends_on "zellij"` — in homebrew-core, so this works.
   - **nono**: not in homebrew-core. Two options: (a) put a nono formula in the same tap and `depends_on "panamafrancis/tap/nono"`, or (b) leave it a documented prerequisite and have `workbench doctor` (section 4) check for it. Recommend (a) if nono has releases; it makes `brew install` a one-shot. Also: `pkg/sandbox` / README pin nono at `/opt/homebrew/bin/nono` — verify the code resolves it via `$PATH` rather than hardcoding, so Intel-mac (`/usr/local`) installs work.
   - `livecheck` + `test do` block (`workbench --version` — requires adding a version flag/ldflags stamp, GoReleaser injects `main.version` by convention).
4. **Prerequisite already met:** LICENSE (MIT) exists; tags exist (`v*`); release workflow exists to be replaced.

### Tasks

- Add `--version` (ldflags-stamped) to `cmd/root.go`
- `.goreleaser.yml`; rewrite `release.yml` around goreleaser-action; trim Makefile `build`
- Create tap repo + PAT secret
- (Optional) nono formula in the tap
- README install section: brew first, `go install` second

---

## 4. First-run setup: `workbench init` + `workbench doctor`

### Problem

A fresh install currently requires: copy the kdl layout, hand-write config.yml (or rely on defaults), set up a nono profile by hand-editing JSON (including the SSH gymnastics in the README), ensure gh auth. None of this is discoverable.

### Design — two commands, one shared check engine

**`workbench doctor`** — read-only diagnostics, re-runnable any time, also the brew `test`/support tool:

| Check | How | Failure hint |
|---|---|---|
| zellij installed, ≥ 0.43 | `zellij --version` | `brew install zellij` |
| nono installed | `$PATH` lookup | install instructions / tap |
| git installed | `git version` | — |
| gh installed + authed | `gh auth status` | `gh auth login` (optional — degrade like the TUI already does) |
| config exists + parses | `config.Load()` | `workbench init` |
| nono profile referenced by each model exists | profile dir lookup | `workbench init --profile` |
| SSH agent socket reachable | `$SSH_AUTH_SOCK` set | hint |
| at least one repo registered | cfg.Repos | `workbench add repo` |

**`workbench init`** — interactive wizard (use `charmbracelet/huh` for forms; it's the same Charm stack already in go.mod's neighborhood). Idempotent: shows current values as defaults, never clobbers without confirming. Steps:

1. Run doctor checks first; refuse to continue past hard failures (no zellij/nono).
2. **Config**: create `~/.workbench/config.yml` if missing (`DefaultConfig()` already exists). Ask: default model (detect `claude`/`codex` binaries on `$PATH` and preselect what's found), worktree base (default fine).
3. **nono profile generation** — the highest-value step, because it's the most error-prone manual task today. Generate `~/.config/nono/profiles/claude-code-local.json` from answers:
   - repo parent dirs to allow (suggest parents of registered repos, or ask)
   - Go toolchain paths (detect via `go env GOPATH`)
   - SSH: glob `~/.ssh/*.pub` *for the user* (the tool can glob; nono profiles can't — this directly fixes the README's "each key must be listed individually" pain), include `config`/`known_hosts`/`bypass_protection` entries, `unix_socket_subtree: /private/tmp` for the agent socket
   - `$HOME/.config/gh` if gh is authed
   - Then point the `claude` model's `nono_profile` at it in config.yml.
4. **gh auth**: if not authed, offer to run `gh auth login` interactively (spawn with inherited TTY).
5. **First repo**: offer `add repo` inline (path + alias, reusing the validation from `cmd/add_repo.go`).
6. Finish with "run `workbench start`".

**First-run trigger:** when `config.Load()` finds no config file and the command isn't `init`/`doctor`/`help`, print a one-liner pointing at `workbench init` (don't auto-launch a wizard from inside the sidebar pane — `ls` runs non-interactively in the layout).

### Sequencing note

Do section 1 first: it deletes the layout-install step from this wizard's scope. The wizard then never touches `~/.config/zellij/` at all.

### Files touched

- `cmd/init.go`, `cmd/doctor.go` (new); `pkg/setup/` for the shared check engine + profile generator (keep it testable, JSON profile writing is pure)
- `cmd/root.go`: first-run hint in `PersistentPreRunE`
- README: Setup section becomes `brew install … && workbench init && workbench start`

---

## 5. Generic agent skills (commits, PRs) without polluting worktrees

### Problem

We want reusable skills/commands ("create a PR following our conventions", "commit with branch-prefix style", "sweep merged worktrees") available to the agent in every worktree session — but:

- Installing them **per-worktree** (`.claude/skills/` in the worktree) dirties `git status` in every worktree, or forces gitignore hacks. `$GIT_DIR/info/exclude` is shared across all worktrees of a repo (it lives in the common git dir), so even that leaks beyond workbench's scope.
- Installing them **globally** (`~/.claude/skills/`) makes them fire in ordinary, non-workbench Claude sessions where "create a worktree PR" makes no sense.

### Key insight: workbench owns the launch command

Workbench generates the pane command (`pkg/zellij/layout.go` → `nono run … -- claude …`). That means it can **inject environment variables** into every worktree session, giving globally-installed skills a reliable, machine-checkable gate that ordinary sessions never have:

```
WORKBENCH=1
WORKBENCH_WORKTREE_NAME=clear-detroit
WORKBENCH_REPO_ALIAS=wb
WORKBENCH_BRANCH=wt/wb/clear-detroit
```

Implementation detail: zellij pane KDL has no per-pane `env` block, so wrap the command — `command "env"` with args `["WORKBENCH_WORKTREE_NAME=…", …, "nono", "run", …]`. (The startup/cleanup scripts already use these variable names — reuse them exactly.) Verify nono passes env through to the sandboxed process; if not, that's a nono feature request or the vars go in via `claude --append-system-prompt` instead.

### Design: a Claude Code plugin, installed globally, gated by env

Ship a **plugin** (e.g. `workbench` plugin in this repo under `plugin/`, or its own repo) rather than loose skill files:

- **Slash commands are namespaced** (`/workbench:pr`, `/workbench:commit`, `/workbench:sweep`) — explicitly invoked, so they're inert in non-workbench sessions by construction. This is the no-interference property for free.
- **Skills** (model-invoked) state their gate in the description and body: *"Only applies when `WORKBENCH_WORKTREE_NAME` is set in the environment; otherwise this skill does not apply."* First instruction: check the env var and bail if absent. Belt-and-braces: the skill content can also verify the cwd is under the workbench worktree base.
- Skill content encodes the conventions workbench creates: branch naming (`wt/<alias>/<name>`), PR base branch, "don't touch checkouts outside this worktree", etc.
- `workbench init` (section 4) gains a step: detect `claude` on `$PATH`, offer to install/register the plugin (`claude plugin install` or marketplace entry pointing at the repo). `doctor` checks whether it's installed and current.

### Initial skill set: rename-branch, then pr

Auto-created branches carry meaningless names (`wt/wb/clear-detroit`), so a PR opened from one looks bad. Two skills, composed:

- **`/workbench:rename-branch`** — derive a meaningful slug from the actual work (e.g. `wt/wb/session-launcher`; keep the `wt/<alias>/` prefix so worktree branches stay recognizable), then rename. **The skill must not use bare `git branch -m`:** workbench stores the branch per worktree in `config.yml` and keys the PR cache (`pkg/github/cache.go`) by branch name, so a raw rename desyncs the sidebar (PR status stops resolving, `ls` shows the old branch). Instead, the skill calls a new CLI command that workbench provides:

  ```
  workbench rename-branch <new-branch> [--worktree <name>]   # default: infer from $WORKBENCH_WORKTREE_NAME / cwd
  ```

  which does: `git branch -m` in the worktree, update `Worktree.Branch` in config.yml (atomic save already exists), migrate/invalidate the PR-cache entry, and — if the old branch was already pushed — print the push/delete-remote steps (or take a `--push` flag to do `git push -u origin <new>` + `git push origin :<old>`). Requires the sandbox profile to allow `~/.workbench` writes, which the recommended profile already does.

- **`/workbench:pr`** — *first step: if the branch still equals the auto-generated `wt/<alias>/<worktree-name>` pattern, invoke `/workbench:rename-branch` before anything else.* The detection is trivial since `$WORKBENCH_BRANCH` and `$WORKBENCH_WORKTREE_NAME` are both in the env — if the branch's last segment equals the worktree name, it was never renamed. Then: commit conventions, push, `gh pr create` with the repo's base branch.

This ordering (rename inside pr-creation, not at worktree creation) is deliberate: at creation time there's nothing to name the branch *after* yet — the meaningful name only exists once the work has taken shape.

### Plugin vs MCP server — would MCP be more portable?

Short answer: MCP is the more portable *transport*, but the portable *substrate* is the CLI itself — and we get that for free. The reasoning:

- **What actually needs to be agent-agnostic is the operations** (rename-branch, list worktrees, sweep). Those live in the `workbench` CLI, and every coding agent has shell access — codex, gemini-cli, etc. can run `workbench rename-branch` today with zero integration work. The env vars are injected for all models regardless.
- **What MCP would add**: discoverability (tools appear in the agent's tool palette with schemas, instead of relying on the agent knowing the CLI exists) and MCP *prompts*, which surface as slash commands in most clients (`/mcp__workbench__pr`) — a portable cousin of plugin commands. A `workbench mcp` stdio subcommand in the same binary is cheap (mcp-go), and registration is one config line per agent.
- **What MCP cannot carry well**: the conventions — the prose about branch naming, PR style, "stay inside your worktree". That's skill/instruction territory and is inherently per-agent (Claude skills, codex `AGENTS.md`, etc.). MCP also still needs per-agent registration, so it isn't zero-setup either.

**Decision: CLI-first now, Claude plugin now, MCP later if a second agent becomes real.** The plugin stays a *thin* shim (conventions + pointers to CLI commands), so supporting another agent later means writing another thin shim or flipping on `workbench mcp` — not porting logic. Don't build the MCP server speculatively; the only agent in actual use is Claude.

### Why not the alternatives

- **`CLAUDE_CONFIG_DIR` profile swap** (separate Claude config per worktree): clean isolation, but loses the user's auth, memory, and settings — too heavy.
- **Copy skills into the worktree on `open` + cleanup on `rm`**: works, but it's exactly the worktree pollution being asked to avoid, and crashes/manual deletes leak files into PRs.
- **Global loose skills with "only in worktrees" prose but no env gate**: the model has no reliable signal; it will misfire.

### Non-Claude models

This mechanism is Claude-specific. Keep it that way: the `models` map stays generic, and the env vars are injected for *all* models (cheap, harmless), so codex/other agents can build equivalent gating later. Don't invent a generic skills abstraction yet.

### Files touched

- `pkg/zellij/layout.go`: env-wrapped command in the KDL template
- `plugin/` (new): plugin manifest, commands, skills (`rename-branch`, `pr`, later `sweep`)
- `cmd/rename_branch.go` (new): the CLI command the skill drives; reusable directly by humans too
- `pkg/github/cache.go`: rename/invalidate entry by branch
- `cmd/init.go` / `cmd/doctor.go`: install/check step
- README: plugin section

---

## 6. E2E install-process test in GitHub Actions

### Goal

Prove the path a new user walks: install binary → `init` → `doctor` → `start` a session → register repo → create worktree → open it → remove it. Run on every PR.

### Prerequisite: scriptable surfaces

The wizard and doctor must be automatable (this constrains section 4's design — build it in from the start):

- `workbench init --non-interactive` (accept all defaults / flag overrides, fail instead of prompting)
- `workbench doctor --json` (machine-readable check results + nonzero exit on hard failures)
- `workbench start --detach` or equivalent (see below)

### The TTY problem, and the zellij escape hatch

CI runners have no TTY, and zellij normally refuses to start without one. The escape hatch — **verified end-to-end on zellij 0.43.1**:

```sh
zellij attach --create-background <name> options --default-layout <path.kdl>
#   creates a detached session, no TTY, with our layout applied
ZELLIJ_SESSION_NAME=<name> zellij action query-tab-names
#   drives the session headlessly ($ZELLIJ_SESSION_NAME is how `action`
#   targets a session — there is no --session flag on `action`)
zellij delete-session <name> --force
```

Notes from the verification: `attach` itself takes no layout flag — the layout goes in via the `options --default-layout` subcommand, and the layout's tabs are present immediately. `workbench start --background` wraps the first line and doubles as the CI mode.

**`workbench open` needs a headless mode too:** `IsInZellij()` checks `$ZELLIJ`, which is only set *inside* panes — in CI (and for cross-session control generally) it's unset, so `open` refuses before reaching zellij. Add `--session <name>` to `open` (and `start`): it bypasses the `IsInZellij` guard and runs zellij commands with `ZELLIJ_SESSION_NAME=<name>` in the child env (`runZellij` gains an optional session parameter).

Fallback if background sessions misbehave in CI specifically: wrap in a PTY (`script -e -c "…"`). Verified locally, so only needed if runners differ.

### nono in CI — Linux works

**Verified: nono supports Linux** via Landlock (nono.sh: "Landlock and Seatbelt enforce irrevocable allow-lists at the kernel level on Linux, macOS, and Windows"; there's even a `busbar-actions/sf-cli` project running nono inside GitHub Actions runners). `ubuntu-latest` kernels (6.x) have Landlock enabled. So the Linux job uses **real nono** — no sandbox-fidelity gap between CI and the mac.

| Where | nono | When | What it proves |
|---|---|---|---|
| CI: `e2e-linux` (`ubuntu-latest`) | real (Landlock) | every PR | full install path incl. real sandbox launch |
| Local: git hook on the dev mac | real (Seatbelt) | pre-push | macOS/Seatbelt path |

No macOS CI job — macOS runners are slow/expensive and the mac coverage comes from the local hook instead.

**Local hook**: `.githooks/pre-push` running `scripts/e2e.sh` against an isolated `$HOME` (mktemp), enrolled via `git config core.hooksPath .githooks` (add a `make hooks` target; `workbench init` could offer it when run inside this repo). Pre-*push* rather than pre-commit — the e2e takes seconds-to-a-minute (zellij session spin-up), too slow to tax every commit; bypass with `--no-verify` when needed.

**Shim fallback**: keep a tiny `nono` shim script (parses `run --profile X --allow Y -- <cmd>…`, `exec`s `<cmd>`) in `scripts/ci/` only as a fallback if runner Landlock restrictions ever surface (some container executors disable it). Not the default path.

The "model" under test is the existing `shell` model (binary `bash`) or a `sleep`-style stub, so no LLM credentials are needed in CI.

### Test script shape (`scripts/e2e.sh`, called from a new `e2e` job in ci.yml)

```
1.  go build → workbench on PATH; install zellij (brew / GH release binary)
2.  HOME=$(mktemp -d)               # isolate all of ~/.workbench, ~/.config
3.  workbench init --non-interactive
4.  workbench doctor --json         # assert all checks pass
5.  git init a fixture repo with a commit
6.  workbench add repo <fixture> --alias=fix
7.  workbench add worktree --repo=fix --name=testwt
8.  assert: worktree dir exists, branch wt/fix/testwt exists, config.yml updated
9.  workbench start --background ci-session
10. workbench open --worktree=testwt --session=ci-session
11. assert: ZELLIJ_SESSION_NAME=ci-session zellij action query-tab-names → contains "testwt"
12. workbench rm worktree testwt
13. assert: dir gone, branch gone, layout kdl gone
14. zellij delete-session ci-session --force
```

`workbench ls` plain output (non-TTY path) gets asserted along the way for free. The TUI itself stays unit-tested (bubbletea models are pure; `teatest` exists if we want golden-file TUI tests later — backlog, not this plan).

### Files touched

- `scripts/e2e.sh` (new), `.github/workflows/ci.yml`: add `e2e-linux` job (real nono via Landlock)
- `.githooks/pre-push` (new) + `make hooks`: local macOS e2e
- `scripts/ci/nono-shim` (new, fallback only)
- `cmd/init.go`, `cmd/doctor.go`: non-interactive + `--json` modes (designed in from section 4)
- `cmd/start.go`: `--background` flag
- `cmd/open.go` + `pkg/zellij/client.go`: `--session` flag → `ZELLIJ_SESSION_NAME` in child env, bypassing the `IsInZellij` guard

---

## 7. Lifecycle: update warning, migrations, uninstall

### Update warning on `start`

`workbench start` (not `ls` — the sidebar must never block on network) checks for a newer release:

- `GET https://api.github.com/repos/panamafrancis/workbench/releases/latest` (unauthenticated is fine at this rate), compare the tag against the ldflags-stamped version (backlog item 2 — now load-bearing).
- Cache the result in `~/.workbench/cache/latest-release.json` with a 24h TTL so repeated `start`s don't hit the network; any network failure is silently skipped (offline must never degrade `start`).
- On newer: one line before exec'ing zellij — `workbench v0.9.0 available (you have v0.7.2) — go install github.com/panamafrancis/workbench@latest`.

### Post-update migrations

The binary version changes out from under existing state. Mechanism: store the last-run version in `~/.workbench/state.yml` (not config.yml — config is the user's file); on any command, if `binaryVersion != state.lastRunVersion`, run migrations then stamp. Each migration is idempotent and cheap:

1. **Config schema**: config.yml already carries `version: 1`. Keep a migration registry (`v1→v2`, …) in `pkg/config`; on load, apply pending migrations and save atomically. New fields with defaults need no migration (yaml zero-values); renames/restructures do.
2. **Layouts**: self-healing by design once section 1 lands — the session layout is regenerated on every `start`, per-worktree layouts on every `open`. Migration step only deletes stale `.kdl` files (`CleanupStaleLayouts` already exists).
3. **Plugin**: if the Claude plugin is registered (section 5), refresh it to the version matching the binary (`claude plugin update workbench` or re-copy, depending on the install mechanism chosen).
4. **nono profile**: do *not* auto-edit — it's user-owned security config. If a new workbench version needs new allowances, `doctor` flags the gap and points at `init --profile`.

### `workbench uninstall`

Inverse of `init`, with a confirm + `--dry-run`. Order matters (worktrees need config to be found):

```
workbench uninstall [--dry-run] [--keep-config]
  1. list everything it will remove, confirm once
  2. per worktree: run cleanup_script, git worktree remove, delete wt/* branch
     (skip + warn on dirty unless --force)
  3. delete all wb-* zellij sessions (zellij delete-session --force)
  4. unregister the Claude plugin (if installed)
  5. remove ~/.workbench (config, layouts, cache, logs) — kept with --keep-config
```

What it deliberately does **not** touch, printed at the end: the registered repos themselves, `~/.config/nono/` profiles (user-owned, may serve other tools), gh auth, and the binary (`rm $(which workbench)` / `go clean -i` / brew, depending on install method — print the right one).

### Files touched

- `cmd/uninstall.go` (new); `cmd/start.go`: update check
- `pkg/config/migrate.go` (new) + `state.yml` handling in `pkg/config/paths.go`
- `pkg/setup/`: shared with init/doctor (plugin registration knows how to unregister)

---

## 8. Polish backlog (smaller items, roughly prioritized)

1. **`workbench open <name>` positional arg** — `--worktree=` is the most-typed flag in the CLI; make it positional like `rm worktree <name>` already is. Keep the flag as an alias.
2. **`--version`** — ldflags-stamped; needed by doctor, e2e, and any future brew formula.
3. **Safer delete** — `deleteWorktree` (`pkg/tui/model.go:444`) currently runs cleanup and removes without checking state. The confirm prompt should warn when the worktree is dirty (`m.tree.dirty`) and when its tab is running (`m.tree.openTabs`) — both maps are already in memory; this is purely wiring them into `modeConfirmDelete`'s prompt text (e.g. `delete "x"? (dirty, running) [y/n]`).
4. **`workbench sweep`** — delete worktrees whose PR status is merged (PR cache already tracks this). TUI: mark merged-PR worktrees as sweepable; CLI: `workbench sweep [--dry-run]`. Natural follow-on from the PR-status work.
5. **Hardcoded `main` as base branch** — `git.CreateWorktree` (`pkg/git/worktree.go:21`) already fetches and branches from `origin/main` (no stale-base problem), but `main` is hardcoded: repos whose default branch is `master`/`develop` can't create worktrees at all. Detect via `git symbolic-ref refs/remotes/origin/HEAD` (fallback: ask once, store per-repo in config).
6. **Create worktree from existing branch / PR** — `workbench add worktree --repo=x --branch=existing` partially exists; add `--from-pr <n>` (gh checkout) for reviewing others' PRs in a sandbox.
7. **Shell completions** — Cobra generates bash/zsh/fish for free; `init` can offer to install them.
8. **Surface the zellij error log** — failures land silently in `~/.workbench/logs/zellij.log` (`pkg/zellij/client.go:125`); `doctor` should print the last few entries, and the TUI error line could hint at the log path.
9. **Mouse scroll in the sidebar** — long repo lists outgrow the pane; bubbletea exposes wheel events, the tree just needs a viewport/offset.
10. **`ls` plain output: include PR + dirty status** — the non-TTY output (`cmd/ls.go:29`) omits everything the sidebar shows; cheap to add and makes it scriptable (`--json` here too, eventually).
11. **Cross-session running indicator** — once multiple sessions are real (section 1): aggregate `query-tab-names` across all `wb-*` sessions so a worktree running in another session shows `▷` in this sidebar, and `open` warns before launching a second agent into the same worktree dir.
12. **TUI-created worktrees missing `CreatedAt`** — `cmd/add_worktree.go:71` stamps it, the TUI path (`pkg/tui/model.go` `createWorktree`) doesn't. One-line fix; matters once anything sorts by age.
13. **Offline worktree creation** *(promote this — it's a reported blocker, not polish)* — `git.CreateWorktree` (`pkg/git/worktree.go:21`) unconditionally runs `git fetch origin main` and branches from `origin/main`, so creating a worktree offline fails outright. Fix: make the fetch best-effort — on network failure, fall back to the existing local `origin/<default>` ref (stale but valid) and surface "offline: branched from last-fetched origin/main" in the TUI message line. Combine with item 5 (default-branch detection) since both touch the same function. Note: `add repo` itself has no network dependency in the code (stat + config write) — if it also failed offline, capture the actual error next time; it may have been the worktree step right after.
14. **Fix the nono link in the README** — `github.com/panamafrancis/nono` 404s; the project lives at [nono.sh](https://nono.sh). Both Requirements and the sandbox section reference it.

---

## Discussion / open questions

1. **What should bare `workbench` do?** Today it prints Cobra help. Once `start` exists, the friendliest behavior: no config → hint at `init`; config + outside Zellij → suggest (or just run) `start`; inside Zellij → help. Auto-running `start` is opinionated but matches "the binary itself starts the session". Proposal: print a one-line suggestion rather than auto-exec, revisit after living with it.
2. **Zellij flag verification — mostly RESOLVED (tested on 0.43.1).** Confirmed: `-s <name> -n <layout>` for create-with-layout; `attach --create-background <name> options --default-layout <layout>` for headless create (layout tabs present immediately); `ZELLIJ_SESSION_NAME` env targets `zellij action` at any session; `list-sessions --short/--no-formatting`; `delete-session --force`. Still untested: resurrection of EXITED sessions (does attach re-run the sidebar command?) and second-client attach to a live session — both need a 2-minute interactive check that can't be done headlessly.
3. **`default_zellij_layout` config key** — confirmed dead: defined at `pkg/config/config.go:20`, read nowhere. Section 1 gives it a real meaning (override the embedded session layout); if we'd rather not support custom session layouts yet, drop the key instead of half-supporting it.
4. **Env passthrough through nono — RESOLVED, it works.** Verified from inside a workbench-launched sandbox: `SSH_AUTH_SOCK`, `TERM_PROGRAM`, `ZELLIJ_SESSION_NAME` etc. all pass through the zellij → nono → agent chain intact. Section 5's env-gate design is sound as written. (Useful side effect: `$ZELLIJ_SESSION_NAME` being visible in-pane means skills/scripts already know which session they're in.)
5. **Plugin location.** In-repo `plugin/` keeps skills versioned with the conventions they describe (branch naming lives in `pkg/git/names.go` *and* the skill text — drift risk either way). Separate repo only makes sense if non-workbench users would want the skills; they wouldn't. Recommend in-repo.
6. **macOS e2e — DECIDED**: no macOS CI job. Linux PRs run real nono (Landlock support confirmed); macOS/Seatbelt coverage comes from the local pre-push hook on the dev machine. Revisit only if non-mac contributors appear.
7. **Update-check privacy/noise.** The `start` release check pings the GitHub API daily. Harmless, but make it disableable (`update_check: false` in config) for principle's sake, and it must fail silent offline (hard requirement — see offline backlog item 13).
8. **Uninstall vs dirty worktrees.** `uninstall` deleting worktrees with uncommitted work is the scariest thing this tool could do. Default: skip dirty worktrees and report them, require `--force` to remove anyway. Worth a dedicated e2e case.
9. **Suggested order of work:** 1 (start) → 2 (sidebar) → 4 (init/doctor, with non-interactive modes designed in) → 6 (e2e, locks the install path against regressions) → 5 (skills plugin) → 7 (lifecycle: update/uninstall) → 8 (backlog opportunistically; item 13 offline-create early — it's a reported blocker). Homebrew deferred.
