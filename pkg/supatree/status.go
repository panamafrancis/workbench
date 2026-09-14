package supatree

import (
	"sort"
	"sync"
	"time"

	"github.com/panamafrancis/workbench/pkg/git"
	"github.com/panamafrancis/workbench/pkg/github"
)

// DefaultStaleAfter is how long a supatree may go without a commit in any
// member before it is flagged stale.
const DefaultStaleAfter = 7 * 24 * time.Hour

// MemberState is where one member repo sits in the ship lifecycle. The zero
// value is deliberately absent: a member with no worktree on disk.
type MemberState string

const (
	MemberAbsent   MemberState = "absent"   // worktree not checked out — run sync
	MemberIdle     MemberState = "idle"     // clean and even with the base: nothing to ship
	MemberWIP      MemberState = "wip"      // local commits or edits that are not on the remote
	MemberPushed   MemberState = "pushed"   // branch is on the remote, no PR yet
	MemberDraft    MemberState = "draft"    // draft PR
	MemberOpen     MemberState = "open"     // PR open, awaiting review
	MemberChanges  MemberState = "changes"  // PR open, changes requested
	MemberApproved MemberState = "approved" // PR open and approved
	MemberMerged   MemberState = "merged"
	MemberClosed   MemberState = "closed" // PR closed without merging
)

// TreeState is the rolled-up state of a whole supatree: how far the least
// advanced member with work still to ship has got.
type TreeState string

const (
	TreeSetup    TreeState = "setup"    // a member worktree is missing — run sync
	TreeNew      TreeState = "new"      // checked out, no work anywhere yet
	TreeWIP      TreeState = "wip"      // work exists that is not on the remote
	TreePushed   TreeState = "pushed"   // everything pushed, a PR is still missing
	TreeReview   TreeState = "review"   // PRs open, awaiting review
	TreeApproved TreeState = "approved" // every open PR is approved — ready to merge
	TreeDone     TreeState = "done"     // every PR merged or closed — safe to remove
)

// MemberLocal is the local git snapshot of one member worktree. It is split out
// from the derivation so the state machine stays pure and testable.
type MemberLocal struct {
	Exists     bool      `json:"exists"`
	Dirty      bool      `json:"dirty"`
	Ahead      int       `json:"ahead"`      // commits ahead of origin/<default branch>
	Unpushed   int       `json:"unpushed"`   // commits HEAD has that origin/<branch> does not
	HasRemote  bool      `json:"has_remote"` // origin/<branch> exists
	LastCommit time.Time `json:"last_commit,omitzero"`
}

// MemberStatus is one member's local snapshot plus its (cached) PR.
type MemberStatus struct {
	Alias  string `json:"alias"`
	Branch string `json:"branch"`
	Path   string `json:"path"`
	MemberLocal
	State MemberState    `json:"state"`
	PR    *github.PRInfo `json:"pr,omitempty"`
	Deps  []string       `json:"depends_on,omitempty"`
}

// TreeStatus is one supatree's rolled-up activity.
type TreeStatus struct {
	Name         string         `json:"name"`
	Slug         string         `json:"slug"`
	Stack        string         `json:"stack"`
	Root         string         `json:"root"`
	State        TreeState      `json:"state"`
	Members      []MemberStatus `json:"members"`
	OpenPRs      int            `json:"open_prs"` // open + draft
	ApprovedPRs  int            `json:"approved_prs"`
	MergedPRs    int            `json:"merged_prs"`
	TotalPRs     int            `json:"total_prs"`
	Dirty        bool           `json:"dirty"`   // uncommitted changes in some member
	Blocked      bool           `json:"blocked"` // changes requested or failing checks
	Stale        bool           `json:"stale"`   // no commit anywhere within StaleAfter
	LastActivity time.Time      `json:"last_activity,omitzero"`
	CreatedAt    time.Time      `json:"created_at,omitzero"`
}

// Reap reports whether the supatree has nothing left to ship and is only
// occupying disk — the cue to run `supatree rm`.
func (t TreeStatus) Reap() bool { return t.State == TreeDone }

// Summary is the whole picture: every supatree plus the roll-up counters a
// dashboard header shows.
type Summary struct {
	Trees       []TreeStatus `json:"trees"`
	OpenPRs     int          `json:"open_prs"`
	ApprovedPRs int          `json:"approved_prs"`
	Stale       int          `json:"stale"`
	Done        int          `json:"done"`
	// LastFetch is the newest PR-cache timestamp across all members, i.e. how
	// fresh the GitHub half of this summary is.
	LastFetch time.Time `json:"last_fetch,omitzero"`
	At        time.Time `json:"at"`
}

// StatusOptions tunes a Status call. The zero value uses DefaultStaleAfter and
// time.Now.
type StatusOptions struct {
	StaleAfter time.Duration
	Now        time.Time
}

func (o StatusOptions) staleAfter() time.Duration {
	if o.StaleAfter > 0 {
		return o.StaleAfter
	}
	return DefaultStaleAfter
}

func (o StatusOptions) now() time.Time {
	if !o.Now.IsZero() {
		return o.Now
	}
	return time.Now()
}

// statusWorkers bounds the concurrent git invocations. Each member costs a
// handful of short git calls; a few dozen members would otherwise fork a
// hundred processes at once.
const statusWorkers = 8

// Status builds the activity summary for the given supatrees from local git
// state plus the on-disk PR cache. It never talks to GitHub — refreshing the
// cache is FetchPRs' job — so it is free to call as often as a UI likes.
func Status(insts []*Instance, cache *github.Cache, opts StatusOptions) Summary {
	now := opts.now()
	sum := Summary{At: now, Trees: make([]TreeStatus, len(insts))}

	type job struct{ tree, member int }
	var jobs []job
	for i, inst := range insts {
		sum.Trees[i] = TreeStatus{
			Name:    inst.Name,
			Slug:    inst.Slug,
			Stack:   inst.Stack,
			Root:    inst.Root,
			Members: make([]MemberStatus, len(inst.Members)),
		}
		if meta, err := LoadMeta(inst.Root); err == nil {
			sum.Trees[i].CreatedAt = meta.CreatedAt
		}
		for j, mem := range inst.Members {
			sum.Trees[i].Members[j] = MemberStatus{
				Alias:  mem.Alias,
				Branch: mem.Branch,
				Path:   mem.Path,
				Deps:   mem.DependsOn,
			}
			jobs = append(jobs, job{i, j})
		}
	}

	sem := make(chan struct{}, statusWorkers)
	var wg sync.WaitGroup
	for _, jb := range jobs {
		wg.Add(1)
		go func(jb job) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			ms := &sum.Trees[jb.tree].Members[jb.member]
			ms.MemberLocal = localState(insts[jb.tree].Members[jb.member])
		}(jb)
	}
	wg.Wait()

	for i := range sum.Trees {
		t := &sum.Trees[i]
		for j := range t.Members {
			ms := &t.Members[j]
			ms.PR = cache.Get(ms.Branch)
			if ms.PR != nil && ms.PR.Status == github.PRNone {
				ms.PR = nil
			}
			ms.State = memberState(ms.MemberLocal, ms.PR)
			if ms.PR != nil && ms.PR.FetchedAt.After(sum.LastFetch) {
				sum.LastFetch = ms.PR.FetchedAt
			}
		}
		rollup(t, now, opts.staleAfter())
		sum.OpenPRs += t.OpenPRs
		sum.ApprovedPRs += t.ApprovedPRs
		if t.Stale {
			sum.Stale++
		}
		if t.State == TreeDone {
			sum.Done++
		}
	}
	sort.SliceStable(sum.Trees, func(i, j int) bool {
		return treeOrder(sum.Trees[i]) < treeOrder(sum.Trees[j])
	})
	return sum
}

// treeOrder sorts the most actionable supatrees first: blocked work, then trees
// that need a human push forward, with finished ones last.
func treeOrder(t TreeStatus) int {
	if t.Blocked {
		return 0
	}
	switch t.State {
	case TreeApproved:
		return 1
	case TreeReview:
		return 2
	case TreePushed:
		return 3
	case TreeWIP:
		return 4
	case TreeSetup:
		return 5
	case TreeNew:
		return 6
	case TreeDone:
		return 7
	}
	return 8
}

// localState gathers the git facts for one member. Every call is best effort:
// a member whose worktree is gone reports absent rather than failing the run.
func localState(mem Member) MemberLocal {
	if !mem.Exists {
		return MemberLocal{}
	}
	l := MemberLocal{Exists: true, Dirty: git.IsDirty(mem.Path)}
	l.Ahead, _ = git.CommitsAhead(mem.Path)
	l.Unpushed, l.HasRemote = git.UnpushedCommits(mem.Path, mem.Branch)
	l.LastCommit, _ = git.LastCommitTime(mem.Path)
	return l
}

// memberState is the pure lifecycle derivation: the PR (when there is one)
// decides, otherwise local git does.
func memberState(l MemberLocal, pr *github.PRInfo) MemberState {
	if !l.Exists {
		return MemberAbsent
	}
	if pr != nil {
		switch pr.Status {
		case github.PRMerged:
			return MemberMerged
		case github.PRClosed:
			return MemberClosed
		case github.PRDraft:
			return MemberDraft
		case github.PROpen:
			switch pr.Review {
			case github.ReviewApproved:
				return MemberApproved
			case github.ReviewChanges:
				return MemberChanges
			case github.ReviewRequired, github.ReviewNone:
				return MemberOpen
			}
			return MemberOpen
		case github.PRNone:
		}
	}
	// No PR: unpushed work outranks a pushed branch, which outranks idle.
	if l.Dirty || (!l.HasRemote && l.Ahead > 0) || l.Unpushed > 0 {
		return MemberWIP
	}
	if l.HasRemote && l.Ahead > 0 {
		return MemberPushed
	}
	return MemberIdle
}

// memberRank orders member states by how far along they are. Idle and absent
// members carry no work, so they are excluded from the rollup by the caller
// rather than ranked.
func memberRank(s MemberState) int {
	switch s {
	case MemberWIP:
		return 1
	case MemberPushed:
		return 2
	case MemberDraft:
		return 3
	case MemberOpen, MemberChanges:
		return 4
	case MemberApproved:
		return 5
	case MemberMerged, MemberClosed:
		return 6
	case MemberAbsent, MemberIdle:
		return 0
	}
	return 0
}

// rollup fills in a tree's aggregate state, counters and flags from its members.
func rollup(t *TreeStatus, now time.Time, staleAfter time.Duration) {
	absent, shipping := false, 0
	minRank := 99
	for _, ms := range t.Members {
		if ms.Dirty {
			t.Dirty = true
		}
		if ms.LastCommit.After(t.LastActivity) {
			t.LastActivity = ms.LastCommit
		}
		if ms.PR != nil {
			t.TotalPRs++
			switch ms.PR.Status {
			case github.PROpen, github.PRDraft:
				t.OpenPRs++
			case github.PRMerged:
				t.MergedPRs++
			case github.PRClosed, github.PRNone:
			}
			if ms.PR.Checks == github.CheckFailing {
				t.Blocked = true
			}
		}
		switch ms.State {
		case MemberAbsent:
			absent = true
			continue
		case MemberIdle:
			continue
		case MemberApproved:
			t.ApprovedPRs++
		case MemberChanges:
			t.Blocked = true
		case MemberWIP, MemberPushed, MemberDraft, MemberOpen, MemberMerged, MemberClosed:
		}
		shipping++
		if r := memberRank(ms.State); r < minRank {
			minRank = r
		}
	}

	switch {
	case absent:
		t.State = TreeSetup
	case shipping == 0:
		t.State = TreeNew
	case minRank >= 6:
		t.State = TreeDone
	case minRank <= 1:
		t.State = TreeWIP
	case minRank == 2:
		t.State = TreePushed
	case minRank <= 4:
		t.State = TreeReview
	default:
		t.State = TreeApproved
	}

	// A finished supatree is not "stale", it is done — only unfinished work goes
	// quiet. A tree with no commits at all falls back to its creation time.
	if t.State != TreeDone {
		last := t.LastActivity
		if last.IsZero() {
			last = t.CreatedAt
		}
		t.Stale = !last.IsZero() && now.Sub(last) > staleAfter
	}
}
