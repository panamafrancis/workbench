package sandbox

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/panamafrancis/workbench/pkg/config"
)

func testConfig() *config.Config {
	return &config.Config{
		Models: map[string]config.Model{
			modelClaude: {
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

// modelClaude is the model key these tests exercise; goconst objects to the
// literal appearing in every table.
const modelClaude = "claude"

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

	if !HasPriorSession(wtPath) {
		t.Fatal("HasPriorSession = false before clear, want true")
	}
	if err := ClearSessionCache(wtPath); err != nil {
		t.Fatalf("ClearSessionCache() error = %v", err)
	}
	if HasPriorSession(wtPath) {
		t.Error("HasPriorSession = true after clear, want false")
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

// An agent gets a bus name only when its model defines one. The token is
// substituted anywhere in the argument, so "--name=x" works as well as a
// separate argument.
func TestBuildNamedAgentNonoArgs(t *testing.T) {
	cfg := &config.Config{Models: map[string]config.Model{
		modelClaude: {
			NonoProfile:    "claude-code",
			Binary:         "claude",
			NewSessionArgs: []string{"--session-id", "{session_id}"},
			AgentNameArgs:  []string{"--name", "{agent_name}"},
		},
		"inline": {
			Binary:        "other",
			AgentNameArgs: []string{"--name={agent_name}"},
		},
		"nobus": {Binary: "codex"},
	}}
	wt := t.TempDir()

	args, err := BuildNamedAgentNonoArgs(wt, modelClaude, cfg, "sid-1", "st-canberra-main", false)
	if err != nil {
		t.Fatalf("BuildNamedAgentNonoArgs: %v", err)
	}
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "--name st-canberra-main") {
		t.Errorf("args = %v, want the bus name", args)
	}
	if !strings.Contains(joined, "--session-id sid-1") {
		t.Errorf("args = %v, want the session id still substituted", args)
	}

	args, _ = BuildNamedAgentNonoArgs(wt, "inline", cfg, "", "st-canberra-main", false)
	if !strings.Contains(strings.Join(args, " "), "--name=st-canberra-main") {
		t.Errorf("args = %v, want the token substituted mid-argument", args)
	}

	// A model with no bus must not have a name flag invented for it.
	args, _ = BuildNamedAgentNonoArgs(wt, "nobus", cfg, "", "st-canberra-main", false)
	if strings.Contains(strings.Join(args, " "), "st-canberra-main") {
		t.Errorf("args = %v, want no name for a model without AgentNameArgs", args)
	}

	// And an unnamed launch stays exactly as it was before agents had names.
	args, _ = BuildNamedAgentNonoArgs(wt, modelClaude, cfg, "sid-1", "", false)
	if strings.Contains(strings.Join(args, " "), "--name") {
		t.Errorf("args = %v, want no name flag when no name is given", args)
	}
}

func TestAppendPrompt(t *testing.T) {
	cfg := &config.Config{Models: map[string]config.Model{
		modelClaude: {Binary: modelClaude, PromptArgs: []string{"{prompt}"}},
		"flagged":   {Binary: "other", PromptArgs: []string{"--prompt={prompt}"}},
		"noprompt":  {Binary: "codex"},
	}}
	base := []string{"run", "--", "claude"}

	got := AppendPrompt(append([]string(nil), base...), modelClaude, cfg, "read your inbox")
	if want := append(append([]string(nil), base...), "read your inbox"); !reflect.DeepEqual(got, want) {
		t.Errorf("AppendPrompt = %v, want %v", got, want)
	}
	got = AppendPrompt(append([]string(nil), base...), "flagged", cfg, "go")
	if got[len(got)-1] != "--prompt=go" {
		t.Errorf("AppendPrompt = %v, want the token substituted mid-argument", got)
	}
	// No PromptArgs, or no prompt, must leave the launch exactly as it was:
	// the agent starts idle, which is what every launch did before this.
	for _, tc := range []struct{ model, prompt string }{{"noprompt", "go"}, {modelClaude, ""}, {"unknown", "go"}} {
		if got := AppendPrompt(append([]string(nil), base...), tc.model, cfg, tc.prompt); !reflect.DeepEqual(got, base) {
			t.Errorf("AppendPrompt(%q, %q) = %v, want unchanged", tc.model, tc.prompt, got)
		}
	}
}
