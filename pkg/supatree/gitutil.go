package supatree

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
)

func runGit(dir string, args ...string) error {
	full := append([]string{"-C", dir}, args...)
	cmd := exec.CommandContext(context.Background(), "git", full...)
	var errBuf bytes.Buffer
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("git %s: %s", strings.Join(args, " "), strings.TrimSpace(errBuf.String()))
	}
	return nil
}

func gitOutput(dir string, args ...string) (string, error) {
	full := append([]string{"-C", dir}, args...)
	cmd := exec.CommandContext(context.Background(), "git", full...)
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
	}
	return strings.TrimSpace(string(out)), nil
}

// isGitRepo reports whether dir is inside a git working tree.
func isGitRepo(dir string) bool {
	cmd := exec.CommandContext(context.Background(), "git", "-C", dir, "rev-parse", "--is-inside-work-tree")
	return cmd.Run() == nil
}

// initStackRepo initializes dir as a git repo whose default branch is "main"
// (matching git.DefaultBranch's offline fallback so worktrees can branch off it).
func initStackRepo(dir string) error {
	if err := runGit(dir, "init"); err != nil {
		return err
	}
	// Point HEAD at main before the first commit regardless of the user's
	// init.defaultBranch setting.
	if err := runGit(dir, "symbolic-ref", "HEAD", "refs/heads/main"); err != nil {
		return err
	}
	return nil
}

// commitAll stages everything in dir and commits with msg.
func commitAll(dir, msg string) error {
	if err := runGit(dir, "add", "-A"); err != nil {
		return err
	}
	return runGit(dir, "commit", "-m", msg)
}

// pushBranchAndDeleteOld pushes the current branch of worktreePath upstream and,
// if oldBranch is non-empty and differs, deletes the old remote branch. Missing
// remotes make this a no-op-with-error the caller may ignore.
func pushBranchAndDeleteOld(worktreePath, oldBranch string) error {
	if err := runGit(worktreePath, "push", "-u", "origin", "HEAD"); err != nil {
		return err
	}
	if oldBranch != "" {
		// Best effort: the old branch may never have been pushed.
		_ = runGit(worktreePath, "push", "origin", "--delete", oldBranch)
	}
	return nil
}
