package supatree

import (
	"testing"
	"time"
)

// jobStandup is the job id most of these tables use.
const jobStandup = "standup"

func jobIDs(jobs []Job) []string {
	out := make([]string, len(jobs))
	for i, j := range jobs {
		out[i] = j.ID
	}
	return out
}

func seen(id string, at time.Time) ScheduleState {
	return ScheduleState{LastFired: map[string]time.Time{id: at}}
}

// A freshly written schedule.yml must not fire every entry at once, on whatever
// afternoon it was saved. "Never fired" reads as "overdue" unless seeded.
func TestDueSeedsOnFirstSight(t *testing.T) {
	jobs := []Job{
		{ID: jobStandup, At: "09:00", When: WhenAlways, Prompt: "x"},
		{ID: "triage", Every: "1h", When: WhenAlways, Prompt: "x"},
	}
	now := time.Date(2026, 9, 20, 14, 0, 0, 0, time.UTC)
	due, next := Due(jobs, ScheduleState{LastFired: map[string]time.Time{}}, nil, now)
	if len(due) != 0 {
		t.Fatalf("first sight fired %v, want nothing", jobIDs(due))
	}
	if len(next.LastFired) != 2 {
		t.Fatalf("seeded %d jobs, want 2", len(next.LastFired))
	}
	// And having been seeded, an interval job fires on its own schedule.
	due, _ = Due(jobs, next, nil, now.Add(2*time.Hour))
	if len(due) != 1 || due[0].ID != "triage" {
		t.Fatalf("after seeding got %v, want [triage]", jobIDs(due))
	}
}

// Close the laptop at 18:00, open it at 09:00: a naive hourly job is fifteen
// deep. A due job fires once however many intervals elapsed.
func TestDueCoalescesMissedFires(t *testing.T) {
	job := Job{ID: "triage", Every: "1h", When: WhenAlways, Prompt: "x"}
	evening := time.Date(2026, 9, 20, 18, 0, 0, 0, time.UTC)
	morning := evening.Add(15 * time.Hour)

	due, next := Due([]Job{job}, seen("triage", evening), nil, morning)
	if len(due) != 1 {
		t.Fatalf("overnight gap fired %d times, want 1", len(due))
	}
	// And the next tick is quiet, because the fire was recorded as of now.
	if again, _ := Due([]Job{job}, next, nil, morning.Add(time.Minute)); len(again) != 0 {
		t.Fatalf("fired again a minute later: %v", jobIDs(again))
	}
}

func TestDueAtTimeOfDay(t *testing.T) {
	job := Job{ID: jobStandup, At: "09:00", Days: []string{"mon", "tue", "wed", "thu", "fri"}, When: WhenAlways, Prompt: "x"}
	// 2026-09-21 is a Monday.
	monday8 := time.Date(2026, 9, 21, 8, 0, 0, 0, time.UTC)
	monday9 := time.Date(2026, 9, 21, 9, 30, 0, 0, time.UTC)
	saturday9 := time.Date(2026, 9, 26, 9, 30, 0, 0, time.UTC)
	yesterday := time.Date(2026, 9, 20, 9, 0, 0, 0, time.UTC)

	if due, _ := Due([]Job{job}, seen(jobStandup, yesterday), nil, monday8); len(due) != 0 {
		t.Error("fired before the hour")
	}
	due, next := Due([]Job{job}, seen(jobStandup, yesterday), nil, monday9)
	if len(due) != 1 {
		t.Fatal("did not fire after 09:00 on a weekday")
	}
	// Not twice on the same day.
	if again, _ := Due([]Job{job}, next, nil, monday9.Add(2*time.Hour)); len(again) != 0 {
		t.Error("fired twice in one day")
	}
	if due, _ := Due([]Job{job}, seen(jobStandup, yesterday), nil, saturday9); len(due) != 0 {
		t.Error("fired on a day it is not scheduled for")
	}
}

// A standup that says "nothing changed" every morning is notification fatigue
// wearing a suit. The events gate is the default for that reason.
func TestDueRequiresSomethingToSay(t *testing.T) {
	job := Job{ID: jobStandup, Every: "1h", Prompt: "x"}
	last := diffNow
	now := last.Add(2 * time.Hour)

	if due, _ := Due([]Job{job}, seen(jobStandup, last), nil, now); len(due) != 0 {
		t.Error("fired with no events since the last run")
	}
	evs := []Event{{At: last.Add(time.Minute), Kind: EventChangesRequested, Tree: treeA}}
	if due, _ := Due([]Job{job}, seen(jobStandup, last), evs, now); len(due) != 1 {
		t.Error("did not fire despite an event since the last run")
	}
	// Events from before the last run are not news.
	old := []Event{{At: last.Add(-time.Hour), Kind: EventChangesRequested, Tree: treeA}}
	if due, _ := Due([]Job{job}, seen(jobStandup, last), old, now); len(due) != 0 {
		t.Error("fired on an event that predates the last run")
	}
	// when: always ignores the gate entirely.
	always := Job{ID: "reap", Every: "1h", When: WhenAlways, Prompt: "x"}
	if due, _ := Due([]Job{always}, seen("reap", last), nil, now); len(due) != 1 {
		t.Error("when: always did not fire")
	}
}

// A gated job that was due must still record the fire, or it is due again on
// every tick until something finally happens — which then fires immediately
// rather than at the scheduled time.
func TestDueRecordsSuppressedFire(t *testing.T) {
	job := Job{ID: jobStandup, Every: "1h", Prompt: "x"}
	last := diffNow
	now := last.Add(2 * time.Hour)
	_, next := Due([]Job{job}, seen(jobStandup, last), nil, now)
	if !next.LastFired[jobStandup].Equal(now) {
		t.Errorf("LastFired = %v, want it advanced to %v even though nothing was said", next.LastFired[jobStandup], now)
	}
}

func TestDueFiltersByKindAndTree(t *testing.T) {
	job := Job{ID: "triage", Every: "1h", Kinds: []string{"checks_failed"}, Tree: treeA, Prompt: "x"}
	last := diffNow
	now := last.Add(2 * time.Hour)

	wrongKind := []Event{{At: last.Add(time.Minute), Kind: EventApproved, Tree: treeA}}
	if due, _ := Due([]Job{job}, seen("triage", last), wrongKind, now); len(due) != 0 {
		t.Error("fired on an event kind it does not watch")
	}
	wrongTree := []Event{{At: last.Add(time.Minute), Kind: EventChecksFailed, Tree: treeB}}
	if due, _ := Due([]Job{job}, seen("triage", last), wrongTree, now); len(due) != 0 {
		t.Error("fired on another supatree's event")
	}
	right := []Event{{At: last.Add(time.Minute), Kind: EventChecksFailed, Tree: treeA}}
	if due, _ := Due([]Job{job}, seen("triage", last), right, now); len(due) != 1 {
		t.Error("did not fire on the event it watches")
	}
}

func TestDueIgnoresMalformedJobs(t *testing.T) {
	last := diffNow
	now := last.Add(24 * time.Hour)
	jobs := []Job{
		{ID: "", Every: "1h", When: WhenAlways, Prompt: "x"},
		{ID: "noprompt", Every: "1h", When: WhenAlways},
		{ID: "badevery", Every: "soon", When: WhenAlways, Prompt: "x"},
		{ID: "badat", At: "25:99", When: WhenAlways, Prompt: "x"},
		{ID: "neither", When: WhenAlways, Prompt: "x"},
	}
	st := ScheduleState{LastFired: map[string]time.Time{
		"noprompt": last, "badevery": last, "badat": last, "neither": last,
	}}
	if due, _ := Due(jobs, st, nil, now); len(due) != 0 {
		t.Fatalf("malformed jobs fired: %v", jobIDs(due))
	}
}

// Running late is worse than not running: a 09:00 standup delivered at 16:00 is
// noise that looks like a bug.
func TestJobExpiry(t *testing.T) {
	j := Job{ID: jobStandup, TTL: "2h"}
	fired := diffNow
	if j.Expired(fired, fired.Add(time.Hour)) {
		t.Error("expired inside its TTL")
	}
	if !j.Expired(fired, fired.Add(3*time.Hour)) {
		t.Error("did not expire past its TTL")
	}
	// An unset TTL still bounds lateness rather than being unbounded.
	def := Job{ID: "x"}
	if !def.Expired(fired, fired.Add(DefaultTTL+time.Minute)) {
		t.Error("a job with no TTL never expires")
	}
}

func TestJobToRequestCarriesProvenance(t *testing.T) {
	j := Job{ID: jobStandup, Tree: treeA, Prompt: "  summarise  "}
	r := j.ToRequest(diffNow)
	if r.From != "schedule:standup" {
		t.Errorf("From = %q, want the job it came from", r.From)
	}
	if r.Tree != treeA || r.Text != "summarise" {
		t.Errorf("request = %+v", r)
	}
}
