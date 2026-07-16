package tui

import (
	"errors"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// ErrPickerCancelled is returned by PickRepos when the user aborts the picker.
var ErrPickerCancelled = errors.New("selection cancelled")

// PickRepos runs an interactive multi-select over the given repo aliases and
// returns the chosen ones (in the input order). It returns ErrPickerCancelled
// if the user quits without confirming.
func PickRepos(aliases []string) ([]string, error) {
	m := &pickerModel{aliases: aliases, checked: make(map[int]bool, len(aliases))}
	out, err := tea.NewProgram(m, tea.WithAltScreen()).Run()
	if err != nil {
		return nil, err
	}
	res := out.(*pickerModel)
	if res.cancelled {
		return nil, ErrPickerCancelled
	}
	var selected []string
	for i, a := range res.aliases {
		if res.checked[i] {
			selected = append(selected, a)
		}
	}
	return selected, nil
}

type pickerModel struct {
	aliases   []string
	checked   map[int]bool
	cursor    int
	cancelled bool
}

func (m *pickerModel) Init() tea.Cmd { return nil }

func (m *pickerModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}
	switch key.String() {
	case "ctrl+c", "esc", "q":
		m.cancelled = true
		return m, tea.Quit
	case "up", "k":
		if m.cursor > 0 {
			m.cursor--
		}
	case "down", "j":
		if m.cursor < len(m.aliases)-1 {
			m.cursor++
		}
	case " ", "x":
		m.checked[m.cursor] = !m.checked[m.cursor]
	case "a":
		all := len(m.selected()) < len(m.aliases)
		for i := range m.aliases {
			m.checked[i] = all
		}
	case "enter":
		return m, tea.Quit
	}
	return m, nil
}

func (m *pickerModel) View() string {
	var b strings.Builder
	b.WriteString(styleHeader.Render("Select member repos"))
	b.WriteString("\n")
	b.WriteString(styleSub.Render("space toggle · a all/none · enter confirm · esc cancel"))
	b.WriteString("\n\n")
	for i, a := range m.aliases {
		box := "[ ]"
		if m.checked[i] {
			box = "[x]"
		}
		line := fmt.Sprintf("%s %s", box, a)
		if i == m.cursor {
			b.WriteString(styleSelected.Render("› " + line))
		} else {
			b.WriteString("  " + styleRow.Render(line))
		}
		b.WriteString("\n")
	}
	return b.String()
}

func (m *pickerModel) selected() []string {
	var out []string
	for i := range m.aliases {
		if m.checked[i] {
			out = append(out, m.aliases[i])
		}
	}
	return out
}
