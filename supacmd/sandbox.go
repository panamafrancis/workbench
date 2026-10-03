package supacmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/panamafrancis/workbench/pkg/sandbox"
	"github.com/panamafrancis/workbench/pkg/supatree"
)

var sandboxPM bool

// sandboxArgsCmd prints the nono flags an agent in a tree (or the PM) is
// launched with, one argument per line, so the grants can be audited — and
// exercised: the e2e enforcement case runs a probe under exactly these.
var sandboxArgsCmd = &cobra.Command{
	Use:    "sandbox-args [tree]",
	Short:  "Print the nono profile and grant flags a tree's agent (or --pm) runs with",
	Hidden: true,
	Args:   cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		var g sandbox.Grants
		model, err := stCfg.Model(stCfg.ResolveModel(""))
		if sandboxPM {
			if model, err = stCfg.Model(stCfg.PMModel()); err != nil {
				return err
			}
			insts, err := supatree.List(stCfg)
			if err != nil {
				return err
			}
			g = supatree.PMGrants(stCfg, insts)
		} else {
			if err != nil {
				return err
			}
			name, err := resolveTreeName(args)
			if err != nil {
				return err
			}
			inst, err := supatree.Get(stCfg, name)
			if err != nil {
				return err
			}
			g = supatree.TreeGrants(stCfg, inst)
		}
		fmt.Println("--profile")
		fmt.Println(model.NonoProfile)
		for _, a := range g.Args() {
			fmt.Println(a)
		}
		return nil
	},
}

func init() {
	sandboxArgsCmd.Flags().BoolVar(&sandboxPM, "pm", false, "the PM's grants instead of a tree agent's")
	rootCmd.AddCommand(sandboxArgsCmd)
}
