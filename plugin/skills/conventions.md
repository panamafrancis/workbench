# Workbench conventions

**This skill only applies when `WORKBENCH_WORKTREE_NAME` is set in the environment.** If that variable is absent, ignore everything below.

## Branch naming

Worktree branches follow the pattern `wt/<repo-alias>/<descriptive-slug>`. Auto-generated branches use the worktree name as the slug (e.g. `wt/wb/clear-detroit`). Before creating a PR, rename to something meaningful using `workbench rename-branch`.

## Scope discipline

- Only modify files inside this worktree. Never touch other checkouts or the bare repo.
- The worktree path is your current working directory.
- The repo alias is in `$WORKBENCH_REPO_ALIAS`.

## Creating PRs

Use `workbench rename-branch` before `gh pr create` — bare `git branch -m` desyncs workbench config and PR cache. Or use the `/workbench:pr` command which handles both.

## Useful environment variables

- `WORKBENCH_WORKTREE_NAME` — the worktree name (also the Zellij tab title)
- `WORKBENCH_REPO_ALIAS` — the repo alias in workbench config
- `WORKBENCH_BRANCH` — the current branch name
- `WORKBENCH` — set to "1" when running inside workbench
