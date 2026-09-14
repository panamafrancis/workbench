// Package dash implements `supatree dash`: a wide, read-mostly dashboard of
// every supatree's activity — where each one sits in the ship lifecycle, its
// PR numbers and review state, and which trees have gone stale or are finished
// and only occupying disk.
//
// It shares the sidebar's GitHub quota discipline (supatree.FetchTargets /
// FetchPRs): the dashboard reads the on-disk PR cache and only fetches on the
// same staleness gate, under the same cross-process try-lock, so running one
// alongside a screenful of sidebars adds no extra load on the API.
package dash

import (
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/panamafrancis/workbench/pkg/github"
	"github.com/panamafrancis/workbench/pkg/supatree"
	"github.com/panamafrancis/workbench/pkg/zellij"
)

const (
	// tickInterval re-derives local state (git) and re-reads the PR cache. The
	// gh fetch behind it stays on supatree.PRStaleAge.
	tickInterval = 30 * time.Second
	// wheelStep is how many rows one mouse-wheel notch scrolls.
	wheelStep = 3
	// fallbackPage is the ctrl+d/ctrl+u distance before the pane reports a size.
	fallbackPage = 10
)

type rowKind int

const (
	rowTree rowKind = iota
	rowMember
)

type row struct {
	kind   rowKind
	tree   int // index into summary.Trees
	member int // index into that tree's Members (rowMember only)
}

// Model is the dashboard's Bubble Tea model.
type Model struct {
	stCfg   *supatree.Config
	cache   *github.Cache
	summary supatree.Summary
	rows    []row

	expanded map[string]bool // tree name -> member rows shown
	cursor   int
	scroll   int
	follow   bool
	viewRows int
	width    int
	height   int
	pending  string // half-typed multi-key sequence ("g")

	loaded   bool
	fetching bool
	prHint   string
	msg      string
	err      error
}

// New builds a dashboard model over the given registry.
func New(stCfg *supatree.Config) *Model {
	cache := github.NewCache(supatree.PRCachePath())
	_ = cache.Load()
	return &Model{
		stCfg:    stCfg,
		cache:    cache,
		expanded: map[string]bool{},
		follow:   true,
	}
}

func (m *Model) Init() tea.Cmd {
	// A non-forced fetch on start: honor the cache so opening the dashboard
	// repeatedly does not re-query GitHub for every member each time.
	return tea.Batch(m.reloadCmd(), m.fetchCmd(false), m.tickCmd())
}

type tickMsg struct{}
type summaryMsg struct{ summary supatree.Summary }
type prMsg struct {
	err     error
	skipped bool
	backoff bool
}
type actionMsg struct {
	msg string
	err error
}

func (m *Model) tickCmd() tea.Cmd {
	return tea.Tick(tickInterval, func(time.Time) tea.Msg { return tickMsg{} })
}

// reloadCmd re-lists the supatrees and re-derives their status off the main
// loop: the local git calls are cheap but numerous, so they must not block
// rendering or keystrokes.
func (m *Model) reloadCmd() tea.Cmd {
	stCfg, cache := m.stCfg, m.cache
	return func() tea.Msg {
		insts, _ := supatree.List(stCfg)
		// Pick up statuses another process (a sidebar, or `supatree status
		// --refresh`) has written since the last pass.
		_ = cache.Load()
		return summaryMsg{summary: supatree.Status(insts, cache, supatree.StatusOptions{})}
	}
}

// fetchCmd refreshes PR status through the shared, locked fetch path. force
// bypasses the staleness gate (the `r` key) but never the persisted backoff.
//
// Everything — the cache re-read, the backoff check and target selection —
// happens inside the returned command. Choosing targets asks git whether each
// member branch has been pushed, which is one process per member; on the main
// loop that would freeze the first paint and every tick.
func (m *Model) fetchCmd(force bool) tea.Cmd {
	if m.fetching {
		return nil
	}
	m.fetching = true
	stCfg, cache := m.stCfg, m.cache
	return func() tea.Msg {
		_ = cache.Load()
		if cache.InBackoff(time.Now()) {
			// A peer process armed the backoff; surface it here too.
			return prMsg{backoff: true}
		}
		insts, _ := supatree.List(stCfg)
		targets := supatree.FetchTargets(insts, cache, force, supatree.PRStaleAge)
		if len(targets) == 0 {
			return prMsg{skipped: true}
		}
		out := supatree.FetchPRs(targets, cache, force, supatree.PRStaleAge)
		return prMsg{err: out.Err, skipped: out.Skipped}
	}
}

// openTreeCmd focuses the Zellij tab of the supatree under the cursor, so the
// dashboard doubles as a switcher when it runs inside a session.
func (m *Model) openTreeCmd(name string) tea.Cmd {
	return func() tea.Msg {
		if !zellij.IsInZellij() {
			return actionMsg{msg: "not inside zellij — run: supatree open " + name}
		}
		if err := zellij.GoToTab(name); err != nil {
			return actionMsg{msg: "no open tab for " + name + " — run: supatree open " + name}
		}
		return actionMsg{}
	}
}

func (m *Model) rebuildRows() {
	rows := make([]row, 0, len(m.summary.Trees))
	for i, t := range m.summary.Trees {
		rows = append(rows, row{kind: rowTree, tree: i})
		if !m.expanded[t.Name] {
			continue
		}
		for j := range t.Members {
			rows = append(rows, row{kind: rowMember, tree: i, member: j})
		}
	}
	m.rows = rows
	m.clampCursor()
}

func (m *Model) clampCursor() {
	if m.cursor >= len(m.rows) {
		m.cursor = len(m.rows) - 1
	}
	if m.cursor < 0 {
		m.cursor = 0
	}
}

func (m *Model) selected() *row {
	if m.cursor < 0 || m.cursor >= len(m.rows) {
		return nil
	}
	return &m.rows[m.cursor]
}

// selectedTree returns the supatree under the cursor, whether the cursor sits
// on its own row or one of its member rows.
func (m *Model) selectedTree() *supatree.TreeStatus {
	r := m.selected()
	if r == nil || r.tree >= len(m.summary.Trees) {
		return nil
	}
	return &m.summary.Trees[r.tree]
}

func (m *Model) moveCursor(delta int) {
	m.cursor += delta
	m.clampCursor()
	m.follow = true
}

func (m *Model) halfPage() int {
	if m.viewRows <= 0 {
		return fallbackPage
	}
	if half := m.viewRows / 2; half > 0 {
		return half
	}
	return 1
}

// scrollBy pans the viewport without moving the cursor (mouse wheel), clearing
// follow so a scrolled-away view survives the background tick.
func (m *Model) scrollBy(delta int) {
	if m.viewRows <= 0 || len(m.rows) <= m.viewRows {
		return
	}
	m.follow = false
	m.scroll += delta
	if maxScroll := len(m.rows) - m.viewRows; m.scroll > maxScroll {
		m.scroll = maxScroll
	}
	if m.scroll < 0 {
		m.scroll = 0
	}
}

// setExpanded shows or hides one supatree's member rows, keeping the cursor on
// the tree row so it never lands in rows that just vanished.
func (m *Model) setExpanded(name string, expanded bool) {
	if m.expanded[name] == expanded {
		return
	}
	m.expanded[name] = expanded
	m.rebuildRows()
	for i, r := range m.rows {
		if r.kind == rowTree && m.summary.Trees[r.tree].Name == name {
			m.cursor = i
			break
		}
	}
	m.follow = true
}

func (m *Model) setAllExpanded(expanded bool) {
	for _, t := range m.summary.Trees {
		m.expanded[t.Name] = expanded
	}
	m.rebuildRows()
	m.follow = true
}
