package tui

import "github.com/charmbracelet/lipgloss"

var (
	colorPrimary  = lipgloss.Color("12") // bright blue
	colorMuted    = lipgloss.Color("8")  // dark grey
	colorSelected = lipgloss.Color("14") // bright cyan
	colorDirty    = lipgloss.Color("11") // yellow
	colorHeader   = lipgloss.Color("15") // white
)

var (
	styleHeader = lipgloss.NewStyle().
			Bold(true).
			Foreground(colorHeader)

	styleRepo = lipgloss.NewStyle().
			Bold(true).
			Foreground(colorPrimary)

	styleWorktree = lipgloss.NewStyle().
			Foreground(colorHeader)

	styleSelected = lipgloss.NewStyle().
			Foreground(colorSelected).
			Bold(true)

	styleMuted = lipgloss.NewStyle().
			Foreground(colorMuted)

	styleDirty = lipgloss.NewStyle().
			Foreground(colorDirty)

	styleStatusBar = lipgloss.NewStyle().
			Foreground(colorMuted)

	styleBorder = lipgloss.NewStyle().
			BorderStyle(lipgloss.NormalBorder()).
			BorderForeground(colorMuted)
)
