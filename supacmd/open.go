package supacmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/panamafrancis/workbench/pkg/supatree"
	"github.com/panamafrancis/workbench/pkg/zellij"
)

var (
	openAgent   string
	openModel   string
	openRepo    string
	openSession string
)

var openCmd = &cobra.Command{
	Use:   "open [name]",
	Short: "Open (or resume) an agent in a supatree's Zellij tab",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name, err := resolveTreeName(args)
		if err != nil {
			return err
		}
		inst, err := supatree.Get(stCfg, name)
		if err != nil {
			return err
		}
		if openRepo != "" {
			return openMemberAgent(inst)
		}
		return openRootAgent(inst)
	},
}

// openRootAgent opens or resumes a named agent running at the supatree root.
func openRootAgent(inst *supatree.Instance) error {
	ensureZellij()
	_, err := supatree.OpenRootAgent(inst, wbCfg, supatreeWorkspace(), stCfg.ResolveSidebarWidth(), openAgent, openModel, os.Stderr)
	return err
}

// openMemberAgent opens an agent scoped to a single member repo.
func openMemberAgent(inst *supatree.Instance) error {
	ensureZellij()
	_, err := supatree.OpenMemberAgent(inst, wbCfg, supatreeWorkspace(), stCfg.ResolveSidebarWidth(), openRepo, openModel)
	return err
}

// ensureZellij makes sure we can open a tab: it honors --session and otherwise
// requires being inside a Zellij session, exiting with guidance when not.
func ensureZellij() {
	if openSession != "" {
		_ = os.Setenv("ZELLIJ_SESSION_NAME", openSession)
		return
	}
	if !zellij.IsInZellij() {
		fmt.Fprintln(os.Stderr, "supatree: not inside a Zellij session.")
		fmt.Fprintln(os.Stderr, "Start one with: supatree start")
		os.Exit(1)
	}
}

func init() {
	openCmd.Flags().StringVar(&openAgent, "agent", "main", "agent name (each is an independently-resumable session at the root)")
	openCmd.Flags().StringVar(&openModel, "model", "", "model override (default: supatree's model)")
	openCmd.Flags().StringVar(&openRepo, "repo", "", "open an agent scoped to a single member repo instead of the root")
	openCmd.Flags().StringVar(&openSession, "session", "", "target a specific Zellij session")
}
