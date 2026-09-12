package git

import (
	"bytes"
	"context"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

func IsDirty(worktreePath string) bool {
	cmd := exec.CommandContext(context.Background(), "git", "-C", worktreePath, "status", "--porcelain")
	out, err := cmd.Output()
	if err != nil {
		return false
	}
	return len(bytes.TrimSpace(out)) > 0
}

func BranchName(worktreePath string) string {
	cmd := exec.CommandContext(context.Background(), "git", "-C", worktreePath, "rev-parse", "--abbrev-ref", "HEAD")
	out, err := cmd.Output()
	if err != nil {
		return "unknown"
	}
	return strings.TrimSpace(string(out))
}

// HasRemoteBranch reports whether refs/remotes/origin/<branch> exists in the
// repo. A branch that was never pushed cannot have a PR, so callers use this to
// skip a guaranteed-empty `gh pr list` — the dominant source of wasted GitHub
// GraphQL quota in the sidebars. Pushing (directly or via `gh pr create`)
// updates the remote-tracking ref, and worktrees share the repo's refs, so a
// branch with a PR always has this ref locally.
func HasRemoteBranch(repoPath, branch string) bool {
	cmd := exec.CommandContext(context.Background(), "git", "-C", repoPath,
		"rev-parse", "--verify", "--quiet", "refs/remotes/origin/"+branch)
	return cmd.Run() == nil
}

// LastCommitTime returns the committer time of HEAD. ok is false when the
// worktree has no commits or is not a repo, which callers treat as "no activity
// recorded" rather than an ancient timestamp.
func LastCommitTime(worktreePath string) (t time.Time, ok bool) {
	cmd := exec.CommandContext(context.Background(), "git", "-C", worktreePath,
		"log", "-1", "--format=%ct")
	out, err := cmd.Output()
	if err != nil {
		return time.Time{}, false
	}
	secs, err := strconv.ParseInt(strings.TrimSpace(string(out)), 10, 64)
	if err != nil {
		return time.Time{}, false
	}
	return time.Unix(secs, 0), true
}

// UnpushedCommits returns how many commits HEAD has that origin/<branch> does
// not. ok is false when there is no remote-tracking ref (nothing was ever
// pushed), which is a different state from "pushed and up to date".
func UnpushedCommits(worktreePath, branch string) (n int, ok bool) {
	remote := "refs/remotes/origin/" + branch
	if !refExists(worktreePath, remote) {
		return 0, false
	}
	cmd := exec.CommandContext(context.Background(), "git", "-C", worktreePath,
		"rev-list", "--count", remote+"..HEAD")
	out, err := cmd.Output()
	if err != nil {
		return 0, false
	}
	n, err = strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil {
		return 0, false
	}
	return n, true
}
