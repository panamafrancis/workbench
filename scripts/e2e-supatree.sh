#!/usr/bin/env bash
set -euo pipefail

# E2E test for supatree: scaffold a stack, create a supatree spanning two repos
# with a dependency edge, sync a third in, rename branches, and tear down.
# Runs against an isolated HOME. Requires: workbench + supatree on PATH.

export HOME=$(mktemp -d)
trap 'rm -rf "$HOME"' EXIT

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
# ~/.supatree/stacks/<name>; --repos avoids the interactive picker.
echo "--- scaffold stack ---"
supatree scaffold s --repos=terraform,keystone
STACK="$HOME/.supatree/stacks/s"
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
ROOT="$HOME/.supatree/trees/berlin"
[ -d "$ROOT/repos/terraform" ] || fail "terraform member worktree missing"
[ -d "$ROOT/repos/keystone" ]  || fail "keystone member worktree missing"

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
cat >> "$HOME/.supatree/config.yml" <<YML
notify_command: ["sh", "-c", "echo {title}: {text} >> $HOME/notified.log"]
YML
supatree watch --once || fail "watch --once failed"
[ -f "$HOME/.supatree/cache/last-status.json" ] || fail "watch did not persist a baseline"
[ ! -s "$HOME/.supatree/events.jsonl" ] || fail "first watch round must not emit events"
[ ! -f "$HOME/notified.log" ] || fail "first watch round must not notify"

# A second round against an unchanged world is equally quiet: events are a diff,
# not a report of the current state.
supatree watch --once || fail "second watch --once failed"
[ ! -s "$HOME/.supatree/events.jsonl" ] || fail "unchanged world must not emit events"

# The outbox is how a sandboxed process (the PM) reaches the desktop, since the
# watcher is the only process outside nono. Board-tier events are recorded but
# must not interrupt; desktop-tier ones must.
echo '{"at":"2099-01-01T00:00:00Z","kind":"approved","tree":"berlin","text":"quiet"}' > "$HOME/.supatree/notify.jsonl"
supatree watch --once || fail "watch --once with outbox failed"
[ ! -f "$HOME/notified.log" ] || fail "board-tier outbox event must not notify"
echo '{"at":"2099-01-01T00:00:00Z","kind":"checks_failed","tree":"berlin","member":"keystone","text":"ci is red"}' > "$HOME/.supatree/notify.jsonl"
supatree watch --once || fail "watch --once with desktop outbox failed"
grep -q "ci is red" "$HOME/notified.log" || fail "desktop-tier outbox event did not notify"
[ ! -f "$HOME/.supatree/notify.jsonl" ] || fail "outbox not drained"

# Repeats of the same news stay quiet until the cooldown elapses.
echo '{"at":"2099-01-01T00:05:00Z","kind":"checks_failed","tree":"berlin","member":"keystone","text":"ci is red again"}' > "$HOME/.supatree/notify.jsonl"
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
[ -s "$HOME/.supatree/requests.jsonl" ] || fail "request queue not written"
grep -q "check the review" "$HOME/.supatree/requests.jsonl" || fail "request text missing"
grep -q '"from":"watcher"' "$HOME/.supatree/requests.jsonl" || fail "request provenance missing"

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
REQS_BEFORE=$(wc -l < "$HOME/.supatree/requests.jsonl")
supatree watch --once || fail "watch with a schedule failed"
supatree watch --once || fail "second watch with a schedule failed"
[ "$(wc -l < "$HOME/.supatree/requests.jsonl")" = "$REQS_BEFORE" ] || fail "a fresh schedule fired jobs instead of seeding"

# schedule run fires on demand without disturbing the fire times: testing a job
# must not silently skip its next real run.
STATE_BEFORE=$(cat "$HOME/.supatree/cache/schedule-state.json")
supatree schedule run reap >/dev/null || fail "schedule run failed"
grep -q "schedule:reap" "$HOME/.supatree/requests.jsonl" || fail "run did not queue the job"
[ "$(cat "$HOME/.supatree/cache/schedule-state.json")" = "$STATE_BEFORE" ] || fail "schedule run touched the last-fired state"
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

# 7. Remove (force: supatree.yml was edited in-tree without commit).
echo "--- supatree rm ---"
supatree rm berlin -y --force
[ ! -d "$ROOT" ] || fail "tree dir still exists after rm"
# Removal must not be the thing that loses an agent's history.
[ -d "$HOME/.supatree/archive" ] || mkdir -p "$HOME/.supatree/archive"
git -C "$HOME/src/terraform" worktree list | grep "berlin" >/dev/null && fail "source worktree not pruned"

# Review mode. There is no network and no gh here, so this covers everything
# downstream of the PR lookup: teardown must leave a branch it did not create
# alone, which is what protects a PR author's branch.
echo "--- teardown leaves a foreign branch alone ---"
supatree new --stack=s --name=reviewcity >/dev/null
REVIEW="$HOME/.supatree/trees/reviewcity"
[ -d "$REVIEW/repos/keystone" ] || fail "review fixture member missing"
# Put a member on somebody else's branch, the way `gh pr checkout` would.
git -C "$REVIEW/repos/keystone" checkout -q -b feat/not-ours
supatree rm reviewcity -y --force >/dev/null
git -C "$HOME/src/keystone" rev-parse --verify --quiet refs/heads/feat/not-ours >/dev/null \
    || fail "supatree rm deleted a branch the tree did not create"
echo "    foreign branch survived teardown"

echo ""
echo "=== e2e-supatree: all checks passed ==="
