# /workbench:rename-branch

Rename this worktree's branch to a meaningful name derived from the work done.

## Prerequisites

This command only works inside a workbench session (WORKBENCH_WORKTREE_NAME must be set).

## Steps

1. Look at the recent commits and changes to understand what this worktree is about.
2. Derive a short, descriptive branch slug (e.g. `session-launcher`, `fix-offline-create`).
3. Keep the `wt/<repo-alias>/` prefix so worktree branches stay recognizable. The full branch name should be `wt/<alias>/<slug>`.
4. Run: `workbench rename-branch wt/$WORKBENCH_REPO_ALIAS/<slug>`
5. If the old branch was already pushed, add `--push` to also update the remote.

Do NOT use bare `git branch -m` — it desyncs workbench config and PR cache.
