package tui

import (
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/panamafrancis/workbench/pkg/config"
	"github.com/panamafrancis/workbench/pkg/github"
	"github.com/panamafrancis/workbench/pkg/supatree"
	"github.com/panamafrancis/workbench/pkg/zellij"
)

func key(s string) tea.KeyMsg {
	if len(s) == 1 {
		return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
	}
	switch s {
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEsc}
	}
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
}

func TestViewEmptyDoesNotPanic(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	m := New(supatree.DefaultConfig(), config.DefaultConfig(), zellij.Workspace{})
	out := m.View()
	if !strings.Contains(out, "supatree") {
		t.Errorf("view missing header:\n%s", out)
	}
	if !strings.Contains(out, "no supatrees") {
		t.Errorf("empty view should prompt to create one:\n%s", out)
	}
}

func TestRebuildRowsStructure(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	m := New(supatree.DefaultConfig(), config.DefaultConfig(), zellij.Workspace{})
	// Inject a synthetic instance and rebuild.
	m.insts = []*supatree.Instance{{
		Name: "berlin",
		Slug: "berlin",
		Members: []supatree.Member{
			{Alias: "terraform", Branch: "st/berlin/terraform"},
			{Alias: "keystone", Branch: "st/berlin/keystone"},
		},
	}}
	m.rebuildRows()
	// Expect: tree, "agents" subheader, main agent, "repositories" subheader, 2 members.
	kinds := make([]rowKind, 0, len(m.rows))
	for _, r := range m.rows {
		kinds = append(kinds, r.kind)
	}
	want := []rowKind{rowTree, rowSubheader, rowAgent, rowSubheader, rowMember, rowMember}
	if len(kinds) != len(want) {
		t.Fatalf("rows = %v, want %v", kinds, want)
	}
	for i := range want {
		if kinds[i] != want[i] {
			t.Fatalf("row %d kind = %v, want %v", i, kinds[i], want[i])
		}
	}
	// Cursor must never rest on a subheader.
	m.cursor = 1
	m.clampCursor()
	if m.rows[m.cursor].kind == rowSubheader {
		t.Error("cursor rested on subheader after clamp")
	}
}

func TestTreeFromTab(t *testing.T) {
	cases := map[string]string{
		"paris":          "paris",
		"paris:reviewer": "paris",
		"":               "",
	}
	for in, want := range cases {
		if got := treeFromTab(in); got != want {
			t.Errorf("treeFromTab(%q) = %q, want %q", in, got, want)
		}
	}
}

// Pressing n with a single stack skips the stack prompt and asks for a name;
// the stack prompt only appears when more than one stack is registered.
func TestNewTreeSingleStackAsksForName(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cfg := supatree.DefaultConfig()
	cfg.Stacks = []supatree.Stack{{Alias: "only", Path: "/tmp/only"}}
	m := New(cfg, config.DefaultConfig(), zellij.Workspace{})

	m.updateNormal(key("n"))
	if m.mode != modeNewTreeName {
		t.Fatalf("single stack: mode = %v, want modeNewTreeName", m.mode)
	}
	if m.actionStack != "" {
		t.Errorf("single stack: actionStack = %q, want empty (resolve default)", m.actionStack)
	}
}

func TestNewTreeMultiStackAsksForStackThenName(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cfg := supatree.DefaultConfig()
	cfg.Stacks = []supatree.Stack{{Alias: "a", Path: "/tmp/a"}, {Alias: "b", Path: "/tmp/b"}}
	m := New(cfg, config.DefaultConfig(), zellij.Workspace{})

	m.updateNormal(key("n"))
	if m.mode != modeNewTree {
		t.Fatalf("multi stack: mode = %v, want modeNewTree", m.mode)
	}
	// The picker lists stacks; move to the second ("b") and select it. This
	// should advance to the name prompt carrying the choice.
	m.updateInput(key("j"))
	m.updateInput(key("enter"))
	if m.mode != modeNewTreeName {
		t.Fatalf("after stack: mode = %v, want modeNewTreeName", m.mode)
	}
	if m.actionStack != "b" {
		t.Errorf("actionStack = %q, want %q", m.actionStack, "b")
	}
}

// reloadWithSelection keeps the cursor on the same logical row even when a new
// supatree sorts in above it and shifts every row index down.
func TestReloadWithSelectionPinsRow(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	m := New(supatree.DefaultConfig(), config.DefaultConfig(), zellij.Workspace{})
	m.insts = []*supatree.Instance{{
		Name:    "milan",
		Members: []supatree.Member{{Alias: "web", Branch: "st/milan/web"}},
	}}
	m.rebuildRows()
	// Select the member row of "milan".
	for i, r := range m.rows {
		if r.kind == rowMember && r.tree == "milan" {
			m.cursor = i
		}
	}
	want := *m.selected()

	// A tree that sorts before "milan" appears and shifts every row index down.
	m.insts = []*supatree.Instance{
		{Name: "athens", Members: []supatree.Member{{Alias: "api", Branch: "st/athens/api"}}},
		{Name: "milan", Members: []supatree.Member{{Alias: "web", Branch: "st/milan/web"}}},
	}
	m.rebuildRows()
	m.selectRow(want)
	if sel := m.selected(); sel == nil || sel.tree != "milan" || sel.kind != rowMember {
		t.Fatalf("selection not pinned to milan member after row shift: %+v", sel)
	}
}

func TestWrapPartsFoldsToWidth(t *testing.T) {
	parts := []string{"aaaa", "bbbb", "cccc"}
	// Width fits "aaaa · bbbb" (11) but not a third part.
	got := wrapParts(parts, " · ", 11)
	lines := strings.Split(got, "\n")
	if len(lines) != 2 {
		t.Fatalf("wrapParts folded into %d lines, want 2:\n%s", len(lines), got)
	}
	// Width 0 keeps everything on one line.
	if one := wrapParts(parts, " · ", 0); strings.Contains(one, "\n") {
		t.Errorf("wrapParts(width=0) should not wrap: %q", one)
	}
}

// Collapsing a tree hides its agents/repos and parks the cursor on the tree row.
func TestCollapseHidesChildrenAndKeepsCursor(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	m := New(supatree.DefaultConfig(), config.DefaultConfig(), zellij.Workspace{})
	m.insts = []*supatree.Instance{{
		Name:    "oslo",
		Members: []supatree.Member{{Alias: "web", Branch: "st/oslo/web"}},
	}}
	m.rebuildRows()
	full := len(m.rows)

	// Fold via the member row; children vanish, cursor lands on the tree row.
	for i, r := range m.rows {
		if r.kind == rowMember {
			m.cursor = i
		}
	}
	m.updateNormal(key(" "))
	if len(m.rows) != 1 || m.rows[0].kind != rowTree {
		t.Fatalf("collapsed rows = %d, want 1 tree row", len(m.rows))
	}
	if sel := m.selected(); sel == nil || sel.kind != rowTree || sel.tree != "oslo" {
		t.Fatalf("cursor not on tree row after collapse: %+v", sel)
	}

	// Unfold restores the children.
	m.updateNormal(key(" "))
	if len(m.rows) != full {
		t.Fatalf("expanded rows = %d, want %d", len(m.rows), full)
	}
}

// The viewport keeps the cursor visible when the list is taller than the pane.
func TestViewportScrollsToCursor(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	m := New(supatree.DefaultConfig(), config.DefaultConfig(), zellij.Workspace{})
	m.rows = make([]row, 20)
	for i := range m.rows {
		m.rows[i] = row{kind: rowTree, tree: "t", label: "t"}
	}

	// Cursor near the bottom with room for only 5 rows must scroll into view.
	m.cursor = 18
	start, end := m.viewport(5)
	if m.cursor < start || m.cursor >= end {
		t.Fatalf("cursor %d outside viewport [%d,%d)", m.cursor, start, end)
	}
	if end-start != 5 {
		t.Fatalf("viewport height = %d, want 5", end-start)
	}

	// Moving back to the top scrolls the window back up.
	m.cursor = 0
	start, _ = m.viewport(5)
	if start != 0 {
		t.Fatalf("scroll did not return to top: start = %d", start)
	}

	// A list that fits shows everything with no offset.
	m.cursor = 3
	start, end = m.viewport(50)
	if start != 0 || end != len(m.rows) {
		t.Fatalf("small list windowed unexpectedly: [%d,%d)", start, end)
	}
}

// A permanent gh error marks gh unavailable so the tick/focus loop stops
// re-fetching every 30s; a transient/successful result restores it and clears
// the hint.
func TestPrMsgGhAvailabilityAndHint(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	m := New(supatree.DefaultConfig(), config.DefaultConfig(), zellij.Workspace{})

	// Permanent error: unavailable, hint set, background loop drops the fetch.
	m.Update(prMsg{err: github.ErrGHAuth})
	if m.ghAvailable {
		t.Fatal("permanent error should mark gh unavailable")
	}
	if m.prHint != "gh auth required" {
		t.Fatalf("prHint = %q, want gh auth required", m.prHint)
	}
	if got := len(m.backgroundCmds()); got != 2 {
		t.Fatalf("backgroundCmds with gh down = %d cmds, want 2 (no fetch)", got)
	}

	// Transient error clears a stale hint without flipping availability back.
	m.prHint = "gh rate limited"
	m.Update(prMsg{err: errors.New("network blip")})
	if m.prHint != "" {
		t.Fatalf("transient error left stale hint %q", m.prHint)
	}

	// Success restores availability and re-enables the fetch.
	m.Update(prMsg{err: nil})
	if !m.ghAvailable || m.prHint != "" {
		t.Fatalf("success should restore gh: ghAvailable=%v prHint=%q", m.ghAvailable, m.prHint)
	}
	if got := len(m.backgroundCmds()); got != 3 {
		t.Fatalf("backgroundCmds with gh up = %d cmds, want 3 (incl fetch)", got)
	}
}

func TestActiveTreeMarkerInView(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	m := New(supatree.DefaultConfig(), config.DefaultConfig(), zellij.Workspace{})
	m.insts = []*supatree.Instance{
		{Name: "here", Members: []supatree.Member{{Alias: "x", Branch: "st/here/x"}}},
		{Name: "there", Members: []supatree.Member{{Alias: "y", Branch: "st/there/y"}}},
	}
	m.rebuildRows()
	m.activeTree = "here"
	out := m.View()
	if !strings.Contains(out, "▸") {
		t.Errorf("active-tree marker missing from view:\n%s", out)
	}
}
