package cmd

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/spf13/cobra"

	"github.com/panamafrancis/workbench/pkg/config"
	"github.com/panamafrancis/workbench/pkg/git"
	"github.com/panamafrancis/workbench/pkg/github"
	"github.com/panamafrancis/workbench/pkg/zellij"
)

var (
	renameBranchWorktree string
	renameBranchPush     bool
)

var renameBranchCmd = &cobra.Command{
	Use:   "rename-branch <new-branch>",
	Short: "Rename a worktree's branch and update config + PR cache",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		newBranch := args[0]

		wtName := renameBranchWorktree
		if wtName == "" {
			wtName = os.Getenv("WORKBENCH_WORKTREE_NAME")
		}
		if wtName == "" {
			return fmt.Errorf("specify --worktree or set WORKBENCH_WORKTREE_NAME")
		}

		wt, repo := cfg.FindWorktree(wtName)
		if wt == nil {
			if wd, err := os.Getwd(); err == nil {
				wt, repo = cfg.FindWorktreeByPath(wd)
			}
		}
		if wt == nil {
			return fmt.Errorf("worktree %q not found", wtName)
		}

		oldBranch := wt.Branch
		if oldBranch == newBranch {
			fmt.Println("branch name unchanged")
			return nil
		}

		gitCmd := exec.CommandContext(context.Background(), "git", "-C", wt.Path, "branch", "-m", newBranch)
		var errBuf bytes.Buffer
		gitCmd.Stderr = &errBuf
		if err := gitCmd.Run(); err != nil {
			return fmt.Errorf("git branch -m: %s", strings.TrimSpace(errBuf.String()))
		}

		oldWtName := wt.Name
		parts := strings.Split(newBranch, "/")
		newWtName := parts[len(parts)-1]

		renameWorktree := false
		if newWtName != oldWtName {
			others := cfg.AllWorktreeNames()
			filtered := others[:0]
			for _, n := range others {
				if n != oldWtName {
					filtered = append(filtered, n)
				}
			}
			if git.ValidateName(newWtName, filtered) == nil {
				renameWorktree = true
			}
		}

		for ri := range cfg.Repos {
			if cfg.Repos[ri].Alias == repo.Alias {
				for wi := range cfg.Repos[ri].Worktrees {
					if cfg.Repos[ri].Worktrees[wi].Name == oldWtName {
						cfg.Repos[ri].Worktrees[wi].Branch = newBranch
						if renameWorktree {
							cfg.Repos[ri].Worktrees[wi].Name = newWtName
						}
						break
					}
				}
				break
			}
		}
		if err := cfg.Save(); err != nil {
			return fmt.Errorf("save config: %w", err)
		}

		prCache := github.NewCache(config.PRCachePath())
		_ = prCache.Load()
		prCache.Rename(oldBranch, newBranch)
		_ = prCache.Save()

		if renameWorktree && zellij.IsInZellij() {
			_ = zellij.RenameTab(oldWtName, newWtName)
		}

		fmt.Printf("renamed branch: %s → %s\n", oldBranch, newBranch)
		if renameWorktree {
			fmt.Printf("renamed worktree: %s → %s\n", oldWtName, newWtName)
		}

		if renameBranchPush {
			pushCmd := exec.CommandContext(context.Background(), "git", "-C", wt.Path, "push", "-u", "origin", newBranch)
			pushCmd.Stdout = os.Stdout
			pushCmd.Stderr = os.Stderr
			if err := pushCmd.Run(); err != nil {
				return fmt.Errorf("git push: %w", err)
			}
			delCmd := exec.CommandContext(context.Background(), "git", "-C", wt.Path, "push", "origin", ":"+oldBranch)
			delCmd.Stdout = os.Stdout
			delCmd.Stderr = os.Stderr
			_ = delCmd.Run()
		} else {
			fmt.Printf("to push: git push -u origin %s && git push origin :%s\n", newBranch, oldBranch)
		}

		return nil
	},
}

func init() {
	renameBranchCmd.Flags().StringVar(&renameBranchWorktree, "worktree", "", "worktree name (default: $WORKBENCH_WORKTREE_NAME)")
	renameBranchCmd.Flags().BoolVar(&renameBranchPush, "push", false, "push new branch and delete old remote branch")
}
