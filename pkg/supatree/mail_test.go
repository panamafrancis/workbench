package supatree

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMailDeliverAndDrain(t *testing.T) {
	root := t.TempDir()

	if msgs, err := Mail(root, "main"); err != nil || len(msgs) != 0 {
		t.Fatalf("Mail on an empty mailbox = %v, %v; want none, nil", msgs, err)
	}
	if n := MailboxCount(root, "main"); n != 0 {
		t.Errorf("MailboxCount = %d, want 0", n)
	}

	if err := Deliver(root, "main", "pm", "look at #412"); err != nil {
		t.Fatalf("Deliver: %v", err)
	}
	if err := Deliver(root, "main", "reviewer", "and rebase first"); err != nil {
		t.Fatalf("Deliver: %v", err)
	}
	if n := MailboxCount(root, "main"); n != 2 {
		t.Fatalf("MailboxCount = %d, want 2", n)
	}

	// Peeking leaves the mailbox alone; draining empties it.
	if msgs, err := Mail(root, "main"); err != nil || len(msgs) != 2 {
		t.Fatalf("Mail = %v, %v; want 2 messages", msgs, err)
	}
	msgs, err := Drain(root, "main")
	if err != nil {
		t.Fatalf("Drain: %v", err)
	}
	if len(msgs) != 2 {
		t.Fatalf("Drain returned %d messages, want 2", len(msgs))
	}
	if msgs[0].From != "pm" || msgs[1].From != "reviewer" {
		t.Errorf("order = %q, %q; want oldest first (pm, reviewer)", msgs[0].From, msgs[1].From)
	}
	if msgs[0].Text != "look at #412" {
		t.Errorf("Text = %q", msgs[0].Text)
	}

	// Each message is delivered once: a second drain is empty, which is what
	// stops an agent re-acting on the same instruction every turn.
	if again, err := Drain(root, "main"); err != nil || len(again) != 0 {
		t.Fatalf("second Drain = %v, %v; want empty", again, err)
	}
}

// Mailboxes are per agent: one agent draining must not take another's mail.
func TestMailIsPerAgent(t *testing.T) {
	root := t.TempDir()
	if err := Deliver(root, "main", "pm", "for main"); err != nil {
		t.Fatalf("Deliver: %v", err)
	}
	if err := Deliver(root, "reviewer", "pm", "for reviewer"); err != nil {
		t.Fatalf("Deliver: %v", err)
	}
	if _, err := Drain(root, "main"); err != nil {
		t.Fatalf("Drain: %v", err)
	}
	msgs, err := Mail(root, "reviewer")
	if err != nil || len(msgs) != 1 || msgs[0].Text != "for reviewer" {
		t.Fatalf("reviewer's mailbox = %v, %v; want its own single message", msgs, err)
	}
}

func TestDeliverRejectsEmpty(t *testing.T) {
	root := t.TempDir()
	if err := Deliver(root, "main", "pm", "   "); err == nil {
		t.Error("Deliver(blank) = nil, want an error")
	}
}

// A torn message must not wedge the inbox: a draining read drops it and takes
// everything else, so one bad file cannot block every later instruction.
func TestDrainSkipsTornMessage(t *testing.T) {
	root := t.TempDir()
	if err := Deliver(root, "main", "pm", "good"); err != nil {
		t.Fatalf("Deliver: %v", err)
	}
	if err := os.WriteFile(filepath.Join(MailDir(root, "main"), "1.json"), []byte("{not json"), 0644); err != nil {
		t.Fatalf("write torn: %v", err)
	}
	msgs, err := Drain(root, "main")
	if err != nil {
		t.Fatalf("Drain: %v", err)
	}
	if len(msgs) != 1 || msgs[0].Text != "good" {
		t.Fatalf("Drain = %v, want the one readable message", msgs)
	}
	if n := MailboxCount(root, "main"); n != 0 {
		t.Errorf("MailboxCount = %d after drain, want 0 (the torn file too)", n)
	}
}
