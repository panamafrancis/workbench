package supacmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/panamafrancis/workbench/pkg/supatree"
)

var renameBranchPush bool

var renameBranchCmd = &cobra.Command{
	Use:   "rename-branch <new-slug> [name]",
	Short: "Rename every member branch to st/<new-slug>/<alias>",
	Args:  cobra.RangeArgs(1, 2),
	RunE: func(cmd *cobra.Command, args []string) error {
		newSlug := args[0]
		name, err := resolveTreeName(args[1:])
		if err != nil {
			return err
		}
		if err := supatree.RenameBranchSlug(stCfg, wbCfg, name, newSlug, renameBranchPush); err != nil {
			return err
		}
		fmt.Printf("renamed member branches to st/%s/<alias>\n", newSlug)
		return nil
	},
}

func init() {
	renameBranchCmd.Flags().BoolVar(&renameBranchPush, "push", false, "push new branches and delete old remote branches")
}
