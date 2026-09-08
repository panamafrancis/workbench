package tui

import (
	"os"
	"strings"
	"testing"

	"github.com/panamafrancis/workbench/pkg/config"
)

// writeConfig persists a config with the given repo aliases to the test HOME.
func writeConfig(t *testing.T, aliases ...string) {
	t.Helper()
	cfg := config.DefaultConfig()
	for _, a := range aliases {
		cfg.Repos = append(cfg.Repos, config.Repo{Alias: a, LocalPath: "/tmp/" + a})
	}
	if err := cfg.Save(); err != nil {
		t.Fatalf("save config: %v", err)
	}
}

func TestReloadLocalStatePicksUpDiskChanges(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if err := os.MkdirAll(config.ConfigDir(), 0755); err != nil {
		t.Fatal(err)
	}

	writeConfig(t, "one")
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	m := New(cfg)
	if got := len(m.cfg.Repos); got != 1 {
		t.Fatalf("initial repos = %d, want 1", got)
	}

	// Another instance adds a repo on disk; a focus/tick reload should see it.
	writeConfig(t, "one", "two")
	m.reloadLocalState()
	if got := len(m.cfg.Repos); got != 2 {
		t.Errorf("after reload repos = %d, want 2", got)
	}
	if m.tree.cfg != m.cfg {
		t.Error("tree.cfg not swapped to the reloaded config")
	}
}

func TestActiveWorktreeMarker(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Repos = []config.Repo{{
		Alias:     "r",
		LocalPath: "/tmp/r",
		Worktrees: []config.Worktree{
			{Name: "tokyo", Branch: "wt/r/tokyo", Model: "claude"},
			{Name: "osaka", Branch: "wt/r/osaka", Model: "claude"},
		},
	}}

	tr := newTree(cfg, nil)
	if strings.Contains(tr.view(80, 0), "▸") {
		t.Error("no active worktree set, but marker ▸ rendered")
	}

	tr.activeWorktree = "osaka"
	out := tr.view(80, 0)
	if !strings.Contains(out, "▸") {
		t.Errorf("active worktree set, but marker ▸ not rendered:\n%s", out)
	}
	// The marker sits on the active row's line, not the other worktree's.
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "▸") && !strings.Contains(line, "osaka") {
			t.Errorf("marker rendered on wrong row: %q", line)
		}
	}
}

func TestReloadLocalStateNoOpWhileBusy(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if err := os.MkdirAll(config.ConfigDir(), 0755); err != nil {
		t.Fatal(err)
	}
	writeConfig(t, "one")
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	m := New(cfg)

	// Disk gains a repo, but an open input mode must not let indices shift.
	writeConfig(t, "one", "two")
	m.mode = modeNewWorktree
	m.reloadLocalState()
	if got := len(m.cfg.Repos); got != 1 {
		t.Errorf("reload during input mode changed repos to %d, want 1 (no-op)", got)
	}

	// A create in flight must not drop the optimistic in-memory entry.
	m.mode = modeNormal
	m.creating = map[string]bool{"pending": true}
	m.reloadLocalState()
	if got := len(m.cfg.Repos); got != 1 {
		t.Errorf("reload during create changed repos to %d, want 1 (no-op)", got)
	}
}

// threeRepos builds a tree of three repos with two worktrees each, for the
// navigation tests. Rows: repo, wt, wt, repo, wt, wt, repo, wt, wt (9).
func threeRepos(t *testing.T) TreeModel {
	t.Helper()
	cfg := config.DefaultConfig()
	for _, alias := range []string{"alpha", "bravo", "charlie"} {
		cfg.Repos = append(cfg.Repos, config.Repo{
			Alias: alias, LocalPath: "/tmp/" + alias,
			Worktrees: []config.Worktree{
				{Name: alias + "-one", Branch: "wt/" + alias + "/one", Model: "claude"},
				{Name: alias + "-two", Branch: "wt/" + alias + "/two", Model: "claude"},
			},
		})
	}
	return newTree(cfg, nil)
}

func TestGotoTopAndBottom(t *testing.T) {
	tr := threeRepos(t)

	tr.gotoBottom()
	items := tr.items()
	if tr.cursor != len(items)-1 {
		t.Fatalf("G: cursor = %d, want %d", tr.cursor, len(items)-1)
	}

	tr.gotoTop()
	// Row 0 is a repo header, so the cursor lands on the first worktree below it.
	if tr.cursor != 1 {
		t.Fatalf("gg: cursor = %d, want 1 (first worktree)", tr.cursor)
	}
	if items[tr.cursor].isRepo {
		t.Error("gg landed on a repo header")
	}
}

func TestJumpRepoMovesBetweenRepos(t *testing.T) {
	tr := threeRepos(t)
	tr.gotoTop() // alpha's first worktree

	tr.jumpRepo(1)
	if got := tr.items()[tr.cursor].alias; got != "bravo" {
		t.Fatalf("} landed in %q, want bravo", got)
	}
	tr.jumpRepo(1)
	if got := tr.items()[tr.cursor].alias; got != "charlie" {
		t.Fatalf("} again landed in %q, want charlie", got)
	}
	// Past the last repo the cursor stays put rather than running off the end.
	before := tr.cursor
	tr.jumpRepo(1)
	if tr.cursor != before {
		t.Fatalf("} past the last repo moved the cursor to %d", tr.cursor)
	}
	tr.jumpRepo(-1)
	if got := tr.items()[tr.cursor].alias; got != "bravo" {
		t.Fatalf("{ landed in %q, want bravo", got)
	}
}

// From the second worktree of a repo, { goes to that repo's first row before
// leaving for the previous repo.
func TestJumpRepoBackwardGoesToRepoStartFirst(t *testing.T) {
	tr := threeRepos(t)
	tr.cursor = 5 // bravo's second worktree (rows: 0 repo,1,2, 3 repo,4,5)

	tr.jumpRepo(-1)
	if tr.cursor != 4 {
		t.Fatalf("{ from bravo's second worktree = %d, want 4 (bravo's first)", tr.cursor)
	}
	tr.jumpRepo(-1)
	if got := tr.items()[tr.cursor].alias; got != "alpha" {
		t.Fatalf("{ again landed in %q, want alpha", got)
	}
}

// Folding must leave the cursor on the repo the user folded, not push it into
// the next one — which means a collapsed repo header is selectable.
func TestCollapseKeepsCursorOnRepo(t *testing.T) {
	tr := threeRepos(t)
	tr.gotoTop() // alpha's first worktree

	tr.collapseContaining()
	sel := tr.selected()
	if sel == nil || sel.alias != "alpha" {
		t.Fatalf("after collapse cursor is on %+v, want the alpha row", sel)
	}
	if !sel.isRepo {
		t.Error("a collapsed repo's own header should hold the cursor")
	}

	tr.expandContaining()
	sel = tr.selected()
	if sel == nil || sel.alias != "alpha" || sel.isRepo {
		t.Fatalf("after expand cursor is on %+v, want alpha's first worktree", sel)
	}
}

func TestFoldAllAndUnfoldAllStayNavigable(t *testing.T) {
	tr := threeRepos(t)
	full := len(tr.items())
	tr.gotoTop()

	tr.setAllCollapsed(true)
	if got := len(tr.items()); got != 3 {
		t.Fatalf("zM: %d rows, want 3 (one header per repo)", got)
	}
	if sel := tr.selected(); sel == nil || sel.alias != "alpha" {
		t.Fatalf("zM moved the cursor out of alpha: %+v", sel)
	}
	// With every repo folded the headers must still be walkable, otherwise the
	// sidebar would be stuck with nothing selectable.
	tr.moveDown()
	if got := tr.items()[tr.cursor].alias; got != "bravo" {
		t.Fatalf("j with all repos folded landed in %q, want bravo", got)
	}

	tr.setAllCollapsed(false)
	if got := len(tr.items()); got != full {
		t.Fatalf("zR: %d rows, want %d", got, full)
	}
}

func TestHalfPageMovesByViewHeight(t *testing.T) {
	tr := threeRepos(t)
	tr.gotoTop()
	tr.viewport(len(tr.items()), 6) // halfPage = 3

	tr.moveBy(tr.halfPage())
	if tr.cursor != 5 {
		t.Fatalf("ctrl+d: cursor = %d, want 5 (3 selectable rows down from 1)", tr.cursor)
	}
	tr.moveBy(-tr.halfPage())
	if tr.cursor != 1 {
		t.Fatalf("ctrl+u: cursor = %d, want 1", tr.cursor)
	}
}

func TestViewportScrollsToCursor(t *testing.T) {
	tr := threeRepos(t)
	total := len(tr.items())

	tr.gotoBottom()
	start, end := tr.viewport(total, 4)
	if tr.cursor < start || tr.cursor >= end {
		t.Fatalf("cursor %d outside viewport [%d,%d)", tr.cursor, start, end)
	}
	if end-start != 4 {
		t.Fatalf("viewport height = %d, want 4", end-start)
	}

	tr.gotoTop()
	if start, _ = tr.viewport(total, 4); start != 0 {
		t.Fatalf("scroll did not return to the top: start = %d", start)
	}

	// A list that fits renders whole, with no offset.
	start, end = tr.viewport(total, 50)
	if start != 0 || end != total {
		t.Fatalf("short list windowed unexpectedly: [%d,%d)", start, end)
	}
}

// The wheel pans the view without moving the cursor, and the pan survives the
// next render instead of snapping back.
func TestWheelScrollsWithoutMovingCursor(t *testing.T) {
	tr := threeRepos(t)
	total := len(tr.items())
	tr.gotoTop()
	tr.viewport(total, 4)

	tr.scrollBy(wheelStep)
	if tr.cursor != 1 {
		t.Fatalf("wheel moved the cursor to %d", tr.cursor)
	}
	if tr.scroll != wheelStep {
		t.Fatalf("scroll = %d, want %d", tr.scroll, wheelStep)
	}
	if start, _ := tr.viewport(total, 4); start != wheelStep {
		t.Fatalf("render snapped back to the cursor: start = %d", start)
	}

	// Wheeling up past the top clamps instead of going negative.
	for range 5 {
		tr.scrollBy(-wheelStep)
	}
	if tr.scroll != 0 {
		t.Fatalf("scroll = %d, want 0", tr.scroll)
	}

	// A cursor move re-arms follow and pulls the window back.
	tr.scroll = total - 4
	tr.follow = false
	tr.moveDown()
	start, end := tr.viewport(total, 4)
	if tr.cursor < start || tr.cursor >= end {
		t.Fatalf("cursor %d outside viewport [%d,%d) after a key press", tr.cursor, start, end)
	}
}

func TestSelectByRowUsesScrollOffset(t *testing.T) {
	tr := threeRepos(t)
	total := len(tr.items())
	tr.viewport(total, 4)
	tr.scrollBy(wheelStep) // scroll = 3, so visible row 1 is item 4

	tr.selectByRow(1)
	if tr.cursor != 4 {
		t.Fatalf("click selected item %d, want 4", tr.cursor)
	}

	// Clicking an expanded repo header folds it rather than selecting it.
	tr.scroll = 0
	tr.selectByRow(0)
	if !tr.collapsed["alpha"] {
		t.Error("clicking an expanded repo header should fold it")
	}
}

// The tree renders only its window, so a long list cannot overflow the pane.
func TestViewRendersOnlyTheWindow(t *testing.T) {
	tr := threeRepos(t)
	out := tr.view(80, 4)
	if got := strings.Count(strings.TrimRight(out, "\n"), "\n") + 1; got != 4 {
		t.Fatalf("view rendered %d lines, want 4", got)
	}
	if full := tr.view(80, 0); strings.Count(full, "\n") != len(tr.items()) {
		t.Fatalf("unbounded view rendered %d lines, want %d", strings.Count(full, "\n"), len(tr.items()))
	}
}

// View must fit the reported pane height: the tree viewport is sized from
// whatever viewTail leaves over, so a long repo list scrolls instead of
// pushing the footer off the bottom.
func TestViewFitsPaneHeight(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if err := os.MkdirAll(config.ConfigDir(), 0755); err != nil {
		t.Fatal(err)
	}
	writeConfig(t, "one")
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	// Many more rows than any of the heights under test.
	for i := range 30 {
		name := "wt" + string(rune('a'+i%26)) + string(rune('0'+i/26))
		cfg.Repos[0].Worktrees = append(cfg.Repos[0].Worktrees, config.Worktree{
			Name: name, Branch: "wt/one/" + name, Model: "claude",
		})
	}

	m := New(cfg)
	m.width = 80
	for _, height := range []int{10, 20, 40} {
		m.height = height
		lines := strings.Count(strings.TrimRight(m.View(), "\n"), "\n") + 1
		if lines > height {
			t.Errorf("height %d: View rendered %d lines", height, lines)
		}
	}
}
