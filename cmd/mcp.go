package cmd

import (
	"github.com/spf13/cobra"

	"github.com/panamafrancis/workbench/pkg/config"
	"github.com/panamafrancis/workbench/pkg/mcp"
	"github.com/panamafrancis/workbench/pkg/version"
)

var mcpCmd = &cobra.Command{
	Use:    "mcp",
	Short:  "Run the MCP server (stdio transport, used by Claude Code)",
	Hidden: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		return mcp.WorkbenchServer(version.Version).WithPrecondition(func() string {
			if config.OldLayout() {
				return config.OldLayoutMessage
			}
			return ""
		}).Run()
	},
}

func init() {
	rootCmd.AddCommand(mcpCmd)
}
