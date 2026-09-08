package git

import (
	"context"
	"os/exec"
	"testing"
)

func initRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, args := range [][]string{
		{"init", "-q", "-b", "main"},
		{"config", "user.email", "test@example.com"},
		{"config", "user.name", "test"},
		{"commit", "-q", "--allow-empty", "-m", "init"},
	} {
		cmd := exec.CommandContext(context.Background(), "git", append([]string{"-C", dir}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	return dir
}

func TestHasRemoteBranchMissing(t *testing.T) {
	dir := initRepo(t)
	if HasRemoteBranch(dir, "feature") {
		t.Fatal("expected no refs/remotes/origin/feature in a fresh repo")
	}
}

func TestHasRemoteBranchPresent(t *testing.T) {
	dir := initRepo(t)
	// Simulate what a push leaves behind: a remote-tracking ref.
	cmd := exec.CommandContext(context.Background(), "git", "-C", dir,
		"update-ref", "refs/remotes/origin/feature", "HEAD")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("update-ref: %v: %s", err, out)
	}
	if !HasRemoteBranch(dir, "feature") {
		t.Fatal("expected refs/remotes/origin/feature to be found")
	}
	// A local branch of the same name must not count as pushed.
	if HasRemoteBranch(dir, "main") {
		t.Fatal("local branch main must not be reported as a remote branch")
	}
}

func TestHasRemoteBranchBadRepo(t *testing.T) {
	if HasRemoteBranch(t.TempDir(), "feature") {
		t.Fatal("expected false outside a git repo")
	}
}
