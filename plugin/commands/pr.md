# /workbench:pr

Create a pull request for this worktree.

## Prerequisites

This command only works inside a workbench session (WORKBENCH_WORKTREE_NAME must be set).

## Steps

1. **Rename if needed**: Check if the branch still has the auto-generated name pattern. If `$WORKBENCH_BRANCH` ends with `/$WORKBENCH_WORKTREE_NAME` (the worktree name is the last path segment), it was never renamed. In that case, run `/workbench:rename-branch` first.

2. **Stage and commit** any remaining changes following conventional commit style.

3. **Push** the branch: `git push -u origin HEAD`

4. **Create the PR**: `gh pr create --fill` or craft a title and description based on the commits. Use the repo's default branch as the base.

5. Report the PR URL when done.
