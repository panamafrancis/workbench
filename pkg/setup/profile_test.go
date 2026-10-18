package setup

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/panamafrancis/workbench/pkg/testutil"
)

func TestProfileJSON(t *testing.T) {
	p := Profile{
		Name:     "supatree-agent",
		Extends:  []string{"claude-code"},
		Allow:    []string{"/a"},
		ReadFile: []string{"/r"},
		Deny:     []string{"/a/x/**"},
	}
	var doc map[string]any
	if err := json.Unmarshal([]byte(p.JSON()), &doc); err != nil {
		t.Fatalf("not JSON: %v", err)
	}
	fs := doc["filesystem"].(map[string]any)
	if got := fs["allow"].([]any); len(got) != 1 || got[0] != "/a" {
		t.Errorf("allow = %v", got)
	}
	// The profile's own carve-out, then the host daemons every profile denies.
	want := append([]string{"/a/x/**"}, HostDaemonSockets()...)
	got := fs["deny"].([]any)
	if len(got) != len(want) {
		t.Fatalf("deny = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("deny[%d] = %v, want %v", i, got[i], want[i])
		}
	}
	// Empty sections are omitted, not written as null: nono rejects null lists.
	if _, ok := fs["allow_file"]; ok {
		t.Error("empty allow_file was written")
	}
	if doc["meta"].(map[string]any)["name"] != "supatree-agent" {
		t.Errorf("meta = %v", doc["meta"])
	}
}

func TestProfileWrite(t *testing.T) {
	testutil.IsolateHome(t)
	path, err := Profile{Name: "x"}.Write()
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(NonoProfilesDir(), "x.json"); path != want {
		t.Errorf("path = %s, want %s", path, want)
	}
	if _, err := os.Stat(path); err != nil {
		t.Error(err)
	}
	if _, err := (Profile{}).Write(); err == nil {
		t.Error("wrote a profile with no name")
	}
}

// Every profile keeps sandboxed agents away from Codex's app-server daemon,
// which runs unsandboxed and executes what it is sent: on macOS a socket is
// only kept out by denying its path.
func TestProfileDeniesHostDaemonSockets(t *testing.T) {
	home := testutil.IsolateHome(t)
	var doc map[string]any
	if err := json.Unmarshal([]byte(Profile{Name: "p"}.JSON()), &doc); err != nil {
		t.Fatal(err)
	}
	deny := map[any]bool{}
	for _, d := range doc["filesystem"].(map[string]any)["deny"].([]any) {
		deny[d] = true
	}
	for _, want := range []string{filepath.Join(home, ".codex", "app-server-daemon")} {
		if !deny[want] {
			t.Errorf("deny lacks %s: %v", want, deny)
		}
	}
	if len(deny) != len(HostDaemonSockets()) {
		t.Errorf("deny = %v, want exactly the host daemon sockets", deny)
	}
}

// A daemon under $CODEX_HOME is denied as well as one under ~/.codex, and
// nothing relative is ever written.
func TestHostDaemonSocketsFollowCodexHome(t *testing.T) {
	home := testutil.IsolateHome(t)
	t.Setenv("CODEX_HOME", "/elsewhere/codex")
	got := HostDaemonSockets()
	for _, want := range []string{"/elsewhere/codex/app-server-daemon", filepath.Join(home, ".codex", "app-server-daemon")} {
		if !slices.Contains(got, want) {
			t.Errorf("HostDaemonSockets() = %v, lacks %s", got, want)
		}
	}
	t.Setenv("CODEX_HOME", "relative/codex")
	for _, p := range HostDaemonSockets() {
		if !filepath.IsAbs(p) {
			t.Errorf("relative deny path %q", p)
		}
	}
}
