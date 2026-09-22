package supatree

import (
	"strings"
	"testing"
	"time"
)

func ev(kind EventKind, tree, member string, at time.Time) Event {
	return Event{At: at, Kind: kind, Tree: tree, Member: member, Text: string(kind)}
}

func emptyState() NotifyState { return NotifyState{Sent: map[string]time.Time{}} }

// Only the two kinds that mean "a human is now blocking on you" may interrupt.
func TestDecideTiers(t *testing.T) {
	now := diffNow
	evs := []Event{
		ev(EventPushed, treeA, "api", now),
		ev(EventPROpened, treeA, "api", now),
		ev(EventChecksPassed, treeA, "api", now),
		ev(EventApproved, treeA, "api", now),
		ev(EventMerged, treeA, "api", now),
		ev(EventTreeDone, treeA, "", now),
		ev(EventStale, treeA, "", now),
		ev(EventChangesRequested, treeA, "api", now),
		ev(EventChecksFailed, treeA, "api", now),
	}
	got, _ := Decide(evs, emptyState(), DecideOptions{})
	wantKinds(t, got, EventChangesRequested, EventChecksFailed)
}

// An unknown kind — a ledger written by a newer version — must stay silent
// rather than defaulting to the loudest surface.
func TestDecideUnknownKindIsSilent(t *testing.T) {
	got, _ := Decide([]Event{ev("teleported", treeA, "api", diffNow)}, emptyState(), DecideOptions{})
	wantKinds(t, got)
	if TierOf("teleported") != TierNever {
		t.Errorf("TierOf(unknown) = %q, want %q", TierOf("teleported"), TierNever)
	}
}

func TestDecideDedupeWithinCooldown(t *testing.T) {
	first := ev(EventChangesRequested, treeA, "api", diffNow)
	got, st := Decide([]Event{first}, emptyState(), DecideOptions{})
	wantKinds(t, got, EventChangesRequested)

	// Same news again inside the cooldown: silent.
	again := ev(EventChangesRequested, treeA, "api", diffNow.Add(5*time.Minute))
	got, st = Decide([]Event{again}, st, DecideOptions{})
	wantKinds(t, got)

	// And audible once the cooldown has elapsed.
	later := ev(EventChangesRequested, treeA, "api", diffNow.Add(DefaultCooldown+time.Minute))
	got, _ = Decide([]Event{later}, st, DecideOptions{})
	wantKinds(t, got, EventChangesRequested)
}

// Flapping CI produces three genuine transitions from one broken build. The
// long cooldown on checks_failed is what stops that being three notifications.
func TestDecideFlappingChecksNotifyOnce(t *testing.T) {
	st := emptyState()
	var delivered []Event
	for i, at := range []time.Time{diffNow, diffNow.Add(20 * time.Minute), diffNow.Add(40 * time.Minute)} {
		got, next := Decide([]Event{ev(EventChecksFailed, treeA, "api", at)}, st, DecideOptions{})
		delivered = append(delivered, got...)
		st = next
		_ = i
	}
	if len(delivered) != 1 {
		t.Fatalf("flapping delivered %d notifications, want 1", len(delivered))
	}
}

// The dedupe key is the thing the news is about, so two members failing are two
// separate notifications and must not silence each other.
func TestDecideKeyIsPerMember(t *testing.T) {
	evs := []Event{
		ev(EventChecksFailed, treeA, "api", diffNow),
		ev(EventChecksFailed, treeA, "web", diffNow),
		ev(EventChecksFailed, treeB, "api", diffNow),
	}
	got, _ := Decide(evs, emptyState(), DecideOptions{})
	if len(got) != 3 {
		t.Fatalf("got %d notifications, want 3 (one per member)", len(got))
	}
}

func TestDecideSuppressesFocusedTree(t *testing.T) {
	evs := []Event{
		ev(EventChangesRequested, treeA, "api", diffNow),
		ev(EventChangesRequested, treeB, "api", diffNow),
	}
	got, _ := Decide(evs, emptyState(), DecideOptions{FocusedTree: treeA})
	if len(got) != 1 || got[0].Tree != treeB {
		t.Fatalf("got %v, want only darwin", kinds(got))
	}
}

// An unknown focused tab must suppress nothing: a missed suppression is an
// annoyance, a missed notification is a bug.
func TestDecideUnknownFocusSuppressesNothing(t *testing.T) {
	got, _ := Decide([]Event{ev(EventChangesRequested, treeA, "api", diffNow)}, emptyState(), DecideOptions{FocusedTree: ""})
	wantKinds(t, got, EventChangesRequested)
}

// A suppressed event must not be recorded as delivered, or the cooldown would
// silence the notification you were meant to get after looking away.
func TestDecideSuppressedEventDoesNotArmCooldown(t *testing.T) {
	first := ev(EventChangesRequested, treeA, "api", diffNow)
	_, st := Decide([]Event{first}, emptyState(), DecideOptions{FocusedTree: treeA})

	got, _ := Decide([]Event{ev(EventChangesRequested, treeA, "api", diffNow.Add(time.Minute))}, st, DecideOptions{})
	wantKinds(t, got, EventChangesRequested)
}

func TestDecideDoesNotMutateInputState(t *testing.T) {
	st := emptyState()
	_, _ = Decide([]Event{ev(EventChecksFailed, treeA, "api", diffNow)}, st, DecideOptions{})
	if len(st.Sent) != 0 {
		t.Errorf("Decide mutated the state it was given (%d entries)", len(st.Sent))
	}
}

func TestPruneNotifyState(t *testing.T) {
	st := NotifyState{Sent: map[string]time.Time{
		"recent": diffNow.Add(-time.Hour),
		"old":    diffNow.Add(-48 * time.Hour),
	}}
	got := PruneNotifyState(st, diffNow)
	if _, ok := got.Sent["recent"]; !ok {
		t.Error("pruned a recent entry")
	}
	if _, ok := got.Sent["old"]; ok {
		t.Error("kept an entry older than every cooldown")
	}
}

// Notification text is short human prose that ends up inside another language's
// string literal (AppleScript, a shell word). Sanitizing beats per-target
// escaping, and it must be total.
func TestSanitizeNotify(t *testing.T) {
	got := sanitizeNotify(`say "hi" \ and $(rm -rf /) ` + "\n`x`")
	for _, bad := range []string{`"`, `\`, "`", "$", "\n"} {
		if strings.Contains(got, bad) {
			t.Errorf("sanitizeNotify left %q in %q", bad, got)
		}
	}
	if long := sanitizeNotify(strings.Repeat("a", 500)); len([]rune(long)) > 201 {
		t.Errorf("sanitizeNotify returned %d runes, want it bounded", len([]rune(long)))
	}
}
