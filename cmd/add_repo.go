package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/panamafrancis/workbench/pkg/config"
)

var addRepoAlias string

var addRepoCmd = &cobra.Command{
	Use:   "repo <local-path>",
	Short: "Register a local git repo",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		localPath, err := filepath.Abs(args[0])
		if err != nil {
			return err
		}
		if _, err := os.Stat(localPath); err != nil {
			return fmt.Errorf("path does not exist: %s", localPath)
		}
		if addRepoAlias == "" {
			addRepoAlias = filepath.Base(localPath)
		}
		if r, _ := cfg.FindRepo(addRepoAlias); r != nil {
			return fmt.Errorf("alias %q already registered", addRepoAlias)
		}
		cfg.Repos = append(cfg.Repos, config.Repo{
			Alias:     addRepoAlias,
			LocalPath: localPath,
		})
		if err := cfg.Save(); err != nil {
			return err
		}
		fmt.Printf("registered %s as %q\n", localPath, addRepoAlias)
		return nil
	},
}

func init() {
	addRepoCmd.Flags().StringVar(&addRepoAlias, "alias", "", "short alias for the repo (default: directory name)")
}
