package supacmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/panamafrancis/workbench/pkg/supatree"
)

var (
	reviewStack  string
	reviewName   string
	reviewModel  string
	reviewIntent string
)

var reviewCmd = &cobra.Command{
	Use:   "review <pr-url>...",
	Short: "Create a review tree: one worktree per repo, checked out at foreign PR heads",
	Long: "review creates a supatree for reviewing someone else's cross-repo change.\n\n" +
		"Each pull request's repo is checked out at that PR's head, on a tree-local\n" +
		"branch, so every repo of the change can be read, built and tested together.\n" +
		"The authoring commands (rename-branch, create_pr, create_prs) refuse in a\n" +
		"review tree: the branches belong to the PRs' authors.\n\n" +
		"Pull requests are given as URLs or as owner/repo#number.",
	Args: cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		refs, err := supatree.ResolvePRRefs(args)
		if err != nil {
			return err
		}
		inst, report, err := supatree.NewReview(stCfg, wbCfg, supatree.ReviewOptions{
			Stack:  reviewStack,
			Name:   reviewName,
			Model:  reviewModel,
			Intent: reviewIntent,
			PRs:    refs,
		})
		if err != nil {
			return err
		}
		for _, w := range report.Warnings {
			fmt.Fprintf(os.Stderr, "warning: %s\n", w)
		}
		fmt.Printf("created review tree %q at %s\n", inst.Name, inst.Root)
		for _, m := range inst.Members {
			if m.Review == nil {
				fmt.Printf("  %-20s %s (no PR — on the base branch)\n", m.Alias, m.Branch)
				continue
			}
			fmt.Printf("  %-20s %s#%d  %s\n", m.Alias, m.Review.Repo, m.Review.Number, m.Branch)
		}
		fmt.Printf("open it with: supatree open %s\n", inst.Name)
		return nil
	},
}

func init() {
	reviewCmd.Flags().StringVar(&reviewStack, "stack", "", "stack alias (required if more than one is registered)")
	reviewCmd.Flags().StringVar(&reviewName, "name", "", "tree name (auto-generated city name if omitted)")
	reviewCmd.Flags().StringVar(&reviewModel, "model", "", "default model for agents (default: stack/registry default)")
	reviewCmd.Flags().StringVar(&reviewIntent, "intent", "", "what this review is for")
}

var reviewRefreshCmd = &cobra.Command{
	Use:   "refresh [name]",
	Short: "Re-fetch the reviewed PR heads and move the worktrees onto them",
	Long: "Authors push during review. refresh re-fetches each tracked pull request's\n" +
		"head, reports which moved, and moves the member worktrees onto them.\n\n" +
		"A member with uncommitted changes is never reset — review notes are not\n" +
		"disposable — and is reported instead.",
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name, err := resolveTreeName(args)
		if err != nil {
			return err
		}
		results, err := supatree.RefreshReview(stCfg, wbCfg, name)
		if err != nil {
			return err
		}
		moved := 0
		for _, r := range results {
			switch {
			case r.Skipped != "":
				fmt.Printf("  %-20s %s: %s\n", r.Alias, r.PR, r.Skipped)
			case r.Moved:
				moved++
				fmt.Printf("  %-20s %s: %s → %s\n", r.Alias, r.PR, short(r.Was), short(r.Now))
			default:
				fmt.Printf("  %-20s %s: unchanged\n", r.Alias, r.PR)
			}
		}
		if moved == 0 {
			fmt.Println("nothing moved — the tree still matches the pull requests")
		} else {
			fmt.Printf("%d member(s) moved. Comments anchored to the old commits will read as outdated on GitHub.\n", moved)
		}
		return nil
	},
}

func short(sha string) string {
	if len(sha) > 8 {
		return sha[:8]
	}
	return sha
}

func init() {
	reviewCmd.AddCommand(reviewRefreshCmd)
}

var reviewForkCmd = &cobra.Command{
	Use:   "fork [name]",
	Short: "Turn a review tree into an authoring tree that proposes changes back",
	Long: "fork converts a review tree into an ordinary authoring one: each member keeps\n" +
		"the commits it is sitting on and moves from review/<slug>/<alias> to\n" +
		"st/<slug>/<alias>.\n\n" +
		"Each pull request's head is recorded as the new base, so the PRs you open\n" +
		"target the author's branch — the change arrives as a proposal on their pull\n" +
		"request rather than as a rival one against main.",
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name, err := resolveTreeName(args)
		if err != nil {
			return err
		}
		results, err := supatree.ForkReview(stCfg, wbCfg, name)
		if err != nil {
			return err
		}
		fmt.Printf("%s is now an authoring tree\n", name)
		for _, r := range results {
			base := r.Base
			if base == "" {
				base = "(repo default)"
			}
			fmt.Printf("  %-20s %s → %s   PRs target %s\n", r.Alias, r.From, r.To, base)
		}
		fmt.Println("\nGive the slug a meaningful name before opening anything: supatree rename-branch <slug> " + name)
		return nil
	},
}

func init() {
	reviewCmd.AddCommand(reviewForkCmd)
}
