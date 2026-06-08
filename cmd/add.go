package cmd

import "github.com/spf13/cobra"

var addCmd = &cobra.Command{
	Use:   "add",
	Short: "Add a repo or worktree",
}

func init() {
	addCmd.AddCommand(addRepoCmd)
	addCmd.AddCommand(addWorktreeCmd)
}
