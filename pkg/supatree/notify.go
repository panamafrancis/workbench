package supatree

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Tier is how loudly an event is allowed to arrive. Getting this table wrong is
// what makes people turn notifications off, so it is data rather than scattered
// conditionals.
type Tier string

const (
	// TierNever is not recorded at all.
	TierNever Tier = "never"
	// TierGlyph only changes a sidebar row.
	TierGlyph Tier = "glyph"
	// TierBoard is worth writing down but must not interrupt.
	TierBoard Tier = "board"
	// TierDesktop interrupts you with an OS notification.
	TierDesktop Tier = "desktop"
)

// eventTier maps each event kind to its loudest permitted surface. Only the
// two kinds that mean "a human is now blocking on you" reach the desktop.
var eventTier = map[EventKind]Tier{
	EventPushed:           TierGlyph,
	EventPROpened:         TierGlyph,
	EventChecksPassed:     TierGlyph,
	EventApproved:         TierBoard,
	EventMerged:           TierBoard,
	EventClosed:           TierBoard,
	EventTreeDone:         TierBoard,
	EventStale:            TierBoard,
	EventChangesRequested: TierDesktop,
	EventChecksFailed:     TierDesktop,
}

// TierOf reports the loudest surface an event kind may use. An unknown kind —
// a ledger written by a newer version — stays silent rather than defaulting to
// the loudest option.
func TierOf(k EventKind) Tier {
	if t, ok := eventTier[k]; ok {
		return t
	}
	return TierNever
}

// DefaultCooldown is how long a given (kind, tree, member) stays quiet after it
// has interrupted you once.
const DefaultCooldown = 30 * time.Minute

// cooldownFor lets the flappable kinds stay quiet for longer.
//
// Note what this is and is not defending against. A persisted baseline makes
// Diff edge-triggered, so a check that is *still* failing emits nothing on the
// next round — repeats are already impossible. The cooldown covers the two
// cases that defeat edge-triggering: a watcher restarted against a missing
// last-status.json, and genuine flapping, where fail → pass → fail produces
// three real transitions from one broken build.
func cooldownFor(k EventKind) time.Duration {
	if k == EventChecksFailed {
		return 2 * time.Hour
	}
	return DefaultCooldown
}

// NotifyState is the notifier's memory: when each dedupe key last interrupted
// you. It is disposable — losing it costs at most one duplicate notification.
type NotifyState struct {
	Sent map[string]time.Time `json:"sent"`
}

// DecideOptions tunes a Decide call. The zero value suppresses nothing and uses
// the default cooldowns.
type DecideOptions struct {
	// FocusedTree is the supatree you are currently looking at, whose desktop
	// notifications are redundant. Empty means "could not tell", which
	// deliberately suppresses nothing: a missed suppression is an annoyance, a
	// missed notification is a bug.
	FocusedTree string
}

// Decide selects which events warrant interrupting you, and returns the
// notifier state that results. It is pure — no clock, no I/O, no process
// launching — which is what makes the whole notification policy a table test.
//
// Events are assumed to arrive oldest-first, as the ledger stores them.
func Decide(evs []Event, st NotifyState, opts DecideOptions) ([]Event, NotifyState) {
	next := NotifyState{Sent: make(map[string]time.Time, len(st.Sent)+len(evs))}
	for k, v := range st.Sent {
		next.Sent[k] = v
	}

	var out []Event
	for _, ev := range evs {
		if TierOf(ev.Kind) != TierDesktop {
			continue
		}
		if opts.FocusedTree != "" && ev.Tree == opts.FocusedTree {
			continue
		}
		key := ev.Key()
		if last, seen := next.Sent[key]; seen && ev.At.Sub(last) < cooldownFor(ev.Kind) {
			continue
		}
		next.Sent[key] = ev.At
		out = append(out, ev)
	}
	return out, next
}

// PruneNotifyState drops entries older than the longest cooldown, so the file
// does not accumulate a key per member repo forever.
func PruneNotifyState(st NotifyState, now time.Time) NotifyState {
	const maxAge = 24 * time.Hour
	out := NotifyState{Sent: make(map[string]time.Time, len(st.Sent))}
	for k, v := range st.Sent {
		if now.Sub(v) < maxAge {
			out.Sent[k] = v
		}
	}
	return out
}

// LoadNotifyState reads the notifier state, returning an empty one when absent
// or unreadable — it is a cache, and re-seeding it is cheap.
func LoadNotifyState() NotifyState {
	data, err := os.ReadFile(NotifyStatePath())
	if err != nil {
		return NotifyState{Sent: map[string]time.Time{}}
	}
	var st NotifyState
	if err := json.Unmarshal(data, &st); err != nil || st.Sent == nil {
		return NotifyState{Sent: map[string]time.Time{}}
	}
	return st
}

// SaveNotifyState writes the notifier state atomically.
func SaveNotifyState(st NotifyState) error {
	return writeJSONAtomic(NotifyStatePath(), st)
}

// LoadLastSummary reads the previous round's summary. A missing file yields the
// zero Summary, which Diff treats as "nothing observed yet" and therefore
// reports no events for — that is what makes a first run quiet.
func LoadLastSummary() Summary {
	data, err := os.ReadFile(LastSummaryPath())
	if err != nil {
		return Summary{}
	}
	var s Summary
	if err := json.Unmarshal(data, &s); err != nil {
		return Summary{}
	}
	return s
}

// SaveLastSummary persists the current summary as the next round's baseline.
func SaveLastSummary(s Summary) error {
	return writeJSONAtomic(LastSummaryPath(), s)
}

func writeJSONAtomic(path string, v any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf("create dir: %w", err)
	}
	data, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("marshal %s: %w", filepath.Base(path), err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return fmt.Errorf("write %s: %w", filepath.Base(path), err)
	}
	return os.Rename(tmp, path)
}

// DefaultNotifyCommand is the macOS notifier. It is config, not code, so Linux
// points notify_command at notify-send without a patch.
func DefaultNotifyCommand() []string {
	return []string{"osascript", "-e", `display notification "{text}" with title "{title}"`}
}

// notifyTimeout bounds a notifier that hangs; the watcher must not wedge
// because a helper is waiting on something.
const notifyTimeout = 10 * time.Second

// Notify delivers one notification by running argv with {title} and {text}
// substituted. Values are sanitized rather than escaped per-target: the text is
// short human prose, and stripping quotes and control characters removes the
// injection risk for every possible notifier at no cost to legibility.
func Notify(ctx context.Context, argv []string, title, text string) error {
	if len(argv) == 0 {
		argv = DefaultNotifyCommand()
	}
	repl := strings.NewReplacer("{title}", sanitizeNotify(title), "{text}", sanitizeNotify(text))
	out := make([]string, len(argv))
	for i, a := range argv {
		out[i] = repl.Replace(a)
	}
	ctx, cancel := context.WithTimeout(ctx, notifyTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, out[0], out[1:]...)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("notify: %w", err)
	}
	return nil
}

// notifyStrip is every character that could end a string literal or start a new
// command in a notifier's own quoting. The substituted value can land inside an
// AppleScript literal (the macOS default) or inside a `sh -c` word, and quoting
// per target is not possible when the argv is user config — so the set is the
// union rather than the intersection: quoting, escaping, expansion, command
// substitution, redirection, and statement separators all go.
// Control characters are not listed: they become spaces below, so a multi-line
// body stays readable instead of running together.
const notifyStrip = "\"'`$\\;|&<>(){}"

// sanitizeNotify strips what could break out of a notifier's own quoting
// (AppleScript string literals, shell words) and bounds the length.
func sanitizeNotify(s string) string {
	s = strings.Map(func(r rune) rune {
		switch {
		case strings.ContainsRune(notifyStrip, r):
			return -1
		case r < ' ':
			return ' '
		default:
			return r
		}
	}, s)
	s = strings.TrimSpace(s)
	if r := []rune(s); len(r) > 200 {
		return string(r[:200]) + "…"
	}
	return s
}

// readJSON decodes a JSON file into v.
func readJSON(path string, v any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, v)
}
