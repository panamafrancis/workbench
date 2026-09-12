package supatree

import (
	"testing"
	"time"

	"github.com/panamafrancis/workbench/pkg/github"
)

func TestMemberStateLocal(t *testing.T) {
	tests := []struct {
		name  string
		local MemberLocal
		want  MemberState
	}{
		{"absent", MemberLocal{}, MemberAbsent},
		{"clean and even", MemberLocal{Exists: true}, MemberIdle},
		{"dirty", MemberLocal{Exists: true, Dirty: true}, MemberWIP},
		{"committed but never pushed", MemberLocal{Exists: true, Ahead: 3}, MemberWIP},
		{"pushed then committed again", MemberLocal{Exists: true, Ahead: 4, Unpushed: 1, HasRemote: true}, MemberWIP},
		{"fully pushed, no PR", MemberLocal{Exists: true, Ahead: 3, HasRemote: true}, MemberPushed},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := memberState(tc.local, nil); got != tc.want {
				t.Errorf("memberState = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestMemberStatePR(t *testing.T) {
	local := MemberLocal{Exists: true, Ahead: 2, HasRemote: true}
	tests := []struct {
		name string
		pr   github.PRInfo
		want MemberState
	}{
		{"draft", github.PRInfo{Status: github.PRDraft}, MemberDraft},
		{"open, no decision", github.PRInfo{Status: github.PROpen}, MemberOpen},
		{"open, review required", github.PRInfo{Status: github.PROpen, Review: github.ReviewRequired}, MemberOpen},
		{"approved", github.PRInfo{Status: github.PROpen, Review: github.ReviewApproved}, MemberApproved},
		{"changes requested", github.PRInfo{Status: github.PROpen, Review: github.ReviewChanges}, MemberChanges},
		{"merged", github.PRInfo{Status: github.PRMerged}, MemberMerged},
		{"closed", github.PRInfo{Status: github.PRClosed}, MemberClosed},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			pr := tc.pr
			if got := memberState(local, &pr); got != tc.want {
				t.Errorf("memberState = %q, want %q", got, tc.want)
			}
		})
	}
}

// A dirty worktree must not hide the PR that is already open for it — the PR is
// the more advanced fact, and the dirt shows up as the tree's dirty flag.
func TestMemberStatePRWinsOverDirt(t *testing.T) {
	pr := github.PRInfo{Status: github.PROpen, Review: github.ReviewApproved}
	if got := memberState(MemberLocal{Exists: true, Dirty: true, HasRemote: true}, &pr); got != MemberApproved {
		t.Errorf("memberState = %q, want %q", got, MemberApproved)
	}
}

func treeWith(states ...MemberState) *TreeStatus {
	t := &TreeStatus{Name: "tree"}
	for i, s := range states {
		ms := MemberStatus{Alias: string(rune('a' + i)), State: s, MemberLocal: MemberLocal{Exists: s != MemberAbsent}}
		switch s {
		case MemberDraft:
			ms.PR = &github.PRInfo{Status: github.PRDraft}
		case MemberOpen:
			ms.PR = &github.PRInfo{Status: github.PROpen}
		case MemberChanges:
			ms.PR = &github.PRInfo{Status: github.PROpen, Review: github.ReviewChanges}
		case MemberApproved:
			ms.PR = &github.PRInfo{Status: github.PROpen, Review: github.ReviewApproved}
		case MemberMerged:
			ms.PR = &github.PRInfo{Status: github.PRMerged}
		case MemberClosed:
			ms.PR = &github.PRInfo{Status: github.PRClosed}
		case MemberAbsent, MemberIdle, MemberWIP, MemberPushed:
		}
		t.Members = append(t.Members, ms)
	}
	return t
}

func TestRollupState(t *testing.T) {
	now := time.Now()
	tests := []struct {
		name   string
		states []MemberState
		want   TreeState
	}{
		{"missing worktree", []MemberState{MemberAbsent, MemberOpen}, TreeSetup},
		{"nothing started", []MemberState{MemberIdle, MemberIdle}, TreeNew},
		{"least advanced wins", []MemberState{MemberWIP, MemberApproved}, TreeWIP},
		{"pushed but no PR", []MemberState{MemberPushed, MemberOpen}, TreePushed},
		{"in review", []MemberState{MemberOpen, MemberApproved}, TreeReview},
		{"draft counts as review", []MemberState{MemberDraft, MemberMerged}, TreeReview},
		{"all approved", []MemberState{MemberApproved, MemberApproved}, TreeApproved},
		{"idle members do not hold it back", []MemberState{MemberIdle, MemberApproved}, TreeApproved},
		{"everything merged", []MemberState{MemberMerged, MemberMerged, MemberIdle}, TreeDone},
		{"merged and closed", []MemberState{MemberMerged, MemberClosed}, TreeDone},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ts := treeWith(tc.states...)
			rollup(ts, now, DefaultStaleAfter)
			if ts.State != tc.want {
				t.Errorf("state = %q, want %q", ts.State, tc.want)
			}
		})
	}
}

func TestRollupCountsAndFlags(t *testing.T) {
	ts := treeWith(MemberApproved, MemberOpen, MemberMerged, MemberIdle)
	ts.Members[1].Dirty = true
	rollup(ts, time.Now(), DefaultStaleAfter)

	if ts.OpenPRs != 2 || ts.ApprovedPRs != 1 || ts.MergedPRs != 1 || ts.TotalPRs != 3 {
		t.Errorf("counts = open %d, approved %d, merged %d, total %d", ts.OpenPRs, ts.ApprovedPRs, ts.MergedPRs, ts.TotalPRs)
	}
	if !ts.Dirty {
		t.Error("Dirty = false, want true")
	}
	if ts.Blocked {
		t.Error("Blocked = true, want false")
	}
}

func TestRollupBlocked(t *testing.T) {
	changes := treeWith(MemberChanges, MemberOpen)
	rollup(changes, time.Now(), DefaultStaleAfter)
	if !changes.Blocked {
		t.Error("changes requested: Blocked = false, want true")
	}

	failing := treeWith(MemberOpen, MemberOpen)
	failing.Members[0].PR.Checks = github.CheckFailing
	rollup(failing, time.Now(), DefaultStaleAfter)
	if !failing.Blocked {
		t.Error("failing checks: Blocked = false, want true")
	}
}

func TestRollupStale(t *testing.T) {
	now := time.Now()
	old := now.Add(-30 * 24 * time.Hour)

	quiet := treeWith(MemberWIP, MemberIdle)
	quiet.Members[0].LastCommit = old
	rollup(quiet, now, DefaultStaleAfter)
	if !quiet.Stale {
		t.Error("Stale = false, want true for a month-old WIP tree")
	}

	// One recent commit anywhere keeps the whole supatree alive.
	quiet2 := treeWith(MemberWIP, MemberWIP)
	quiet2.Members[0].LastCommit = old
	quiet2.Members[1].LastCommit = now.Add(-time.Hour)
	rollup(quiet2, now, DefaultStaleAfter)
	if quiet2.Stale {
		t.Error("Stale = true, want false when one member committed an hour ago")
	}

	// Merged work is finished, not stale.
	done := treeWith(MemberMerged, MemberMerged)
	done.Members[0].LastCommit = old
	done.Members[1].LastCommit = old
	rollup(done, now, DefaultStaleAfter)
	if done.Stale {
		t.Error("Stale = true, want false for a done tree")
	}
	if !done.Reap() {
		t.Error("Reap() = false, want true for a done tree")
	}
}

// A tree that has never had a commit falls back to its creation time, so a
// supatree created and then abandoned still surfaces.
func TestRollupStaleFallsBackToCreation(t *testing.T) {
	now := time.Now()
	ts := treeWith(MemberWIP)
	ts.CreatedAt = now.Add(-30 * 24 * time.Hour)
	rollup(ts, now, DefaultStaleAfter)
	if !ts.Stale {
		t.Error("Stale = false, want true for a tree created a month ago with no commits")
	}
}

func TestTreeOrderPutsActionableFirst(t *testing.T) {
	blocked := TreeStatus{State: TreeReview, Blocked: true}
	if treeOrder(blocked) >= treeOrder(TreeStatus{State: TreeApproved}) {
		t.Error("blocked trees must sort before approved ones")
	}
	if treeOrder(TreeStatus{State: TreeDone}) <= treeOrder(TreeStatus{State: TreeWIP}) {
		t.Error("done trees must sort last")
	}
}
