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

# 1. Fixture repos, registered with workbench.
echo "--- fixture repos + workbench add repo ---"
for r in terraform keystone admin; do
    mkdir -p "$HOME/src/$r"
    git -C "$HOME/src/$r" init -q
    git -C "$HOME/src/$r" commit --allow-empty -qm initial
    workbench add repo "$HOME/src/$r" --alias="$r" >/dev/null
done

# 2. Scaffold a stack (two repos to start).
echo "--- scaffold stack ---"
supatree scaffold "$HOME/stacks/s" --repos terraform,keystone --alias s
[ -f "$HOME/stacks/s/supatree.yml" ] || fail "supatree.yml not scaffolded"
[ -f "$HOME/stacks/s/AGENTS.md" ]   || fail "AGENTS.md not scaffolded"

# Add a dependency edge (keystone depends on terraform) and commit it.
cat > "$HOME/stacks/s/supatree.yml" <<'YML'
members: [terraform, keystone]
deps:
  keystone: [terraform]
YML
git -C "$HOME/stacks/s" commit -qam "add dep edge"

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
supatree ls | grep -q berlin || fail "berlin missing from ls"

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
git -C "$HOME/src/terraform" worktree list | grep -q "berlin" && fail "source worktree not pruned"

echo ""
echo "=== e2e-supatree: all checks passed ==="
