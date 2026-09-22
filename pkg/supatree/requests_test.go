package supatree

import (
	"os"
	"testing"
)

func TestRequestQueueCatchUp(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	if reqs, _, err := PendingRequests(); err != nil || len(reqs) != 0 {
		t.Fatalf("PendingRequests on a fresh install = %v, %v; want none, nil", reqs, err)
	}

	for _, text := range []string{"first", "second"} {
		if err := AppendRequest(Request{From: "cli", Tree: treeA, Text: text}); err != nil {
			t.Fatalf("AppendRequest: %v", err)
		}
	}
	reqs, offset, err := PendingRequests()
	if err != nil {
		t.Fatalf("PendingRequests: %v", err)
	}
	if len(reqs) != 2 || reqs[0].Text != "first" || reqs[1].Text != "second" {
		t.Fatalf("PendingRequests = %+v, want both in order", reqs)
	}

	// Reading without committing must return the same backlog: a PM that dies
	// mid-turn re-reads rather than loses.
	if again, _, _ := PendingRequests(); len(again) != 2 {
		t.Fatalf("uncommitted re-read returned %d, want 2", len(again))
	}

	if err := CommitRequests(offset); err != nil {
		t.Fatalf("CommitRequests: %v", err)
	}
	if after, _, _ := PendingRequests(); len(after) != 0 {
		t.Fatalf("after commit = %+v, want none", after)
	}

	// New requests after the commit are picked up from the stored offset — this
	// is the "PM has been down for a day" case.
	if err := AppendRequest(Request{From: "watcher", Text: "third"}); err != nil {
		t.Fatalf("AppendRequest: %v", err)
	}
	reqs, offset, _ = PendingRequests()
	if len(reqs) != 1 || reqs[0].Text != "third" {
		t.Fatalf("PendingRequests = %+v, want only the new one", reqs)
	}
	if err := CommitRequests(offset); err != nil {
		t.Fatalf("CommitRequests: %v", err)
	}
}

// A truncated or rotated queue must be re-read from the start rather than
// seeked past the end — losing the backlog is the failure the offset exists to
// avoid, so the safe direction is to re-deliver.
func TestRequestQueueSurvivesTruncation(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	for _, text := range []string{"a", "b", "c"} {
		if err := AppendRequest(Request{From: "cli", Text: text}); err != nil {
			t.Fatalf("AppendRequest: %v", err)
		}
	}
	_, offset, _ := PendingRequests()
	if err := CommitRequests(offset); err != nil {
		t.Fatalf("CommitRequests: %v", err)
	}

	if err := os.WriteFile(RequestsPath(), []byte(""), 0644); err != nil {
		t.Fatalf("truncate: %v", err)
	}
	if err := AppendRequest(Request{From: "cli", Text: "after rotation"}); err != nil {
		t.Fatalf("AppendRequest: %v", err)
	}
	reqs, _, err := PendingRequests()
	if err != nil {
		t.Fatalf("PendingRequests: %v", err)
	}
	if len(reqs) != 1 || reqs[0].Text != "after rotation" {
		t.Fatalf("PendingRequests = %+v, want the post-rotation entry", reqs)
	}
}

func TestAppendRequestRejectsEmpty(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if err := AppendRequest(Request{From: "cli", Text: "  "}); err == nil {
		t.Error("AppendRequest(blank) = nil, want an error")
	}
}
