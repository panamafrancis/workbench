package git

import (
	"bytes"
	"fmt"
	"os/exec"
	"strings"
)

func FetchOriginMain(repoPath string) error {
	cmd := exec.Command("git", "-C", repoPath, "fetch", "origin", "main")
	var errBuf bytes.Buffer
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("git fetch: %s", strings.TrimSpace(errBuf.String()))
	}
	return nil
}

func CreateWorktree(repoPath, worktreePath, branch string) error {
	if err := FetchOriginMain(repoPath); err != nil {
		return err
	}
	cmd := exec.Command("git", "-C", repoPath, "worktree", "add", "-b", branch, worktreePath, "origin/main")
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

func DeleteBranch(repoPath, branch string) error {
	cmd := exec.Command("git", "-C", repoPath, "branch", "-D", branch)
	var errBuf bytes.Buffer
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("git branch -D: %s", strings.TrimSpace(errBuf.String()))
	}
	return nil
}
