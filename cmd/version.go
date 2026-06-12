package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/panamafrancis/workbench/pkg/version"
)

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print the workbench version",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Println("workbench " + version.Version)
	},
}
