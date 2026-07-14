package supatree

import (
	"testing"
	"time"

	"github.com/panamafrancis/workbench/pkg/config"
	"github.com/panamafrancis/workbench/pkg/sandbox"
)

func TestEnsureAgentCreatesThenResumes(t *testing.T) {
	root := t.TempDir()
	now := time.Now()

	a1, created, err := EnsureAgent(root, "main", "claude", now)
	if err != nil {
		t.Fatalf("EnsureAgent() error = %v", err)
	}
	if !created {
		t.Error("first EnsureAgent should report created=true")
	}
	if a1.SessionID == "" {
		t.Error("agent should get a session id")
	}

	a2, created, err := EnsureAgent(root, "main", "claude", now)
	if err != nil {
		t.Fatal(err)
	}
	if created {
		t.Error("second EnsureAgent should report created=false")
	}
	if a2.SessionID != a1.SessionID {
		t.Errorf("session id changed on resume: %q -> %q", a1.SessionID, a2.SessionID)
	}
}

func TestEnsureAgentDistinctSessions(t *testing.T) {
	root := t.TempDir()
	now := time.Now()
	a, _, _ := EnsureAgent(root, "main", "claude", now)
	b, _, _ := EnsureAgent(root, "reviewer", "claude", now)
	if a.SessionID == b.SessionID {
		t.Error("distinct agents must get distinct session ids")
	}
	agents, _ := LoadAgents(root)
	if len(agents) != 2 {
		t.Errorf("agents persisted = %d, want 2", len(agents))
	}
}

func TestSessionArgSubstitution(t *testing.T) {
	cfg := config.DefaultConfig()
	sid := "abc-123"
	// Fresh session uses NewSessionArgs with {session_id} substituted.
	args, err := sandbox.BuildAgentNonoArgs("/root", "claude", cfg, sid, false)
	if err != nil {
		t.Fatal(err)
	}
	if !containsSeq(args, "--session-id", sid) {
		t.Errorf("new-session args missing --session-id %s: %v", sid, args)
	}
	// Resume uses ResumeSessionArgs.
	rargs, _ := sandbox.BuildAgentNonoArgs("/root", "claude", cfg, sid, true)
	if !containsSeq(rargs, "--resume", sid) {
		t.Errorf("resume args missing --resume %s: %v", sid, rargs)
	}
}

func containsSeq(args []string, a, b string) bool {
	for i := 0; i+1 < len(args); i++ {
		if args[i] == a && args[i+1] == b {
			return true
		}
	}
	return false
}
