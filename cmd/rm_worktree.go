package cmd

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/panamafrancis/workbench/pkg/git"
	"github.com/panamafrancis/workbench/pkg/zellij"
)

var rmWorktreeCmd = &cobra.Command{
	Use:   "worktree <name>",
	Short: "Remove a worktree",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]
		wt, repo := cfg.FindWorktree(name)
		if wt == nil {
			return fmt.Errorf("worktree %q not found", name)
		}

		prompt := fmt.Sprintf("Remove worktree %q at %s?", name, wt.Path)
		if zellij.IsInZellij() {
			if tabs, err := zellij.TabNames(); err == nil && tabs[name] {
				prompt = fmt.Sprintf("Worktree %q has a running Zellij tab. Remove anyway?", name)
			}
		}
		fmt.Printf("%s [y/N] ", prompt)
		r := bufio.NewReader(os.Stdin)
		line, _ := r.ReadString('\n')
		if !strings.HasPrefix(strings.ToLower(strings.TrimSpace(line)), "y") {
			fmt.Println("aborted")
			return nil
		}

		if err := repo.RunCleanup(wt.Path, wt.Name); err != nil {
			fmt.Fprintf(os.Stderr, "cleanup script failed: %v\n", err)
		}

		if err := git.RemoveWorktree(repo.LocalPath, wt.Path); err != nil {
			return err
		}

		repoEntry, _ := cfg.FindRepo(repo.Alias)
		for i, w := range repoEntry.Worktrees {
			if w.Name == name {
				repoEntry.Worktrees = append(repoEntry.Worktrees[:i], repoEntry.Worktrees[i+1:]...)
				break
			}
		}
		for i := range cfg.Repos {
			if cfg.Repos[i].Alias == repo.Alias {
				cfg.Repos[i] = *repoEntry
				break
			}
		}

		if err := cfg.Save(); err != nil {
			return err
		}
		zellij.CleanupLayout(name)
		fmt.Printf("removed worktree %q\n", name)
		return nil
	},
}
