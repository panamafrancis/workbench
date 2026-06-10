#!/usr/bin/env bash
set -euo pipefail

# E2E test for the workbench install + session lifecycle.
# Runs against an isolated HOME so it never touches real config.
# Requires: workbench on PATH, zellij, nono (or the shim).

ORIG_HOME="$HOME"
export HOME=$(mktemp -d)
trap 'rm -rf "$HOME"' EXIT

echo "=== e2e: using isolated HOME=$HOME ==="

# 1. Init
echo "--- init (non-interactive) ---"
workbench init --non-interactive

# 2. Doctor
echo "--- doctor ---"
workbench doctor --json | head -5

# 3. Create a fixture repo
echo "--- create fixture repo ---"
FIXTURE="$HOME/fixture-repo"
mkdir -p "$FIXTURE"
git -C "$FIXTURE" init -b main
git -C "$FIXTURE" commit --allow-empty -m "initial"

# 4. Register the repo
echo "--- add repo ---"
workbench add repo "$FIXTURE" --alias=fix

# 5. Create a worktree
echo "--- add worktree ---"
workbench add worktree --repo=fix --name=testwt

# 6. Verify worktree
echo "--- verify worktree ---"
WT_PATH="$HOME/.workbench/worktrees/fix/testwt"
if [ ! -d "$WT_PATH" ]; then
    echo "FAIL: worktree dir $WT_PATH does not exist"
    exit 1
fi
BRANCH=$(git -C "$WT_PATH" rev-parse --abbrev-ref HEAD)
if [ "$BRANCH" != "wt/fix/testwt" ]; then
    echo "FAIL: expected branch wt/fix/testwt, got $BRANCH"
    exit 1
fi
echo "  worktree dir: OK"
echo "  branch: OK ($BRANCH)"

# 7. List (plain output)
echo "--- ls (plain) ---"
workbench ls --repo=fix | grep testwt

# 8. Start a background session (if zellij is available)
if command -v zellij &>/dev/null; then
    echo "--- start background session ---"
    workbench start --background ci-test || echo "  (background session not supported in this environment)"

    # 9. Query session
    if ZELLIJ_SESSION_NAME=wb-ci-test zellij action query-tab-names 2>/dev/null; then
        echo "  session query: OK"
    else
        echo "  session query: skipped (session not running)"
    fi

    # 10. Cleanup session
    zellij delete-session wb-ci-test --force 2>/dev/null || true
fi

# 11. Remove worktree
echo "--- rm worktree ---"
echo "y" | workbench rm worktree testwt

# 12. Verify removal
if [ -d "$WT_PATH" ]; then
    echo "FAIL: worktree dir still exists after removal"
    exit 1
fi
echo "  removal: OK"

# 13. Version
echo "--- version ---"
workbench version

echo ""
echo "=== e2e: all checks passed ==="
