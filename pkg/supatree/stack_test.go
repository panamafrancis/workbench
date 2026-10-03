package supatree

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/panamafrancis/workbench/pkg/testutil"
)

// stackFixture scaffolds a stack with one member and returns the config.
func stackFixture(t *testing.T) (*Config, string, string) {
	t.Helper()
	testutil.IsolateHome(t)
	gitGlobal(t, "user.email", "t@example.com")
	gitGlobal(t, "user.name", "t")
	a, b := originRepo(t), originRepo(t)
	c := &Config{}
	if _, err := Scaffold(c, "s", "", map[string]string{"a": a}); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	return cfg, a, b
}

// Every stack edit is a commit of supatree.yml, and nothing else.
func TestStackEdits(t *testing.T) {
	c, a, b := stackFixture(t)
	path := c.FindStack("s").Path
	writeFile(t, filepath.Join(path, "AGENTS.md"), "someone's draft")

	if err := c.StackAdd("s", "b", b, true); err != nil {
		t.Fatalf("add: %v", err)
	}
	if err := c.StackAdd("s", "b2", b, false); err == nil || !strings.Contains(err.Error(), "already a member") {
		t.Errorf("adding the same repo twice: %v", err)
	}
	if err := c.StackDep("s", "b", "a"); err != nil {
		t.Fatalf("dep: %v", err)
	}
	if err := c.StackDep("s", "a", "b"); err == nil {
		t.Error("a dependency cycle was accepted")
	}
	spec, _ := LoadSpec(path)
	if spec.Members["b"] != b || len(spec.Deps["b"]) != 1 {
		t.Fatalf("spec = %+v", spec)
	}
	if !isClone(filepath.Join(ReposDir(), mustKey(t, b))) {
		t.Error("stack add did not clone into the cache")
	}
	if err := c.StackRm("s", "a"); err != nil {
		t.Fatalf("rm: %v", err)
	}
	spec, _ = LoadSpec(path)
	if _, ok := spec.Members["a"]; ok || len(spec.Deps) != 0 {
		t.Errorf("rm left %+v", spec)
	}
	_ = a

	// Each edit committed the spec alone; the unrelated draft is untouched.
	log, _ := gitOutput(path, "log", "--oneline")
	if n := strings.Count(log, "\n") + 1; n != 4 {
		t.Errorf("commits = %d, want scaffold + add + dep + rm:\n%s", n, log)
	}
	if status, _ := gitOutput(path, "status", "--porcelain"); !strings.Contains(status, "AGENTS.md") {
		t.Error("an edit swept someone's uncommitted AGENTS.md into its commit")
	}
	writeFile(t, filepath.Join(path, SpecName), "members: {}\n")
	if err := c.StackAdd("s", "a", a, false); err == nil {
		t.Error("edited over an uncommitted supatree.yml")
	}
}

func mustKey(t *testing.T, url string) string {
	t.Helper()
	k, err := CacheKey(url)
	if err != nil {
		t.Fatal(err)
	}
	return k
}

// A teammate adopts a stack by URL: registered, members cached, setup shown.
func TestStackClone(t *testing.T) {
	c, a, _ := stackFixture(t)
	src := c.FindStack("s").Path
	writeFile(t, filepath.Join(src, SetupScript), "echo hi\n")
	run(t, src, "add", "-A")
	run(t, src, "commit", "-q", "-m", "setup")
	if err := os.RemoveAll(ReposDir()); err != nil {
		t.Fatal(err)
	}

	res, err := c.StackClone(src, "team", "")
	if err != nil {
		t.Fatalf("clone: %v", err)
	}
	if res.Setup != "echo hi\n" {
		t.Errorf("setup not surfaced: %q", res.Setup)
	}
	cfg, _ := Load()
	if s := cfg.FindStack("team"); s == nil || s.Path != DefaultStackPath("team") {
		t.Errorf("not registered: %+v", cfg.Stacks)
	}
	if !isClone(filepath.Join(ReposDir(), mustKey(t, a))) {
		t.Error("members not cloned into the cache")
	}
}

// --from reads origins only.
func TestScanOrigins(t *testing.T) {
	testutil.IsolateHome(t)
	dir := t.TempDir()
	withOrigin := filepath.Join(dir, "api")
	run(t, dir, "init", "-q", withOrigin)
	run(t, withOrigin, "remote", "add", "origin", "git@github.com:o/Keystone-API.git")
	run(t, dir, "init", "-q", filepath.Join(dir, "noorigin"))
	if err := os.MkdirAll(filepath.Join(dir, "notarepo"), 0755); err != nil {
		t.Fatal(err)
	}
	got, err := ScanOrigins(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got["keystone-api"] != "git@github.com:o/Keystone-API.git" {
		t.Errorf("ScanOrigins = %v", got)
	}
}

// A clone a tree still uses cannot be removed from the cache.
func TestRemoveCachedRepoRefusesInUse(t *testing.T) {
	c, a, _ := stackFixture(t)
	inst, _, err := New(c, CreateOptions{Stack: "s", Name: treeLima})
	if err != nil {
		t.Fatal(err)
	}
	r, err := FindCachedRepo(a)
	if err != nil {
		t.Fatal(err)
	}
	if err := RemoveCachedRepo(r, []*Instance{inst}); err == nil || !strings.Contains(err.Error(), treeLima) {
		t.Errorf("err = %v, want a refusal naming the tree", err)
	}
	if err := RemoveCachedRepo(r, nil); err != nil {
		t.Errorf("unused clone not removed: %v", err)
	}
}
