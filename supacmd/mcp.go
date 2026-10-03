package supacmd

import (
	"github.com/spf13/cobra"

	"github.com/panamafrancis/workbench/pkg/supatree"
	"github.com/panamafrancis/workbench/pkg/version"
	"github.com/panamafrancis/workbench/pkg/zellij"
)

var mcpCmd = &cobra.Command{
	Use:               "mcp",
	Short:             "Run the supatree MCP server (stdio transport, used by Claude Code)",
	Hidden:            true,
	PersistentPreRunE: func(*cobra.Command, []string) error { return nil },
	RunE: func(cmd *cobra.Command, args []string) error {
		zellij.LogDir = supatree.LogsDir()
		return supatree.MCPServer(version.Version).WithPrecondition(func() string {
			if supatree.OldLayout() {
				return supatree.OldLayoutMessage
			}
			return ""
		}).Run()
	},
}
