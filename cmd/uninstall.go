package cmd

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/panamafrancis/workbench/pkg/config"
	"github.com/panamafrancis/workbench/pkg/git"
	"github.com/panamafrancis/workbench/pkg/zellij"
)

var (
	uninstallDryRun  bool
	uninstallKeepCfg bool
	uninstallForce   bool
)

var uninstallCmd = &cobra.Command{
	Use:   "uninstall",
	Short: "Remove all workbench state (worktrees, sessions, config)",
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Println("workbench uninstall")
		fmt.Println()

		var worktrees []worktreeInfo
		for _, r := range cfg.Repos {
			for _, w := range r.Worktrees {
				worktrees = append(worktrees, worktreeInfo{
					name:     w.Name,
					path:     w.Path,
					branch:   w.Branch,
					repoPath: r.LocalPath,
					repo:     r,
				})
			}
		}

		fmt.Printf("Will remove:\n")
		fmt.Printf("  %d worktree(s)\n", len(worktrees))
		for _, w := range worktrees {
			dirty := ""
			if git.IsDirty(w.path) {
				dirty = " (dirty)"
			}
			fmt.Printf("    - %s%s\n", w.name, dirty)
		}
		fmt.Printf("  All wb-* Zellij sessions\n")
		if !uninstallKeepCfg {
			fmt.Printf("  %s\n", config.ConfigDir())
		}
		fmt.Println()

		if uninstallDryRun {
			fmt.Println("(dry run — nothing removed)")
			return nil
		}

		if !uninstallForce {
			fmt.Print("Continue? [y/N] ")
			r := bufio.NewReader(os.Stdin)
			line, _ := r.ReadString('\n')
			if !strings.HasPrefix(strings.ToLower(strings.TrimSpace(line)), "y") {
				fmt.Println("aborted")
				return nil
			}
		}

		for _, w := range worktrees {
			if git.IsDirty(w.path) && !uninstallForce {
				fmt.Printf("  skipping %s (dirty — use --force to remove)\n", w.name)
				continue
			}
			_ = w.repo.RunCleanup(w.path, w.name)
			if err := git.RemoveWorktree(w.repoPath, w.path); err != nil {
				fmt.Fprintf(os.Stderr, "  warning: remove %s: %v\n", w.name, err)
			}
			_ = git.DeleteBranch(w.repoPath, w.branch)
			zellij.CleanupLayout(w.name)
			fmt.Printf("  removed worktree %s\n", w.name)
		}

		sessions, _ := zellij.ListSessions()
		prefix := zellij.SessionPrefix()
		for _, s := range sessions {
			if strings.HasPrefix(s.Name, prefix) {
				_ = zellij.DeleteSession(s.Name)
				fmt.Printf("  deleted session %s\n", s.Name)
			}
		}

		if !uninstallKeepCfg {
			if err := os.RemoveAll(config.ConfigDir()); err != nil {
				fmt.Fprintf(os.Stderr, "  warning: remove %s: %v\n", config.ConfigDir(), err)
			} else {
				fmt.Printf("  removed %s\n", config.ConfigDir())
			}
		}

		fmt.Println()
		fmt.Println("Uninstall complete. Not removed (do manually if desired):")
		fmt.Println("  - Your git repos (untouched)")
		fmt.Println("  - nono profiles (~/.config/nono/)")
		fmt.Println("  - gh auth")
		fmt.Println("  - The workbench binary itself")
		return nil
	},
}

type worktreeInfo struct {
	name     string
	path     string
	branch   string
	repoPath string
	repo     config.Repo
}

func init() {
	uninstallCmd.Flags().BoolVar(&uninstallDryRun, "dry-run", false, "show what would be removed without doing it")
	uninstallCmd.Flags().BoolVar(&uninstallKeepCfg, "keep-config", false, "keep ~/.workbench config directory")
	uninstallCmd.Flags().BoolVar(&uninstallForce, "force", false, "remove dirty worktrees too (dangerous)")
}
