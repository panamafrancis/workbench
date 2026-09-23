package supatree

import (
	"os"
	"strings"
	"testing"
)

func TestLaunchQueueRoundTrip(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	if reqs, err := DrainLaunches(); err != nil || len(reqs) != 0 {
		t.Fatalf("DrainLaunches on no queue = %v, %v; want none, nil", reqs, err)
	}
	if err := QueueLaunch(LaunchRequest{Tree: treeA, Session: "st-main"}); err != nil {
		t.Fatalf("QueueLaunch: %v", err)
	}
	if err := QueueLaunch(LaunchRequest{Tree: "hobart", Agent: "reviewer"}); err != nil {
		t.Fatalf("QueueLaunch: %v", err)
	}
	// A torn line costs that request only.
	f, err := os.OpenFile(LaunchPath(), os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = f.WriteString("{\"tree\":\n")
	_ = f.Close()

	reqs, err := DrainLaunches()
	if err != nil {
		t.Fatalf("DrainLaunches: %v", err)
	}
	if len(reqs) != 2 {
		t.Fatalf("got %d requests, want 2: %+v", len(reqs), reqs)
	}
	if reqs[0].Tree != treeA || reqs[0].Agent != "main" || reqs[0].Session != "st-main" {
		t.Errorf("first = %+v; want canberra/main in st-main (agent defaults to main)", reqs[0])
	}
	if reqs[1].Agent != "reviewer" {
		t.Errorf("second agent = %q, want reviewer", reqs[1].Agent)
	}
	// Each launch happens once: a second drain is empty, or every watcher tick
	// would reopen the tab.
	if again, _ := DrainLaunches(); len(again) != 0 {
		t.Errorf("second drain = %+v, want empty", again)
	}
}

func TestQueueLaunchRejectsBadInput(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if err := QueueLaunch(LaunchRequest{}); err == nil {
		t.Error("QueueLaunch with no tree succeeded")
	}
	// The agent name becomes a tab name and a mailbox path.
	if err := QueueLaunch(LaunchRequest{Tree: treeA, Agent: "../x"}); err == nil {
		t.Error("QueueLaunch with a path-like agent succeeded")
	}
}

func TestForwardPMMail(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	root := t.TempDir()
	inst := &Instance{Name: treeA, Root: root}

	if err := Deliver(root, PMAgentName, "main", "LGTM on api#12, blocking issue on web#40"); err != nil {
		t.Fatal(err)
	}
	// Mail for anyone else stays where it is.
	if err := Deliver(root, "main", "pm", "review these"); err != nil {
		t.Fatal(err)
	}

	n, err := ForwardPMMail([]*Instance{inst})
	if err != nil || n != 1 {
		t.Fatalf("ForwardPMMail = %d, %v; want 1, nil", n, err)
	}
	reqs, _, err := PendingRequests()
	if err != nil {
		t.Fatal(err)
	}
	if len(reqs) != 1 || reqs[0].Tree != treeA || reqs[0].From != "main" || !strings.Contains(reqs[0].Text, "web#40") {
		t.Fatalf("requests = %+v; want the agent's reply, attributed to its tree and sender", reqs)
	}
	if HasMail(root, PMAgentName) {
		t.Error("forwarded mail left in the tree mailbox — it would be forwarded again next round")
	}
	if !HasMail(root, "main") {
		t.Error("ForwardPMMail consumed mail addressed to another agent")
	}
	if n, _ := ForwardPMMail([]*Instance{inst}); n != 0 {
		t.Errorf("second forward moved %d, want 0", n)
	}
}

func TestStartAgentBriefsThenQueues(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("ZELLIJ_SESSION_NAME", "st-main")
	root := t.TempDir()
	inst := &Instance{Name: treeA, Root: root, Model: "claude"}

	// Nothing to do and nothing waiting is an error, not an idle launch.
	if _, err := startAgent(inst, "", ""); err == nil {
		t.Error("startAgent with no brief and no mail succeeded")
	}

	if _, err := startAgent(inst, "", "fix the flaky test"); err != nil {
		t.Fatalf("startAgent: %v", err)
	}
	agents, err := LoadAgents(root)
	if err != nil || FindAgent(agents, "main") == nil {
		t.Fatalf("main not registered (%v): a launch resumes by the session id registered here", err)
	}
	msgs, _ := Mail(root, "main")
	if len(msgs) != 1 || msgs[0].Text != "fix the flaky test" || msgs[0].From != PMAgentName {
		t.Fatalf("mailbox = %+v; want the brief, from the PM", msgs)
	}
	reqs, _ := DrainLaunches()
	if len(reqs) != 1 || reqs[0].Tree != treeA || reqs[0].Agent != "main" || reqs[0].Session != "st-main" {
		t.Fatalf("launches = %+v; want canberra/main in the PM's session", reqs)
	}
}

func TestStartAgentDefaultsReviewBrief(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	root := t.TempDir()
	inst := &Instance{Name: "hobart", Root: root, Model: "claude", Mode: ModeReviewing, Members: []Member{
		{Alias: "api", Review: &ReviewRef{Repo: "o/api", Number: 12, URL: "https://github.com/o/api/pull/12"}},
		{Alias: "docs"},
	}}
	if _, err := startAgent(inst, "main", ""); err != nil {
		t.Fatalf("startAgent: %v", err)
	}
	msgs, _ := Mail(root, "main")
	if len(msgs) != 1 {
		t.Fatalf("mailbox = %+v; want the default review brief", msgs)
	}
	for _, want := range []string{"https://github.com/o/api/pull/12", ".supatree/review.md", "review_post", `agent "pm"`} {
		if !strings.Contains(msgs[0].Text, want) {
			t.Errorf("review brief missing %q:\n%s", want, msgs[0].Text)
		}
	}
}
