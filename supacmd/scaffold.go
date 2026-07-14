package supacmd

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/panamafrancis/workbench/pkg/supatree"
)

var (
	scaffoldAlias string
	scaffoldRepos string
)

var scaffoldCmd = &cobra.Command{
	Use:   "scaffold <dir>",
	Short: "Create and register a new stack repo (repo selection + agent guide)",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		var members []string
		for _, r := range strings.Split(scaffoldRepos, ",") {
			if r = strings.TrimSpace(r); r != "" {
				members = append(members, r)
			}
		}
		res, err := supatree.Scaffold(args[0], scaffoldAlias, members, wbCfg)
		if err != nil {
			return err
		}
		fmt.Printf("Scaffolded stack %q at %s\n", res.Alias, res.Path)
		fmt.Printf("  edit %s to adjust repos and dependencies, then: supatree new --stack %s\n", supatree.SpecName, res.Alias)
		return nil
	},
}

func init() {
	scaffoldCmd.Flags().StringVar(&scaffoldAlias, "alias", "", "stack alias (default: directory base name)")
	scaffoldCmd.Flags().StringVar(&scaffoldRepos, "repos", "", "comma-separated workbench repo aliases to include")
}
