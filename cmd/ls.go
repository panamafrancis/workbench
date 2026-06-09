package cmd

import (
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/spf13/cobra"

	"github.com/panamafrancis/workbench/pkg/tui"
)

var lsRepo string

var lsCmd = &cobra.Command{
	Use:   "ls",
	Short: "List worktrees (TUI when interactive, plain text otherwise)",
	RunE: func(cmd *cobra.Command, args []string) error {
		// Plain text when piped or --repo filter
		if lsRepo != "" || !isInteractive() {
			return lsPlain()
		}
		p := tea.NewProgram(tui.New(cfg), tea.WithAltScreen(), tea.WithMouseCellMotion(), tea.WithReportFocus())
		_, err := p.Run()
		return err
	},
}

func lsPlain() error {
	filter := lsRepo
	for _, r := range cfg.Repos {
		if filter != "" && r.Alias != filter {
			continue
		}
		fmt.Printf("%s (%s)\n", r.Alias, r.LocalPath)
		for _, w := range r.Worktrees {
			fmt.Printf("  %-20s %s  [%s]  %s\n", w.Name, w.Branch, w.Model, w.Path)
		}
	}
	return nil
}

func isInteractive() bool {
	fi, err := os.Stdout.Stat()
	if err != nil {
		return false
	}
	return (fi.Mode() & os.ModeCharDevice) != 0
}

func init() {
	lsCmd.Flags().StringVar(&lsRepo, "repo", "", "filter to a specific repo alias")
}
