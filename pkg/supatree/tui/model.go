// Package tui implements the supatree sidebar: a Bubble Tea model showing each
// supatree with its agents and member repositories (dirty + PR status), and
// keys to open/resume agents, sync, and delete.
package tui

import (
	"os"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/panamafrancis/workbench/pkg/config"
	"github.com/panamafrancis/workbench/pkg/github"
	"github.com/panamafrancis/workbench/pkg/supatree"
	"github.com/panamafrancis/workbench/pkg/zellij"
)

const tickInterval = 30 * time.Second

type mode int

const (
	modeNormal mode = iota
	modeNewAgent
	modeNewTree
	modeConfirmDelete
	modeConfirmQuit
)

type rowKind int

const (
	rowTree rowKind = iota
	rowSubheader
	rowAgent
	rowMember
)

type row struct {
	kind  rowKind
	tree  string // supatree name this row belongs to
	label string // agent name / member alias / subheader text
	alias string // member alias (rowMember)
}

// Model is the supatree sidebar model.
type Model struct {
	stCfg      *supatree.Config
	wbCfg      *config.Config
	ws         zellij.Workspace
	prCache    *github.Cache
	insts      []*supatree.Instance
	rows       []row
	cursor     int
	dirty      map[string]bool // member path -> dirty
	openTabs   map[string]bool
	isSidebar  bool
	width      int
	height     int
	mode       mode
	input      textinput.Model
	actionTree string // tree targeted by the active input mode
	msg        string
	err        error
}

// New builds the sidebar model.
func New(stCfg *supatree.Config, wbCfg *config.Config, ws zellij.Workspace) *Model {
	cache := github.NewCache(supatree.PRCachePath())
	_ = cache.Load()
	m := &Model{
		stCfg:     stCfg,
		wbCfg:     wbCfg,
		ws:        ws,
		prCache:   cache,
		dirty:     map[string]bool{},
		openTabs:  map[string]bool{},
		isSidebar: os.Getenv("SUPATREE_SIDEBAR") == "1",
		input:     textinput.New(),
	}
	m.reload()
	return m
}

func (m *Model) reload() {
	insts, _ := supatree.List(m.stCfg)
	m.insts = insts
	m.rebuildRows()
}

func (m *Model) rebuildRows() {
	var rows []row
	for _, inst := range m.insts {
		rows = append(rows, row{kind: rowTree, tree: inst.Name, label: inst.Name})
		agents, _ := supatree.LoadAgents(inst.Root)
		rows = append(rows, row{kind: rowSubheader, tree: inst.Name, label: "agents"})
		if len(agents) == 0 {
			rows = append(rows, row{kind: rowAgent, tree: inst.Name, label: "main"})
		}
		for _, a := range agents {
			rows = append(rows, row{kind: rowAgent, tree: inst.Name, label: a.Name})
		}
		rows = append(rows, row{kind: rowSubheader, tree: inst.Name, label: "repositories"})
		for _, mem := range inst.Members {
			rows = append(rows, row{kind: rowMember, tree: inst.Name, label: mem.Alias, alias: mem.Alias})
		}
	}
	m.rows = rows
	m.clampCursor()
}

func (m *Model) instance(name string) *supatree.Instance {
	for _, i := range m.insts {
		if i.Name == name {
			return i
		}
	}
	return nil
}

func (m *Model) Init() tea.Cmd {
	return tea.Batch(m.tickCmd(), m.refreshDirtyCmd(), m.refreshRunningCmd(), m.fetchPRCmd())
}

func (m *Model) clampCursor() {
	if m.cursor >= len(m.rows) {
		m.cursor = len(m.rows) - 1
	}
	if m.cursor < 0 {
		m.cursor = 0
	}
	// Never rest on a subheader.
	for m.cursor < len(m.rows) && m.rows[m.cursor].kind == rowSubheader {
		m.cursor++
	}
	if m.cursor >= len(m.rows) {
		for m.cursor >= 0 && (m.cursor >= len(m.rows) || m.rows[m.cursor].kind == rowSubheader) {
			m.cursor--
		}
	}
}

func (m *Model) selected() *row {
	if m.cursor >= 0 && m.cursor < len(m.rows) {
		return &m.rows[m.cursor]
	}
	return nil
}

func (m *Model) tickCmd() tea.Cmd {
	return tea.Tick(tickInterval, func(t time.Time) tea.Msg { return tickMsg{} })
}

type tickMsg struct{}
type dirtyMsg struct{ dirty map[string]bool }
type runningMsg struct{ tabs map[string]bool }
type prMsg struct{}
type actionDoneMsg struct {
	msg string
	err error
}
