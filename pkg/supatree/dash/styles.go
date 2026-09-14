package dash

import "github.com/charmbracelet/lipgloss"

var (
	colorMuted      = lipgloss.Color("8")
	colorHeader     = lipgloss.Color("15")
	colorPrimary    = lipgloss.Color("12")
	colorSelected   = lipgloss.Color("14")
	colorSelectedBg = lipgloss.Color("237")
	colorGreen      = lipgloss.Color("10")
	colorYellow     = lipgloss.Color("11")
	colorRed        = lipgloss.Color("9")
	colorMagenta    = lipgloss.Color("13")

	styleTitle    = lipgloss.NewStyle().Bold(true).Foreground(colorHeader)
	styleColumns  = lipgloss.NewStyle().Foreground(colorMuted).Bold(true)
	styleMuted    = lipgloss.NewStyle().Foreground(colorMuted)
	styleTree     = lipgloss.NewStyle().Bold(true).Foreground(colorPrimary)
	styleSelected = lipgloss.NewStyle().Foreground(colorSelected).Background(colorSelectedBg).Bold(true)
	styleGreen    = lipgloss.NewStyle().Foreground(colorGreen)
	styleYellow   = lipgloss.NewStyle().Foreground(colorYellow)
	styleRed      = lipgloss.NewStyle().Foreground(colorRed)
	styleMagenta  = lipgloss.NewStyle().Foreground(colorMagenta)
)
