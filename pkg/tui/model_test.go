package tui

import (
	"os"
	"strings"
	"testing"

	"github.com/panamafrancis/workbench/pkg/config"
)

// writeConfig persists a config with the given repo aliases to the test HOME.
func writeConfig(t *testing.T, aliases ...string) {
	t.Helper()
	cfg := config.DefaultConfig()
	for _, a := range aliases {
		cfg.Repos = append(cfg.Repos, config.Repo{Alias: a, LocalPath: "/tmp/" + a})
	}
	if err := cfg.Save(); err != nil {
		t.Fatalf("save config: %v", err)
	}
}

func TestReloadLocalStatePicksUpDiskChanges(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if err := os.MkdirAll(config.ConfigDir(), 0755); err != nil {
		t.Fatal(err)
	}

	writeConfig(t, "one")
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	m := New(cfg)
	if got := len(m.cfg.Repos); got != 1 {
		t.Fatalf("initial repos = %d, want 1", got)
	}

	// Another instance adds a repo on disk; a focus/tick reload should see it.
	writeConfig(t, "one", "two")
	m.reloadLocalState()
	if got := len(m.cfg.Repos); got != 2 {
		t.Errorf("after reload repos = %d, want 2", got)
	}
	if m.tree.cfg != m.cfg {
		t.Error("tree.cfg not swapped to the reloaded config")
	}
}

func TestActiveWorktreeMarker(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Repos = []config.Repo{{
		Alias:     "r",
		LocalPath: "/tmp/r",
		Worktrees: []config.Worktree{
			{Name: "tokyo", Branch: "wt/r/tokyo", Model: "claude"},
			{Name: "osaka", Branch: "wt/r/osaka", Model: "claude"},
		},
	}}

	tr := newTree(cfg, nil)
	if strings.Contains(tr.view(80), "▸") {
		t.Error("no active worktree set, but marker ▸ rendered")
	}

	tr.activeWorktree = "osaka"
	out := tr.view(80)
	if !strings.Contains(out, "▸") {
		t.Errorf("active worktree set, but marker ▸ not rendered:\n%s", out)
	}
	// The marker sits on the active row's line, not the other worktree's.
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "▸") && !strings.Contains(line, "osaka") {
			t.Errorf("marker rendered on wrong row: %q", line)
		}
	}
}

func TestReloadLocalStateNoOpWhileBusy(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if err := os.MkdirAll(config.ConfigDir(), 0755); err != nil {
		t.Fatal(err)
	}
	writeConfig(t, "one")
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	m := New(cfg)

	// Disk gains a repo, but an open input mode must not let indices shift.
	writeConfig(t, "one", "two")
	m.mode = modeNewWorktree
	m.reloadLocalState()
	if got := len(m.cfg.Repos); got != 1 {
		t.Errorf("reload during input mode changed repos to %d, want 1 (no-op)", got)
	}

	// A create in flight must not drop the optimistic in-memory entry.
	m.mode = modeNormal
	m.creating = map[string]bool{"pending": true}
	m.reloadLocalState()
	if got := len(m.cfg.Repos); got != 1 {
		t.Errorf("reload during create changed repos to %d, want 1 (no-op)", got)
	}
}
