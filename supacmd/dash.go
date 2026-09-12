package supacmd

import (
	tea "github.com/charmbracelet/bubbletea"
	"github.com/spf13/cobra"

	"github.com/panamafrancis/workbench/pkg/supatree/dash"
)

var dashCmd = &cobra.Command{
	Use:   "dash",
	Short: "Full-screen dashboard of supatree activity",
	Long: "dash is a wide, read-mostly view of every supatree: its state in the ship\n" +
		"lifecycle, PR numbers and review verdicts, which trees have gone stale, and\n" +
		"which are finished and only occupying disk. Run it in its own pane or window,\n" +
		"or press D in the supatree sidebar.\n\n" +
		"It reads the shared PR cache and refreshes on the same staleness gate and\n" +
		"cross-process lock as the sidebar, so it adds no extra GitHub API load.",
	RunE: func(cmd *cobra.Command, args []string) error {
		if !isInteractive() {
			return statusCmd.RunE(statusCmd, nil)
		}
		p := tea.NewProgram(dash.New(stCfg),
			tea.WithAltScreen(), tea.WithMouseCellMotion(), tea.WithReportFocus())
		_, err := p.Run()
		return err
	},
}
