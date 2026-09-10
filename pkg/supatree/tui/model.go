// Package tui implements the supatree sidebar: a Bubble Tea model showing each
// supatree with its agents and member repositories (dirty + PR status), and
// keys to open/resume agents, sync, and delete.
package tui

import (
	"os"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/panamafrancis/workbench/pkg/config"
	"github.com/panamafrancis/workbench/pkg/github"
	"github.com/panamafrancis/workbench/pkg/supatree"
	"github.com/panamafrancis/workbench/pkg/zellij"
)

const (
	tickInterval = 30 * time.Second
	// prStaleAge bounds how old a cached PR status may be before a fetch is
	// allowed. Combined with the on-disk cache and InBackoff, it stops the
	// sidebar's restart loop (and per-tab sidebars) from exhausting the gh
	// rate limit.
	prStaleAge = 10 * time.Minute
	// wheelStep is how many rows one mouse-wheel notch scrolls.
	wheelStep = 3
	// fallbackPage is the half-page distance used by ctrl+d/ctrl+u before the
	// pane has reported its size.
	fallbackPage = 5
	// rowsTopOffset is the number of lines View renders above the first row (the
	// "supatree" header). Mouse Y coordinates are translated through it.
	rowsTopOffset = 1
)

type mode int

const (
	modeNormal mode = iota
	modeNewAgent
	modeNewTree     // choosing a stack (only when >1 stack is registered)
	modeNewTreeName // naming the supatree
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
	stCfg       *supatree.Config
	wbCfg       *config.Config
	ws          zellij.Workspace
	prCache     *github.Cache
	insts       []*supatree.Instance
	rows        []row
	cursor      int
	dirty       map[string]bool // member path -> dirty
	openTabs    map[string]bool
	collapsed   map[string]bool // supatree name -> folded (agents/repos hidden)
	isSidebar   bool
	width       int
	height      int
	scroll      int    // index of the first rendered row (viewport top)
	viewHeight  int    // rows the viewport last rendered; drives ctrl+d/ctrl+u and wheel clamping
	follow      bool   // keep the cursor in view on the next render (false while wheel-scrolled away)
	pending     string // half-typed multi-key sequence ("g" or "z")
	mode        mode
	input       textinput.Model
	actionTree  string // tree targeted by the active input mode
	actionStack string // stack chosen for a pending new-tree create
	stackCursor int    // cursor within the modeNewTree stack picker
	activeTree  string // supatree whose Zellij tab this sidebar belongs to ("you are here")
	fetching    bool   // a PR fetch is in flight
	ghAvailable bool   // gh usable; false after a permanent error suppresses tick fetches
	prHint      string // persistent PR-fetch hint (e.g. "gh rate limited")
	msg         string
	err         error
}

// New builds the sidebar model.
func New(stCfg *supatree.Config, wbCfg *config.Config, ws zellij.Workspace) *Model {
	cache := github.NewCache(supatree.PRCachePath())
	_ = cache.Load()
	m := &Model{
		stCfg:       stCfg,
		wbCfg:       wbCfg,
		ws:          ws,
		prCache:     cache,
		dirty:       map[string]bool{},
		openTabs:    map[string]bool{},
		collapsed:   map[string]bool{},
		ghAvailable: true,
		follow:      true,
		isSidebar:   os.Getenv("SUPATREE_SIDEBAR") == "1",
		activeTree:  treeFromTab(os.Getenv("SUPATREE_ACTIVE_TREE")),
		input:       textinput.New(),
	}
	m.reload()
	return m
}

func (m *Model) reload() {
	insts, _ := supatree.List(m.stCfg)
	m.insts = insts
	m.rebuildRows()
}

// reloadWithSelection re-reads live state (so newly created/removed supatrees
// appear without a manual refresh) while keeping the cursor pinned to the same
// logical row across the rebuild, since another tab may have reordered rows.
func (m *Model) reloadWithSelection() {
	var want *row
	if r := m.selected(); r != nil {
		cp := *r
		want = &cp
	}
	m.reload()
	if want != nil {
		m.selectRow(*want)
	}
}

// selectRow moves the cursor to the row matching want's identity (kind + tree +
// label + alias), leaving it where clampCursor lands if there is no match.
func (m *Model) selectRow(want row) {
	for i, r := range m.rows {
		if r.kind == want.kind && r.tree == want.tree && r.label == want.label && r.alias == want.alias {
			m.cursor = i
			break
		}
	}
	m.clampCursor()
}

// setCollapse folds or unfolds a supatree's agents/repos and parks the cursor on
// its (still-visible) tree row so it never lands in the rows that just vanished.
func (m *Model) setCollapse(tree string, collapsed bool) {
	if m.collapsed[tree] == collapsed {
		return
	}
	m.collapsed[tree] = collapsed
	m.rebuildRows()
	m.selectRow(row{kind: rowTree, tree: tree, label: tree})
	m.follow = true
}

// setAllCollapsed folds or unfolds every supatree at once (vim's zM / zR),
// keeping the cursor on the supatree it was already in.
func (m *Model) setAllCollapsed(collapsed bool) {
	cur := ""
	if r := m.selected(); r != nil {
		cur = r.tree
	}
	for _, inst := range m.insts {
		m.collapsed[inst.Name] = collapsed
	}
	m.rebuildRows()
	if cur != "" {
		m.selectRow(row{kind: rowTree, tree: cur, label: cur})
	}
	m.follow = true
}

// gotoTop / gotoBottom are vim's gg and G.
func (m *Model) gotoTop() {
	m.cursor = 0
	m.clampCursor()
	m.follow = true
}

func (m *Model) gotoBottom() {
	m.cursor = len(m.rows) - 1
	m.clampCursor()
	m.follow = true
}

// jumpTree moves the cursor to the next (delta > 0) or previous (delta < 0)
// supatree header row — vim's paragraph motions over the tree blocks. A jump
// backwards from inside a tree lands on that tree's own header first, which is
// what "go up one tree" means when the cursor sits on an agent or member row.
func (m *Model) jumpTree(delta int) {
	for i := m.cursor + delta; i >= 0 && i < len(m.rows); i += delta {
		if m.rows[i].kind == rowTree {
			m.cursor = i
			m.follow = true
			return
		}
	}
}

// halfPage is the ctrl+d / ctrl+u distance: half the visible rows, or a fixed
// fallback before the pane has reported its size.
func (m *Model) halfPage() int {
	if m.viewHeight <= 0 {
		return fallbackPage
	}
	if half := m.viewHeight / 2; half > 0 {
		return half
	}
	return 1
}

// scrollBy pans the viewport without moving the cursor (mouse wheel). It clears
// the follow flag so the view stays where the user left it instead of snapping
// back to the cursor on the next render or tick.
func (m *Model) scrollBy(delta int) {
	if m.viewHeight <= 0 || len(m.rows) <= m.viewHeight {
		return
	}
	m.follow = false
	m.scroll += delta
	if maxScroll := len(m.rows) - m.viewHeight; m.scroll > maxScroll {
		m.scroll = maxScroll
	}
	if m.scroll < 0 {
		m.scroll = 0
	}
}

// selectByRow moves the cursor to the row at a viewport-relative offset (a mouse
// click). Subheaders are not selectable, so a click on one is ignored.
func (m *Model) selectByRow(visible int) {
	i := m.scroll + visible
	if i < 0 || i >= len(m.rows) || m.rows[i].kind == rowSubheader {
		return
	}
	m.cursor = i
	m.follow = true
}

// treeFromTab extracts the supatree name from a Zellij tab identity, which is
// "<tree>" for the main agent and "<tree>:<agent>" otherwise (see TabName).
func treeFromTab(tab string) string {
	name, _, _ := strings.Cut(tab, ":")
	return name
}

func (m *Model) rebuildRows() {
	var rows []row
	for _, inst := range m.insts {
		rows = append(rows, row{kind: rowTree, tree: inst.Name, label: inst.Name})
		if m.collapsed[inst.Name] {
			continue
		}
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
	// Non-forced fetch: honor the on-disk cache. The sidebar runs in a restart
	// loop, so forcing here would re-hit the gh API for every member on every
	// restart and exhaust the rate limit.
	return tea.Batch(m.tickCmd(), m.refreshDirtyCmd(), m.refreshRunningCmd(), m.fetchPRCmd(false))
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
type prMsg struct {
	err error
	// deferred counts branches held back to stay above the shared rate-limit
	// reserve; the footer surfaces it so a missing status has a reason.
	deferred int
}

// prSkippedMsg is emitted when a fetch round was ceded to another sidebar
// process (the shared fetch lock was busy); it only clears the in-flight flag,
// leaving the rate-limit hint and gh availability untouched.
type prSkippedMsg struct{}
type actionDoneMsg struct {
	msg string
	err error
	// reveal, when set, is a supatree name to move the cursor onto after the
	// post-action reload — used so a freshly created tree is scrolled into view
	// instead of being added off-screen while the cursor stays where it was.
	reveal string
}
