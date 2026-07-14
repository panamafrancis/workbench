package sandbox

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/panamafrancis/workbench/pkg/config"
)

func writeTranscript(t *testing.T, wtPath, session string) {
	t.Helper()
	home, _ := os.UserHomeDir()
	dir := filepath.Join(home, ".claude", "projects", encodeProjectPath(wtPath))
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, session+".jsonl"), []byte("{}"), 0644); err != nil {
		t.Fatal(err)
	}
}

// noSessionCfg models a CLI with no session-id flags, only a directory-scoped
// --continue.
func noSessionCfg() *config.Config {
	return &config.Config{Models: map[string]config.Model{
		"m": {NonoProfile: "default", Binary: "claude", ResumeArgs: []string{"--continue"}},
	}}
}

func TestBuildAgentNonoArgsNewAgentNeverContinues(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	wt := "/wt/path"
	writeTranscript(t, wt, "other") // some other session already ran here

	// A brand-new agent (resume=false) must NOT inherit the directory's last
	// session — this was the "new agent lands in main's chat" bug.
	args, err := BuildAgentNonoArgs(wt, "m", noSessionCfg(), "sid", false)
	if err != nil {
		t.Fatal(err)
	}
	if contains(args, "--continue") {
		t.Errorf("new agent must not resume via --continue: %v", args)
	}
}

func TestBuildAgentNonoArgsResumeUsesContinueFallback(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	wt := "/wt/path"
	writeTranscript(t, wt, "other")

	args, err := BuildAgentNonoArgs(wt, "m", noSessionCfg(), "sid", true)
	if err != nil {
		t.Fatal(err)
	}
	if !contains(args, "--continue") {
		t.Errorf("resume with no session args should fall back to --continue: %v", args)
	}
}

func TestSessionExists(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	wt := "/wt/x"
	if SessionExists(wt, "abc") {
		t.Error("SessionExists = true before any transcript written")
	}
	writeTranscript(t, wt, "abc")
	if !SessionExists(wt, "abc") {
		t.Error("SessionExists = false after transcript written")
	}
	if SessionExists(wt, "") {
		t.Error("SessionExists(empty) should be false")
	}
}

func contains(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}
