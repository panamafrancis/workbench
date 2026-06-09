package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/panamafrancis/workbench/pkg/sandbox"
	"github.com/panamafrancis/workbench/pkg/zellij"
)

var (
	openRepo     string
	openWorktree string
	openModel    string
	openNoZellij bool
)

var openCmd = &cobra.Command{
	Use:   "open",
	Short: "Open a worktree in a new Zellij tab (or print the command with --no-zellij)",
	RunE: func(cmd *cobra.Command, args []string) error {
		wt, repo := cfg.FindWorktree(openWorktree)
		if wt == nil {
			return fmt.Errorf("worktree %q not found", openWorktree)
		}
		if openRepo != "" && repo.Alias != openRepo {
			return fmt.Errorf("worktree %q does not belong to repo %q", openWorktree, openRepo)
		}

		modelKey := cfg.ResolveModel(openModel)
		if wt.Model != "" && openModel == "" {
			modelKey = cfg.ResolveModel(wt.Model)
		}

		nonoArgs, err := sandbox.BuildNonoArgs(wt.Path, modelKey, cfg)
		if err != nil {
			return err
		}

		if openNoZellij {
			fmt.Printf("cd %s && nono", wt.Path)
			for _, a := range nonoArgs {
				fmt.Printf(" %q", a)
			}
			fmt.Println()
			return nil
		}

		if !zellij.IsInZellij() {
			fmt.Fprintln(os.Stderr, "workbench: not running inside a Zellij session.")
			fmt.Fprintln(os.Stderr, "Start a session with: zellij --layout ~/.config/zellij/layouts/wb.kdl")
			fmt.Fprintln(os.Stderr, "Or run without Zellij: workbench open --no-zellij ...")
			os.Exit(1)
		}

		created, err := zellij.OpenOrFocusTab(wt.Name, wt.Path, nonoArgs)
		if err != nil {
			return err
		}
		if created {
			if err := repo.RunStartup(wt.Path, wt.Name); err != nil {
				return fmt.Errorf("startup script: %w", err)
			}
		}
		return nil
	},
}

func init() {
	openCmd.Flags().StringVar(&openRepo, "repo", "", "repo alias (optional, disambiguates if needed)")
	openCmd.Flags().StringVar(&openWorktree, "worktree", "", "worktree name (required)")
	openCmd.Flags().StringVar(&openModel, "model", "", "model override (default: worktree's model or config default_model)")
	openCmd.Flags().BoolVar(&openNoZellij, "no-zellij", false, "print the nono command instead of opening a tab")
	_ = openCmd.MarkFlagRequired("worktree")
}
