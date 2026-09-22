package supatree

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/panamafrancis/workbench/pkg/config"
)

// originRepo creates a repo with one commit that worktrees can branch from.
func originRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	run(t, dir, "init", "-q", "-b", "main")
	run(t, dir, "config", "user.email", "test@example.com")
	run(t, dir, "config", "user.name", "test")
	run(t, dir, "commit", "-q", "--allow-empty", "-m", "init")
	return dir
}

func run(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.CommandContext(context.Background(), "git", append([]string{"-C", dir}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
}

func branchExists(t *testing.T, repo, branch string) bool {
	t.Helper()
	cmd := exec.CommandContext(context.Background(), "git", "-C", repo,
		"rev-parse", "--verify", "--quiet", "refs/heads/"+branch)
	return cmd.Run() == nil
}

// setupMember checks a member worktree out of repo on branch, and returns the
// tree root it lives under.
func setupMember(t *testing.T, repo, alias, branch string) (root string, wb *config.Config) {
	t.Helper()
	root = t.TempDir()
	path := MemberPath(root, alias)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	run(t, repo, "worktree", "add", "-q", "-b", branch, path, "main")
	return root, &config.Config{Repos: []config.Repo{{Alias: alias, LocalPath: repo}}}
}

// Teardown deletes the branch it created. That is the ordinary path and must
// keep working — the guard is not allowed to make `rm` leave rubbish behind.
func TestRemoveMemberDeletesItsOwnBranch(t *testing.T) {
	repo := originRepo(t)
	const branch = "st/canberra/keystone"
	root, wb := setupMember(t, repo, aliasKeystone, branch)

	report := &SyncReport{}
	removeMember(root, aliasKeystone, branch, wb, report)

	if branchExists(t, repo, branch) {
		t.Errorf("branch %q survived teardown; the tree created it and owns it", branch)
	}
	if len(report.Warnings) != 0 {
		t.Errorf("unexpected warnings: %v", report.Warnings)
	}
}

// The branch a member is *actually* on is not always the one the tree created:
// a `gh pr checkout` in a member worktree puts someone else's branch there. This
// runs `git branch -D`, so without the guard an ordinary teardown destroys it.
func TestRemoveMemberLeavesAForeignBranchAlone(t *testing.T) {
	repo := originRepo(t)
	const foreign = "feat/refunds-baseline-context"
	root, wb := setupMember(t, repo, aliasKeystone, foreign)

	report := &SyncReport{}
	removeMember(root, aliasKeystone, "st/canberra/keystone", wb, report)

	if !branchExists(t, repo, foreign) {
		t.Fatalf("teardown deleted %q — a branch this tree did not create", foreign)
	}
	if len(report.Warnings) == 0 {
		t.Error("leaving a branch behind must be reported, not silent")
	}
	if _, err := os.Stat(MemberPath(root, aliasKeystone)); !os.IsNotExist(err) {
		t.Error("the worktree itself should still have been removed")
	}
}

// The guard protects the author's branch, but must not make teardown leak the
// tree's own: a member checked out elsewhere still leaves st/<slug>/<alias>
// behind in the clone, and that would accumulate on every such removal.
func TestRemoveMemberStillReapsItsOwnBranchWhenMemberIsForeign(t *testing.T) {
	repo := originRepo(t)
	const own = "st/canberra/keystone"
	const foreign = "feat/refunds-baseline-context"
	root, wb := setupMember(t, repo, aliasKeystone, own)
	run(t, MemberPath(root, aliasKeystone), "checkout", "-q", "-b", foreign)

	removeMember(root, aliasKeystone, own, wb, &SyncReport{})

	if branchExists(t, repo, own) {
		t.Errorf("branch %q leaked: the tree created it and should reap it", own)
	}
	if !branchExists(t, repo, foreign) {
		t.Errorf("branch %q was deleted: the tree did not create it", foreign)
	}
}

// writeTree lays out a minimal supatree on disk: a spec, a meta, and member
// worktrees checked out of repo. It returns the tree root.
func writeTree(t *testing.T, repo string, meta *Meta, aliases ...string) string {
	t.Helper()
	root := t.TempDir()
	if err := SaveSpec(root, &Spec{Members: aliases}); err != nil {
		t.Fatal(err)
	}
	if err := meta.Save(root); err != nil {
		t.Fatal(err)
	}
	for _, alias := range aliases {
		path := MemberPath(root, alias)
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		run(t, repo, "worktree", "add", "-q", "-b", meta.MemberBranch(alias), path, "main")
	}
	return root
}

// Forking keeps the commits and moves each member onto its authoring branch,
// recording the author's branch as the PR base so the change is proposed on
// their pull request instead of competing with it.
func TestForkReviewConvertsAndRecordsBase(t *testing.T) {
	repo := originRepo(t)
	meta := &Meta{
		Name: treeA, Slug: treeA, Stack: "s", Mode: ModeReviewing,
		Review: map[string]ReviewRef{
			aliasKeystone: {Repo: repoKeystone, Number: 600, HeadRef: headRefRefunds, Head: "abc123"},
		},
	}
	root := writeTree(t, repo, meta, aliasKeystone)

	cfg := &Config{TreesBase: filepath.Dir(root), Stacks: []Stack{{Alias: "s", Path: repo}}}
	// Get() resolves by name under the trees base, so the dir must be the name.
	named := filepath.Join(filepath.Dir(root), treeA)
	if err := os.Rename(root, named); err != nil {
		t.Fatal(err)
	}
	wb := &config.Config{Repos: []config.Repo{{Alias: aliasKeystone, LocalPath: repo}}}

	results, err := ForkReview(cfg, wb, treeA)
	if err != nil {
		t.Fatalf("fork: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("got %d results, want 1", len(results))
	}
	if got, want := results[0].To, "st/canberra/keystone"; got != want {
		t.Errorf("To = %q, want %q", got, want)
	}
	if got, want := results[0].Base, headRefRefunds; got != want {
		t.Errorf("Base = %q, want the author's branch %q", got, want)
	}
	if got := currentBranch(t, MemberPath(named, aliasKeystone)); got != "st/canberra/keystone" {
		t.Errorf("member is on %q after fork", got)
	}

	after, err := LoadMeta(named)
	if err != nil {
		t.Fatal(err)
	}
	if after.Reviewing() {
		t.Error("still a review tree after forking")
	}
	if after.Base[aliasKeystone] != headRefRefunds {
		t.Errorf("base not recorded: %+v", after.Base)
	}
	if len(after.Review) == 0 {
		t.Error("the pull requests this came from should be kept for provenance")
	}
}

// Forking something that was never a review is a mistake worth naming, not a
// no-op that silently rewrites branches.
func TestForkReviewRefusesAuthoringTree(t *testing.T) {
	repo := originRepo(t)
	meta := &Meta{Name: "berlin", Slug: "berlin", Stack: "s"}
	root := writeTree(t, repo, meta, aliasKeystone)
	named := filepath.Join(filepath.Dir(root), "berlin")
	if err := os.Rename(root, named); err != nil {
		t.Fatal(err)
	}
	cfg := &Config{TreesBase: filepath.Dir(named), Stacks: []Stack{{Alias: "s", Path: repo}}}
	wb := &config.Config{Repos: []config.Repo{{Alias: aliasKeystone, LocalPath: repo}}}
	if _, err := ForkReview(cfg, wb, "berlin"); err == nil {
		t.Error("forking an authoring tree was allowed")
	}
}

// Forking keeps the reviewed pull requests in meta for provenance, but the
// members must stop keying their status on them: a forked tree opens its own
// pull requests, and while CacheKey still said pr:<repo>#<n> those were invisible
// to every status surface while the author's PR was reported in their place.
func TestForkedMembersStopKeyingOnTheReviewedPR(t *testing.T) {
	repo := originRepo(t)
	meta := &Meta{
		Name: treeA, Slug: treeA, Stack: "s", Mode: ModeReviewing,
		Review: map[string]ReviewRef{
			aliasKeystone: {Repo: repoKeystone, Number: 600, HeadRef: headRefRefunds, Head: "abc123"},
		},
	}
	root := writeTree(t, repo, meta, aliasKeystone)
	named := filepath.Join(filepath.Dir(root), treeA)
	if err := os.Rename(root, named); err != nil {
		t.Fatal(err)
	}
	cfg := &Config{TreesBase: filepath.Dir(named), Stacks: []Stack{{Alias: "s", Path: repo}}}
	wb := &config.Config{Repos: []config.Repo{{Alias: aliasKeystone, LocalPath: repo}}}

	before, err := LoadInstance(named)
	if err != nil {
		t.Fatal(err)
	}
	if got := before.FindMember(aliasKeystone).CacheKey(); got != "pr:"+repoKeystone+"#600" {
		t.Fatalf("while reviewing, CacheKey = %q, want the PR-scoped key", got)
	}

	if _, err := ForkReview(cfg, wb, treeA); err != nil {
		t.Fatalf("fork: %v", err)
	}

	after, err := LoadInstance(named)
	if err != nil {
		t.Fatal(err)
	}
	m := after.FindMember(aliasKeystone)
	if m.Review != nil {
		t.Error("a forked member still carries the reviewed PR, so its status resolves to the author's")
	}
	if got, want := m.CacheKey(), "st/"+treeA+"/"+aliasKeystone; got != want {
		t.Errorf("CacheKey = %q, want %q — a forked tree tracks its own pull requests", got, want)
	}
	// Provenance is still recorded; it just no longer drives status.
	saved, err := LoadMeta(named)
	if err != nil {
		t.Fatal(err)
	}
	if len(saved.Review) == 0 {
		t.Error("the pull requests this was forked from should still be recorded")
	}
}
