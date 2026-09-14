// Package supacmd implements the supatree CLI (Cobra commands). It reuses the
// workbench packages (config, git, github, zellij, sandbox, mcp) and the
// supatree domain package.
package supacmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/panamafrancis/workbench/pkg/config"
	"github.com/panamafrancis/workbench/pkg/supatree"
	"github.com/panamafrancis/workbench/pkg/zellij"
)

// stCfg is the supatree registry; wbCfg is workbench's config (repo/model
// definitions). Both are loaded in PersistentPreRunE.
var (
	stCfg *supatree.Config
	wbCfg *config.Config
)

// supatreeWorkspace is the zellij workspace for supatree sessions and layouts.
func supatreeWorkspace() zellij.Workspace {
	return zellij.Workspace{
		LayoutsDir:          supatree.LayoutsDir(),
		SidebarCommand:      "supatree ls",
		SidebarEnvVar:       "SUPATREE_SIDEBAR",
		SidebarActiveEnvVar: "SUPATREE_ACTIVE_TREE",
		SessionPrefix:       "st-",
		SessionTab:          "supatree",
	}
}

var rootCmd = &cobra.Command{
	Use:   "supatree",
	Short: "Manage worktrees across multiple repos for a single issue",
	Long: "supatree manages a set of git worktrees — one per repo — for a single\n" +
		"cross-repo issue. A stack is a git repo holding the repo selection and agent\n" +
		"instructions; a supatree is a worktree of it with each member repo checked out\n" +
		"underneath, so one agent can run at the root and see every repo.",
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		if zellij.IsInZellij() {
			return cmd.Help()
		}
		return startCmd.RunE(startCmd, nil)
	},
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		var err error
		if stCfg, err = supatree.Load(); err != nil {
			return fmt.Errorf("load supatree config: %w", err)
		}
		if wbCfg, err = config.Load(); err != nil {
			return fmt.Errorf("load workbench config: %w", err)
		}
		return nil
	},
}

// Execute runs the supatree CLI.
func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func init() {
	rootCmd.AddCommand(scaffoldCmd)
	rootCmd.AddCommand(newCmd)
	rootCmd.AddCommand(syncCmd)
	rootCmd.AddCommand(lsCmd)
	rootCmd.AddCommand(statusCmd)
	rootCmd.AddCommand(dashCmd)
	rootCmd.AddCommand(openCmd)
	rootCmd.AddCommand(rmCmd)
	rootCmd.AddCommand(startCmd)
	rootCmd.AddCommand(renameBranchCmd)
	rootCmd.AddCommand(initCmd)
	rootCmd.AddCommand(mcpCmd)
	rootCmd.AddCommand(versionCmd)
}
