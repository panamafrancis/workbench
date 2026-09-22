package supacmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/panamafrancis/workbench/pkg/config"
	"github.com/panamafrancis/workbench/pkg/github"
	"github.com/panamafrancis/workbench/pkg/supatree"
	"github.com/panamafrancis/workbench/pkg/zellij"
)

var (
	watchOnce    bool
	watchSession string
	watchQuiet   bool
)

var watchCmd = &cobra.Command{
	Use:   "watch",
	Short: "Watch every supatree and notify when something needs you",
	Long: "watch is the single background poller. It refreshes PR status on the shared\n" +
		"staleness gate, derives the activity summary, diffs it against the previous\n" +
		"round, appends what changed to ~/.supatree/events.jsonl, and delivers the few\n" +
		"events that warrant interrupting you as desktop notifications.\n\n" +
		"It is a singleton: a second instance exits quietly rather than doubling the\n" +
		"GitHub API load. `supatree start` spawns one automatically, so running this by\n" +
		"hand is for debugging or for a cron entry that covers the hours when no\n" +
		"session is open.",
	RunE: func(cmd *cobra.Command, args []string) error {
		err := config.TryFileLock(supatree.WatchLockPath(), func() error {
			return runWatch(cmd.Context())
		})
		// Losing the election is the designed outcome, not a failure: it is what
		// stops a cron entry double-fetching while the daemon is up.
		if errors.Is(err, config.ErrLockBusy) {
			if !watchQuiet {
				fmt.Fprintln(os.Stderr, "another supatree watcher is running — nothing to do")
			}
			return nil
		}
		return err
	},
}

func runWatch(ctx context.Context) error {
	if watchOnce {
		return watchRound(ctx)
	}

	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	interval := stCfg.ResolveWatchInterval()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	if err := watchRound(ctx); err != nil {
		logWatch("round failed: %v", err)
	}
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			// The daemon is spawned by `supatree start` and outlives its parent
			// (start execs into zellij), so nothing else will ever reap it. Its
			// own exit condition is the absence of the sessions it exists to
			// serve — without this it polls GitHub forever.
			if !watchSessionsAlive() {
				logWatch("no supatree sessions left — exiting")
				return nil
			}
			if err := watchRound(ctx); err != nil {
				logWatch("round failed: %v", err)
			}
		}
	}
}

// watchRound is one full pass: refresh, derive, diff, record, notify.
func watchRound(ctx context.Context) error {
	// Re-read the registry every round: a stack registered while the daemon has
	// been up must not need a restart to be watched.
	cfg, err := supatree.Load()
	if err != nil {
		return fmt.Errorf("load supatree config: %w", err)
	}
	insts, err := supatree.List(cfg)
	if err != nil {
		return err
	}

	cache := github.NewCache(supatree.PRCachePath())
	_ = cache.Load()
	if !cache.InBackoff(time.Now()) {
		targets := supatree.FetchTargets(insts, cache, false, supatree.PRStaleAge)
		if out := supatree.FetchPRs(targets, cache, false, supatree.PRStaleAge); out.Err != nil {
			// A failed fetch is not a failed round: the summary below is still
			// derivable from local git plus whatever the cache already holds.
			logWatch("PR fetch incomplete: %v", out.Err)
		}
	}

	cur := supatree.Status(insts, cache, supatree.StatusOptions{})
	evs := supatree.Diff(supatree.LoadLastSummary(), cur)
	// Drain before recording: an outbox event is news like any other, and a
	// board-tier one exists precisely to be written down rather than delivered.
	// Recording only the diff would silently discard everything the PM said it
	// had recorded quietly.
	evs = append(evs, drainNotifyOutbox()...)

	if err := supatree.AppendEvents(evs); err != nil {
		return err
	}
	// Persist the baseline before notifying. A notifier that hangs or crashes
	// must not cause the same round to be diffed again on restart, which would
	// replay every event in it.
	if err := supatree.SaveLastSummary(cur); err != nil {
		return err
	}

	deliver(ctx, cfg, evs, cur.At)
	runSchedule(cur.At)
	return nil
}

// runSchedule fires any due jobs by queueing them for the PM.
//
// It lives in the watcher rather than in its own daemon because the watcher
// already has a loop, already holds the singleton lock, and already runs
// whether or not a PM tab is open. Firing a job is an append to the same
// request queue the sidebar uses, so the PM needs no timer and no new channel:
// a scheduled job and a pressed `m` arrive identically.
func runSchedule(now time.Time) {
	sched, err := supatree.LoadSchedule()
	if err != nil {
		logWatch("schedule: %v", err)
		return
	}
	if len(sched.Jobs) == 0 {
		return
	}
	// Scheduled work reads the cache and never fetches, which is why N jobs
	// across M trees cost nothing: the events below are the ones this round
	// already derived.
	evs, err := supatree.ReadEvents(now.Add(-scheduleEventWindow))
	if err != nil {
		logWatch("schedule events: %v", err)
		return
	}
	due, next := supatree.Due(sched.Jobs, supatree.LoadScheduleState(), evs, now)
	if err := supatree.SaveScheduleState(next); err != nil {
		logWatch("save schedule state: %v", err)
	}
	for _, j := range due {
		if err := supatree.AppendRequest(j.ToRequest(now)); err != nil {
			logWatch("queue %s: %v", j.ID, err)
			continue
		}
		if j.Once {
			if err := supatree.RemoveJob(j.ID); err != nil {
				logWatch("remove one-shot %s: %v", j.ID, err)
			}
		}
	}
}

// scheduleEventWindow bounds how far back the `events_since_last` gate looks.
// A job that has not fired in longer than this asks about a window it cannot
// see, which is the right way round: it fires, and reports that it has been a
// while, rather than replaying a week.
const scheduleEventWindow = 7 * 24 * time.Hour

// deliver applies the tier policy and sends what survives it.
func deliver(ctx context.Context, cfg *supatree.Config, evs []supatree.Event, now time.Time) {
	if len(evs) == 0 {
		return
	}
	state := supatree.LoadNotifyState()
	due, next := supatree.Decide(evs, state, supatree.DecideOptions{FocusedTree: focusedTree()})
	if err := supatree.SaveNotifyState(supatree.PruneNotifyState(next, now)); err != nil {
		logWatch("save notify state: %v", err)
	}

	argv := cfg.ResolveNotifyCommand()
	for _, ev := range due {
		title := ev.Tree
		if ev.Member != "" {
			title = ev.Tree + "/" + ev.Member
		}
		if err := supatree.Notify(ctx, argv, title, ev.Text); err != nil {
			logWatch("notify: %v", err)
		}
	}
}

// drainNotifyOutbox takes everything sandboxed processes have queued for
// delivery and empties the file. The watcher is the only process outside nono,
// so it is the only one that can reach the desktop; the PM and anything else
// append here. They go through the same Decide call as the watcher's own
// events, because an outbox that bypassed the tier policy would be used as a
// way around it.
func drainNotifyOutbox() []supatree.Event {
	var evs []supatree.Event
	err := config.WithFileLock(supatree.EventsLockPath(), func() error {
		data, err := os.ReadFile(supatree.NotifyPath())
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			return err
		}
		for _, line := range strings.Split(string(data), "\n") {
			if strings.TrimSpace(line) == "" {
				continue
			}
			var ev supatree.Event
			if err := json.Unmarshal([]byte(line), &ev); err != nil {
				continue
			}
			evs = append(evs, ev)
		}
		return os.Remove(supatree.NotifyPath())
	})
	if err != nil {
		logWatch("drain notify outbox: %v", err)
	}
	return evs
}

// focusedTree is the supatree you are currently looking at, whose notifications
// would be telling you what is already on your screen. Tab names are the tree
// name for the main agent and "<tree>:<agent>" otherwise.
//
// The watcher runs outside every zellij session, so a bare `zellij action` has
// nothing to target and the session must be named explicitly. Failure yields
// "", which suppresses nothing — deliberately, since a missed suppression is an
// annoyance and a missed notification is a bug.
func focusedTree() string {
	if watchSession == "" {
		return ""
	}
	tab := zellij.FocusedTab(watchSession)
	if tab == "" {
		return ""
	}
	name, _, _ := strings.Cut(tab, ":")
	return name
}

// watchSessionsAlive reports whether any supatree zellij session is still up.
func watchSessionsAlive() bool {
	sessions, err := zellij.ListSessions()
	if err != nil {
		// Treat an unreadable session list as "still alive": exiting on a
		// transient zellij error would silently stop notifications for the rest
		// of the day, which is far worse than one extra round of polling.
		return true
	}
	prefix := supatreeWorkspace().SessionPrefix
	for _, s := range sessions {
		if strings.HasPrefix(s.Name, prefix) && !s.Exited {
			return true
		}
	}
	return false
}

// logWatch writes a diagnostic line. The daemon's stdio is redirected to a log
// file by whoever spawned it, so this is simply stderr.
func logWatch(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "[%s] "+format+"\n", append([]any{time.Now().Format(time.RFC3339)}, args...)...)
}

func init() {
	watchCmd.Flags().BoolVar(&watchOnce, "once", false, "run a single round and exit (for cron or scripts)")
	watchCmd.Flags().StringVar(&watchSession, "session", "", "zellij session to consult for the focused tab")
	watchCmd.Flags().BoolVar(&watchQuiet, "quiet", false, "exit silently when another watcher holds the lock")
}
