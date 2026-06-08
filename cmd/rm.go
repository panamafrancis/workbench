package cmd

import "github.com/spf13/cobra"

var rmCmd = &cobra.Command{
	Use:   "rm",
	Short: "Remove a repo or worktree",
}

func init() {
	rmCmd.AddCommand(rmRepoCmd)
	rmCmd.AddCommand(rmWorktreeCmd)
}
