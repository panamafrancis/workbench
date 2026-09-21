package supatree

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// SchedulePath is the hand-editable timetable (~/.supatree/schedule.yml).
func SchedulePath() string {
	return filepath.Join(Dir(), "schedule.yml")
}

// ScheduleStatePath records when each job last fired, beside last-status.json
// and equally disposable.
func ScheduleStatePath() string {
	return filepath.Join(CacheDir(), "schedule-state.json")
}

// Job is one recurring or one-shot piece of PM work.
//
// Time lives here, in Go, rather than in the agent. An agent cannot be trusted
// to hold a timer: it is mid-turn, it is blocked on a tool call, it was
// restarted an hour ago, its monitor did not survive. Every one of those
// silently drops a job, and a schedule that silently drops jobs is worse than
// none.
type Job struct {
	ID string `yaml:"id"`
	// Every is a repeat interval ("1h", "30m"). Mutually exclusive with At.
	Every string `yaml:"every,omitempty"`
	// At is a local time of day ("09:00"), optionally narrowed by Days.
	At   string   `yaml:"at,omitempty"`
	Days []string `yaml:"days,omitempty"`
	// When gates the job on there being something to say. The default,
	// "events_since_last", is load-bearing: a 09:00 standup that reports
	// "nothing changed" every morning is notification fatigue wearing a suit,
	// and people mute it inside a week.
	When string `yaml:"when,omitempty"`
	// Kinds narrows which event kinds count for When.
	Kinds []string `yaml:"kinds,omitempty"`
	// TTL is how late the job may still run. Past it the request is dropped
	// rather than executed: a 09:00 standup delivered at 16:00 is noise, and
	// worse, noise that looks like a bug.
	TTL string `yaml:"ttl,omitempty"`
	// Tree scopes the job to one supatree.
	Tree string `yaml:"tree,omitempty"`
	// Autonomy opts a job out of the scheduled cap. A scheduled turn runs with
	// nobody watching, so it caps at `nudge` even in an `auto` tree unless this
	// says otherwise.
	Autonomy string `yaml:"autonomy,omitempty"`
	// Once removes the job after it fires — the deferred one-shot ("check back
	// on #412 in two hours"), which is what turns notifications from a feed
	// into a workflow.
	Once   bool   `yaml:"once,omitempty"`
	Prompt string `yaml:"prompt"`
}

// Schedule is the timetable file.
type Schedule struct {
	Jobs []Job `yaml:"jobs"`
}

// WhenAlways runs a job whether or not anything has happened.
const WhenAlways = "always"

// DefaultTTL bounds how late a job may run when it does not say.
const DefaultTTL = 4 * time.Hour

// LoadSchedule reads the timetable. A missing file is an empty schedule, not an
// error: most installs never write one.
func LoadSchedule() (*Schedule, error) {
	data, err := os.ReadFile(SchedulePath())
	if os.IsNotExist(err) {
		return &Schedule{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read schedule: %w", err)
	}
	var s Schedule
	if err := yaml.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("parse schedule: %w", err)
	}
	return &s, nil
}

// SaveSchedule writes the timetable atomically.
func SaveSchedule(s *Schedule) error {
	data, err := yaml.Marshal(s)
	if err != nil {
		return fmt.Errorf("marshal schedule: %w", err)
	}
	if err := os.MkdirAll(Dir(), 0755); err != nil {
		return fmt.Errorf("create supatree dir: %w", err)
	}
	tmp := SchedulePath() + ".tmp"
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return fmt.Errorf("write schedule: %w", err)
	}
	return os.Rename(tmp, SchedulePath())
}

// ScheduleState is when each job last fired, keyed by job id.
type ScheduleState struct {
	LastFired map[string]time.Time `json:"last_fired"`
}

// LoadScheduleState reads the fire times, returning an empty state when absent.
func LoadScheduleState() ScheduleState {
	st := ScheduleState{LastFired: map[string]time.Time{}}
	var on ScheduleState
	if err := readJSON(ScheduleStatePath(), &on); err != nil || on.LastFired == nil {
		return st
	}
	return on
}

// SaveScheduleState persists the fire times.
func SaveScheduleState(st ScheduleState) error {
	return writeJSONAtomic(ScheduleStatePath(), st)
}

// Due selects the jobs that should fire now, and returns the state that
// results. It is pure — no clock, no I/O — which is what makes every rule below
// a table test rather than something you discover by being notified at 3am.
//
// Three rules carry the design:
//
// **Seed on first sight.** A job id absent from the state has never fired, and
// the naive reading of "never fired" is "overdue" — so a freshly written
// schedule.yml would fire every entry at once, on a Tuesday afternoon. An
// unseen id is recorded as of now and fires nothing.
//
// **Coalesce missed fires; never replay them.** Close the laptop at 18:00, open
// it at 09:00, and a naive hourly job is fifteen deep. A job that is due fires
// once, however many intervals elapsed — anacron's rule, not cron's.
//
// **A job with nothing to say does not run.** `when: events_since_last` is the
// default, and it is what separates a digest people read from one they filter.
func Due(jobs []Job, st ScheduleState, evs []Event, now time.Time) ([]Job, ScheduleState) {
	next := ScheduleState{LastFired: make(map[string]time.Time, len(st.LastFired)+len(jobs))}
	for k, v := range st.LastFired {
		next.LastFired[k] = v
	}

	var due []Job
	for _, j := range jobs {
		if j.ID == "" || strings.TrimSpace(j.Prompt) == "" {
			continue
		}
		last, seen := next.LastFired[j.ID]
		if !seen {
			next.LastFired[j.ID] = now
			continue
		}
		if !jobDue(j, last, now) {
			continue
		}
		// Record the fire even when the gate below suppresses it: the job *was*
		// due, and leaving the timestamp stale would make it due again on every
		// tick until something finally happened.
		next.LastFired[j.ID] = now
		if !hasSomethingToSay(j, evs, last) {
			continue
		}
		due = append(due, j)
	}
	return due, next
}

// jobDue reports whether a job's own schedule has come round since last.
func jobDue(j Job, last, now time.Time) bool {
	if j.Every != "" {
		d, err := time.ParseDuration(j.Every)
		if err != nil || d <= 0 {
			return false
		}
		return !now.Before(last.Add(d))
	}
	if j.At == "" {
		return false
	}
	hh, mm, ok := parseHHMM(j.At)
	if !ok || !dayAllowed(j.Days, now.Weekday()) {
		return false
	}
	// Today's occurrence, and it has passed, and we have not already fired for
	// it. Comparing against the occurrence rather than counting elapsed time is
	// what makes the daylight-saving cases behave: the clock going back does not
	// manufacture a second 09:00.
	occurrence := time.Date(now.Year(), now.Month(), now.Day(), hh, mm, 0, 0, now.Location())
	return !now.Before(occurrence) && last.Before(occurrence)
}

func parseHHMM(s string) (hh, mm int, ok bool) {
	parts := strings.Split(strings.TrimSpace(s), ":")
	if len(parts) != 2 {
		return 0, 0, false
	}
	if _, err := fmt.Sscanf(parts[0], "%d", &hh); err != nil {
		return 0, 0, false
	}
	if _, err := fmt.Sscanf(parts[1], "%d", &mm); err != nil {
		return 0, 0, false
	}
	if hh < 0 || hh > 23 || mm < 0 || mm > 59 {
		return 0, 0, false
	}
	return hh, mm, true
}

var weekdayNames = map[string]time.Weekday{
	"sun": time.Sunday, "mon": time.Monday, "tue": time.Tuesday, "wed": time.Wednesday,
	"thu": time.Thursday, "fri": time.Friday, "sat": time.Saturday,
}

func dayAllowed(days []string, d time.Weekday) bool {
	if len(days) == 0 {
		return true
	}
	for _, name := range days {
		if w, ok := weekdayNames[strings.ToLower(strings.TrimSpace(name))]; ok && w == d {
			return true
		}
	}
	return false
}

// hasSomethingToSay applies the `when` gate.
func hasSomethingToSay(j Job, evs []Event, since time.Time) bool {
	if j.When == WhenAlways {
		return true
	}
	for _, ev := range evs {
		if !ev.At.After(since) {
			continue
		}
		if j.Tree != "" && ev.Tree != j.Tree {
			continue
		}
		if len(j.Kinds) == 0 {
			return true
		}
		for _, k := range j.Kinds {
			if EventKind(strings.TrimSpace(k)) == ev.Kind {
				return true
			}
		}
	}
	return false
}

// TTLFor returns how late a job may still run.
func (j Job) TTLFor() time.Duration {
	if d, err := time.ParseDuration(j.TTL); err == nil && d > 0 {
		return d
	}
	return DefaultTTL
}

// ToRequest renders a due job as a request for the PM.
func (j Job) ToRequest(now time.Time) Request {
	return Request{
		At:   now,
		From: "schedule:" + j.ID,
		Tree: j.Tree,
		Text: strings.TrimSpace(j.Prompt),
	}
}

// Expired reports whether a queued scheduled request is too old to be worth
// running. Dropping beats running late: a standup that arrives seven hours
// after the morning is noise that looks like a bug.
func (j Job) Expired(firedAt, now time.Time) bool {
	return now.Sub(firedAt) > j.TTLFor()
}

// RemoveJob drops a one-shot job from the timetable after it has fired.
func RemoveJob(id string) error {
	s, err := LoadSchedule()
	if err != nil {
		return err
	}
	out := s.Jobs[:0]
	for _, j := range s.Jobs {
		if j.ID != id {
			out = append(out, j)
		}
	}
	s.Jobs = out
	return SaveSchedule(s)
}
