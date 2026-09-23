package tui

import "github.com/charmbracelet/lipgloss"

var (
	colorPrimary    = lipgloss.Color("12") // bright blue
	colorMuted      = lipgloss.Color("8")  // dark grey
	colorSelected   = lipgloss.Color("14") // bright cyan
	colorDirty      = lipgloss.Color("11") // yellow
	colorHeader     = lipgloss.Color("15") // white
	colorGreen      = lipgloss.Color("10")
	colorRed        = lipgloss.Color("9")
	colorMagenta    = lipgloss.Color("13")
	colorSelectedBg = lipgloss.Color("237")

	styleHeader   = lipgloss.NewStyle().Bold(true).Foreground(colorHeader)
	styleTree     = lipgloss.NewStyle().Bold(true).Foreground(colorPrimary)
	styleSub      = lipgloss.NewStyle().Foreground(colorMuted).Italic(true)
	styleRow      = lipgloss.NewStyle().Foreground(colorHeader)
	styleSelected = lipgloss.NewStyle().Foreground(colorSelected).Background(colorSelectedBg).Bold(true)
	styleMuted    = lipgloss.NewStyle().Foreground(colorMuted)
	styleDirty    = lipgloss.NewStyle().Foreground(colorDirty)
	styleStatus   = lipgloss.NewStyle().Foreground(colorMuted)
	styleRunning  = lipgloss.NewStyle().Foreground(colorGreen)
	// stylePM sets the PM row apart from the supatrees below it.
	stylePM = lipgloss.NewStyle().Bold(true).Foreground(colorMagenta)
	// styleAttention marks a supatree holding news you have not looked at.
	styleAttention = lipgloss.NewStyle().Bold(true).Foreground(colorRed)

	stylePRDraft  = lipgloss.NewStyle().Foreground(colorMuted)
	stylePROpen   = lipgloss.NewStyle().Foreground(colorGreen)
	stylePRMerged = lipgloss.NewStyle().Foreground(colorMagenta)
	stylePRClosed = lipgloss.NewStyle().Foreground(colorRed)
)
