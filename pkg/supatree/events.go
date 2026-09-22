package supatree

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/panamafrancis/workbench/pkg/config"
	"github.com/panamafrancis/workbench/pkg/github"
)

// EventKind names a transition worth recording. The set is deliberately
// limited to what Diff can derive from two Summary values — nothing here
// requires a new data source, which is why the watcher adds no GitHub load.
type EventKind string

const (
	EventPushed           EventKind = "pushed"
	EventPROpened         EventKind = "pr_opened"
	EventChangesRequested EventKind = "changes_requested"
	EventApproved         EventKind = "approved"
	EventChecksFailed     EventKind = "checks_failed"
	EventChecksPassed     EventKind = "checks_passed"
	EventMerged           EventKind = "merged"
	EventClosed           EventKind = "closed"
	EventTreeDone         EventKind = "tree_done"
	EventStale            EventKind = "stale"
	// EventTreeReviewed is a review tree whose pull requests have all landed.
	// Separate from EventTreeDone so History never counts someone else's merge
	// as work this tree shipped.
	EventTreeReviewed EventKind = "tree_reviewed"
	// EventAuthorPushed is the author moving the head out from under a review
	// in progress — the one transition that makes a reviewer's work stale.
	EventAuthorPushed EventKind = "author_pushed"
)

// Event is one entry in the activity ledger (~/.supatree/events.jsonl).
type Event struct {
	At   time.Time `json:"at"`
	Kind EventKind `json:"kind"`
	Tree string    `json:"tree"`
	// Mode is the tree's lifecycle when the event was recorded. It is written
	// even though nothing reads it yet: the ledger is append-only, so an entry
	// stored without it can never be reinterpreted, and an authoring merge and
	// a merge on a PR you were merely reviewing are not the same news.
	Mode   Mode   `json:"mode,omitempty"`
	Member string `json:"member,omitempty"`
	PR     int    `json:"pr,omitempty"`
	Text   string `json:"text"`
}

// Key identifies the thing an event is about, ignoring when it happened. It is
// the dedupe key: two "checks failed on keystone-api in canberra" are the same
// news however far apart they arrive.
func (e Event) Key() string {
	return string(e.Kind) + "\x00" + e.Tree + "\x00" + e.Member
}

// Diff derives the events between two consecutive summaries.
//
// It is pure — no clock, no I/O — and takes its timestamps from cur.At, so the
// whole notification policy can be tested as a table.
//
// The rule that makes the first run quiet is structural rather than a special
// case in the caller: a tree or member that does not appear in prev is not
// diffed at all. So Diff(Summary{}, cur) returns nothing, and neither does the
// round in which a supatree is created — in both cases there is no observed
// previous state to have transitioned *from*, and announcing the initial state
// as if it were news is exactly the bug that makes people mute notifications on
// day one.
func Diff(prev, cur Summary) []Event {
	prevTrees := make(map[string]TreeStatus, len(prev.Trees))
	for _, t := range prev.Trees {
		prevTrees[t.Name] = t
	}

	var evs []Event
	for _, t := range cur.Trees {
		pt, seen := prevTrees[t.Name]
		if !seen {
			continue
		}
		prevMembers := make(map[string]MemberStatus, len(pt.Members))
		for _, m := range pt.Members {
			prevMembers[m.Alias] = m
		}
		for _, m := range t.Members {
			pm, seenMember := prevMembers[m.Alias]
			if !seenMember {
				continue
			}
			evs = append(evs, memberEvents(t.Name, pm, m, cur.At, t.Mode)...)
		}
		evs = append(evs, treeEvents(pt, t, cur.At)...)
	}
	return evs
}

// memberEvents reports the transitions of one member repo between two rounds.
func memberEvents(tree string, prev, cur MemberStatus, at time.Time, mode Mode) []Event {
	var evs []Event
	add := func(kind EventKind, text string) {
		evs = append(evs, Event{At: at, Kind: kind, Tree: tree, Mode: mode, Member: cur.Alias, PR: prNumber(cur.PR), Text: text})
	}

	if cur.State != prev.State {
		switch cur.State {
		case MemberPushed:
			add(EventPushed, fmt.Sprintf("%s/%s pushed, no PR yet", tree, cur.Alias))
		case MemberDraft, MemberOpen:
			// Only news when the PR is new. open→draft→open is the author
			// toggling ready-for-review, and changes→open is a re-review that
			// the review transitions below already cover.
			if !hasPR(prev.PR) {
				add(EventPROpened, fmt.Sprintf("%s/%s opened PR #%d", tree, cur.Alias, prNumber(cur.PR)))
			}
		case MemberChanges:
			// Whose verdict this is flips with the mode. On your own tree it is
			// something to act on; on a review tree it is your review landing.
			if mode == ModeReviewing {
				add(EventChangesRequested, fmt.Sprintf("%s/%s: #%d now has changes requested", tree, cur.Alias, prNumber(cur.PR)))
			} else {
				add(EventChangesRequested, fmt.Sprintf("%s/%s: changes requested on #%d", tree, cur.Alias, prNumber(cur.PR)))
			}
		case MemberApproved:
			add(EventApproved, fmt.Sprintf("%s/%s: #%d approved", tree, cur.Alias, prNumber(cur.PR)))
		case MemberMerged:
			if mode == ModeReviewing {
				add(EventMerged, fmt.Sprintf("%s/%s: #%d merged by its author", tree, cur.Alias, prNumber(cur.PR)))
			} else {
				add(EventMerged, fmt.Sprintf("%s/%s: #%d merged", tree, cur.Alias, prNumber(cur.PR)))
			}
		case MemberClosed:
			add(EventClosed, fmt.Sprintf("%s/%s: #%d closed without merging", tree, cur.Alias, prNumber(cur.PR)))
		case MemberAbsent, MemberIdle, MemberWIP, MemberInReview, MemberForeign:
			// Not news: no worktree, nothing to ship, work in progress, or a
			// review tree simply sitting at the head it was created at.
			// Foreign is a fault the status surfaces report directly rather
			// than a transition worth a desktop notification.
		}
	}

	// The author moving the head is not a lifecycle transition — the PR sits in
	// `open` throughout — but it is the one thing that invalidates a review in
	// progress, so it is diffed on its own.
	if cur.AuthorPushed && !prev.AuthorPushed {
		add(EventAuthorPushed, fmt.Sprintf("%s/%s: #%d has new commits since you checked it out — run `supatree review refresh`",
			tree, cur.Alias, prNumber(cur.PR)))
	}

	// Checks are orthogonal to the lifecycle state — a PR sits in `open` while
	// CI goes red and green underneath it — so they are diffed separately.
	prevChecks, curChecks := checkState(prev.PR), checkState(cur.PR)
	if curChecks != prevChecks {
		switch curChecks {
		case github.CheckFailing:
			add(EventChecksFailed, fmt.Sprintf("%s/%s: checks failing on #%d", tree, cur.Alias, prNumber(cur.PR)))
		case github.CheckPassing:
			// Only worth saying after a failure; "CI went green" is otherwise
			// just the expected path and belongs to the glyph tier at most.
			if prevChecks == github.CheckFailing {
				add(EventChecksPassed, fmt.Sprintf("%s/%s: checks green on #%d", tree, cur.Alias, prNumber(cur.PR)))
			}
		case github.CheckNone, github.CheckPending:
			// A run that has not reported yet is not a verdict.
		}
	}
	return evs
}

// treeEvents reports the rolled-up transitions of one supatree.
func treeEvents(prev, cur TreeStatus, at time.Time) []Event {
	var evs []Event
	if cur.State == TreeDone && prev.State != TreeDone {
		evs = append(evs, Event{At: at, Kind: EventTreeDone, Tree: cur.Name, Mode: cur.Mode,
			Text: fmt.Sprintf("%s is done — every PR merged or closed", cur.Name)})
	}
	if cur.State == TreeReviewed && prev.State != TreeReviewed {
		evs = append(evs, Event{At: at, Kind: EventTreeReviewed, Tree: cur.Name, Mode: cur.Mode,
			Text: fmt.Sprintf("%s: everything under review has landed — safe to remove", cur.Name)})
	}
	if cur.Stale && !prev.Stale {
		evs = append(evs, Event{At: at, Kind: EventStale, Tree: cur.Name, Mode: cur.Mode,
			Text: staleText(cur)})
	}
	return evs
}

// staleText says what went quiet. On a review tree the last commit is the
// author's, so the same silence means their pull request has stopped moving,
// not that you have.
func staleText(t TreeStatus) string {
	if t.Mode == ModeReviewing {
		return fmt.Sprintf("%s: the pull requests under review have gone quiet", t.Name)
	}
	return fmt.Sprintf("%s has gone stale", t.Name)
}

func hasPR(pr *github.PRInfo) bool { return pr != nil && pr.Number != 0 }

func prNumber(pr *github.PRInfo) int {
	if pr == nil {
		return 0
	}
	return pr.Number
}

func checkState(pr *github.PRInfo) github.CheckState {
	if pr == nil {
		return github.CheckNone
	}
	return pr.Checks
}

// eventsMaxBytes is when the ledger rotates. Roughly 50k events — years of
// ordinary use — but bounded, because the ledger is append-only and every
// reader scans it: the sidebar, the dashboard feed, the schedule gate and
// History. Unbounded growth here degrades all four silently.
const eventsMaxBytes = 8 << 20

// AppendEvents adds events to the ledger under EventsLockPath. Appending
// nothing is a no-op, so callers need not guard the empty case.
func AppendEvents(evs []Event) error {
	if len(evs) == 0 {
		return nil
	}
	return config.WithFileLock(EventsLockPath(), func() error {
		// Rotate before appending, not after: rotating afterwards renames away
		// the very file the new events just landed in, leaving no current
		// ledger until the next append.
		if err := rotateEvents(); err != nil {
			return err
		}
		return appendJSONL(EventsPath(), evs)
	})
}

// rotateEvents moves an oversized ledger aside, keeping exactly one generation.
//
// One generation rather than none because History and the schedule gate read
// back weeks; one rather than many because this is a convenience log, not an
// audit trail, and a rotation scheme nobody prunes is the same unbounded growth
// with extra steps. The caller holds the lock.
func rotateEvents() error {
	info, err := os.Stat(EventsPath())
	if err != nil || info.Size() < eventsMaxBytes {
		return nil //nolint:nilerr // a missing ledger is not a rotation failure
	}
	return os.Rename(EventsPath(), eventsPrevPath())
}

// eventsPrevPath is the previous ledger generation.
func eventsPrevPath() string { return EventsPath() + ".1" }

// ReadEvents returns every ledger entry at or after since, oldest first. A
// zero `since` reads the whole file. A missing ledger is not an error — it is
// what a fresh install looks like.
func ReadEvents(since time.Time) ([]Event, error) {
	// Oldest generation first, so the result stays a timeline.
	out, err := readEventFile(eventsPrevPath(), since, nil)
	if err != nil {
		return nil, err
	}
	return readEventFile(EventsPath(), since, out)
}

func readEventFile(path string, since time.Time, out []Event) ([]Event, error) {
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return out, nil
	}
	if err != nil {
		return nil, fmt.Errorf("open events: %w", err)
	}
	defer func() { _ = f.Close() }()

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var e Event
		if err := json.Unmarshal(line, &e); err != nil {
			// A torn or hand-edited line must not cost us the rest of the
			// ledger: this file is append-only history, not config.
			continue
		}
		if !since.IsZero() && e.At.Before(since) {
			continue
		}
		out = append(out, e)
	}
	if err := sc.Err(); err != nil {
		// A line longer than the buffer means this file is damaged from here on
		// — but everything before it is valid, and four separate readers depend
		// on this call. Degrade to a short read rather than failing them all,
		// the same way a torn JSON line costs one entry and not the ledger.
		if errors.Is(err, bufio.ErrTooLong) {
			return out, nil
		}
		return nil, fmt.Errorf("read events: %w", err)
	}
	return out, nil
}

// appendJSONL writes one JSON object per line to path, creating it as needed.
// The caller holds the relevant lock.
func appendJSONL[T any](path string, items []T) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf("create dir: %w", err)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("open %s: %w", filepath.Base(path), err)
	}
	defer func() { _ = f.Close() }()
	enc := json.NewEncoder(f)
	for i := range items {
		if err := enc.Encode(items[i]); err != nil {
			return fmt.Errorf("write %s: %w", filepath.Base(path), err)
		}
	}
	return nil
}

// AppendNotification queues an event for the watcher to deliver.
//
// It exists because the watcher is the only supatree process that runs outside
// the nono sandbox, and therefore the only one that can reach the desktop.
// Everything else — the PM above all — appends here, and the watcher drains it
// through the *same* tier and dedupe policy as its own events. An outbox that
// bypassed that policy would be used as a way around it.
func AppendNotification(ev Event) error {
	if ev.At.IsZero() {
		ev.At = time.Now().UTC()
	}
	return config.WithFileLock(EventsLockPath(), func() error {
		return appendJSONL(NotifyPath(), []Event{ev})
	})
}
