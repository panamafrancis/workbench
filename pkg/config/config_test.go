package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func isolatedHome(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	return dir
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
	content := "#!/bin/bash\necho \"$WORKBENCH_WORKTREE_PATH $WORKBENCH_WORKTREE_NAME\" > " + outFile + "\n"
	if err := os.WriteFile(script, []byte(content), 0755); err != nil {
		t.Fatal(err)
	}

	r := &Repo{StartupScript: script}
	if err := r.RunStartup(dir, "myname"); err != nil {
		t.Fatalf("RunStartup() error = %v", err)
	}

	got, err := os.ReadFile(outFile)
	if err != nil {
		t.Fatalf("read output: %v", err)
	}
	want := dir + " myname\n"
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
