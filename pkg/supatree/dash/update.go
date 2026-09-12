package dash

import (
	tea "github.com/charmbracelet/bubbletea"

	"github.com/panamafrancis/workbench/pkg/github"
)

func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
	case tea.FocusMsg:
		return m, m.reloadCmd()
	case tickMsg:
		return m, tea.Batch(m.reloadCmd(), m.fetchCmd(false), m.tickCmd())
	case summaryMsg:
		m.summary = msg.summary
		m.loaded = true
		m.rebuildRows()
	case prMsg:
		m.fetching = false
		switch {
		case msg.backoff:
			m.prHint = "gh rate limited"
		case msg.skipped:
			// Another process owned the round; its results land in the cache the
			// next reload re-reads.
		case msg.err == nil:
			m.prHint = ""
		case github.IsRateLimited(msg.err):
			m.prHint = "gh rate limited"
		case github.IsPermanentError(msg.err):
			m.prHint = "gh auth required"
		default:
			m.prHint = ""
		}
		return m, m.reloadCmd()
	case actionMsg:
		m.msg = msg.msg
		m.err = msg.err
	case tea.MouseMsg:
		return m.updateMouse(msg)
	case tea.KeyMsg:
		return m.updateKey(msg)
	}
	return m, nil
}

func (m *Model) updateMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	switch {
	case msg.Button == tea.MouseButtonWheelUp:
		m.scrollBy(-wheelStep)
	case msg.Button == tea.MouseButtonWheelDown:
		m.scrollBy(wheelStep)
	case msg.Button == tea.MouseButtonLeft && msg.Action == tea.MouseActionRelease:
		if i := m.scroll + msg.Y - rowsTopOffset; i >= 0 && i < len(m.rows) {
			m.cursor = i
			m.follow = true
		}
	}
	return m, nil
}

func (m *Model) updateKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// Two-key vim sequences: gg (top), zM/zR (fold/unfold all). An unrecognized
	// pair clears the prefix and falls through to the switch below, so a
	// mistyped `g` never swallows a command.
	if m.pending != "" {
		seq := m.pending + msg.String()
		m.pending = ""
		switch seq {
		case "gg":
			m.cursor = 0
			m.follow = true
			return m, nil
		case "zM":
			m.setAllExpanded(false)
			return m, nil
		case "zR":
			m.setAllExpanded(true)
			return m, nil
		}
	}

	m.msg, m.err = "", nil
	switch msg.String() {
	case "q", "esc", "ctrl+c":
		return m, tea.Quit
	case "j", "down":
		m.moveCursor(1)
	case "k", "up":
		m.moveCursor(-1)
	case "ctrl+d":
		m.moveCursor(m.halfPage())
	case "ctrl+u":
		m.moveCursor(-m.halfPage())
	case "g", "z":
		m.pending = msg.String()
	case "G":
		m.cursor = len(m.rows) - 1
		m.clampCursor()
		m.follow = true
	case "}", "]":
		m.jumpTree(1)
	case "{", "[":
		m.jumpTree(-1)
	case " ":
		if t := m.selectedTree(); t != nil {
			m.setExpanded(t.Name, !m.expanded[t.Name])
		}
	case "l", "right":
		if t := m.selectedTree(); t != nil {
			m.setExpanded(t.Name, true)
		}
	case "h", "left":
		if t := m.selectedTree(); t != nil {
			m.setExpanded(t.Name, false)
		}
	case "enter", "o":
		if t := m.selectedTree(); t != nil {
			return m, m.openTreeCmd(t.Name)
		}
	case "r":
		return m, tea.Batch(m.fetchCmd(true), m.reloadCmd())
	}
	return m, nil
}

// jumpTree moves to the next or previous supatree row — vim's paragraph motion
// over the tree blocks.
func (m *Model) jumpTree(delta int) {
	for i := m.cursor + delta; i >= 0 && i < len(m.rows); i += delta {
		if m.rows[i].kind == rowTree {
			m.cursor = i
			m.follow = true
			return
		}
	}
}
