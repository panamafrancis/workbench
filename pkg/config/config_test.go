package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func isolatedHome(t *testing.T) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
}

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.Version != 1 {
		t.Errorf("Version = %d, want 1", cfg.Version)
	}
	if cfg.DefaultModel != "claude" {
		t.Errorf("DefaultModel = %q, want %q", cfg.DefaultModel, "claude")
	}
	for _, key := range []string{"claude", "codex", "opencode", "dirac", "shell"} {
		if _, ok := cfg.Models[key]; !ok {
			t.Errorf("missing default model %q", key)
		}
	}
	if m := cfg.Models["claude"]; m.NonoProfile != "claude-code" || m.Binary != "claude" {
		t.Errorf("claude model = %+v, unexpected defaults", m)
	}
}

func TestLoadMissingReturnsDefault(t *testing.T) {
	isolatedHome(t)
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.DefaultModel != "claude" {
		t.Errorf("DefaultModel = %q, want %q", cfg.DefaultModel, "claude")
	}
}

func TestSaveAndLoadRoundtrip(t *testing.T) {
	isolatedHome(t)

	orig := DefaultConfig()
	orig.DefaultModel = "codex"
	orig.WorktreeBase = "/custom/base"
	orig.Repos = []Repo{
		{
			Alias:     "myrepo",
			LocalPath: "/some/path",
			Worktrees: []Worktree{
				{
					Name:      "myworktree",
					Branch:    "wt/myrepo/myworktree",
					Path:      "/some/path/wt",
					CreatedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
					Model:     "codex",
				},
			},
		},
	}

	if err := orig.Save(); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	loaded, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if loaded.DefaultModel != orig.DefaultModel {
		t.Errorf("DefaultModel = %q, want %q", loaded.DefaultModel, orig.DefaultModel)
	}
	if loaded.WorktreeBase != orig.WorktreeBase {
		t.Errorf("WorktreeBase = %q, want %q", loaded.WorktreeBase, orig.WorktreeBase)
	}
	if len(loaded.Repos) != 1 || loaded.Repos[0].Alias != "myrepo" {
		t.Errorf("Repos = %+v, want one repo with alias myrepo", loaded.Repos)
	}
	if len(loaded.Repos[0].Worktrees) != 1 || loaded.Repos[0].Worktrees[0].Name != "myworktree" {
		t.Errorf("Worktrees = %+v, unexpected", loaded.Repos[0].Worktrees)
	}
}

func TestAddWorktreeReadModifyWrite(t *testing.T) {
	isolatedHome(t)
	base := DefaultConfig()
	base.Repos = []Repo{{Alias: "r1", LocalPath: "/r1"}}
	if err := base.Save(); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	if err := AddWorktree("r1", Worktree{Name: "alpha", Branch: "wt/r1/alpha"}); err != nil {
		t.Fatalf("AddWorktree() error = %v", err)
	}

	loaded, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got := loaded.AllWorktreeNames(); len(got) != 1 || got[0] != "alpha" {
		t.Errorf("worktrees = %v, want [alpha]", got)
	}
}

func TestAddWorktreeUnknownRepo(t *testing.T) {
	isolatedHome(t)
	if err := DefaultConfig().Save(); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	if err := AddWorktree("nope", Worktree{Name: "x"}); err == nil {
		t.Error("AddWorktree(unknown repo) = nil, want error")
	}
}

// A stale caller adding a worktree must not resurrect a worktree that was
// deleted from disk after the caller took its in-memory snapshot.
func TestAddWorktreeDoesNotResurrectDeleted(t *testing.T) {
	isolatedHome(t)
	base := DefaultConfig()
	base.Repos = []Repo{{Alias: "r1", LocalPath: "/r1", Worktrees: []Worktree{
		{Name: "alpha", Branch: "wt/r1/alpha"},
	}}}
	if err := base.Save(); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	// Simulate another process deleting "alpha" from disk.
	if err := RemoveWorktreeEntry("alpha"); err != nil {
		t.Fatalf("RemoveWorktreeEntry() error = %v", err)
	}

	// "base" is now stale (still has alpha). Adding bravo via the helper must
	// read current disk state, so alpha stays gone.
	if err := AddWorktree("r1", Worktree{Name: "bravo", Branch: "wt/r1/bravo"}); err != nil {
		t.Fatalf("AddWorktree() error = %v", err)
	}

	loaded, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	got := loaded.AllWorktreeNames()
	if len(got) != 1 || got[0] != "bravo" {
		t.Errorf("worktrees = %v, want [bravo] (alpha must not be resurrected)", got)
	}
}

// Concurrent AddWorktree calls must not lose updates: the per-call read from
// disk could otherwise clobber a sibling add, but the config lock serializes
// the Load→Save sequences.
func TestAddWorktreeConcurrent(t *testing.T) {
	isolatedHome(t)
	base := DefaultConfig()
	base.Repos = []Repo{{Alias: "r1", LocalPath: "/r1"}}
	if err := base.Save(); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	const n = 16
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for i := range n {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			name := fmt.Sprintf("wt%02d", i)
			errs <- AddWorktree("r1", Worktree{Name: name, Branch: "wt/r1/" + name})
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("AddWorktree() error = %v", err)
		}
	}

	loaded, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got := len(loaded.AllWorktreeNames()); got != n {
		t.Errorf("worktree count = %d, want %d (lost updates under concurrency)", got, n)
	}
}

func TestRemoveWorktreeEntry(t *testing.T) {
	isolatedHome(t)
	base := DefaultConfig()
	base.Repos = []Repo{{Alias: "r1", LocalPath: "/r1", Worktrees: []Worktree{
		{Name: "alpha"}, {Name: "bravo"},
	}}}
	if err := base.Save(); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	if err := RemoveWorktreeEntry("alpha"); err != nil {
		t.Fatalf("RemoveWorktreeEntry() error = %v", err)
	}
	// Removing a missing name is a no-op, not an error.
	if err := RemoveWorktreeEntry("ghost"); err != nil {
		t.Fatalf("RemoveWorktreeEntry(missing) error = %v", err)
	}

	loaded, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got := loaded.AllWorktreeNames(); len(got) != 1 || got[0] != "bravo" {
		t.Errorf("worktrees = %v, want [bravo]", got)
	}
}

func TestSaveIsAtomic(t *testing.T) {
	isolatedHome(t)
	cfg := DefaultConfig()
	if err := cfg.Save(); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	// temp file must not be left behind
	if _, err := os.Stat(ConfigPath() + ".tmp"); !os.IsNotExist(err) {
		t.Error("temp file still present after Save()")
	}
}

func TestFindRepoFound(t *testing.T) {
	cfg := &Config{
		Repos: []Repo{
			{Alias: "aa"},
			{Alias: "bb"},
		},
	}
	r, idx := cfg.FindRepo("bb")
	if r == nil || idx != 1 {
		t.Errorf("FindRepo(bb) = (%v, %d), want (non-nil, 1)", r, idx)
	}
}

func TestFindRepoNotFound(t *testing.T) {
	cfg := &Config{Repos: []Repo{{Alias: "aa"}}}
	r, idx := cfg.FindRepo("zz")
	if r != nil || idx != -1 {
		t.Errorf("FindRepo(zz) = (%v, %d), want (nil, -1)", r, idx)
	}
}

func TestFindWorktreeFound(t *testing.T) {
	cfg := &Config{
		Repos: []Repo{
			{Alias: "r1", Worktrees: []Worktree{{Name: "w1"}}},
			{Alias: "r2", Worktrees: []Worktree{{Name: "w2"}, {Name: "w3"}}},
		},
	}
	wt, repo := cfg.FindWorktree("w3")
	if wt == nil || repo == nil {
		t.Fatal("FindWorktree(w3) returned nil")
	}
	if wt.Name != "w3" || repo.Alias != "r2" {
		t.Errorf("FindWorktree(w3) = (%q, %q), want (w3, r2)", wt.Name, repo.Alias)
	}
}

func TestFindWorktreeNotFound(t *testing.T) {
	cfg := &Config{
		Repos: []Repo{{Alias: "r1", Worktrees: []Worktree{{Name: "w1"}}}},
	}
	wt, repo := cfg.FindWorktree("nope")
	if wt != nil || repo != nil {
		t.Errorf("FindWorktree(nope) = (%v, %v), want (nil, nil)", wt, repo)
	}
}

func TestAllWorktreeNamesEmpty(t *testing.T) {
	cfg := &Config{}
	if names := cfg.AllWorktreeNames(); len(names) != 0 {
		t.Errorf("AllWorktreeNames() = %v, want empty", names)
	}
}

func TestAllWorktreeNamesMultiRepo(t *testing.T) {
	cfg := &Config{
		Repos: []Repo{
			{Worktrees: []Worktree{{Name: "a"}, {Name: "b"}}},
			{Worktrees: []Worktree{{Name: "c"}}},
		},
	}
	names := cfg.AllWorktreeNames()
	if len(names) != 3 {
		t.Errorf("AllWorktreeNames() = %v, want [a b c]", names)
	}
}

func TestResolveModelExplicit(t *testing.T) {
	cfg := &Config{DefaultModel: "codex"}
	if got := cfg.ResolveModel("dirac"); got != "dirac" {
		t.Errorf("ResolveModel(dirac) = %q, want %q", got, "dirac")
	}
}

func TestResolveModelFallsBackToDefault(t *testing.T) {
	cfg := &Config{DefaultModel: "codex"}
	if got := cfg.ResolveModel(""); got != "codex" {
		t.Errorf("ResolveModel('') = %q, want %q", got, "codex")
	}
}

func TestResolveModelFallsBackToClaude(t *testing.T) {
	cfg := &Config{}
	if got := cfg.ResolveModel(""); got != "claude" {
		t.Errorf("ResolveModel('') with no default = %q, want %q", got, "claude")
	}
}

func TestResolveWorktreeBaseCustom(t *testing.T) {
	cfg := &Config{WorktreeBase: "/my/base"}
	if got := cfg.ResolveWorktreeBase(); got != "/my/base" {
		t.Errorf("ResolveWorktreeBase() = %q, want /my/base", got)
	}
}

func TestResolveWorktreeBaseDefault(t *testing.T) {
	isolatedHome(t)
	cfg := &Config{}
	got := cfg.ResolveWorktreeBase()
	if got == "" {
		t.Error("ResolveWorktreeBase() returned empty string")
	}
}

func TestRunStartupNoScript(t *testing.T) {
	r := &Repo{}
	if err := r.RunStartup("/some/path", "myname"); err != nil {
		t.Errorf("RunStartup with no script = %v, want nil", err)
	}
}

func TestRunCleanupNoScript(t *testing.T) {
	r := &Repo{}
	if err := r.RunCleanup("/some/path", "myname"); err != nil {
		t.Errorf("RunCleanup with no script = %v, want nil", err)
	}
}

func TestRunStartupScriptReceivesEnv(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "startup.sh")
	outFile := filepath.Join(dir, "out.txt")
	content := "#!/bin/bash\necho \"$WORKBENCH_REPO_BASE_PATH $WORKBENCH_WORKTREE_PATH $WORKBENCH_WORKTREE_NAME\" > " + outFile + "\n"
	if err := os.WriteFile(script, []byte(content), 0755); err != nil {
		t.Fatal(err)
	}

	repoDir := t.TempDir()
	r := &Repo{LocalPath: repoDir, StartupScript: script}
	if err := r.RunStartup(dir, "myname"); err != nil {
		t.Fatalf("RunStartup() error = %v", err)
	}

	got, err := os.ReadFile(outFile)
	if err != nil {
		t.Fatalf("read output: %v", err)
	}
	want := repoDir + " " + dir + " myname\n"
	if string(got) != want {
		t.Errorf("script output = %q, want %q", string(got), want)
	}
}

func TestRunStartupScriptNotFound(t *testing.T) {
	r := &Repo{StartupScript: "/nonexistent/script.sh"}
	if err := r.RunStartup("/wt/path", "myname"); err == nil {
		t.Error("RunStartup with missing script should return error")
	}
}

func TestRunCopyFilesFile(t *testing.T) {
	repoDir := t.TempDir()
	wtDir := t.TempDir()

	if err := os.WriteFile(filepath.Join(repoDir, ".env"), []byte("SECRET=1"), 0644); err != nil {
		t.Fatal(err)
	}

	r := &Repo{LocalPath: repoDir, CopyFiles: []string{".env"}}
	if err := r.RunCopyFiles(wtDir); err != nil {
		t.Fatalf("RunCopyFiles() error = %v", err)
	}

	got, err := os.ReadFile(filepath.Join(wtDir, ".env"))
	if err != nil {
		t.Fatalf("read copied file: %v", err)
	}
	if string(got) != "SECRET=1" {
		t.Errorf("copied content = %q, want %q", string(got), "SECRET=1")
	}
}

func TestRunCopyFilesDir(t *testing.T) {
	repoDir := t.TempDir()
	wtDir := t.TempDir()

	subDir := filepath.Join(repoDir, ".claude")
	if err := os.MkdirAll(subDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(subDir, "settings.json"), []byte(`{"key":true}`), 0644); err != nil {
		t.Fatal(err)
	}

	r := &Repo{LocalPath: repoDir, CopyFiles: []string{".claude"}}
	if err := r.RunCopyFiles(wtDir); err != nil {
		t.Fatalf("RunCopyFiles() error = %v", err)
	}

	got, err := os.ReadFile(filepath.Join(wtDir, ".claude", "settings.json"))
	if err != nil {
		t.Fatalf("read copied file: %v", err)
	}
	if string(got) != `{"key":true}` {
		t.Errorf("copied content = %q, want %q", string(got), `{"key":true}`)
	}
}

func TestRunCopyFilesMissingSrc(t *testing.T) {
	repoDir := t.TempDir()
	wtDir := t.TempDir()

	r := &Repo{LocalPath: repoDir, CopyFiles: []string{".nonexistent"}}
	if err := r.RunCopyFiles(wtDir); err == nil {
		t.Error("RunCopyFiles with missing source should return error")
	}
}

func TestRunCopyFilesRejectsAbsPath(t *testing.T) {
	r := &Repo{LocalPath: "/repo", CopyFiles: []string{"/etc/passwd"}}
	if err := r.RunCopyFiles("/wt"); err == nil {
		t.Error("RunCopyFiles with absolute path should return error")
	}
}

func TestRunCopyFilesRejectsTraversal(t *testing.T) {
	r := &Repo{LocalPath: "/repo", CopyFiles: []string{"../etc/passwd"}}
	err := r.RunCopyFiles("/wt")
	if err == nil {
		t.Error("RunCopyFiles with parent traversal should return error")
	}
	if err != nil && !strings.Contains(err.Error(), "escapes repo root") {
		t.Errorf("unexpected error = %v, want 'escapes repo root'", err)
	}
}

func TestRunCopyFilesEmpty(t *testing.T) {
	r := &Repo{LocalPath: "/repo"}
	if err := r.RunCopyFiles("/wt"); err != nil {
		t.Errorf("RunCopyFiles with no files = %v, want nil", err)
	}
}

func TestLoadPreservesCustomModel(t *testing.T) {
	isolatedHome(t)
	cfg := DefaultConfig()
	cfg.Models["mymodel"] = Model{
		NonoProfile: "custom",
		Binary:      "mytool",
		Args:        []string{"--flag"},
	}
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	m, ok := loaded.Models["mymodel"]
	if !ok {
		t.Fatal("custom model not preserved after save/load")
	}
	if m.Binary != "mytool" || m.NonoProfile != "custom" {
		t.Errorf("custom model = %+v, unexpected", m)
	}
}
