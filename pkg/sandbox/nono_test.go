package sandbox

import (
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
