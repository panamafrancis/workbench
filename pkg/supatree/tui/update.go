package tui

import (
	"fmt"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/panamafrancis/workbench/pkg/git"
	"github.com/panamafrancis/workbench/pkg/github"
	"github.com/panamafrancis/workbench/pkg/supatree"
)

func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
	case tickMsg:
		return m, tea.Batch(m.tickCmd(), m.refreshDirtyCmd(), m.refreshRunningCmd())
	case dirtyMsg:
		m.dirty = msg.dirty
	case runningMsg:
		m.openTabs = msg.tabs
	case prMsg:
		_ = m.prCache.Load()
	case actionDoneMsg:
		m.msg = msg.msg
		m.err = msg.err
		m.reload()
		return m, tea.Batch(m.refreshDirtyCmd(), m.refreshRunningCmd())
	case tea.KeyMsg:
		if m.mode != modeNormal {
			return m.updateInput(msg)
		}
		return m.updateNormal(msg)
	}
	return m, nil
}

func (m *Model) updateNormal(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "q", "ctrl+c":
		if m.isSidebar && msg.String() == "q" {
			m.mode = modeConfirmQuit
			return m, nil
		}
		return m, tea.Quit
	case "j", "down":
		m.moveCursor(1)
	case "k", "up":
		m.moveCursor(-1)
	case "r":
		m.reload()
		return m, tea.Batch(m.refreshDirtyCmd(), m.refreshRunningCmd(), m.fetchPRCmd())
	case "enter", "o":
		return m, m.openSelected()
	case "a":
		if r := m.selected(); r != nil {
			m.mode = modeNewAgent
			m.actionTree = r.tree
			m.input.SetValue("")
			m.input.Placeholder = "agent name"
			m.input.Focus()
		}
	case "n":
		m.mode = modeNewTree
		m.input.SetValue("")
		m.input.Placeholder = stackPlaceholder(m)
		m.input.Focus()
	case "s":
		if r := m.selected(); r != nil {
			return m, m.syncTree(r.tree)
		}
	case "d":
		if r := m.selected(); r != nil {
			m.mode = modeConfirmDelete
			m.actionTree = r.tree
		}
	}
	return m, nil
}

func (m *Model) updateInput(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.mode == modeConfirmDelete || m.mode == modeConfirmQuit {
		confirmed := msg.String() == "y" || msg.String() == "Y"
		wasDelete := m.mode == modeConfirmDelete
		tree := m.actionTree
		m.mode = modeNormal
		if !confirmed {
			return m, nil
		}
		if wasDelete {
			return m, m.removeTree(tree)
		}
		return m, tea.Quit
	}

	// Text-input modes (new agent / new tree).
	switch msg.String() {
	case "esc":
		m.mode = modeNormal
		m.input.Blur()
		return m, nil
	case "enter":
		val := m.input.Value()
		isAgent := m.mode == modeNewAgent
		tree := m.actionTree
		m.mode = modeNormal
		m.input.Blur()
		if isAgent {
			if val == "" {
				return m, nil
			}
			return m, m.openAgent(tree, val)
		}
		return m, m.newTree(val)
	default:
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(msg)
		return m, cmd
	}
}

func (m *Model) moveCursor(delta int) {
	m.cursor += delta
	for m.cursor >= 0 && m.cursor < len(m.rows) && m.rows[m.cursor].kind == rowSubheader {
		m.cursor += delta
	}
	if m.cursor < 0 {
		m.cursor = 0
		m.clampCursor()
	}
	if m.cursor >= len(m.rows) {
		m.cursor = len(m.rows) - 1
		m.clampCursor()
	}
}

// --- actions (return tea.Cmd producing actionDoneMsg) ---

func (m *Model) openSelected() tea.Cmd {
	r := m.selected()
	if r == nil {
		return nil
	}
	switch r.kind {
	case rowMember:
		return m.openMember(r.tree, r.alias)
	case rowAgent:
		return m.openAgent(r.tree, r.label)
	case rowTree, rowSubheader:
		return m.openAgent(r.tree, "main")
	}
	return nil
}

func (m *Model) openAgent(tree, agent string) tea.Cmd {
	return func() tea.Msg {
		inst := m.instance(tree)
		if inst == nil {
			return actionDoneMsg{err: fmt.Errorf("supatree %q gone", tree)}
		}
		_, err := supatree.OpenRootAgent(inst, m.wbCfg, m.ws, m.stCfg.ResolveSidebarWidth(), agent, "", nil)
		return actionDoneMsg{msg: "opened " + supatree.TabName(tree, agent), err: err}
	}
}

func (m *Model) openMember(tree, alias string) tea.Cmd {
	return func() tea.Msg {
		inst := m.instance(tree)
		if inst == nil {
			return actionDoneMsg{err: fmt.Errorf("supatree %q gone", tree)}
		}
		_, err := supatree.OpenMemberAgent(inst, m.wbCfg, m.ws, m.stCfg.ResolveSidebarWidth(), alias, "")
		return actionDoneMsg{msg: "opened " + supatree.TabName(tree, alias), err: err}
	}
}

func (m *Model) syncTree(tree string) tea.Cmd {
	return func() tea.Msg {
		inst := m.instance(tree)
		if inst == nil {
			return actionDoneMsg{err: fmt.Errorf("supatree %q gone", tree)}
		}
		report, err := supatree.Sync(inst.Root, m.wbCfg, false)
		if err != nil {
			return actionDoneMsg{err: err}
		}
		return actionDoneMsg{msg: fmt.Sprintf("synced %s (+%d)", tree, len(report.Created))}
	}
}

func (m *Model) removeTree(tree string) tea.Cmd {
	return func() tea.Msg {
		res, err := supatree.Remove(m.stCfg, m.wbCfg, tree, supatree.RemoveOptions{Force: true})
		if err != nil {
			return actionDoneMsg{err: err}
		}
		m.ws.CleanupLayout(tree)
		for _, a := range res.Agents {
			m.ws.CleanupLayout(supatree.TabName(tree, a.Name))
		}
		return actionDoneMsg{msg: "removed " + tree}
	}
}

func (m *Model) newTree(stack string) tea.Cmd {
	return func() tea.Msg {
		inst, _, err := supatree.New(m.stCfg, m.wbCfg, supatree.CreateOptions{Stack: stack})
		if err != nil {
			return actionDoneMsg{err: err}
		}
		return actionDoneMsg{msg: "created " + inst.Name}
	}
}

// --- background polling ---

func (m *Model) refreshDirtyCmd() tea.Cmd {
	insts := m.insts
	return func() tea.Msg {
		dirty := map[string]bool{}
		for _, inst := range insts {
			for _, mem := range inst.Members {
				if mem.Exists && git.IsDirty(mem.Path) {
					dirty[mem.Path] = true
				}
			}
		}
		return dirtyMsg{dirty: dirty}
	}
}

func (m *Model) refreshRunningCmd() tea.Cmd {
	return func() tea.Msg {
		tabs, err := zellijTabs()
		if err != nil {
			return runningMsg{tabs: map[string]bool{}}
		}
		return runningMsg{tabs: tabs}
	}
}

func (m *Model) fetchPRCmd() tea.Cmd {
	insts := m.insts
	cache := m.prCache
	return func() tea.Msg {
		for _, inst := range insts {
			for _, mem := range inst.Members {
				if !mem.Exists {
					continue
				}
				info, err := github.LookupPR(mem.Path, mem.Branch)
				if err != nil {
					if github.IsPermanentError(err) {
						return prMsg{}
					}
					continue
				}
				cache.Set(mem.Branch, info)
			}
		}
		_ = cache.Save()
		return prMsg{}
	}
}
