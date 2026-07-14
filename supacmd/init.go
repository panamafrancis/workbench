package supacmd

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/spf13/cobra"

	"github.com/panamafrancis/workbench/pkg/supatree"
)

var initCmd = &cobra.Command{
	Use:   "init",
	Short: "Set up ~/.supatree and register the MCP server with Claude Code",
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := os.MkdirAll(supatree.Dir(), 0755); err != nil {
			return fmt.Errorf("create %s: %w", supatree.Dir(), err)
		}
		if _, err := os.Stat(supatree.ConfigPath()); os.IsNotExist(err) {
			if err := supatree.DefaultConfig().Save(); err != nil {
				return err
			}
		}
		fmt.Printf("supatree home: %s\n", supatree.Dir())
		registerMCP()
		fmt.Println("Next: supatree scaffold <dir> --repos=<a,b,c>")
		return nil
	},
}

func registerMCP() {
	claudePath, err := exec.LookPath("claude")
	if err != nil {
		fmt.Println("claude: not installed (skipping MCP registration)")
		return
	}
	check := exec.CommandContext(context.Background(), claudePath, "mcp", "list")
	if out, err := check.Output(); err == nil && strings.Contains(string(out), "supatree") {
		fmt.Println("MCP server: already registered with Claude Code")
		return
	}
	stPath, err := exec.LookPath("supatree")
	if err != nil {
		stPath = "supatree"
	}
	add := exec.CommandContext(context.Background(),
		claudePath, "mcp", "add", "supatree", "-s", "user", "--", stPath, "mcp")
	add.Stdout = os.Stdout
	add.Stderr = os.Stderr
	if err := add.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "warning: MCP registration failed: %v\n", err)
		fmt.Fprintf(os.Stderr, "  register manually: claude mcp add supatree -s user -- %s mcp\n", stPath)
		return
	}
	fmt.Println("Registered supatree MCP server with Claude Code")
}
