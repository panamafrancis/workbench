package supacmd

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/panamafrancis/workbench/pkg/supatree"
	"github.com/panamafrancis/workbench/pkg/zellij"
)

var pmCmd = &cobra.Command{
	Use:   "pm",
	Short: "Open the PM agent — a standing agent that manages supatrees",
	Long: "pm opens (or focuses) an agent rooted at ~/.supatree/pm that can see every\n" +
		"supatree at once: what is blocked, what reviewers said, who is working where.\n" +
		"It is optional — nothing else depends on it running.\n\n" +
		"It is not rooted in a supatree, because one that manages many cannot live in\n" +
		"one of them. Its sandbox allows its own state, each tree's .supatree/ and the\n" +
		"stack repos, and nothing under repos/ — it coordinates work rather than doing\n" +
		"it. Press P in the supatree sidebar for the same thing.",
	RunE: func(cmd *cobra.Command, args []string) error {
		if !zellij.IsInZellij() {
			return fmt.Errorf("run this inside a supatree session (supatree start)")
		}
		// Singleton by tab name, not by file lock: OpenOrFocusTab focuses the
		// live PM tab rather than opening a second one, and PMTab is reserved so
		// no supatree can collide with it. A lock would be theatre — nothing in
		// this process stays alive to hold one on the agent's behalf.
		_, err := supatree.OpenPM(stCfg, wbCfg, supatreeWorkspace(), stCfg.ResolveSidebarWidth())
		return err
	},
}

var pmRequestFrom string

var requestCmd = &cobra.Command{
	Use:   "request <text>...",
	Short: "Queue something for the PM agent to look at",
	Long: "request appends to ~/.supatree/requests.jsonl, which the PM reads at the top\n" +
		"of each turn. Anything that can append to a file can raise one — the sidebar,\n" +
		"the watcher, a shell — which is what lets Go processes reach an agent at all.\n\n" +
		"The PM need not be running: the queue is read from a stored offset, so one\n" +
		"that has been down for a day catches up rather than losing the backlog.",
	Args: cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		req := supatree.Request{From: pmRequestFrom, Tree: pmRequestTree, Text: strings.Join(args, " ")}
		if err := supatree.AppendRequest(req); err != nil {
			return err
		}
		fmt.Println("queued for the PM")
		return nil
	},
}

var pmRequestTree string

func init() {
	requestCmd.Flags().StringVar(&pmRequestFrom, "from", "cli", "who is asking")
	requestCmd.Flags().StringVar(&pmRequestTree, "tree", "", "supatree the request concerns")
}
