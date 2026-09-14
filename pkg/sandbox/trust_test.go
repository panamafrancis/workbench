package sandbox

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// claudeHome sets up an isolated HOME holding a Claude config with the given
// projects map, laid out the way the real one is: ~/.claude/claude.json with
// ~/.claude.json symlinked to it.
func claudeHome(t *testing.T, projects map[string]any) (home, real string) {
	t.Helper()
	home = t.TempDir()
	t.Setenv("HOME", home)

	dir := filepath.Join(home, ".claude")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	real = filepath.Join(dir, "claude.json")
	cfg := map[string]any{"numStartups": 7, "projects": projects}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(real, data, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(".claude", "claude.json"), filepath.Join(home, ".claude.json")); err != nil {
		t.Fatal(err)
	}
	return home, real
}

func readProjects(t *testing.T, path string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var cfg map[string]any
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("config is not valid json: %v", err)
	}
	projects, _ := cfg["projects"].(map[string]any)
	return projects
}

func TestTrustDirCreatesEntry(t *testing.T) {
	_, real := claudeHome(t, map[string]any{})

	if err := TrustDir("/trees/berlin"); err != nil {
		t.Fatalf("TrustDir: %v", err)
	}

	entry, _ := readProjects(t, real)["/trees/berlin"].(map[string]any)
	if entry == nil {
		t.Fatal("expected a project entry for the directory")
	}
	if trusted, _ := entry[trustFlag].(bool); !trusted {
		t.Errorf("%s = %v, want true", trustFlag, entry[trustFlag])
	}
	// The skeleton matters: a seeded entry should look like one Claude wrote.
	for _, field := range []string{"allowedTools", "mcpServers", "hasClaudeMdExternalIncludesApproved"} {
		if _, ok := entry[field]; !ok {
			t.Errorf("seeded entry missing %q", field)
		}
	}
}

func TestTrustDirFlipsExistingEntry(t *testing.T) {
	_, real := claudeHome(t, map[string]any{
		"/trees/berlin": map[string]any{trustFlag: false, "allowedTools": []any{"Bash"}},
	})

	if err := TrustDir("/trees/berlin"); err != nil {
		t.Fatalf("TrustDir: %v", err)
	}

	entry, _ := readProjects(t, real)["/trees/berlin"].(map[string]any)
	if trusted, _ := entry[trustFlag].(bool); !trusted {
		t.Error("an untrusted entry should be flipped to trusted")
	}
	tools, _ := entry["allowedTools"].([]any)
	if len(tools) != 1 {
		t.Errorf("allowedTools = %v, want the existing value preserved", entry["allowedTools"])
	}
}

func TestTrustDirPreservesOtherProjects(t *testing.T) {
	_, real := claudeHome(t, map[string]any{
		"/other": map[string]any{trustFlag: true, "lastCost": 1.5},
	})

	if err := TrustDir("/trees/berlin"); err != nil {
		t.Fatalf("TrustDir: %v", err)
	}

	projects := readProjects(t, real)
	other, _ := projects["/other"].(map[string]any)
	if other == nil || other["lastCost"] == nil {
		t.Fatalf("other projects must survive untouched, got %v", projects["/other"])
	}
	if len(projects) != 2 {
		t.Errorf("projects = %d entries, want 2", len(projects))
	}
}

func TestTrustDirNoWriteWhenAlreadyTrusted(t *testing.T) {
	_, real := claudeHome(t, map[string]any{
		"/trees/berlin": map[string]any{trustFlag: true},
	})
	before, err := os.ReadFile(real)
	if err != nil {
		t.Fatal(err)
	}

	if err := TrustDir("/trees/berlin"); err != nil {
		t.Fatalf("TrustDir: %v", err)
	}

	after, err := os.ReadFile(real)
	if err != nil {
		t.Fatal(err)
	}
	// Every rewrite is a chance to lose a concurrent Claude process's changes,
	// so an already-trusted directory must not cause one.
	if string(before) != string(after) {
		t.Error("config was rewritten even though the directory was already trusted")
	}
}

func TestTrustDirKeepsSymlink(t *testing.T) {
	home, real := claudeHome(t, map[string]any{})
	link := filepath.Join(home, ".claude.json")

	if err := TrustDir("/trees/berlin"); err != nil {
		t.Fatalf("TrustDir: %v", err)
	}

	// Renaming onto the link itself would replace the user's symlink with a
	// regular file, quietly splitting their config in two.
	info, err := os.Lstat(link)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Error("~/.claude.json must still be a symlink after seeding")
	}
	if _, ok := readProjects(t, real)["/trees/berlin"]; !ok {
		t.Error("the write should have landed on the symlink target")
	}
}

func TestTrustDirWithoutClaudeConfig(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if err := TrustDir("/trees/berlin"); err == nil {
		t.Error("expected an error when there is no claude config to seed")
	}
}

func TestTrustDirEmptyDir(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if err := TrustDir(""); err != nil {
		t.Errorf("empty dir should be a no-op, got %v", err)
	}
}
