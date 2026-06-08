package cmd

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/panamafrancis/workbench/pkg/git"
)

var rmRepoCmd = &cobra.Command{
	Use:   "repo <alias>",
	Short: "Unregister a repo and remove all its worktrees",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		alias := args[0]
		repo, idx := cfg.FindRepo(alias)
		if repo == nil {
			return fmt.Errorf("repo %q not found", alias)
		}

		if len(repo.Worktrees) > 0 {
			fmt.Printf("This will remove %d worktree(s) for %q. Continue? [y/N] ", len(repo.Worktrees), alias)
			r := bufio.NewReader(os.Stdin)
			line, _ := r.ReadString('\n')
			if !strings.HasPrefix(strings.ToLower(strings.TrimSpace(line)), "y") {
				fmt.Println("aborted")
				return nil
			}
		}

		for _, wt := range repo.Worktrees {
			if err := repo.RunCleanup(wt.Path, wt.Name); err != nil {
				fmt.Fprintf(os.Stderr, "cleanup script failed for %s: %v\n", wt.Name, err)
			}
			if err := git.RemoveWorktree(repo.LocalPath, wt.Path); err != nil {
				fmt.Fprintf(os.Stderr, "remove worktree %s: %v\n", wt.Name, err)
			}
		}

		cfg.Repos = append(cfg.Repos[:idx], cfg.Repos[idx+1:]...)
		if err := cfg.Save(); err != nil {
			return err
		}
		fmt.Printf("removed repo %q\n", alias)
		return nil
	},
}
