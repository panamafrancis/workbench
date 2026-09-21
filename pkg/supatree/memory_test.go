package supatree

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestHistoryFromLedger(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	base := diffNow
	evs := []Event{
		{At: base, Kind: EventPROpened, Tree: treeA, Member: "api", PR: 1, Text: "x"},
		{At: base.Add(time.Hour), Kind: EventMerged, Tree: treeA, Member: "api", PR: 1, Text: "x"},
		{At: base.Add(2 * time.Hour), Kind: EventMerged, Tree: treeA, Member: "web", PR: 2, Text: "x"},
		{At: base.Add(3 * time.Hour), Kind: EventClosed, Tree: treeB, Member: "api", PR: 3, Text: "x"},
	}
	if err := AppendEvents(evs); err != nil {
		t.Fatalf("AppendEvents: %v", err)
	}

	got, err := History(time.Time{}, "")
	if err != nil {
		t.Fatalf("History: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("History returned %d trees, want 2", len(got))
	}
	// Most recent first.
	if got[0].Tree != treeB {
		t.Errorf("first entry = %q, want the most recently active (%q)", got[0].Tree, treeB)
	}
	var a HistoryEntry
	for _, e := range got {
		if e.Tree == treeA {
			a = e
		}
	}
	if a.Merged != 2 || a.Closed != 0 {
		t.Errorf("%s: %d merged / %d closed, want 2/0", treeA, a.Merged, a.Closed)
	}
	if strings.Join(a.Repos, ",") != "api,web" {
		t.Errorf("repos = %v, want api,web sorted", a.Repos)
	}

	// Filtering by repo keeps only the work that touched it.
	got, err = History(time.Time{}, "web")
	if err != nil {
		t.Fatalf("History(repo): %v", err)
	}
	if len(got) != 1 || got[0].Tree != treeA {
		t.Fatalf("History(web) = %+v, want only %s", got, treeA)
	}
}

func TestRecallAndRemember(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	stack := t.TempDir()
	if err := ScaffoldNotes(stack); err != nil {
		t.Fatalf("ScaffoldNotes: %v", err)
	}

	// A miss is still an answer, and must still be logged — "nobody has read
	// this" is what the kill criterion measures.
	out, err := Recall(stack, "backfill")
	if err != nil {
		t.Fatalf("Recall: %v", err)
	}
	if !strings.Contains(out, "nothing in notes matches") {
		t.Errorf("Recall on empty notes = %q", out)
	}

	note := filepath.Join(NotesDir(stack), "clicktracking.md")
	if err := os.WriteFile(note, []byte("# Clicktracking\n\nThe migration needed a backfill nobody expected.\n"), 0644); err != nil {
		t.Fatalf("write note: %v", err)
	}
	out, err = Recall(stack, "BACKFILL")
	if err != nil {
		t.Fatalf("Recall: %v", err)
	}
	if !strings.Contains(out, "backfill nobody expected") {
		t.Errorf("Recall = %q, want the matching line (case-insensitively)", out)
	}

	if _, err := os.Stat(RecallLogPath()); err != nil {
		t.Errorf("recall log not written: %v — instrumentation is what makes the kill criterion measurable", err)
	}
	if _, err := Recall(stack, "  "); err == nil {
		t.Error("Recall(blank) = nil error, want a rejection")
	}
}

// The injection budget is the part that decides whether this works: what loads
// into every session must be capped, with the rest behind recall.
func TestMemorySummaryIsCapped(t *testing.T) {
	stack := t.TempDir()
	if err := ScaffoldNotes(stack); err != nil {
		t.Fatalf("ScaffoldNotes: %v", err)
	}
	for _, n := range []string{"a", "b", "c", "d", "e", "f", "g"} {
		if err := os.WriteFile(filepath.Join(NotesDir(stack), n+".md"), []byte("x"), 0644); err != nil {
			t.Fatalf("write: %v", err)
		}
	}
	got := MemorySummary(stack, 3)
	if n := strings.Count(got, "\n"); n != 3 {
		t.Errorf("MemorySummary listed %d notes, want 3", n)
	}
	// The scaffolded README is documentation, not a memory.
	if strings.Contains(got, "README") {
		t.Error("MemorySummary listed README.md")
	}
	if MemorySummary(t.TempDir(), 3) != "" {
		t.Error("MemorySummary on a stack with no notes should be empty, not a heading with nothing under it")
	}
}
