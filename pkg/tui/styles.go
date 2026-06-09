package tui

import "github.com/charmbracelet/lipgloss"

var (
	colorPrimary  = lipgloss.Color("12") // bright blue
	colorMuted    = lipgloss.Color("8")  // dark grey
	colorSelected = lipgloss.Color("14") // bright cyan
	colorDirty    = lipgloss.Color("11") // yellow
	colorHeader   = lipgloss.Color("15") // white
	colorGreen    = lipgloss.Color("10")
	colorRed      = lipgloss.Color("9")
	colorMagenta  = lipgloss.Color("13")
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

	stylePRDraft  = lipgloss.NewStyle().Foreground(colorMuted)
	stylePROpen   = lipgloss.NewStyle().Foreground(colorGreen)
	stylePRMerged = lipgloss.NewStyle().Foreground(colorMagenta)
	stylePRClosed = lipgloss.NewStyle().Foreground(colorRed)

	stylePRDraftSelected  = lipgloss.NewStyle().Foreground(colorMuted).Bold(true)
	stylePROpenSelected   = lipgloss.NewStyle().Foreground(colorGreen).Bold(true)
	stylePRMergedSelected = lipgloss.NewStyle().Foreground(colorMagenta).Bold(true)
	stylePRClosedSelected = lipgloss.NewStyle().Foreground(colorRed).Bold(true)
)
