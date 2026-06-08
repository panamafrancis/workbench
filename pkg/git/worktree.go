package git

import (
	"bytes"
	"fmt"
	"os/exec"
	"strings"
)

func CreateWorktree(repoPath, worktreePath, branch string) error {
	cmd := exec.Command("git", "-C", repoPath, "worktree", "add", "-b", branch, worktreePath)
	var errBuf bytes.Buffer
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("git worktree add: %s", strings.TrimSpace(errBuf.String()))
	}
	return nil
}

func RemoveWorktree(repoPath, worktreePath string) error {
	cmd := exec.Command("git", "-C", repoPath, "worktree", "remove", "--force", worktreePath)
	var errBuf bytes.Buffer
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("git worktree remove: %s", strings.TrimSpace(errBuf.String()))
	}
	return nil
}
