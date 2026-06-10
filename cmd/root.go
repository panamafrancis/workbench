package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/panamafrancis/workbench/pkg/config"
)

var cfg *config.Config

var rootCmd = &cobra.Command{
	Use:   "workbench",
	Short: "Sandboxed git worktree manager",
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		var err error
		cfg, err = config.Load()
		if err != nil {
			return fmt.Errorf("load config: %w", err)
		}
		name := cmd.Name()
		if name != "init" && name != "doctor" && name != "help" && name != "version" {
			if _, statErr := os.Stat(config.ConfigPath()); os.IsNotExist(statErr) {
				fmt.Fprintln(os.Stderr, "No config found. Run: workbench init")
			}
		}
		return nil
	},
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func init() {
	rootCmd.AddCommand(addCmd)
	rootCmd.AddCommand(rmCmd)
	rootCmd.AddCommand(lsCmd)
	rootCmd.AddCommand(openCmd)
	rootCmd.AddCommand(startCmd)
	rootCmd.AddCommand(initCmd)
	rootCmd.AddCommand(doctorCmd)
	rootCmd.AddCommand(renameBranchCmd)
	rootCmd.AddCommand(uninstallCmd)
	rootCmd.AddCommand(versionCmd)
}
