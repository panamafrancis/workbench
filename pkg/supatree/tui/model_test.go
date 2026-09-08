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

// Tree names shared by the navigation tests.
const (
	berlin = "berlin"
	cairo  = "cairo"
	delhi  = "delhi"
)

// threeTrees builds a model holding three synthetic supatrees, each with two
// member repos, for the navigation tests.
func threeTrees(t *testing.T) *Model {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	m := New(supatree.DefaultConfig(), config.DefaultConfig(), zellij.Workspace{})
	for _, name := range []string{berlin, cairo, delhi} {
		m.insts = append(m.insts, &supatree.Instance{
			Name: name, Slug: name,
			Members: []supatree.Member{
				{Alias: "terraform", Branch: "st/" + name + "/terraform"},
				{Alias: "keystone", Branch: "st/" + name + "/keystone"},
			},
		})
	}
	m.rebuildRows()
	return m
}

func treeAt(m *Model, i int) string {
	if i < 0 || i >= len(m.rows) {
		return ""
	}
	return m.rows[i].tree
}

func TestGotoTopAndBottom(t *testing.T) {
	m := threeTrees(t)

	m.cursor = 5
	if _, _ = m.Update(key("G")); m.cursor != len(m.rows)-1 {
		t.Fatalf("G: cursor = %d, want %d", m.cursor, len(m.rows)-1)
	}

	// gg is a two-key sequence: the first g only arms the prefix.
	_, _ = m.Update(key("g"))
	if m.pending != "g" {
		t.Fatalf("first g did not arm the prefix: pending = %q", m.pending)
	}
	if m.cursor == 0 {
		t.Fatal("a lone g must not move the cursor")
	}
	_, _ = m.Update(key("g"))
	if m.cursor != 0 {
		t.Fatalf("gg: cursor = %d, want 0", m.cursor)
	}
	if m.pending != "" {
		t.Fatalf("prefix not cleared: %q", m.pending)
	}
}

// An unrecognized second key cancels the prefix and is handled on its own, so a
// mistyped g doesn't swallow the next command.
func TestPendingPrefixFallsThrough(t *testing.T) {
	m := threeTrees(t)
	m.cursor = 0
	_, _ = m.Update(key("g"))
	_, _ = m.Update(key("j"))
	if m.pending != "" {
		t.Fatalf("prefix not cleared: %q", m.pending)
	}
	if m.cursor == 0 {
		t.Fatal("j after a cancelled g prefix should still move down")
	}
}

func TestJumpTreeMovesBetweenSupatrees(t *testing.T) {
	m := threeTrees(t)
	m.cursor = 0 // berlin's tree row

	_, _ = m.Update(key("}"))
	if got := treeAt(m, m.cursor); got != cairo || m.rows[m.cursor].kind != rowTree {
		t.Fatalf("} from berlin landed on %q (kind %v), want cairo tree row", got, m.rows[m.cursor].kind)
	}
	_, _ = m.Update(key("}"))
	if got := treeAt(m, m.cursor); got != delhi {
		t.Fatalf("} again landed on %q, want delhi", got)
	}
	// Past the last tree the cursor stays put rather than falling off the end.
	_, _ = m.Update(key("}"))
	if got := treeAt(m, m.cursor); got != delhi {
		t.Fatalf("} past the last tree moved to %q", got)
	}
	_, _ = m.Update(key("{"))
	if got := treeAt(m, m.cursor); got != cairo {
		t.Fatalf("{ landed on %q, want cairo", got)
	}
}

// From inside a tree, { goes to that tree's own header first — "up one level"
// before "up one tree".
func TestJumpTreeBackwardFromMemberRow(t *testing.T) {
	m := threeTrees(t)
	m.selectRow(row{kind: rowMember, tree: cairo, label: "keystone", alias: "keystone"})
	_, _ = m.Update(key("{"))
	if got := treeAt(m, m.cursor); got != cairo || m.rows[m.cursor].kind != rowTree {
		t.Fatalf("{ from a cairo member landed on %q (kind %v), want cairo tree row", got, m.rows[m.cursor].kind)
	}
}

func TestFoldAllAndUnfoldAll(t *testing.T) {
	m := threeTrees(t)
	full := len(m.rows)

	_, _ = m.Update(key("z"))
	_, _ = m.Update(key("M"))
	if len(m.rows) != 3 {
		t.Fatalf("zM: %d rows, want 3 (one per collapsed tree)", len(m.rows))
	}
	for _, name := range []string{berlin, cairo, delhi} {
		if !m.collapsed[name] {
			t.Errorf("zM did not collapse %s", name)
		}
	}

	_, _ = m.Update(key("z"))
	_, _ = m.Update(key("R"))
	if len(m.rows) != full {
		t.Fatalf("zR: %d rows, want %d", len(m.rows), full)
	}
}

// The wheel pans the viewport without moving the cursor, and the view stays
// where it was left instead of snapping back on the next render.
func TestWheelScrollsWithoutMovingCursor(t *testing.T) {
	m := threeTrees(t)
	m.cursor = 0
	m.viewport(5) // establish viewHeight

	_, _ = m.Update(tea.MouseMsg{Button: tea.MouseButtonWheelDown, Action: tea.MouseActionPress})
	if m.cursor != 0 {
		t.Fatalf("wheel moved the cursor to %d", m.cursor)
	}
	if m.scroll != wheelStep {
		t.Fatalf("scroll = %d, want %d", m.scroll, wheelStep)
	}
	if start, _ := m.viewport(5); start != wheelStep {
		t.Fatalf("render snapped back to the cursor: start = %d", start)
	}

	// Wheeling up past the top clamps rather than going negative.
	for range 5 {
		_, _ = m.Update(tea.MouseMsg{Button: tea.MouseButtonWheelUp, Action: tea.MouseActionPress})
	}
	if m.scroll != 0 {
		t.Fatalf("scroll = %d, want 0", m.scroll)
	}

	// A cursor move re-arms follow, pulling the window back to the cursor.
	m.scroll = 10
	m.follow = false
	_, _ = m.Update(key("j"))
	start, end := m.viewport(5)
	if m.cursor < start || m.cursor >= end {
		t.Fatalf("cursor %d outside viewport [%d,%d) after a key press", m.cursor, start, end)
	}
}

func TestClickSelectsRow(t *testing.T) {
	m := threeTrees(t)
	m.cursor = 0
	m.viewport(50) // everything visible, scroll 0

	// Row index 4 is berlin's first member (tree, agents, main, repositories, ...).
	_, _ = m.Update(tea.MouseMsg{
		Button: tea.MouseButtonLeft, Action: tea.MouseActionRelease, Y: 4 + rowsTopOffset,
	})
	if m.cursor != 4 {
		t.Fatalf("click selected row %d, want 4", m.cursor)
	}

	// Clicking a subheader is ignored — the cursor never rests on one.
	_, _ = m.Update(tea.MouseMsg{
		Button: tea.MouseButtonLeft, Action: tea.MouseActionRelease, Y: 1 + rowsTopOffset,
	})
	if m.cursor != 4 {
		t.Fatalf("click on a subheader moved the cursor to %d", m.cursor)
	}
}

// A background reload (tick/focus) must not yank a wheel-scrolled view back to
// the cursor — that would make the sidebar unreadable while scrolling.
func TestReloadKeepsScrollPosition(t *testing.T) {
	m := threeTrees(t)
	m.cursor = 0
	m.viewport(5)
	m.scrollBy(wheelStep)

	m.reloadWithSelection()
	if m.follow {
		t.Fatal("reload re-armed follow, which would snap the view back to the cursor")
	}
}
