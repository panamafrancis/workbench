#!/usr/bin/env bash
set -euo pipefail

# E2E test for supatree: scaffold a stack, create a supatree spanning two repos
# with a dependency edge, sync a third in, rename branches, and tear down.
# Runs against an isolated HOME. Requires: workbench + supatree on PATH.

# The sandbox enforcement case below needs a real, enforcing nono, and cannot
# run from inside a nono sandbox (macOS refuses nested sandboxes). When it can
# run, the isolated HOME goes under the real one rather than $TMPDIR, which
# nono's base profiles leave writable — a probe there would prove nothing.
ENFORCE=
if nono --version >/dev/null 2>&1 && [ -z "${NONO_CAP_FILE:-}" ]; then ENFORCE=1; fi
if [ -n "$ENFORCE" ]; then
    export HOME=$(mktemp -d "$HOME/.supatree-e2e.XXXXXX")
else
    export HOME=$(mktemp -d)
fi
trap 'rm -rf "$HOME"' EXIT
# Paths come from XDG first, so HOME alone does not isolate: a value exported
# by the developer's shell would point the run at their real directories.
export XDG_CONFIG_HOME="$HOME/.config" XDG_STATE_HOME="$HOME/.local/state" XDG_CACHE_HOME="$HOME/.cache"
# A private zellij: commands here list and close tabs in every live session,
# and must never reach the developer's own.
# (Short path: a unix socket path is capped near 104 bytes.)
export ZELLIJ_SOCKET_DIR=$(mktemp -d /tmp/zj.XXXXXX)
trap 'rm -rf "$HOME" "$ZELLIJ_SOCKET_DIR"' EXIT
unset ZELLIJ ZELLIJ_SESSION_NAME ZELLIJ_PANE_ID
ST_CONFIG="$XDG_CONFIG_HOME/supatree"
ST_STATE="$XDG_STATE_HOME/supatree"
ST_CACHE="$XDG_CACHE_HOME/supatree"

echo "=== e2e-supatree: isolated HOME=$HOME ==="

git config --global user.email "e2e@test.local"
git config --global user.name "e2e"
git config --global init.defaultBranch main

fail() { echo "FAIL: $1"; exit 1; }

# Note: pipe into plain `grep`, never `grep -q`. Under `set -o pipefail`, -q
# exits on the first match, the Go process writing to the pipe dies of SIGPIPE
# (141), and the pipeline fails even though the assertion passed. It is rare
# enough (~3% per call) to look like flakiness rather than a bug.

# 1. Fixture repos, registered with workbench.
echo "--- fixture repos + workbench add repo ---"
for r in terraform keystone admin; do
    mkdir -p "$HOME/src/$r"
    git -C "$HOME/src/$r" init -q
    git -C "$HOME/src/$r" commit --allow-empty -qm initial
    workbench add repo "$HOME/src/$r" --alias="$r" >/dev/null
done

# 2. Scaffold a stack (two repos to start). Default location is
# ~/supatree/stacks/<name>; --repos avoids the interactive picker.
echo "--- scaffold stack ---"
supatree scaffold s --repos=terraform,keystone
STACK="$HOME/supatree/stacks/s"
[ -f "$STACK/supatree.yml" ] || fail "supatree.yml not scaffolded at default location"
[ -f "$STACK/AGENTS.md" ]    || fail "AGENTS.md not scaffolded"

# Add a dependency edge (keystone depends on terraform) and commit it.
cat > "$STACK/supatree.yml" <<'YML'
members: [terraform, keystone]
deps:
  keystone: [terraform]
YML
git -C "$STACK" commit -qam "add dep edge"

# 3. Create a supatree.
echo "--- supatree new ---"
supatree new --stack s --name berlin
ROOT="$HOME/supatree/trees/berlin"
[ -d "$ROOT/repos/terraform" ] || fail "terraform member worktree missing"
[ -d "$ROOT/repos/keystone" ]  || fail "keystone member worktree missing"

# Per-tree state lives outside the tree; the tree links to it.
[ -L "$ROOT/.supatree" ] || fail ".supatree is not a link into supatree's state"
[ "$(readlink "$ROOT/.supatree")" = "$ST_STATE/trees/berlin" ] || fail ".supatree points at $(readlink "$ROOT/.supatree"), not the tree's state dir"
[ -f "$ST_STATE/trees/berlin/meta.yml" ] || fail "meta.yml not in the state dir"

# Meta-worktree branch is st/<name>; members are st/<name>/<alias>.
[ "$(git -C "$ROOT" rev-parse --abbrev-ref HEAD)" = "st/berlin" ] || fail "meta branch wrong"
[ "$(git -C "$ROOT/repos/terraform" rev-parse --abbrev-ref HEAD)" = "st/berlin/terraform" ] || fail "terraform branch wrong"

# The meta-worktree must be git-clean (members + .supatree are gitignored).
[ -z "$(git -C "$ROOT" status --porcelain --untracked-files=no)" ] || fail "meta worktree not clean"

# info.md lists both members in dependency order.
grep -q "terraform → keystone" "$ROOT/.supatree/info.md" || fail "merge order missing from info.md"

# 4. ls (plain).
echo "--- supatree ls ---"
supatree ls | grep berlin >/dev/null || fail "berlin missing from ls"

# 4b. status: derived activity state, plain and JSON. No PRs exist here, so the
# members are freshly checked out and idle and the tree reads as "new".
echo "--- supatree status ---"
supatree status | grep berlin >/dev/null || fail "berlin missing from status"
supatree status | grep "new" >/dev/null || fail "fresh supatree should read as new"
supatree status --json | grep '"state": "new"' >/dev/null || fail "status --json missing tree state"
supatree status --json | grep '"has_remote"' >/dev/null || fail "status --json missing local git state"
# dash falls back to status when stdout is not a terminal.
supatree dash | grep berlin >/dev/null || fail "dash fallback did not print status"

# 4c. watch --once: the event ledger and the notifier. The first round has no
# baseline to diff against, so it must record nothing and notify nothing — that
# quiet first run is what stops a fresh install announcing every supatree it
# finds. A stub notify_command makes delivery observable without a desktop.
echo "--- supatree watch --once ---"
mkdir -p "$ST_CONFIG"
cat >> "$ST_CONFIG/config.yml" <<YML
notify_command: ["sh", "-c", "echo {title}: {text} >> $HOME/notified.log"]
YML
supatree watch --once || fail "watch --once failed"
[ -f "$ST_CACHE/last-status.json" ] || fail "watch did not persist a baseline"
[ ! -s "$ST_STATE/ledger/events.jsonl" ] || fail "first watch round must not emit events"
[ ! -f "$HOME/notified.log" ] || fail "first watch round must not notify"

# A second round against an unchanged world is equally quiet: events are a diff,
# not a report of the current state.
supatree watch --once || fail "second watch --once failed"
[ ! -s "$ST_STATE/ledger/events.jsonl" ] || fail "unchanged world must not emit events"

# The outbox is how a sandboxed process (the PM) reaches the desktop, since the
# watcher is the only process outside nono. Board-tier events are recorded but
# must not interrupt; desktop-tier ones must.
mkdir -p "$ST_STATE/outbox"
echo '{"at":"2099-01-01T00:00:00Z","kind":"approved","tree":"berlin","text":"quiet"}' > "$ST_STATE/outbox/notify.jsonl"
supatree watch --once || fail "watch --once with outbox failed"
[ ! -f "$HOME/notified.log" ] || fail "board-tier outbox event must not notify"
echo '{"at":"2099-01-01T00:00:00Z","kind":"checks_failed","tree":"berlin","member":"keystone","text":"ci is red"}' > "$ST_STATE/outbox/notify.jsonl"
supatree watch --once || fail "watch --once with desktop outbox failed"
grep -q "ci is red" "$HOME/notified.log" || fail "desktop-tier outbox event did not notify"
[ ! -s "$ST_STATE/outbox/notify.jsonl" ] || fail "outbox not drained"

# Repeats of the same news stay quiet until the cooldown elapses.
echo '{"at":"2099-01-01T00:05:00Z","kind":"checks_failed","tree":"berlin","member":"keystone","text":"ci is red again"}' > "$ST_STATE/outbox/notify.jsonl"
supatree watch --once || fail "watch --once with repeat failed"
grep -q "ci is red again" "$HOME/notified.log" && fail "repeat inside the cooldown must not notify"

# 4d. Agent mailboxes: the portable half of agent-to-agent messaging. Delivery
# must work with no agent running — that is the whole point of a file mailbox —
# and mailboxes must not leak between agents.
echo "--- agent mail ---"
supatree message berlin main "look at the review on keystone" >/dev/null || fail "message failed"
supatree message berlin reviewer --from pm "rebase first" >/dev/null || fail "message to a second agent failed"
supatree inbox berlin main | grep "look at the review" >/dev/null || fail "message not in main's inbox"
supatree inbox berlin main | grep "rebase first" >/dev/null && fail "reviewer's mail leaked into main's inbox"
supatree inbox berlin reviewer | grep "rebase first" >/dev/null || fail "message not in reviewer's inbox"
# Reading from the CLI must not consume: only the agent clears its own mail.
supatree inbox berlin main | grep "look at the review" >/dev/null || fail "CLI read consumed the message"
supatree inbox berlin nobody | grep "no messages" >/dev/null || fail "empty mailbox should report no messages"

# 4e. The PM's request queue. The PM itself needs zellij and an agent, so what
# is checked here is the part that must work without either: anything can queue
# a request, and the queue is read from a stored offset so a PM that has been
# down does not lose the backlog.
echo "--- pm requests ---"
supatree request --tree berlin "check the review on keystone" >/dev/null || fail "request failed"
supatree request --from watcher "berlin has gone stale" >/dev/null || fail "second request failed"
[ -s "$ST_STATE/requests/requests.jsonl" ] || fail "request queue not written"
grep -q "check the review" "$ST_STATE/requests/requests.jsonl" || fail "request text missing"
grep -q '"from":"watcher"' "$ST_STATE/requests/requests.jsonl" || fail "request provenance missing"

# 4f. Autonomy, board, notes, schedule.
echo "--- board + notes + schedule ---"
[ -d "$STACK/notes" ] || fail "notes/ not scaffolded into the stack"
[ -f "$STACK/notes/README.md" ] || fail "notes README missing"

# The schedule must not fire everything the moment it is written: an unseen job
# is seeded, not treated as overdue.
supatree schedule | grep "No scheduled jobs" >/dev/null || fail "expected an empty schedule"
supatree schedule init >/dev/null || fail "schedule init failed"
supatree schedule | grep standup >/dev/null || fail "schedule not listed"
supatree schedule | grep never >/dev/null || fail "a fresh job should report never fired"
REQS_BEFORE=$(wc -l < "$ST_STATE/requests/requests.jsonl")
supatree watch --once || fail "watch with a schedule failed"
supatree watch --once || fail "second watch with a schedule failed"
[ "$(wc -l < "$ST_STATE/requests/requests.jsonl")" = "$REQS_BEFORE" ] || fail "a fresh schedule fired jobs instead of seeding"

# schedule run fires on demand without disturbing the fire times: testing a job
# must not silently skip its next real run.
STATE_BEFORE=$(cat "$ST_CACHE/schedule-state.json")
supatree schedule run reap >/dev/null || fail "schedule run failed"
grep -q "schedule:reap" "$ST_STATE/requests/requests.jsonl" || fail "run did not queue the job"
[ "$(cat "$ST_CACHE/schedule-state.json")" = "$STATE_BEFORE" ] || fail "schedule run touched the last-fired state"
supatree schedule run nosuchjob >/dev/null 2>&1 && fail "running an unknown job should error"

# 5. Add a third repo by editing supatree.yml + sync.
echo "--- edit supatree.yml + sync ---"
cat > "$ROOT/supatree.yml" <<'YML'
members: [terraform, keystone, admin]
deps:
  keystone: [terraform]
  admin: [keystone]
YML
supatree sync berlin
[ -d "$ROOT/repos/admin" ] || fail "admin member not created by sync"

# 6. Rename branches (members change; meta stays st/berlin).
echo "--- rename-branch ---"
supatree rename-branch payments berlin
[ "$(git -C "$ROOT/repos/admin" rev-parse --abbrev-ref HEAD)" = "st/payments/admin" ] || fail "member not renamed"
[ "$(git -C "$ROOT" rev-parse --abbrev-ref HEAD)" = "st/berlin" ] || fail "meta branch should not rename"

# 7. Remove (force: supatree.yml was edited in-tree without commit). Removing
# a tree closes its tabs in every supatree session, which is what stops its
# agents (supatree#2) — so give it some to close, in a background session.
echo "--- supatree rm ---"
zellij attach --create-background st-e2e
zellij --session st-e2e action new-tab --name berlin
zellij --session st-e2e action new-tab --name berlin:reviewer
zellij --session st-e2e action new-tab --name berlinx
supatree rm berlin -y --force
TABS=$(zellij --session st-e2e action query-tab-names)
echo "$TABS" | grep -x "berlin" >/dev/null && fail "rm left the tree's tab open"
echo "$TABS" | grep -x "berlin:reviewer" >/dev/null && fail "rm left an agent tab open"
echo "$TABS" | grep -x "berlinx" >/dev/null || fail "rm closed a tab that was not the tree's"
zellij delete-session st-e2e --force >/dev/null 2>&1 || true
[ ! -d "$ROOT" ] || fail "tree dir still exists after rm"
[ ! -d "$ST_STATE/trees/berlin" ] || fail "tree state survived rm — List would keep reporting it"
# Removal must not be the thing that loses an agent's history.
[ -d "$ST_STATE/archive" ] || mkdir -p "$ST_STATE/archive"
git -C "$HOME/src/terraform" worktree list | grep "berlin" >/dev/null && fail "source worktree not pruned"

# Review mode. There is no network and no gh here, so this covers everything
# downstream of the PR lookup: teardown must leave a branch it did not create
# alone, which is what protects a PR author's branch.
echo "--- teardown leaves a foreign branch alone ---"
supatree new --stack=s --name=reviewcity >/dev/null
REVIEW="$HOME/supatree/trees/reviewcity"
[ -d "$REVIEW/repos/keystone" ] || fail "review fixture member missing"
# Put a member on somebody else's branch, the way `gh pr checkout` would.
git -C "$REVIEW/repos/keystone" checkout -q -b feat/not-ours
supatree rm reviewcity -y --force >/dev/null
git -C "$HOME/src/keystone" rev-parse --verify --quiet refs/heads/feat/not-ours >/dev/null \
    || fail "supatree rm deleted a branch the tree did not create"
echo "    foreign branch survived teardown"

# Old layout: every command but migrate/version refuses, with one line saying
# what to do, rather than half-working against files it no longer reads.
echo "--- old-layout guard ---"
OLD=$(mktemp -d)
mkdir -p "$OLD/.supatree"
( export HOME="$OLD" XDG_CONFIG_HOME="$OLD/.config" XDG_STATE_HOME="$OLD/.local/state" XDG_CACHE_HOME="$OLD/.cache"
  out=$(supatree ls 2>&1) && fail "supatree ls ran on the old layout"
  echo "$out" | grep "supatree migrate" >/dev/null || fail "old-layout refusal does not say what to run: $out"
  supatree version >/dev/null || fail "supatree version refused on the old layout" )
rm -rf "$OLD"

# Sandbox enforcement: run a probe under exactly the flags a tree agent gets
# and check the boundary holds (see ENFORCE at the top).
echo "--- sandbox enforcement ---"
if [ -n "$ENFORCE" ]; then
    supatree init >/dev/null 2>&1 || fail "supatree init failed"
    supatree new --stack=s --name=lima >/dev/null
    supatree new --stack=s --name=quito >/dev/null
    LIMA="$HOME/supatree/trees/lima"
    ARGS=()
    while IFS= read -r a; do ARGS+=("$a"); done < <(supatree sandbox-args lima)
    probe() { nono run -s --no-rollback "${ARGS[@]}" -- sh -c "echo x >> '$1'" >/dev/null 2>&1; }
    probe "$LIMA/repos/keystone/probe"     || fail "agent cannot write its own member"
    probe "$ST_STATE/trees/lima/probe"     || fail "agent cannot write its own tree state"
    probe "$ST_STATE/pm/launch.jsonl"      && fail "agent can write the PM's launch queue"
    probe "$ST_STATE/trees/quito/probe"    && fail "agent can write another tree's state"
    probe "$HOME/supatree/trees/quito/x"   && fail "agent can write another tree"
    probe "$ST_CACHE/layouts/x.kdl"        && fail "agent can write layouts zellij runs unsandboxed"
    supatree rm lima -y --force >/dev/null
    supatree rm quito -y --force >/dev/null
    echo "    boundary holds"
else
    echo "    skipped (no enforcing nono here)"
fi

echo ""
echo "=== e2e-supatree: all checks passed ==="
