package supatree

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/panamafrancis/workbench/pkg/git"
	"github.com/panamafrancis/workbench/pkg/testutil"
)

// A hook an agent drops into a clone it can write never runs in an
// unsandboxed git started by a hardened process.
func TestHardenedGitIgnoresRepoHooks(t *testing.T) {
	testutil.IsolateHome(t)
	for _, k := range []string{"GIT_CONFIG_COUNT", "SUPATREE_GIT_HARDENED"} {
		t.Setenv(k, "")
	}
	repo := originRepo(t)
	marker := filepath.Join(t.TempDir(), "pwned")
	hook := filepath.Join(repo, ".git", "hooks", "pre-commit")
	writeFile(t, hook, "#!/bin/sh\ntouch "+marker+"\n")
	if err := os.Chmod(hook, 0755); err != nil {
		t.Fatal(err)
	}
	git.HardenGit()
	run(t, repo, "commit", "-q", "--allow-empty", "-m", "x")
	if _, err := os.Stat(marker); err == nil {
		t.Error("a repository hook ran under HardenGit")
	}
}

// A filter driver in a clone's config — a command git would run on checkout —
// stops supatree from touching the clone at all.
func TestRiskyCloneRefused(t *testing.T) {
	c, a, _ := stackFixture(t)
	bc, err := c.ResolveMember("a", a)
	if err != nil {
		t.Fatal(err)
	}
	run(t, bc.Clone, "config", "filter.x.smudge", "sh -c 'touch /tmp/pwned'")
	if _, err := c.ResolveMember("a", a); err == nil || !strings.Contains(err.Error(), "filter.x.smudge") {
		t.Errorf("err = %v, want a refusal naming the setting", err)
	}
	if _, _, err := New(c, CreateOptions{Stack: "s", Name: treeLima}); err == nil {
		t.Error("created a tree from a clone whose config runs a command")
	}
}

// A tree may sync only repositories its stack or the cache already knows: its
// own spec is agent-writable.
func TestTreeSyncCannotReachUnknownRepos(t *testing.T) {
	c, _, b := stackFixture(t)
	inst, _, err := New(c, CreateOptions{Stack: "s", Name: treeLima})
	if err != nil {
		t.Fatal(err)
	}
	spec, _ := LoadSpec(inst.Root)
	spec.Members["b"] = b
	if err := SaveSpec(inst.Root, spec); err != nil {
		t.Fatal(err)
	}
	res := c.RunOp(ClaimedOp{Dir: StateDir(inst.Root), Op: Op{ID: "x", Kind: OpSync, Tree: treeLima}}, OpHooks{})
	if !strings.Contains(res.Err, "neither in stack") {
		t.Errorf("tree sync of an unknown repo: %+v", res)
	}
	// The PM (or the human) may.
	res = c.RunOp(ClaimedOp{Dir: PMDir(), Op: Op{ID: "y", Kind: OpSync, Tree: treeLima}}, OpHooks{})
	if res.Err != "" {
		t.Errorf("PM sync refused: %s", res.Err)
	}
}
