package supatree

import (
	"bytes"
	"os"
	"testing"
	"time"

	"github.com/panamafrancis/workbench/pkg/github"
)

var diffNow = time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC)

// treeA and treeB are the two supatree names these tests use throughout;
// goconst objects to the literals appearing in every table.
const (
	treeA = "canberra"
	treeB = "darwin"
)

// summaryOf builds a one-tree, one-member summary. Every Diff case below is a
// transition of that single member or of the tree around it.
func summaryOf(tree TreeStatus) Summary {
	return Summary{Trees: []TreeStatus{tree}, At: diffNow}
}

func member(alias string, state MemberState, pr *github.PRInfo) MemberStatus {
	return MemberStatus{Alias: alias, State: state, PR: pr}
}

func tree(name string, state TreeState, members ...MemberStatus) TreeStatus {
	return TreeStatus{Name: name, State: state, Members: members}
}

func kinds(evs []Event) []EventKind {
	out := make([]EventKind, len(evs))
	for i, e := range evs {
		out[i] = e.Kind
	}
	return out
}

func wantKinds(t *testing.T, got []Event, want ...EventKind) {
	t.Helper()
	k := kinds(got)
	if len(k) != len(want) {
		t.Fatalf("kinds = %v, want %v", k, want)
	}
	for i := range want {
		if k[i] != want[i] {
			t.Fatalf("kinds = %v, want %v", k, want)
		}
	}
}

func TestDiffMemberTransitions(t *testing.T) {
	open := &github.PRInfo{Number: 7, Status: github.PROpen}
	tests := []struct {
		name string
		prev MemberStatus
		cur  MemberStatus
		want []EventKind
	}{
		{"nothing changed", member("api", MemberOpen, open), member("api", MemberOpen, open), nil},
		{"pushed", member("api", MemberWIP, nil), member("api", MemberPushed, nil), []EventKind{EventPushed}},
		{"pr opened", member("api", MemberPushed, nil), member("api", MemberOpen, open), []EventKind{EventPROpened}},
		{"changes requested", member("api", MemberOpen, open), member("api", MemberChanges, open), []EventKind{EventChangesRequested}},
		{"approved", member("api", MemberOpen, open), member("api", MemberApproved, open), []EventKind{EventApproved}},
		{"merged", member("api", MemberApproved, open), member("api", MemberMerged, open), []EventKind{EventMerged}},
		{"closed", member("api", MemberOpen, open), member("api", MemberClosed, open), []EventKind{EventClosed}},
		{"work started", member("api", MemberIdle, nil), member("api", MemberWIP, nil), nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := Diff(summaryOf(tree(treeA, TreeWIP, tc.prev)), summaryOf(tree(treeA, TreeWIP, tc.cur)))
			wantKinds(t, got, tc.want...)
		})
	}
}

// A draft going ready, or a re-review clearing "changes requested", must not
// read as a brand new pull request.
func TestDiffPROpenedOnlyWhenPRIsNew(t *testing.T) {
	pr := &github.PRInfo{Number: 7, Status: github.PROpen}
	draft := &github.PRInfo{Number: 7, Status: github.PRDraft}

	got := Diff(summaryOf(tree(treeA, TreeReview, member("api", MemberDraft, draft))),
		summaryOf(tree(treeA, TreeReview, member("api", MemberOpen, pr))))
	wantKinds(t, got)

	got = Diff(summaryOf(tree(treeA, TreeReview, member("api", MemberChanges, pr))),
		summaryOf(tree(treeA, TreeReview, member("api", MemberOpen, pr))))
	wantKinds(t, got)
}

func TestDiffChecks(t *testing.T) {
	pr := func(c github.CheckState) *github.PRInfo {
		return &github.PRInfo{Number: 7, Status: github.PROpen, Checks: c}
	}
	tests := []struct {
		name       string
		prev, cur  github.CheckState
		want       []EventKind
		wantDetail string
	}{
		{name: "pending to failing", prev: github.CheckPending, cur: github.CheckFailing, want: []EventKind{EventChecksFailed}},
		{name: "failing to passing", prev: github.CheckFailing, cur: github.CheckPassing, want: []EventKind{EventChecksPassed}},
		{name: "pending to passing is not news", prev: github.CheckPending, cur: github.CheckPassing},
		{name: "passing to pending is not a verdict", prev: github.CheckPassing, cur: github.CheckPending},
		{name: "unchanged", prev: github.CheckFailing, cur: github.CheckFailing},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := Diff(summaryOf(tree(treeA, TreeReview, member("api", MemberOpen, pr(tc.prev)))),
				summaryOf(tree(treeA, TreeReview, member("api", MemberOpen, pr(tc.cur)))))
			wantKinds(t, got, tc.want...)
		})
	}
}

func TestDiffTreeLevel(t *testing.T) {
	prev := tree(treeA, TreeReview, member("api", MemberOpen, nil))
	cur := tree(treeA, TreeDone, member("api", MemberMerged, nil))
	got := Diff(summaryOf(prev), summaryOf(cur))
	wantKinds(t, got, EventMerged, EventTreeDone)

	prev = tree(treeA, TreeWIP)
	cur = tree(treeA, TreeWIP)
	cur.Stale = true
	wantKinds(t, Diff(summaryOf(prev), summaryOf(cur)), EventStale)

	// Already stale last round is not news again.
	prev.Stale = true
	wantKinds(t, Diff(summaryOf(prev), summaryOf(cur)))
}

// The rule that keeps a first run quiet: an entity with no observed previous
// state is not diffed at all. This covers both the empty last-status.json and a
// supatree created between two rounds — without it, `supatree new` would
// announce its own initial state as if it were news.
func TestDiffIgnoresUnobservedEntities(t *testing.T) {
	cur := summaryOf(tree(treeA, TreeReview,
		member("api", MemberChanges, &github.PRInfo{Number: 7, Status: github.PROpen, Checks: github.CheckFailing})))

	if got := Diff(Summary{}, cur); len(got) != 0 {
		t.Fatalf("first run emitted %d events, want 0: %v", len(got), kinds(got))
	}

	// A member added to a known tree is equally unobserved.
	prev := summaryOf(tree(treeA, TreeReview))
	if got := Diff(prev, cur); len(got) != 0 {
		t.Fatalf("new member emitted %d events, want 0: %v", len(got), kinds(got))
	}
}

func TestDiffCarriesContext(t *testing.T) {
	pr := &github.PRInfo{Number: 412, Status: github.PROpen}
	got := Diff(summaryOf(tree(treeA, TreeReview, member("keystone-api", MemberOpen, pr))),
		summaryOf(tree(treeA, TreeReview, member("keystone-api", MemberChanges, pr))))
	if len(got) != 1 {
		t.Fatalf("got %d events, want 1", len(got))
	}
	e := got[0]
	if e.Tree != treeA || e.Member != "keystone-api" || e.PR != 412 {
		t.Errorf("context = %q/%q #%d, want canberra/keystone-api #412", e.Tree, e.Member, e.PR)
	}
	if !e.At.Equal(diffNow) {
		t.Errorf("At = %v, want %v (taken from cur.At, not a clock)", e.At, diffNow)
	}
	if e.Text == "" {
		t.Error("Text is empty — it is what the notification says")
	}
}

// The ledger is append-only history rather than config, so a torn or
// hand-edited line must cost that line and not the rest of the file.
func TestLedgerRoundTrip(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	if got, err := ReadEvents(time.Time{}); err != nil || got != nil {
		t.Fatalf("ReadEvents on a fresh install = %v, %v; want nil, nil", got, err)
	}

	old := ev(EventMerged, treeA, "api", diffNow.Add(-time.Hour))
	recent := ev(EventChangesRequested, treeB, "web", diffNow)
	if err := AppendEvents([]Event{old, recent}); err != nil {
		t.Fatalf("AppendEvents: %v", err)
	}
	if err := AppendEvents(nil); err != nil {
		t.Fatalf("AppendEvents(nil): %v", err)
	}

	got, err := ReadEvents(time.Time{})
	if err != nil {
		t.Fatalf("ReadEvents: %v", err)
	}
	wantKinds(t, got, EventMerged, EventChangesRequested)

	got, err = ReadEvents(diffNow.Add(-time.Minute))
	if err != nil {
		t.Fatalf("ReadEvents(since): %v", err)
	}
	wantKinds(t, got, EventChangesRequested)

	f, err := os.OpenFile(EventsPath(), os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		t.Fatalf("open ledger: %v", err)
	}
	if _, err := f.WriteString("{not json\n"); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if err := AppendEvents([]Event{ev(EventTreeDone, treeA, "", diffNow)}); err != nil {
		t.Fatalf("AppendEvents after torn line: %v", err)
	}
	got, err = ReadEvents(time.Time{})
	if err != nil {
		t.Fatalf("ReadEvents after torn line: %v", err)
	}
	wantKinds(t, got, EventMerged, EventChangesRequested, EventTreeDone)
}

// Diff walks every supatree, and the events it returns are grouped per tree in
// the order the summary lists them — the ledger is read back as a timeline, so
// a stable order matters.
func TestDiffAcrossTrees(t *testing.T) {
	pr := &github.PRInfo{Number: 7, Status: github.PROpen}
	prev := Summary{At: diffNow, Trees: []TreeStatus{
		tree(treeA, TreeReview, member("api", MemberOpen, pr)),
		tree(treeB, TreeReview, member("web", MemberOpen, pr)),
	}}
	cur := Summary{At: diffNow, Trees: []TreeStatus{
		tree(treeA, TreeReview, member("api", MemberChanges, pr)),
		tree(treeB, TreeReview, member("web", MemberApproved, pr)),
	}}
	got := Diff(prev, cur)
	wantKinds(t, got, EventChangesRequested, EventApproved)
	if got[0].Tree != treeA || got[1].Tree != treeB {
		t.Errorf("trees = %q, %q; want canberra, darwin", got[0].Tree, got[1].Tree)
	}
}

// The ledger is append-only and every reader scans it, so it must not grow
// without bound — and rotating must not lose the history History() reports.
func TestLedgerRotation(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	early := ev(EventMerged, treeA, "api", diffNow.Add(-time.Hour))
	if err := AppendEvents([]Event{early}); err != nil {
		t.Fatalf("AppendEvents: %v", err)
	}
	// Push the current generation over the cap, then append again: the oversized
	// file rotates aside and a fresh one takes over.
	// Grow it the way real use does — many lines — rather than one huge blob.
	filler := append(bytes.Repeat([]byte("x"), 1023), '\n')
	big := bytes.Repeat(filler, (eventsMaxBytes/len(filler))+1)
	if err := os.WriteFile(EventsPath(), big, 0644); err != nil {
		t.Fatalf("grow ledger: %v", err)
	}
	if err := AppendEvents([]Event{ev(EventApproved, treeA, "api", diffNow)}); err != nil {
		t.Fatalf("AppendEvents: %v", err)
	}
	if _, err := os.Stat(eventsPrevPath()); err != nil {
		t.Fatalf("ledger did not rotate: %v", err)
	}
	info, err := os.Stat(EventsPath())
	if err != nil {
		t.Fatalf("stat current ledger: %v", err)
	}
	if info.Size() >= eventsMaxBytes {
		t.Errorf("current ledger is %d bytes, want a fresh one", info.Size())
	}

	// A later append lands in the new generation, and reads span both.
	if err := AppendEvents([]Event{ev(EventTreeDone, treeB, "", diffNow.Add(time.Hour))}); err != nil {
		t.Fatalf("AppendEvents: %v", err)
	}
	got, err := ReadEvents(time.Time{})
	if err != nil {
		t.Fatalf("ReadEvents: %v", err)
	}
	if len(got) < 2 {
		t.Fatalf("ReadEvents returned %d events, want both generations", len(got))
	}
	if !got[0].At.Before(got[len(got)-1].At) {
		t.Error("ReadEvents did not return the older generation first")
	}
}

// Four separate readers depend on ReadEvents — the sidebar, the dashboard feed,
// the schedule gate and History. A damaged ledger must cost the entries after
// the damage, never all four callers at once.
func TestReadEventsSurvivesAnOverlongLine(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	good := ev(EventMerged, treeA, "api", diffNow)
	if err := AppendEvents([]Event{good}); err != nil {
		t.Fatalf("AppendEvents: %v", err)
	}
	f, err := os.OpenFile(EventsPath(), os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if _, err := f.Write(append(bytes.Repeat([]byte("x"), 2<<20), '\n')); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	got, err := ReadEvents(time.Time{})
	if err != nil {
		t.Fatalf("ReadEvents = %v, want a short read rather than an error", err)
	}
	wantKinds(t, got, EventMerged)
}
