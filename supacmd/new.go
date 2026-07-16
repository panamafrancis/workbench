package supacmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/panamafrancis/workbench/pkg/supatree"
)

var (
	newStack       string
	newName        string
	newModel       string
	newKeepPartial bool
)

var newCmd = &cobra.Command{
	Use:   "new",
	Short: "Create a supatree (one worktree per repo) from a stack",
	RunE: func(cmd *cobra.Command, args []string) error {
		inst, report, err := supatree.New(stCfg, wbCfg, supatree.CreateOptions{
			Stack:       newStack,
			Name:        newName,
			Model:       newModel,
			KeepPartial: newKeepPartial,
		})
		if err != nil {
			return err
		}
		for _, w := range report.Warnings {
			fmt.Fprintf(os.Stderr, "warning: %s\n", w)
		}
		fmt.Printf("created supatree %q at %s\n", inst.Name, inst.Root)
		for _, m := range inst.Members {
			fmt.Printf("  %-20s %s\n", m.Alias, m.Branch)
		}
		fmt.Printf("open it with: supatree open %s\n", inst.Name)
		return nil
	},
}

func init() {
	newCmd.Flags().StringVar(&newStack, "stack", "", "stack alias (required if more than one is registered)")
	newCmd.Flags().StringVar(&newName, "name", "", "supatree name (auto-generated city name if omitted)")
	newCmd.Flags().StringVar(&newModel, "model", "", "default model for agents (default: stack/registry default)")
	newCmd.Flags().BoolVar(&newKeepPartial, "keep-partial", false, "on failure, keep partially-created worktrees for debugging")
}
