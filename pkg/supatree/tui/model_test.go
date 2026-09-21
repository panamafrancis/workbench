package tui

import (
	"errors"
	"slices"
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
	// Repositories start folded, so out of the box: tree, "agents" subheader,
	// main agent, "repositories" header — and no member rows.
	want := []rowKind{rowTree, rowSubheader, rowAgent, rowRepos}
	if got := rowKinds(m); !slices.Equal(got, want) {
		t.Fatalf("folded rows = %v, want %v", got, want)
	}

	// Unfolding the section adds the members under it.
	expandRepos(m)
	want = []rowKind{rowTree, rowSubheader, rowAgent, rowRepos, rowMember, rowMember}
	if got := rowKinds(m); !slices.Equal(got, want) {
		t.Fatalf("unfolded rows = %v, want %v", got, want)
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
	expandRepos(m)
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
	expandRepos(m)
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
	m := osloModel(t)
	full := len(m.rows)

	// Fold via the tree row; children vanish, cursor stays on the tree row.
	m.cursor = rowIndex(m, rowTree)
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

// space folds the innermost section the cursor is in: from a member row that is
// the repositories list, not the whole supatree around it.
func TestSpaceOnMemberRowFoldsOnlyRepos(t *testing.T) {
	m := osloModel(t)

	m.cursor = rowIndex(m, rowMember)
	m.updateNormal(key(" "))
	if rowIndex(m, rowMember) != -1 {
		t.Fatal("space on a member row left the member rows visible")
	}
	if rowIndex(m, rowAgent) == -1 {
		t.Fatal("space on a member row folded the whole supatree, not just its repos")
	}
	// The cursor parks on the section header, the row that survived the fold.
	if sel := m.selected(); sel == nil || sel.kind != rowRepos {
		t.Fatalf("cursor = %+v, want the repositories header", sel)
	}

	// h on the already-folded header steps out and folds the supatree itself.
	m.updateNormal(key("h"))
	if rowIndex(m, rowAgent) != -1 {
		t.Fatal("h on a folded repositories header did not fold the supatree")
	}
}

// The repositories section is folded until asked otherwise, and its header
// carries the members' PR statuses as coloured counts while it is.
func TestReposFoldedByDefaultWithCountBadge(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	m := New(supatree.DefaultConfig(), config.DefaultConfig(), zellij.Workspace{})
	m.insts = []*supatree.Instance{{
		Name: "oslo",
		Members: []supatree.Member{
			{Alias: "web", Branch: "st/oslo/web"},
			{Alias: "api", Branch: "st/oslo/api"},
			{Alias: "db", Branch: "st/oslo/db"},
		},
	}}
	m.prCache.Set("st/oslo/web", &github.PRInfo{Status: github.PROpen, Number: 1})
	m.prCache.Set("st/oslo/api", &github.PRInfo{Status: github.PRMerged, Number: 2})
	m.rebuildRows()

	if rowIndex(m, rowMember) != -1 {
		t.Fatal("repositories section was not folded on first render")
	}
	got := m.prCounts("oslo")
	// One open, one merged, one member with no PR at all.
	for _, want := range []string{"◉1", "✓1", "·1"} {
		if !strings.Contains(got, want) {
			t.Errorf("prCounts = %q, want it to contain %q", got, want)
		}
	}
	if strings.Contains(got, "open") || strings.Contains(got, "merged") {
		t.Errorf("prCounts = %q, want colours and glyphs rather than status words", got)
	}
	if !strings.Contains(m.View(), got) {
		t.Error("the count badge is missing from the rendered sidebar")
	}
}

// Folds are shared state: one sidebar per Zellij tab means a fold made in one
// tab has to show up in the next tab's reload, not just in the tab that made it.
func TestFoldStateIsSharedAcrossSidebars(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	one := New(supatree.DefaultConfig(), config.DefaultConfig(), zellij.Workspace{})
	two := New(supatree.DefaultConfig(), config.DefaultConfig(), zellij.Workspace{})

	one.setCollapse("oslo", true)
	one.setReposCollapse("bergen", false)

	two.reload()
	if !two.ui.TreeCollapsed("oslo") {
		t.Error("a fold made in one sidebar did not reach the other")
	}
	if two.ui.ReposCollapsed("bergen") {
		t.Error("an unfolded repositories section did not reach the other sidebar")
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
	// Asserted as a delta rather than an absolute count, so adding an unrelated
	// background command does not fail a test about the PR fetch.
	down := len(m.backgroundCmds())
	m.ghAvailable = true
	up := len(m.backgroundCmds())
	m.ghAvailable = false
	if up != down+1 {
		t.Fatalf("backgroundCmds = %d with gh down, %d with gh up; want exactly one more (the fetch)", down, up)
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
	if got := len(m.backgroundCmds()); got != up {
		t.Fatalf("backgroundCmds after recovery = %d, want %d (the fetch is back)", got, up)
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
	// The navigation tests are about moving over member rows, so they start from
	// a fully unfolded list rather than the folded-repos default.
	expandRepos(m)
	return m
}

// expandRepos unfolds every supatree's repositories section — the sidebar keeps
// them folded by default, and most tests want the member rows on screen.
func expandRepos(m *Model) {
	names := make([]string, 0, len(m.insts))
	for _, inst := range m.insts {
		names = append(names, inst.Name)
	}
	// Through the persisting path, not straight into m.ui: every other fold goes
	// to disk and is read back, so a test fold that only lived in memory would be
	// dropped by the next one.
	m.persistUI(func(u *supatree.UIState) {
		for _, name := range names {
			u.SetReposCollapsed(name, false)
		}
	})
	m.rebuildRows()
}

// rowKinds is the shape of the rendered list, for structural assertions.
func rowKinds(m *Model) []rowKind {
	kinds := make([]rowKind, 0, len(m.rows))
	for _, r := range m.rows {
		kinds = append(kinds, r.kind)
	}
	return kinds
}

// osloModel is a one-supatree, one-member model with its repositories unfolded.
func osloModel(t *testing.T) *Model {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	m := New(supatree.DefaultConfig(), config.DefaultConfig(), zellij.Workspace{})
	m.insts = []*supatree.Instance{{
		Name:    "oslo",
		Members: []supatree.Member{{Alias: "web", Branch: "st/oslo/web"}},
	}}
	expandRepos(m)
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
		if !m.ui.TreeCollapsed(name) {
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

// rowIndex returns the index of the first row of the given kind.
func rowIndex(m *Model, k rowKind) int {
	for i, r := range m.rows {
		if r.kind == k {
			return i
		}
	}
	return -1
}

// memberModel builds a one-supatree model whose single member is checked out on
// disk, so the open paths get past their existence check.
func memberModel(t *testing.T) (*Model, string) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	path := t.TempDir()
	m := New(supatree.DefaultConfig(), config.DefaultConfig(), zellij.Workspace{})
	m.insts = []*supatree.Instance{{
		Name: berlin, Slug: berlin,
		Members: []supatree.Member{
			{Alias: "terraform", Path: path, Branch: "st/berlin/terraform", Exists: true},
		},
	}}
	expandRepos(m)
	return m, path
}

// Enter on a member row stands in the repo (a shell pane); it must not launch
// the repo-scoped agent, which is what `a` is for.
func TestEnterOnMemberRowOpensShell(t *testing.T) {
	m, path := memberModel(t)
	t.Setenv("ZELLIJ", "")

	m.cursor = rowIndex(m, rowMember)
	_, cmd := m.updateNormal(key("enter"))
	if cmd == nil {
		t.Fatal("enter on a member row returned no command")
	}
	done, ok := cmd().(actionDoneMsg)
	if !ok {
		t.Fatalf("enter produced %T, want actionDoneMsg", cmd())
	}
	// Outside zellij there is no pane to open, and the shell path says so while
	// naming the directory. The agent path would have failed on the sandbox
	// instead, so this is what distinguishes the two.
	if done.err == nil || !strings.Contains(done.err.Error(), "not inside zellij") {
		t.Fatalf("err = %v, want the shell path's not-inside-zellij error", done.err)
	}
	if !strings.Contains(done.err.Error(), path) {
		t.Errorf("err = %v, want it to name the member path %q", done.err, path)
	}
}

// `a` means "give me an agent here" on every row: a name prompt at the tree
// level, the repo-scoped agent on a member row.
func TestAgentKeyIsContextual(t *testing.T) {
	m, _ := memberModel(t)

	m.cursor = rowIndex(m, rowMember)
	if _, cmd := m.updateNormal(key("a")); cmd == nil {
		t.Error("a on a member row returned no command")
	}
	if m.mode != modeNormal {
		t.Errorf("a on a member row: mode = %v, want modeNormal (no name prompt)", m.mode)
	}

	m.cursor = rowIndex(m, rowTree)
	m.updateNormal(key("a"))
	if m.mode != modeNewAgent {
		t.Fatalf("a on a tree row: mode = %v, want modeNewAgent", m.mode)
	}
	if m.actionTree != berlin {
		t.Errorf("actionTree = %q, want %q", m.actionTree, berlin)
	}
}

// The footer names whatever enter does on the row under the cursor, so the
// split between "stand in it" and "open it" is discoverable without the docs.
func TestFooterHintFollowsRowKind(t *testing.T) {
	m, _ := memberModel(t)

	m.cursor = rowIndex(m, rowMember)
	if got := m.footer(); !strings.Contains(got, "enter shell") {
		t.Errorf("member row footer = %q, want it to hint a shell", got)
	}
	m.cursor = rowIndex(m, rowAgent)
	if got := m.footer(); !strings.Contains(got, "enter open") {
		t.Errorf("agent row footer = %q, want it to hint open", got)
	}
}

// An invalid supatree name is reported while it is still being typed, the
// prompt stays open so it can be fixed in place, and the complaint disappears
// with the character that caused it — rather than being left in the footer to
// sit under the next attempt.
func TestInvalidTreeNameWarnsInlineAndClears(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	m := New(supatree.DefaultConfig(), config.DefaultConfig(), zellij.Workspace{})

	m.updateNormal(key("n"))
	if m.mode != modeNewTreeName {
		t.Fatalf("n: mode = %v, want modeNewTreeName", m.mode)
	}
	for _, r := range "feature-v1.1" {
		m.Update(key(string(r)))
	}
	if m.inputErr == nil {
		t.Fatal("a dotted name typed into the prompt raised no warning")
	}
	if !strings.Contains(m.footer(), m.inputErr.Error()) {
		t.Errorf("footer %q does not show the warning", m.footer())
	}

	// Enter keeps the prompt open rather than firing a create that would fail.
	if _, cmd := m.Update(key("enter")); cmd != nil {
		t.Error("enter on an invalid name started a create")
	}
	if m.mode != modeNewTreeName {
		t.Fatalf("enter on an invalid name: mode = %v, want the prompt still open", m.mode)
	}

	// Deleting the offending characters clears the warning immediately.
	for range 2 {
		m.Update(tea.KeyMsg{Type: tea.KeyBackspace})
	}
	if m.inputErr != nil {
		t.Errorf("warning survived the fix: %v", m.inputErr)
	}
}

// A name already taken by a supatree or a workbench worktree is refused too —
// both would collide on the generated branch and tab names.
func TestTreeNameValidationRejectsDuplicates(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	wb := config.DefaultConfig()
	wb.Repos = []config.Repo{{Alias: "web", Worktrees: []config.Worktree{{Name: "lisbon"}}}}
	m := New(supatree.DefaultConfig(), wb, zellij.Workspace{})
	m.insts = []*supatree.Instance{{Name: berlin}}

	if err := m.validateTreeName(""); err != nil {
		t.Errorf("a blank name is auto-generated, not an error: %v", err)
	}
	if err := m.validateTreeName("madrid"); err != nil {
		t.Errorf("valid name rejected: %v", err)
	}
	if err := m.validateTreeName(berlin); err == nil {
		t.Error("an existing supatree name was accepted")
	}
	if err := m.validateTreeName("lisbon"); err == nil {
		t.Error("an existing workbench worktree name was accepted")
	}
}

// A message or error from a finished action is cleared by the next keystroke,
// so it never outlives the moment it described.
func TestKeypressClearsLastActionResult(t *testing.T) {
	m := osloModel(t)
	m.err = errors.New("name must be lowercase alphanumeric and hyphens")
	m.msg = "created oslo"

	m.updateNormal(key("j"))
	if m.err != nil || m.msg != "" {
		t.Errorf("stale result survived a keystroke: err = %v, msg = %q", m.err, m.msg)
	}
}

// ? opens the keybinding reference and any key closes it again.
func TestHelpOpensAndCloses(t *testing.T) {
	m := osloModel(t)

	m.Update(key("?"))
	if m.mode != modeHelp {
		t.Fatalf("?: mode = %v, want modeHelp", m.mode)
	}
	out := m.View()
	for _, want := range []string{"Navigation", "Folding", "zM / zR", "dashboard"} {
		if !strings.Contains(out, want) {
			t.Errorf("help view missing %q:\n%s", want, out)
		}
	}

	m.Update(key("j"))
	if m.mode != modeNormal {
		t.Fatalf("a key did not dismiss the help: mode = %v", m.mode)
	}
	if strings.Contains(m.View(), "press any key to close") {
		t.Error("help still rendered after being dismissed")
	}
}
