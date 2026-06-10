package cmd

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/panamafrancis/workbench/pkg/setup"
)

var doctorJSON bool

var doctorCmd = &cobra.Command{
	Use:   "doctor",
	Short: "Check that workbench dependencies are installed and configured",
	RunE: func(cmd *cobra.Command, args []string) error {
		results := setup.RunChecks(cfg)

		if doctorJSON {
			data, err := json.MarshalIndent(results, "", "  ")
			if err != nil {
				return err
			}
			fmt.Println(string(data))
			if setup.HasHardFailures(results) {
				os.Exit(1)
			}
			return nil
		}

		hasIssues := false
		for _, r := range results {
			icon := "✓"
			switch r.Status {
			case setup.StatusFail:
				icon = "✗"
				hasIssues = true
			case setup.StatusWarn:
				icon = "!"
				hasIssues = true
			case setup.StatusSkip:
				icon = "-"
			}
			fmt.Printf("  %s %-24s %s\n", icon, r.Name, r.Message)
			if r.Hint != "" && r.Status != setup.StatusOK {
				fmt.Printf("    → %s\n", r.Hint)
			}
		}
		if hasIssues {
			fmt.Println()
			fmt.Println("Run workbench init to fix configuration issues.")
		} else {
			fmt.Println()
			fmt.Println("All checks passed.")
		}
		if setup.HasHardFailures(results) {
			os.Exit(1)
		}
		return nil
	},
}

func init() {
	doctorCmd.Flags().BoolVar(&doctorJSON, "json", false, "output results as JSON")
}
