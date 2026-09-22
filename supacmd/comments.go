package supacmd

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/panamafrancis/workbench/pkg/github"
	"github.com/panamafrancis/workbench/pkg/supatree"
)

var (
	commentsJSON  bool
	commentsForce bool
	commentsAll   bool
)

var commentsCmd = &cobra.Command{
	Use:   "comments <supatree> [repo]",
	Short: "Show the review feedback on a supatree's PRs",
	Long: "comments reads what reviewers have said on a member repo's pull request:\n" +
		"top-level comments, review verdicts, and line-anchored threads with their\n" +
		"resolved state. Unresolved threads are the part still waiting on an answer.\n\n" +
		"Unlike status, this costs GitHub API quota — thread resolution only exists in\n" +
		"the GraphQL API, so it is a second query per PR. It is therefore fetched only\n" +
		"when asked for, never on a timer, and cached until the PR changes.",
	Args: cobra.RangeArgs(1, 2),
	RunE: func(cmd *cobra.Command, args []string) error {
		inst, err := supatree.Get(stCfg, args[0])
		if err != nil {
			return err
		}
		cache := github.NewCache(supatree.PRCachePath())
		_ = cache.Load()

		aliases := []string{}
		switch {
		case len(args) == 2:
			aliases = append(aliases, args[1])
		case commentsAll:
			for _, m := range inst.Members {
				aliases = append(aliases, m.Alias)
			}
		default:
			return fmt.Errorf("name a member repo, or pass --all for every one of them")
		}

		out := map[string]*github.PRFeedback{}
		for _, alias := range aliases {
			fb, err := supatree.Comments(inst, alias, cache, commentsForce)
			if err != nil {
				// With --all, a member that has no PR yet is the normal case
				// rather than a failure; say so and carry on.
				if len(aliases) > 1 {
					fmt.Fprintf(os.Stderr, "%s: %v\n", alias, err)
					continue
				}
				return err
			}
			out[alias] = fb
			if !commentsJSON {
				fmt.Println(supatree.FormatFeedback(alias, fb))
			}
		}
		if commentsJSON {
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			return enc.Encode(out)
		}
		return nil
	},
}

func init() {
	commentsCmd.Flags().BoolVar(&commentsJSON, "json", false, "output the raw feedback as JSON")
	commentsCmd.Flags().BoolVar(&commentsForce, "force", false, "re-fetch even if the cached copy is still valid")
	commentsCmd.Flags().BoolVar(&commentsAll, "all", false, "every member repo with a PR")
}
