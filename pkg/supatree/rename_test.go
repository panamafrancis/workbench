package supatree

import (
	"context"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/panamafrancis/workbench/pkg/git"
)

// memberRepo creates a git repo checked out on branch, standing in for one
// member worktree of a supatree.
func memberRepo(t *testing.T, branch string) string {
	t.Helper()
	dir := t.TempDir()
	for _, args := range [][]string{
		{"init", "-q", "-b", "main"},
		{"config", "user.email", "test@example.com"},
		{"config", "user.name", "test"},
		{"commit", "-q", "--allow-empty", "-m", "init"},
		{"checkout", "-q", "-b", branch},
	} {
		cmd := exec.CommandContext(context.Background(), "git", append([]string{"-C", dir}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	return dir
}

func currentBranch(t *testing.T, dir string) string {
	t.Helper()
	b, err := git.CurrentBranch(dir)
	if err != nil {
		t.Fatalf("current branch: %v", err)
	}
	return b
}

func TestRenameMemberBranches(t *testing.T) {
	a := memberRepo(t, "st/old/a")
	b := memberRepo(t, "st/old/b")
	inst := &Instance{Members: []Member{
		{Alias: "a", Path: a, Branch: "st/old/a", Exists: true},
		{Alias: "b", Path: b, Branch: "st/old/b", Exists: true},
		{Alias: "c", Path: filepath.Join(t.TempDir(), "missing"), Branch: "st/old/c"},
	}}

	renamed, err := renameMemberBranches(inst, "new")
	if err != nil {
		t.Fatalf("renameMemberBranches: %v", err)
	}
	if len(renamed) != 2 {
		t.Fatalf("renamed %d members, want 2 (the absent member is skipped)", len(renamed))
	}
	if got := currentBranch(t, a); got != "st/new/a" {
		t.Errorf("a on %q, want st/new/a", got)
	}
	if got := currentBranch(t, b); got != "st/new/b" {
		t.Errorf("b on %q, want st/new/b", got)
	}
	if renamed[0].oldBranch != "st/old/a" || renamed[0].newBranch != "st/new/a" {
		t.Errorf("renamed[0] = %+v, want the old and new branch of a", renamed[0])
	}
}

func TestRenameMemberBranchesRollsBack(t *testing.T) {
	// A mid-loop failure used to leave earlier members on the new slug while
	// meta.Slug stayed on the old one, orphaning them from their own tree.
	a := memberRepo(t, "st/old/a")
	b := memberRepo(t, "st/old/b")
	broken := t.TempDir() // not a git repo: the rename here must fail
	inst := &Instance{Members: []Member{
		{Alias: "a", Path: a, Branch: "st/old/a", Exists: true},
		{Alias: "b", Path: b, Branch: "st/old/b", Exists: true},
		{Alias: "broken", Path: broken, Branch: "st/old/broken", Exists: true},
	}}

	renamed, err := renameMemberBranches(inst, "new")
	if err == nil {
		t.Fatal("expected an error from the member that cannot be renamed")
	}
	if renamed != nil {
		t.Errorf("renamed = %+v, want nil on failure", renamed)
	}
	if got := currentBranch(t, a); got != "st/old/a" {
		t.Errorf("a on %q, want st/old/a — earlier renames must be rolled back", got)
	}
	if got := currentBranch(t, b); got != "st/old/b" {
		t.Errorf("b on %q, want st/old/b — earlier renames must be rolled back", got)
	}
}

func TestRenameMemberBranchesFirstMemberFails(t *testing.T) {
	inst := &Instance{Members: []Member{
		{Alias: "broken", Path: t.TempDir(), Branch: "st/old/broken", Exists: true},
	}}
	if _, err := renameMemberBranches(inst, "new"); err == nil {
		t.Fatal("expected an error")
	}
}
