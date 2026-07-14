package supacmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/panamafrancis/workbench/pkg/supatree"
)

var syncPrune bool

var syncCmd = &cobra.Command{
	Use:   "sync [name]",
	Short: "Reconcile a supatree's member worktrees with its supatree.yml",
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
		report, err := supatree.Sync(inst.Root, wbCfg, syncPrune)
		if err != nil {
			return err
		}
		for _, w := range report.Warnings {
			fmt.Fprintf(os.Stderr, "warning: %s\n", w)
		}
		if len(report.Created) == 0 && len(report.Pruned) == 0 {
			fmt.Println("already in sync")
			return nil
		}
		for _, a := range report.Created {
			fmt.Printf("  + %s\n", a)
		}
		for _, a := range report.Pruned {
			fmt.Printf("  - %s\n", a)
		}
		return nil
	},
}

func init() {
	syncCmd.Flags().BoolVar(&syncPrune, "prune", false, "remove member worktrees no longer listed in supatree.yml")
}
