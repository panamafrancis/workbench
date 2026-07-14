package supacmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/panamafrancis/workbench/pkg/version"
)

var versionCmd = &cobra.Command{
	Use:               "version",
	Short:             "Print the supatree version",
	PersistentPreRunE: func(*cobra.Command, []string) error { return nil },
	RunE: func(*cobra.Command, []string) error {
		fmt.Printf("supatree %s\n", version.Version)
		return nil
	},
}
