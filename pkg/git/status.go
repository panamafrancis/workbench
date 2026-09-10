package git

import (
	"bytes"
	"context"
	"os/exec"
	"strings"
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

// OriginURL returns the repo's origin remote URL, or "" when it has no origin.
// Callers derive the GitHub owner/name from it, which is free — asking the API
// which repo a directory belongs to is not.
func OriginURL(repoPath string) string {
	cmd := exec.CommandContext(context.Background(), "git", "-C", repoPath, "remote", "get-url", "origin")
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
