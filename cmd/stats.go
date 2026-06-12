package cmd

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/panamafrancis/workbench/pkg/config"
	"github.com/panamafrancis/workbench/pkg/git"
)

var statsCmd = &cobra.Command{
	Use:   "stats",
	Short: "Show lifetime workbench statistics",
	RunE: func(cmd *cobra.Command, args []string) error {
		state, err := config.LoadState()
		if err != nil {
			return err
		}
		printStats(state)
		return nil
	},
}

func printStats(s *config.State) {
	totalCities := len(git.Cities)
	visited := len(s.CitiesVisited)
	streak := s.CurrentStreak()

	fmt.Println("workbench stats")
	fmt.Println(strings.Repeat("─", 40))
	fmt.Println()
	fmt.Printf("  Worktrees created:  %d\n", s.WorktreesCreated)
	fmt.Printf("  PRs merged:         %d\n", s.WorktreesMerged)
	fmt.Printf("  Cities visited:     %d / %d\n", visited, totalCities)
	fmt.Printf("  Current streak:     %d day(s)\n", streak)
	fmt.Println()

	pct := 0
	if totalCities > 0 {
		pct = visited * 100 / totalCities
	}
	barWidth := 30
	filled := barWidth * pct / 100
	bar := strings.Repeat("█", filled) + strings.Repeat("░", barWidth-filled)
	fmt.Printf("  [%s] %d%%\n", bar, pct)
	fmt.Println()

	allDefs := config.AllAchievementDefs()
	unlocked := make(map[string]bool)
	for _, a := range s.Achievements {
		unlocked[a.ID] = true
	}

	fmt.Println("  Achievements")
	fmt.Println("  " + strings.Repeat("─", 36))
	for _, def := range allDefs {
		icon := "○"
		if unlocked[def.ID] {
			icon = "●"
		}
		fmt.Printf("  %s %s\n", icon, def.Description)
	}
	fmt.Println()
}

func init() {
	rootCmd.AddCommand(statsCmd)
}
