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
	Short: "Set up supatree: its directories, config, nono profile and MCP server",
	Long: "init creates supatree's config, state and cache directories, writes a default\n" +
		"config.yml if there is none, writes the " + supatree.AgentProfileName + " nono profile the default\n" +
		"models run under, and registers the supatree MCP server with Claude Code.\n" +
		"It writes only supatree's own files.",
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := supatree.EnsureLayout(); err != nil {
			return fmt.Errorf("create supatree dirs: %w", err)
		}
		if _, err := os.Stat(supatree.ConfigPath()); os.IsNotExist(err) {
			c := supatree.DefaultConfig()
			c.Models = supatree.DefaultModels()
			if err := c.Save(); err != nil {
				return err
			}
			fmt.Printf("wrote %s\n", supatree.ConfigPath())
		} else {
			fmt.Printf("config: %s\n", supatree.ConfigPath())
		}
		// The profile is generated, and supatree is its only writer, so it is
		// rewritten every time rather than left to drift from what the code
		// expects.
		path, err := supatree.AgentProfile().Write()
		if err != nil {
			return fmt.Errorf("write nono profile: %w", err)
		}
		fmt.Printf("nono profile: %s\n", path)
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
