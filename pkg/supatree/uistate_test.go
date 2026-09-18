package supatree

import (
	"os"
	"path/filepath"
	"testing"
)

// Defaults matter more than the stored values here: a supatree nobody has
// folded is open, and its repositories section is shut.
func TestUIStateDefaults(t *testing.T) {
	u := NewUIState()
	if u.TreeCollapsed("oslo") {
		t.Error("an untouched supatree should be expanded")
	}
	if !u.ReposCollapsed("oslo") {
		t.Error("an untouched repositories section should be collapsed")
	}
}

func TestUIStateRoundTrips(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	if _, err := UpdateUIState(func(u *UIState) {
		u.SetTreeCollapsed("oslo", true)
		u.SetReposCollapsed("bergen", false)
	}); err != nil {
		t.Fatalf("UpdateUIState: %v", err)
	}

	got := LoadUIState()
	if !got.TreeCollapsed("oslo") {
		t.Error("collapsed supatree did not survive the round trip")
	}
	if got.ReposCollapsed("bergen") {
		t.Error("expanded repositories section did not survive the round trip")
	}
	if got.TreeCollapsed("bergen") || !got.ReposCollapsed("oslo") {
		t.Error("an unrelated supatree lost its defaults")
	}
}

// Two sidebars folding different supatrees must not overwrite each other: each
// change is applied to what is on disk, not to the snapshot the process holds.
func TestUpdateUIStateMergesConcurrentEdits(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	first := LoadUIState() // the snapshot an already-running sidebar holds
	if _, err := UpdateUIState(func(u *UIState) { u.SetTreeCollapsed("oslo", true) }); err != nil {
		t.Fatalf("UpdateUIState: %v", err)
	}
	// The stale holder now folds a different supatree.
	first.SetTreeCollapsed("bergen", true)
	if _, err := UpdateUIState(func(u *UIState) { u.SetTreeCollapsed("bergen", true) }); err != nil {
		t.Fatalf("UpdateUIState: %v", err)
	}

	got := LoadUIState()
	if !got.TreeCollapsed("oslo") || !got.TreeCollapsed("bergen") {
		t.Errorf("a concurrent fold was lost: %+v", got.CollapsedTrees)
	}
}

// Entries back at their default are dropped, so the file does not accumulate a
// row per supatree that was ever folded and unfolded again.
func TestUIStateSavePrunesDefaults(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	if _, err := UpdateUIState(func(u *UIState) { u.SetTreeCollapsed("oslo", true) }); err != nil {
		t.Fatalf("UpdateUIState: %v", err)
	}
	out, err := UpdateUIState(func(u *UIState) { u.SetTreeCollapsed("oslo", false) })
	if err != nil {
		t.Fatalf("UpdateUIState: %v", err)
	}
	if out.TreeCollapsed("oslo") {
		t.Fatal("unfold did not take effect")
	}
	if len(LoadUIState().CollapsedTrees) != 0 {
		t.Errorf("default entry was kept on disk: %+v", LoadUIState().CollapsedTrees)
	}
}

// A missing or unparseable file falls back to the defaults: fold state is a
// convenience and is never worth failing a render over.
func TestLoadUIStateToleratesBadFile(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if u := LoadUIState(); u.TreeCollapsed("oslo") {
		t.Error("absent file should yield defaults")
	}

	if err := os.MkdirAll(filepath.Dir(UIStatePath()), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(UIStatePath(), []byte("{{ not yaml"), 0644); err != nil {
		t.Fatal(err)
	}
	u := LoadUIState()
	if u.TreeCollapsed("oslo") || !u.ReposCollapsed("oslo") {
		t.Error("unparseable file should yield defaults")
	}
	// And it is repaired by the next write rather than staying broken.
	if _, err := UpdateUIState(func(u *UIState) { u.SetTreeCollapsed("oslo", true) }); err != nil {
		t.Fatalf("UpdateUIState over a bad file: %v", err)
	}
	if !LoadUIState().TreeCollapsed("oslo") {
		t.Error("write over a bad file did not take")
	}
}
