package sandbox

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/panamafrancis/workbench/pkg/config"
)

func testConfig() *config.Config {
	return &config.Config{
		Models: map[string]config.Model{
			"claude": {
				NonoProfile: "claude-code",
				Binary:      "claude",
				Args:        []string{},
			},
			"shell": {
				NonoProfile: "default",
				Binary:      "bash",
				Args:        []string{},
			},
			"custom": {
				NonoProfile: "custom-profile",
				Binary:      "mytool",
				Args:        []string{"--flag", "--verbose"},
			},
		},
	}
}

func TestBuildNonoArgsClaude(t *testing.T) {
	cfg := testConfig()
	got, err := BuildNonoArgs("/wt/path", "claude", cfg)
	if err != nil {
		t.Fatalf("BuildNonoArgs() error = %v", err)
	}
	want := []string{"run", "--profile", "claude-code", "--allow", "/wt/path", "--", "claude"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("BuildNonoArgs() = %v, want %v", got, want)
	}
}

func TestBuildNonoArgsWithExtraArgs(t *testing.T) {
	cfg := testConfig()
	got, err := BuildNonoArgs("/wt/path", "custom", cfg)
	if err != nil {
		t.Fatalf("BuildNonoArgs() error = %v", err)
	}
	want := []string{"run", "--profile", "custom-profile", "--allow", "/wt/path", "--", "mytool", "--flag", "--verbose"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("BuildNonoArgs() = %v, want %v", got, want)
	}
}

func TestBuildNonoArgsUnknownModel(t *testing.T) {
	cfg := testConfig()
	_, err := BuildNonoArgs("/wt/path", "nosuchmodel", cfg)
	if err == nil {
		t.Error("BuildNonoArgs(unknown model) = nil, want error")
	}
}

func TestBuildNonoArgsPathIsAllowed(t *testing.T) {
	cfg := testConfig()
	path := "/Users/stefan/.workbench/worktrees/repo/branch"
	got, err := BuildNonoArgs(path, "claude", cfg)
	if err != nil {
		t.Fatal(err)
	}
	// --allow must be immediately followed by the worktree path
	for i, arg := range got {
		if arg == "--allow" {
			if i+1 >= len(got) || got[i+1] != path {
				t.Errorf("--allow not followed by %q in args %v", path, got)
			}
			return
		}
	}
	t.Errorf("--allow not found in args %v", got)
}

func TestClearSessionCache(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	wtPath := "/some/worktree/path"

	dir := filepath.Join(home, ".claude", "projects", encodeProjectPath(wtPath))
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "session.jsonl"), []byte("{}"), 0644); err != nil {
		t.Fatal(err)
	}

	if !hasPriorSession(wtPath) {
		t.Fatal("hasPriorSession = false before clear, want true")
	}
	if err := ClearSessionCache(wtPath); err != nil {
		t.Fatalf("ClearSessionCache() error = %v", err)
	}
	if hasPriorSession(wtPath) {
		t.Error("hasPriorSession = true after clear, want false")
	}
}

func TestClearSessionCacheMissingIsNoOp(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if err := ClearSessionCache("/never/created"); err != nil {
		t.Errorf("ClearSessionCache(missing) = %v, want nil", err)
	}
}

// An empty path must not delete the whole projects root.
func TestClearSessionCacheEmptyPathIsGuarded(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	other := filepath.Join(home, ".claude", "projects", "someotherworktree")
	if err := os.MkdirAll(other, 0755); err != nil {
		t.Fatal(err)
	}

	if err := ClearSessionCache(""); err != nil {
		t.Fatalf("ClearSessionCache(\"\") error = %v", err)
	}
	if _, err := os.Stat(other); err != nil {
		t.Errorf("projects root was wiped by empty path: %v", err)
	}
}

func TestBuildNonoArgsSeparatorPresent(t *testing.T) {
	cfg := testConfig()
	got, err := BuildNonoArgs("/wt/path", "shell", cfg)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, a := range got {
		if a == "--" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("-- separator not found in args %v", got)
	}
}
