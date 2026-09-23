package supacmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
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
			// Only the winner owns the log: a loser rotating it would move the
			// file out from under the watcher that is actually running.
			if !watchOnce {
				openWatchLog()
				defer closeWatchLog()
			}
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
	// Launches have their own, much faster tick: someone is waiting to see the
	// agent appear, and checking is one stat of a file, not a GitHub round.
	launchTicker := time.NewTicker(launchPollInterval)
	defer launchTicker.Stop()

	if err := watchRound(ctx); err != nil {
		logWatch("round failed: %v", err)
	}
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-launchTicker.C:
			runLaunches(ctx)
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
	if !cache.InBackoff(github.ResourceCore, time.Now()) {
		if out := supatree.FetchPRs(supatree.FetchTargets(insts), cache, false); out.Err != nil {
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
	if _, err := supatree.ForwardPMMail(insts); err != nil {
		logWatch("forward PM mail: %v", err)
	}
	runLaunches(ctx)
	return nil
}

// launchPollInterval is how often the watcher checks the launch queue.
const launchPollInterval = 2 * time.Second

// runLaunches opens the agents the PM has asked to have started.
//
// The PM cannot open a tab itself: it runs inside nono, and the watcher is the
// only supatree process outside it. Each launch runs as a child `supatree open
// --background`, which reuses the ordinary open path unchanged — the child is
// pointed at the session through ZELLIJ_SESSION_NAME, exactly as `--session`
// already does — and keeps one failed launch from touching the daemon.
func runLaunches(ctx context.Context) {
	if _, err := os.Stat(supatree.LaunchPath()); err != nil {
		return
	}
	reqs, err := supatree.DrainLaunches()
	if err != nil {
		logWatch("drain launches: %v", err)
		return
	}
	exe, err := os.Executable()
	if err != nil {
		logWatch("launch: %v", err)
		return
	}
	for _, r := range reqs {
		session := r.Session
		if session == "" {
			session = watchSession
		}
		if session == "" {
			logWatch("launch %s:%s: no zellij session to open it in (start the watcher with --session)", r.Tree, r.Agent)
			continue
		}
		// An unreadable session list is not proof the session is gone, so only
		// a definite "not running" skips the launch.
		if alive, err := zellij.SessionAlive(session); err == nil && !alive {
			logWatch("launch %s:%s: zellij session %s is not running — open the agent from the sidebar instead", r.Tree, r.Agent, session)
			continue
		}
		lctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
		cmd := exec.CommandContext(lctx, exe, "open", r.Tree, "--agent", r.Agent, "--session", session, "--background")
		out, err := cmd.CombinedOutput()
		cancel()
		if err != nil {
			logWatch("launch %s:%s: %v: %s", r.Tree, r.Agent, err, strings.TrimSpace(string(out)))
			continue
		}
		logWatch("launched %s:%s in %s", r.Tree, r.Agent, session)
	}
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
	// dump-layout sent to a session that is shutting down makes zellij's
	// server panic ("Failed to dump layout"). The watcher only notices a dead
	// session on its next tick, so check before asking.
	if alive, err := zellij.SessionAlive(watchSession); err != nil || !alive {
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

// watchLogMax is the size at which watch.log is rotated to watch.log.1. One
// generation, like the event ledger: enough to see what led up to a problem,
// bounded so a repeating error cannot fill the disk.
const watchLogMax = 1 << 20

var watchLog struct {
	f    *os.File
	size int64
}

// openWatchLog points logWatch at ~/.supatree/logs/watch.log. Best effort: if
// it cannot be opened, lines go to stderr as before.
func openWatchLog() {
	if err := os.MkdirAll(supatree.LogsDir(), 0755); err != nil {
		return
	}
	path := watchLogPath()
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return
	}
	watchLog.f = f
	if st, err := f.Stat(); err == nil {
		watchLog.size = st.Size()
	}
}

func closeWatchLog() {
	if watchLog.f != nil {
		_ = watchLog.f.Close()
		watchLog.f = nil
	}
}

func watchLogPath() string { return filepath.Join(supatree.LogsDir(), "watch.log") }

// rotateWatchLog moves a full log aside and starts a fresh one. Rotating
// before the write, not after, so the line that tripped it lands in the new
// file rather than the one being renamed away.
func rotateWatchLog() {
	closeWatchLog()
	_ = os.Rename(watchLogPath(), watchLogPath()+".1")
	openWatchLog()
}

// logWatch writes a diagnostic line to watch.log, or to stderr when the
// watcher has no log of its own (a --once run from a shell or cron).
func logWatch(format string, args ...any) {
	line := fmt.Sprintf("[%s] "+format+"\n", append([]any{time.Now().Format(time.RFC3339)}, args...)...)
	if watchLog.f == nil {
		fmt.Fprint(os.Stderr, line)
		return
	}
	if watchLog.size+int64(len(line)) > watchLogMax {
		rotateWatchLog()
		if watchLog.f == nil {
			fmt.Fprint(os.Stderr, line)
			return
		}
	}
	n, _ := watchLog.f.WriteString(line)
	watchLog.size += int64(n)
}

func init() {
	watchCmd.Flags().BoolVar(&watchOnce, "once", false, "run a single round and exit (for cron or scripts)")
	watchCmd.Flags().StringVar(&watchSession, "session", "", "zellij session to consult for the focused tab")
	watchCmd.Flags().BoolVar(&watchQuiet, "quiet", false, "exit silently when another watcher holds the lock")
}
