package supacmd

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/panamafrancis/workbench/pkg/github"
	"github.com/panamafrancis/workbench/pkg/supatree"
)

var (
	statusJSON    bool
	statusRefresh bool
	statusAll     bool
	statusStale   time.Duration
)

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show the activity state of every supatree (open / pushed / approved / merged / stale)",
	Long: "status derives where each supatree sits in the ship lifecycle from local git\n" +
		"state plus the cached GitHub PR status. It reads the PR cache rather than\n" +
		"querying GitHub, so it costs no API quota; pass --refresh to fetch first.",
	RunE: func(cmd *cobra.Command, args []string) error {
		insts, err := supatree.List(stCfg)
		if err != nil {
			return err
		}
		cache := github.NewCache(supatree.PRCachePath())
		_ = cache.Load()

		if statusRefresh {
			if cache.InBackoff(time.Now()) {
				fmt.Fprintln(os.Stderr, "gh fetches are paused (rate limited) — showing cached status")
			} else {
				targets := supatree.FetchTargets(insts, cache, true, supatree.PRStaleAge)
				out := supatree.FetchPRs(targets, cache, true, supatree.PRStaleAge)
				switch {
				case out.Skipped:
					fmt.Fprintln(os.Stderr, "another supatree process is fetching — showing cached status")
				case out.Err != nil:
					fmt.Fprintf(os.Stderr, "PR fetch incomplete: %v\n", out.Err)
				}
			}
		}

		sum := supatree.Status(insts, cache, supatree.StatusOptions{StaleAfter: statusStale})
		if statusJSON {
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			return enc.Encode(sum)
		}
		printSummary(sum, statusAll)
		return nil
	},
}

func printSummary(sum supatree.Summary, all bool) {
	if len(sum.Trees) == 0 {
		fmt.Println("No supatrees. Create one with: supatree new")
		return
	}
	fmt.Printf("%d supatrees · %d open PRs · %d approved · %d stale · %d done%s\n\n",
		len(sum.Trees), sum.OpenPRs, sum.ApprovedPRs, sum.Stale, sum.Done, fetchedSuffix(sum))

	for _, t := range sum.Trees {
		fmt.Printf("%-24s %-10s %-12s %s\n", t.Name, t.Stack, t.State, strings.Join(treeFlags(t, sum.At), " "))
		if !all && t.State == supatree.TreeDone {
			continue
		}
		for _, m := range t.Members {
			pr, detail := memberDetail(m)
			fmt.Printf("    %-22s %-10s %-8s %s\n", m.Alias, m.State, pr, detail)
		}
	}
}

func fetchedSuffix(sum supatree.Summary) string {
	if sum.LastFetch.IsZero() {
		return " · PR status never fetched"
	}
	if d := sum.At.Sub(sum.LastFetch); d >= time.Minute {
		return fmt.Sprintf(" · PR status fetched %s ago", ago(d))
	}
	return " · PR status just fetched"
}

// treeFlags renders the attention-worthy facts about a supatree: what is
// blocking it, whether it has gone quiet, and whether it is finished.
func treeFlags(t supatree.TreeStatus, now time.Time) []string {
	var out []string
	if t.TotalPRs > 0 {
		out = append(out, fmt.Sprintf("%d/%d PRs open", t.OpenPRs, t.TotalPRs))
	}
	if t.ApprovedPRs > 0 {
		out = append(out, fmt.Sprintf("%d approved", t.ApprovedPRs))
	}
	if t.Blocked {
		out = append(out, "BLOCKED")
	}
	if t.Dirty {
		out = append(out, "dirty")
	}
	if t.Stale {
		out = append(out, "stale")
	}
	if t.Reap() {
		out = append(out, "→ supatree rm "+t.Name)
	}
	if !t.LastActivity.IsZero() {
		out = append(out, ago(now.Sub(t.LastActivity)))
	}
	return out
}

// memberDetail splits a member's line into its PR column and the free-form
// notes that follow, so the columns stay aligned however much detail a member
// happens to have.
func memberDetail(m supatree.MemberStatus) (pr, detail string) {
	var parts []string
	if m.PR != nil && m.PR.Number > 0 {
		pr = fmt.Sprintf("#%d", m.PR.Number)
		if m.PR.Review != github.ReviewNone {
			parts = append(parts, string(m.PR.Review))
		}
		if m.PR.Checks != github.CheckNone {
			parts = append(parts, "checks "+string(m.PR.Checks))
		}
	}
	if m.Dirty {
		parts = append(parts, "dirty")
	}
	if m.Unpushed > 0 {
		parts = append(parts, fmt.Sprintf("%d unpushed", m.Unpushed))
	} else if m.PR == nil && m.Ahead > 0 {
		parts = append(parts, fmt.Sprintf("%d commits", m.Ahead))
	}
	parts = append(parts, m.Branch)
	return pr, strings.Join(parts, "  ")
}

// ago renders a duration the way a dashboard column wants it: one unit, no
// decimals, so the column stays narrow.
func ago(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}

func init() {
	statusCmd.Flags().BoolVar(&statusJSON, "json", false, "emit the full summary as JSON")
	statusCmd.Flags().BoolVar(&statusRefresh, "refresh", false, "fetch PR status from GitHub before reporting")
	statusCmd.Flags().BoolVar(&statusAll, "all", false, "list members of finished supatrees too")
	statusCmd.Flags().DurationVar(&statusStale, "stale-after", supatree.DefaultStaleAfter, "how long without a commit before a supatree is stale")
}
