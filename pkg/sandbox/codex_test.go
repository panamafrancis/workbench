package sandbox

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/panamafrancis/workbench/pkg/config"
	"github.com/panamafrancis/workbench/pkg/testutil"
)

var codexModel = config.Model{
	NonoProfile:       "default",
	Binary:            "codex",
	ResumeArgs:        []string{"resume", "--last"},
	ResumeSessionArgs: []string{"resume", "{session_id}"},
	SessionStore:      config.SessionStoreCodex,
}

// isolateCodex gives the test its own CODEX_HOME and returns it.
func isolateCodex(t *testing.T) string {
	t.Helper()
	home := testutil.IsolateHome(t)
	ch := filepath.Join(home, "codex-home")
	t.Setenv("CODEX_HOME", ch)
	return ch
}

// writeRollout writes a rollout the way codex lays it out: a session_meta
// first line, then turns.
func writeRollout(t *testing.T, codexHome, id, cwd string, start time.Time) string {
	t.Helper()
	dir := filepath.Join(codexHome, "sessions", start.Format("2006"), start.Format("01"), start.Format("02"))
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "rollout-"+start.Format("2006-01-02T15-04-05")+"-"+id+".jsonl")
	ts := start.UTC().Format(time.RFC3339Nano)
	body := fmt.Sprintf(`{"timestamp":%q,"type":"session_meta","payload":{"id":%q,"timestamp":%q,"cwd":%q,"originator":"codex_cli_rs"}}`+"\n"+
		`{"timestamp":%q,"type":"response_item","payload":{}}`+"\n", ts, id, ts, cwd, ts)
	if err := os.WriteFile(path, []byte(body), 0644); err != nil {
		t.Fatal(err)
	}
	return path
}

const (
	idA = "0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b"
	idB = "0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5c"
	idC = "0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5d"
)

func TestCodexRolloutID(t *testing.T) {
	for name, want := range map[string]string{
		"rollout-2026-10-18T09-30-00-" + idA + ".jsonl":     idA,
		"rollout-2026-10-18T09-30-00-" + idA + ".jsonl.zst": "",
		"rollout-short.jsonl":                               "",
		"session_index.jsonl":                               "",
	} {
		got, _ := codexRolloutID(name)
		if got != want {
			t.Errorf("codexRolloutID(%q) = %q, want %q", name, got, want)
		}
	}
}

func TestCodexSessionExists(t *testing.T) {
	ch := isolateCodex(t)
	if SessionExistsFor(codexModel, "/tree", idA) {
		t.Fatal("exists before any rollout")
	}
	path := writeRollout(t, ch, idA, "/tree", time.Now())
	if !SessionExistsFor(codexModel, "/tree", idA) {
		t.Error("SessionExistsFor = false after the rollout was written")
	}
	if got := SessionTranscriptFor(codexModel, "/tree", idA); got != path {
		t.Errorf("SessionTranscriptFor = %q, want %q", got, path)
	}
	// A claude transcript of the same id is not a codex session, and vice versa.
	if SessionExists("/tree", idA) {
		t.Error("claude store found a codex session")
	}
}

func TestDiscoverCodexSession(t *testing.T) {
	ch := isolateCodex(t)
	tree := t.TempDir()
	launch := time.Now()

	writeRollout(t, ch, idA, tree, launch.Add(-time.Hour))          // an earlier session here
	writeRollout(t, ch, idB, "/elsewhere", launch.Add(time.Second)) // another directory
	writeRollout(t, ch, idC, tree, launch.Add(2*time.Second))       // this launch's

	// The hour-old rollout was touched by the test just now, so the mtime
	// prefilter does not exclude it: the recorded start time must.
	got, ok := DiscoverSession(codexModel, tree, launch, nil)
	if !ok || got != idC {
		t.Fatalf("DiscoverSession = %q, %v; want %q", got, ok, idC)
	}
	if _, ok := DiscoverSession(codexModel, tree, launch, map[string]bool{idC: true}); ok {
		t.Error("a claimed session was discovered again")
	}
}

func TestDiscoverSessionClaudeStoreFindsNothing(t *testing.T) {
	testutil.IsolateHome(t)
	if _, ok := DiscoverSession(config.Model{}, "/tree", time.Now(), nil); ok {
		t.Error("claude picks no session ID of its own to discover")
	}
}

func TestCodexResumeArgs(t *testing.T) {
	ch := isolateCodex(t)
	tree := t.TempDir()

	// No session yet: a member agent (no session id) starts fresh.
	if args := BuildAgentNonoArgs(tree, codexModel, "", true); contains(args, "--last") {
		t.Errorf("resumed with no codex session here: %v", args)
	}
	writeRollout(t, ch, idA, tree, time.Now())
	if args := BuildAgentNonoArgs(tree, codexModel, "", true); !contains(args, "--last") {
		t.Errorf("did not resume the codex session here: %v", args)
	}
	// A known id resumes exactly that session.
	args := BuildAgentNonoArgs(tree, codexModel, idA, true)
	if !contains(args, idA) || contains(args, "--last") {
		t.Errorf("resume by id: %v", args)
	}
}
