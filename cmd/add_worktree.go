package cmd

import (
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/panamafrancis/workbench/pkg/config"
	"github.com/panamafrancis/workbench/pkg/git"
)

var (
	addWorktreeRepo   string
	addWorktreeName   string
	addWorktreeBranch string
	addWorktreeModel  string
)

var addWorktreeCmd = &cobra.Command{
	Use:   "worktree",
	Short: "Create a new git worktree",
	RunE: func(cmd *cobra.Command, args []string) error {
		repo, _ := cfg.FindRepo(addWorktreeRepo)
		if repo == nil {
			return fmt.Errorf("repo %q not found — run: workbench add repo <path> --alias=%s", addWorktreeRepo, addWorktreeRepo)
		}

		existing := cfg.AllWorktreeNames()
		name := addWorktreeName
		if name == "" {
			var err error
			name, err = git.GenerateName(existing)
			if err != nil {
				return err
			}
		} else {
			if err := git.ValidateName(name, existing); err != nil {
				return err
			}
		}

		branch := addWorktreeBranch
		if branch == "" {
			branch = fmt.Sprintf("wt/%s/%s", addWorktreeRepo, name)
		}

		base := cfg.ResolveWorktreeBase()
		worktreePath := config.WorktreePath(base, addWorktreeRepo, name)

		if err := os.MkdirAll(worktreePath, 0755); err != nil {
			return fmt.Errorf("create worktree dir: %w", err)
		}
		// git worktree add creates the dir itself; remove the pre-created one
		if err := os.Remove(worktreePath); err != nil {
			// ignore — may already not exist
			_ = os.RemoveAll(worktreePath)
		}

		offline, err := git.CreateWorktree(repo.LocalPath, worktreePath, branch)
		if err != nil {
			return err
		}
		if offline {
			fmt.Fprintln(os.Stderr, "warning: offline — branched from last-fetched origin")
		}

		if err := repo.RunCopyFiles(worktreePath); err != nil {
			return err
		}

		modelKey := cfg.ResolveModel(addWorktreeModel)

		repo.Worktrees = append(repo.Worktrees, config.Worktree{
			Name:      name,
			Branch:    branch,
			Path:      worktreePath,
			CreatedAt: time.Now(),
			Model:     modelKey,
		})

		// update the slice in cfg since FindRepo returns a pointer into the slice
		for i := range cfg.Repos {
			if cfg.Repos[i].Alias == addWorktreeRepo {
				cfg.Repos[i] = *repo
				break
			}
		}

		if err := cfg.Save(); err != nil {
			return err
		}

		state, _ := config.LoadState()
		state.RecordWorktreeCreated(name)
		newAchievements := state.CheckAndUnlockAchievements()
		_ = state.Save()

		fmt.Printf("created worktree %q at %s\n", name, worktreePath)
		for _, a := range newAchievements {
			fmt.Printf("  Achievement unlocked: %s\n", config.AchievementDescription(a.ID))
		}
		return nil
	},
}

func init() {
	addWorktreeCmd.Flags().StringVar(&addWorktreeRepo, "repo", "", "repo alias (required)")
	addWorktreeCmd.Flags().StringVar(&addWorktreeName, "name", "", "worktree name (auto-generated if omitted)")
	addWorktreeCmd.Flags().StringVar(&addWorktreeBranch, "branch", "", "branch name (default: wt/<repo>/<name>)")
	addWorktreeCmd.Flags().StringVar(&addWorktreeModel, "model", "", "model to use when opening (default: config default_model)")
	_ = addWorktreeCmd.MarkFlagRequired("repo")
}
