package git

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestCreateWorktreeThenRollsBackOnFailure(t *testing.T) {
	repo := initRepo(t)
	path := filepath.Join(t.TempDir(), "wt")
	boom := errors.New("boom")

	_, err := CreateWorktreeThen(repo, path, "wt/x", func() error { return boom })
	if !errors.Is(err, boom) {
		t.Fatalf("want the finish error, got %v", err)
	}
	if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
		t.Fatalf("worktree dir left behind: %v", statErr)
	}
	if refExists(repo, "refs/heads/wt/x") {
		t.Fatal("branch left behind")
	}

	// The same name works on retry.
	if _, err := CreateWorktreeThen(repo, path, "wt/x", func() error { return nil }); err != nil {
		t.Fatalf("retry: %v", err)
	}
	if !refExists(repo, "refs/heads/wt/x") {
		t.Fatal("retry did not create the branch")
	}
}

func TestCreateWorktreeThenKeepsAnExistingBranch(t *testing.T) {
	repo := initRepo(t)
	cmd := exec.CommandContext(context.Background(), "git", "-C", repo, "branch", "taken")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("branch: %v: %s", err, out)
	}
	called := false
	_, err := CreateWorktreeThen(repo, filepath.Join(t.TempDir(), "wt"), "taken", func() error {
		called = true
		return nil
	})
	if err == nil || called {
		t.Fatalf("want a refusal before finish runs, got err=%v called=%v", err, called)
	}
	if !refExists(repo, "refs/heads/taken") {
		t.Fatal("a branch this call did not create was deleted")
	}
}
