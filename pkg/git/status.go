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
