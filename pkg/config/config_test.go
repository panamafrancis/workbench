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

// Model keys used across the config tests. They stand in for whatever the
// default config ships with, so they are named once rather than spelled out at
// every assertion.
const (
	modelClaude = "claude"
	modelCodex  = "codex"
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
	if cfg.DefaultModel != modelClaude {
		t.Errorf("DefaultModel = %q, want %q", cfg.DefaultModel, modelClaude)
	}
	for _, key := range []string{modelClaude, modelCodex, "opencode", "dirac", "shell"} {
		if _, ok := cfg.Models[key]; !ok {
			t.Errorf("missing default model %q", key)
		}
	}
	if m := cfg.Models[modelClaude]; m.NonoProfile != "claude-code" || m.Binary != "claude" {
		t.Errorf("claude model = %+v, unexpected defaults", m)
	}
}

func TestLoadMissingReturnsDefault(t *testing.T) {
	isolatedHome(t)
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.DefaultModel != modelClaude {
		t.Errorf("DefaultModel = %q, want %q", cfg.DefaultModel, modelClaude)
	}
}

func TestSaveAndLoadRoundtrip(t *testing.T) {
	isolatedHome(t)

	orig := DefaultConfig()
	orig.DefaultModel = modelCodex
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
					Model:     modelCodex,
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
	cfg := &Config{DefaultModel: modelCodex}
	if got := cfg.ResolveModel("dirac"); got != "dirac" {
		t.Errorf("ResolveModel(dirac) = %q, want %q", got, "dirac")
	}
}

func TestResolveModelFallsBackToDefault(t *testing.T) {
	cfg := &Config{DefaultModel: modelCodex}
	if got := cfg.ResolveModel(""); got != modelCodex {
		t.Errorf("ResolveModel('') = %q, want %q", got, modelCodex)
	}
}

func TestResolveModelFallsBackToClaude(t *testing.T) {
	cfg := &Config{}
	if got := cfg.ResolveModel(""); got != modelClaude {
		t.Errorf("ResolveModel('') with no default = %q, want %q", got, modelClaude)
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

func TestCopyFilesFile(t *testing.T) {
	repoDir := t.TempDir()
	wtDir := t.TempDir()

	if err := os.WriteFile(filepath.Join(repoDir, ".env"), []byte("SECRET=1"), 0644); err != nil {
		t.Fatal(err)
	}

	if missing, err := CopyFiles(repoDir, wtDir, []string{".env"}); err != nil || len(missing) != 0 {
		t.Fatalf("CopyFiles() = %v, %v", missing, err)
	}

	got, err := os.ReadFile(filepath.Join(wtDir, ".env"))
	if err != nil {
		t.Fatalf("read copied file: %v", err)
	}
	if string(got) != "SECRET=1" {
		t.Errorf("copied content = %q, want %q", string(got), "SECRET=1")
	}
}

func TestCopyFilesDir(t *testing.T) {
	repoDir := t.TempDir()
	wtDir := t.TempDir()

	subDir := filepath.Join(repoDir, ".claude")
	if err := os.MkdirAll(subDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(subDir, "settings.json"), []byte(`{"key":true}`), 0644); err != nil {
		t.Fatal(err)
	}

	if _, err := CopyFiles(repoDir, wtDir, []string{".claude"}); err != nil {
		t.Fatalf("CopyFiles() error = %v", err)
	}

	got, err := os.ReadFile(filepath.Join(wtDir, ".claude", "settings.json"))
	if err != nil {
		t.Fatalf("read copied file: %v", err)
	}
	if string(got) != `{"key":true}` {
		t.Errorf("copied content = %q, want %q", string(got), `{"key":true}`)
	}
}

// A missing source is reported, not fatal: the rest of the list still copies.
func TestCopyFilesMissingSrcContinues(t *testing.T) {
	repoDir := t.TempDir()
	wtDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(repoDir, ".env"), []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}

	missing, err := CopyFiles(repoDir, wtDir, []string{".nonexistent", ".env"})
	if err != nil {
		t.Fatalf("CopyFiles() error = %v, want nil", err)
	}
	if len(missing) != 1 || missing[0] != ".nonexistent" {
		t.Errorf("missing = %v, want [.nonexistent]", missing)
	}
	if _, err := os.Stat(filepath.Join(wtDir, ".env")); err != nil {
		t.Errorf(".env after a missing entry was not copied: %v", err)
	}
}

func TestCopyFilesRejectsAbsPath(t *testing.T) {
	if _, err := CopyFiles("/repo", "/wt", []string{"/etc/passwd"}); err == nil {
		t.Error("CopyFiles with absolute path should return error")
	}
}

func TestCopyFilesRejectsTraversal(t *testing.T) {
	_, err := CopyFiles("/repo", "/wt", []string{"../etc/passwd"})
	if err == nil {
		t.Error("CopyFiles with parent traversal should return error")
	}
	if err != nil && !strings.Contains(err.Error(), "escapes repo root") {
		t.Errorf("unexpected error = %v, want 'escapes repo root'", err)
	}
}

func TestCopyFilesEmpty(t *testing.T) {
	if missing, err := CopyFiles("/repo", "/wt", nil); err != nil || len(missing) != 0 {
		t.Errorf("CopyFiles with no files = %v, %v, want nil", missing, err)
	}
}

func TestModelLookup(t *testing.T) {
	cfg := DefaultConfig()
	if m, err := cfg.Model(modelClaude); err != nil || m.Binary != modelClaude {
		t.Errorf("Model(claude) = %+v, %v", m, err)
	}
	if _, err := cfg.Model("nosuchmodel"); err == nil || !strings.Contains(err.Error(), "models:") {
		t.Errorf("Model(unknown) error = %v, want one naming the models section", err)
	}
}

// DefaultModels hands out a fresh map: one caller adding an entry must not leak
// into the next config built from it.
func TestDefaultModelsIsFresh(t *testing.T) {
	a := DefaultModels()
	a["mine"] = Model{Binary: "x"}
	if _, ok := DefaultModels()["mine"]; ok {
		t.Error("DefaultModels returned a shared map")
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
