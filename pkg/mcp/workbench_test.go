package mcp

import (
	"strings"
	"testing"
)

// The workbench PR/rename tools must stay inert unless we're actually inside a
// workbench worktree, and must redirect (not dead-end) inside a supatree.
func TestWorkbenchGate(t *testing.T) {
	srv := WorkbenchServer("test")

	t.Run("docs always allowed", func(t *testing.T) {
		t.Setenv("WORKBENCH", "")
		t.Setenv("SUPATREE", "")
		if msg := srv.Gate("docs"); msg != "" {
			t.Errorf("docs gated: %q", msg)
		}
	})

	t.Run("supatree redirects to supatree tools", func(t *testing.T) {
		t.Setenv("WORKBENCH", "")
		t.Setenv("SUPATREE", "1")
		msg := srv.Gate("create_pr")
		if msg == "" {
			t.Fatal("create_pr allowed inside a supatree, want redirect")
		}
		if !strings.Contains(msg, "supatree") || !strings.Contains(msg, "create_prs") {
			t.Errorf("redirect message unhelpful: %q", msg)
		}
	})

	t.Run("no session blocks with WORKBENCH hint", func(t *testing.T) {
		t.Setenv("WORKBENCH", "")
		t.Setenv("SUPATREE", "")
		msg := srv.Gate("create_pr")
		if !strings.Contains(msg, "WORKBENCH") {
			t.Errorf("want WORKBENCH hint, got %q", msg)
		}
	})

	t.Run("workbench session allows", func(t *testing.T) {
		t.Setenv("WORKBENCH", "1")
		t.Setenv("SUPATREE", "")
		if msg := srv.Gate("create_pr"); msg != "" {
			t.Errorf("create_pr gated inside workbench: %q", msg)
		}
	})
}
