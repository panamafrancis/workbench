package supacmd

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/panamafrancis/workbench/pkg/supatree"
)

var (
	rmForce bool
	rmPush  bool
	rmYes   bool
)

var rmCmd = &cobra.Command{
	Use:   "rm [name]",
	Short: "Remove a supatree (all member worktrees + branches)",
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
		if !rmYes {
			fmt.Printf("Remove supatree %q and its %d member worktree(s) at %s? [y/N] ", name, len(inst.Members), inst.Root)
			reader := bufio.NewReader(os.Stdin)
			line, _ := reader.ReadString('\n')
			if strings.ToLower(strings.TrimSpace(line)) != "y" {
				fmt.Println("aborted")
				return nil
			}
		}
		res, err := supatree.Remove(stCfg, wbCfg, name, supatree.RemoveOptions{Force: rmForce, Push: rmPush})
		if err != nil {
			return err
		}
		ws := supatreeWorkspace()
		ws.CleanupLayout(name)
		for _, a := range res.Agents {
			ws.CleanupLayout(supatree.TabName(name, a.Name))
		}
		for _, w := range res.Warnings {
			fmt.Fprintf(os.Stderr, "warning: %s\n", w)
		}
		fmt.Printf("removed supatree %q\n", name)
		return nil
	},
}

func init() {
	rmCmd.Flags().BoolVar(&rmForce, "force", false, "remove even with uncommitted stack changes")
	rmCmd.Flags().BoolVar(&rmPush, "push", false, "push the st/<name> branch before deleting it locally")
	rmCmd.Flags().BoolVarP(&rmYes, "yes", "y", false, "skip confirmation")
}
