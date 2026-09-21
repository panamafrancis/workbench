package supacmd

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/panamafrancis/workbench/pkg/supatree"
)

var scheduleCmd = &cobra.Command{
	Use:   "schedule",
	Short: "Recurring PM work (standups, triage, reminders)",
	Long: "schedule lists the jobs in ~/.supatree/schedule.yml with when each last fired.\n" +
		"A due job is fired by the watcher, which queues it for the PM exactly as the\n" +
		"sidebar's `m` does — so the PM needs no timer of its own, which is the point:\n" +
		"an agent mid-turn, blocked, or restarted cannot be trusted to hold one.\n\n" +
		"Jobs read the cache and never fetch, so a timetable costs no API quota.",
	RunE: func(cmd *cobra.Command, args []string) error {
		sched, err := supatree.LoadSchedule()
		if err != nil {
			return err
		}
		if len(sched.Jobs) == 0 {
			fmt.Printf("No scheduled jobs. Create some in %s:\n\n%s", supatree.SchedulePath(), scheduleExample)
			return nil
		}
		st := supatree.LoadScheduleState()
		fmt.Printf("%-16s %-12s %-22s %s\n", "ID", "WHEN", "LAST FIRED", "PROMPT")
		for _, j := range sched.Jobs {
			when := j.Every
			if when == "" {
				when = j.At
				if len(j.Days) > 0 {
					when += " " + strings.Join(j.Days, ",")
				}
			}
			last := "never"
			if t, ok := st.LastFired[j.ID]; ok {
				last = t.Local().Format("2006-01-02 15:04")
			}
			fmt.Printf("%-16s %-12s %-22s %s\n", j.ID, when, last, truncateLine(j.Prompt, 50))
		}
		return nil
	},
}

var scheduleRunCmd = &cobra.Command{
	Use:   "run <id>",
	Short: "Fire one job now, for testing its prompt",
	Long: "run queues a job immediately without waiting for its schedule.\n\n" +
		"It deliberately does NOT touch the last-fired state: testing a job must not\n" +
		"silently skip its next real run.",
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		sched, err := supatree.LoadSchedule()
		if err != nil {
			return err
		}
		for _, j := range sched.Jobs {
			if j.ID != args[0] {
				continue
			}
			if err := supatree.AppendRequest(j.ToRequest(time.Now().UTC())); err != nil {
				return err
			}
			fmt.Printf("queued %q for the PM\n", j.ID)
			return nil
		}
		return fmt.Errorf("no job %q in %s", args[0], supatree.SchedulePath())
	},
}

// scheduleExample is printed when there is no timetable yet. A blank file is a
// feature nobody discovers, so the empty state shows what one looks like.
const scheduleExample = `jobs:
  - id: standup
    at: "09:00"
    days: [mon, tue, wed, thu, fri]
    when: events_since_last          # default: stay silent when nothing moved
    prompt: "Summarise what moved since yesterday. Update the boards; notify only if something is blocked."

  - id: triage
    every: 1h
    kinds: [changes_requested, checks_failed]
    prompt: "For each affected PR, read the comments and brief that tree's agent."

  - id: reap
    at: "17:00"
    days: [fri]
    when: always
    prompt: "List every done supatree and propose reaping it. Do not remove anything."
`

func truncateLine(s string, n int) string {
	s = strings.ReplaceAll(strings.TrimSpace(s), "\n", " ")
	// Runes, not bytes: a prompt is prose, and slicing one mid-rune prints a
	// replacement character in the middle of the table.
	r := []rune(s)
	if n < 1 || len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

var scheduleInitCmd = &cobra.Command{
	Use:   "init",
	Short: "Write an example schedule.yml",
	RunE: func(cmd *cobra.Command, args []string) error {
		if _, err := os.Stat(supatree.SchedulePath()); err == nil {
			return fmt.Errorf("%s already exists", supatree.SchedulePath())
		}
		if err := os.MkdirAll(supatree.Dir(), 0755); err != nil {
			return err
		}
		if err := os.WriteFile(supatree.SchedulePath(), []byte(scheduleExample), 0644); err != nil {
			return err
		}
		fmt.Printf("wrote %s\n", supatree.SchedulePath())
		return nil
	},
}

func init() {
	scheduleCmd.AddCommand(scheduleRunCmd)
	scheduleCmd.AddCommand(scheduleInitCmd)
}
