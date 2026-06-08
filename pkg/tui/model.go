package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/panamafrancis/workbench/pkg/config"
	"github.com/panamafrancis/workbench/pkg/sandbox"
	"github.com/panamafrancis/workbench/pkg/zellij"
)

type refreshMsg struct{}
type openErrMsg struct{ err error }

type Model struct {
	cfg    *config.Config
	tree   TreeModel
	width  int
	height int
	keys   KeyMap
	err    error
	msg    string
}

func New(cfg *config.Config) *Model {
	t := newTree(cfg)
	t.refreshDirty()
	return &Model{
		cfg:  cfg,
		tree: t,
		keys: DefaultKeyMap,
	}
}

func (m *Model) Init() tea.Cmd {
	return nil
}

func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height

	case refreshMsg:
		m.tree.refreshDirty()
		m.msg = "refreshed"

	case openErrMsg:
		m.err = msg.err

	case tea.KeyMsg:
		m.err = nil
		m.msg = ""
		switch {
		case key.Matches(msg, m.keys.Quit):
			return m, tea.Quit
		case key.Matches(msg, m.keys.Up):
			m.tree.moveUp()
		case key.Matches(msg, m.keys.Down):
			m.tree.moveDown()
		case key.Matches(msg, m.keys.Toggle):
			m.tree.toggleCollapse()
		case key.Matches(msg, m.keys.Refresh):
			return m, func() tea.Msg { return refreshMsg{} }
		case key.Matches(msg, m.keys.Open):
			return m, m.openSelected("")
		}
	}
	return m, nil
}

func (m *Model) openSelected(modelOverride string) tea.Cmd {
	sel := m.tree.selected()
	if sel == nil || sel.isRepo {
		return nil
	}
	wt := m.cfg.Repos[sel.repoIdx].Worktrees[sel.worktreeIdx]
	repo := m.cfg.Repos[sel.repoIdx]
	modelKey := m.cfg.ResolveModel(modelOverride)
	if wt.Model != "" {
		modelKey = m.cfg.ResolveModel(wt.Model)
	}
	if modelOverride != "" {
		modelKey = modelOverride
	}

	return func() tea.Msg {
		if !zellij.IsInZellij() {
			return openErrMsg{fmt.Errorf("not inside a Zellij session")}
		}
		if err := repo.RunStartup(wt.Path, wt.Name); err != nil {
			return openErrMsg{fmt.Errorf("startup script: %w", err)}
		}
		nonoArgs, err := sandbox.BuildNonoArgs(wt.Path, modelKey, m.cfg)
		if err != nil {
			return openErrMsg{err}
		}
		if err := zellij.OpenTab(wt.Name, wt.Path, nonoArgs); err != nil {
			return openErrMsg{err}
		}
		return nil
	}
}

func (m *Model) View() string {
	var sb strings.Builder

	title := styleHeader.Render("workbench")
	hint := styleMuted.Render("[?] j/k=nav  enter=open  space=collapse  r=refresh  q=quit")
	header := lipgloss.JoinHorizontal(lipgloss.Top, title, "  ", hint)
	sb.WriteString(header)
	sb.WriteString("\n")
	sb.WriteString(styleMuted.Render(strings.Repeat("─", m.width)))
	sb.WriteString("\n")

	sb.WriteString(m.tree.view(m.width))

	sb.WriteString(styleMuted.Render(strings.Repeat("─", m.width)))
	sb.WriteString("\n")

	if m.err != nil {
		sb.WriteString(styleDirty.Render("error: " + m.err.Error()))
	} else if m.msg != "" {
		sb.WriteString(styleMuted.Render(m.msg))
	} else {
		sb.WriteString(styleStatusBar.Render("[n]ew  [o]pen  [d]el  [r]efresh  [q]uit"))
	}

	return sb.String()
}
