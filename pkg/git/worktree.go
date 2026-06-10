package git

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
)

func DefaultBranch(repoPath string) string {
	cmd := exec.CommandContext(context.Background(), "git", "-C", repoPath,
		"symbolic-ref", "refs/remotes/origin/HEAD")
	out, err := cmd.Output()
	if err == nil {
		ref := strings.TrimSpace(string(out))
		if after, ok := strings.CutPrefix(ref, "refs/remotes/origin/"); ok {
			return after
		}
	}
	for _, candidate := range []string{"main", "master"} {
		check := exec.CommandContext(context.Background(), "git", "-C", repoPath,
			"rev-parse", "--verify", "origin/"+candidate)
		if check.Run() == nil {
			return candidate
		}
	}
	return "main"
}

func hasRemote(repoPath, name string) bool {
	cmd := exec.CommandContext(context.Background(), "git", "-C", repoPath, "remote", "get-url", name)
	return cmd.Run() == nil
}

func refExists(repoPath, ref string) bool {
	cmd := exec.CommandContext(context.Background(), "git", "-C", repoPath, "rev-parse", "--verify", ref)
	return cmd.Run() == nil
}

func FetchOrigin(repoPath, branch string) error {
	cmd := exec.CommandContext(context.Background(), "git", "-C", repoPath, "fetch", "origin", branch)
	var errBuf bytes.Buffer
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("git fetch: %s", strings.TrimSpace(errBuf.String()))
	}
	return nil
}

func CreateWorktree(repoPath, worktreePath, branch string) (offline bool, err error) {
	defaultBranch := DefaultBranch(repoPath)

	base := "origin/" + defaultBranch
	if hasRemote(repoPath, "origin") {
		if fetchErr := FetchOrigin(repoPath, defaultBranch); fetchErr != nil {
			if !refExists(repoPath, base) {
				return false, fmt.Errorf("git fetch failed and no local %s: %w", base, fetchErr)
			}
			offline = true
		}
	} else {
		base = defaultBranch
		if !refExists(repoPath, base) {
			return false, fmt.Errorf("no remote origin and local branch %q does not exist", base)
		}
		offline = true
	}

	cmd := exec.CommandContext(context.Background(), "git", "-C", repoPath, "worktree", "add", "-b", branch, worktreePath, base)
	var errBuf bytes.Buffer
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		return offline, fmt.Errorf("git worktree add: %s", strings.TrimSpace(errBuf.String()))
	}
	return offline, nil
}

func RemoveWorktree(repoPath, worktreePath string) error {
	cmd := exec.CommandContext(context.Background(), "git", "-C", repoPath, "worktree", "remove", "--force", worktreePath)
	var errBuf bytes.Buffer
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("git worktree remove: %s", strings.TrimSpace(errBuf.String()))
	}
	return nil
}

func DeleteBranch(repoPath, branch string) error {
	cmd := exec.CommandContext(context.Background(), "git", "-C", repoPath, "branch", "-D", branch)
	var errBuf bytes.Buffer
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("git branch -D: %s", strings.TrimSpace(errBuf.String()))
	}
	return nil
}
