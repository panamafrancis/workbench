package supatree

import (
	"strings"
	"testing"
	"time"

	"github.com/panamafrancis/workbench/pkg/config"
	"github.com/panamafrancis/workbench/pkg/sandbox"
)

func TestEnsureAgentCreatesThenResumes(t *testing.T) {
	root := t.TempDir()
	now := time.Now()

	a1, created, err := EnsureAgent(root, "canberra", "main", "claude", now)
	if err != nil {
		t.Fatalf("EnsureAgent() error = %v", err)
	}
	if !created {
		t.Error("first EnsureAgent should report created=true")
	}
	if a1.SessionID == "" {
		t.Error("agent should get a session id")
	}

	a2, created, err := EnsureAgent(root, "canberra", "main", "claude", now)
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

func TestEnsureAgentRejectsUnsafeName(t *testing.T) {
	root := t.TempDir()
	for _, bad := range []string{"x=1 && curl evil", "a:b", "a b", ""} {
		if _, _, err := EnsureAgent(root, "canberra", bad, "claude", time.Now()); err == nil {
			t.Errorf("EnsureAgent(%q) = nil error, want rejection", bad)
		}
	}
}

func TestEnsureAgentDistinctSessions(t *testing.T) {
	root := t.TempDir()
	now := time.Now()
	a, _, _ := EnsureAgent(root, "canberra", "main", "claude", now)
	b, _, _ := EnsureAgent(root, "canberra", "reviewer", "claude", now)
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

// Addresses are exact, not prefixes. Verified against claude 2.1.261 on
// 2026-09-20: a session launched with --name appears on the bus under exactly
// that string, unlike a directory-derived name, which gains a two-character
// suffix. Two supatrees each with a "main" agent must not collide.
func TestAgentAddress(t *testing.T) {
	if got := AgentAddress("canberra", "main"); got != "st-canberra-main" {
		t.Errorf("AgentAddress = %q, want st-canberra-main", got)
	}
	if AgentAddress("canberra", "main") == AgentAddress("darwin", "main") {
		t.Error("two trees with a main agent produced the same address")
	}
	// Not the zellij tab identity: a colon has no business in a bus name, and
	// the two namespaces must be free to diverge.
	if strings.Contains(AgentAddress("canberra", "reviewer"), ":") {
		t.Error("address contains a colon")
	}
}

func TestEnsureAgentRecordsAddress(t *testing.T) {
	root := t.TempDir()
	a, _, err := EnsureAgent(root, "canberra", "reviewer", "claude", time.Now())
	if err != nil {
		t.Fatalf("EnsureAgent: %v", err)
	}
	if a.Address != "st-canberra-reviewer" {
		t.Errorf("Address = %q, want st-canberra-reviewer", a.Address)
	}
	agents, err := LoadAgents(root)
	if err != nil {
		t.Fatalf("LoadAgents: %v", err)
	}
	if len(agents) != 1 || agents[0].Address != a.Address {
		t.Errorf("address did not persist: %+v", agents)
	}
}
