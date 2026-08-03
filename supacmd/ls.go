package supacmd

import (
	"fmt"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/spf13/cobra"

	"github.com/panamafrancis/workbench/pkg/github"
	"github.com/panamafrancis/workbench/pkg/supatree"
	stui "github.com/panamafrancis/workbench/pkg/supatree/tui"
)

var lsCmd = &cobra.Command{
	Use:   "ls",
	Short: "List supatrees and their member PR status (TUI when interactive)",
	RunE: func(cmd *cobra.Command, args []string) error {
		if !isInteractive() {
			return lsPlain()
		}
		p := tea.NewProgram(stui.New(stCfg, wbCfg, supatreeWorkspace()),
			tea.WithAltScreen(), tea.WithMouseCellMotion(), tea.WithReportFocus())
		_, err := p.Run()
		return err
	},
}

func lsPlain() error {
	insts, err := supatree.List(stCfg)
	if err != nil {
		return err
	}
	if len(insts) == 0 {
		fmt.Println("No supatrees. Create one with: supatree new")
		return nil
	}
	prCache := github.NewCache(supatree.PRCachePath())
	_ = prCache.Load()
	for _, inst := range insts {
		fmt.Printf("%s  (stack %s, slug st/%s)\n", inst.Name, inst.Stack, inst.Slug)
		for _, m := range inst.Members {
			status := "-"
			if info := prCache.Get(m.Branch); info != nil && info.Status != github.PRNone {
				if info.Number > 0 {
					status = fmt.Sprintf("%s #%d", info.Status, info.Number)
				} else {
					status = string(info.Status)
				}
			}
			missing := ""
			if !m.Exists {
				missing = " (not created — run: supatree sync)"
			}
			fmt.Printf("  %-20s %-28s %s%s\n", m.Alias, m.Branch, status, missing)
		}
	}
	return nil
}
