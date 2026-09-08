package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/panamafrancis/workbench/pkg/config"
	"github.com/panamafrancis/workbench/pkg/git"
	"github.com/panamafrancis/workbench/pkg/github"
	"github.com/panamafrancis/workbench/pkg/zellij"
)

type item struct {
	isRepo        bool
	isPlaceholder bool
	repoIdx       int
	worktreeIdx   int
	alias         string
	worktreeName  string
}

type TreeModel struct {
	cfg       *config.Config
	prCache   *github.Cache
	collapsed map[string]bool
	cursor    int
	dirty     map[string]bool
	openTabs  map[string]bool
	// activeWorktree is the worktree whose Zellij tab this sidebar belongs to,
	// injected via WORKBENCH_WORKTREE_NAME. Empty for the root session sidebar.
	// It drives a passive "you are here" marker, distinct from the cursor.
	activeWorktree string
	scroll         int  // index of the first rendered row (viewport top)
	viewHeight     int  // rows the viewport last rendered; sizes ctrl+d/ctrl+u and clamps the wheel
	follow         bool // keep the cursor in view on the next render (cleared while wheel-scrolled away)
}

func newTree(cfg *config.Config, prCache *github.Cache) TreeModel {
	return TreeModel{
		cfg:       cfg,
		prCache:   prCache,
		collapsed: map[string]bool{},
		dirty:     map[string]bool{},
		follow:    true,
	}
}

func (t *TreeModel) items() []item {
	var out []item
	for ri, r := range t.cfg.Repos {
		out = append(out, item{isRepo: true, repoIdx: ri, alias: r.Alias})
		if t.collapsed[r.Alias] {
			continue
		}
		if len(r.Worktrees) == 0 {
			out = append(out, item{isPlaceholder: true, repoIdx: ri, alias: r.Alias})
		}
		for wi, w := range r.Worktrees {
			out = append(out, item{repoIdx: ri, worktreeIdx: wi, alias: r.Alias, worktreeName: w.Name})
		}
	}
	return out
}

// selectable reports whether the cursor may rest on a row. The cursor normally
// lives on worktrees and skips repo headers — except for a *collapsed* repo,
// which has no rows of its own. Without that exception, folding a repo would
// strand the cursor in a different repo, and folding every repo would leave
// nothing selectable at all.
func (t *TreeModel) selectable(it item) bool {
	return !it.isRepo || t.collapsed[it.alias]
}

func (t *TreeModel) clamp() {
	items := t.items()
	if len(items) == 0 {
		t.cursor = 0
		return
	}
	if t.cursor >= len(items) {
		t.cursor = len(items) - 1
	}
	if t.cursor < 0 {
		t.cursor = 0
	}
	if !t.selectable(items[t.cursor]) {
		t.skipToNextWorktree(1)
	}
}

func (t *TreeModel) moveUp() {
	items := t.items()
	for i := t.cursor - 1; i >= 0; i-- {
		if t.selectable(items[i]) {
			t.cursor = i
			t.follow = true
			return
		}
	}
}

func (t *TreeModel) moveDown() {
	items := t.items()
	for i := t.cursor + 1; i < len(items); i++ {
		if t.selectable(items[i]) {
			t.cursor = i
			t.follow = true
			return
		}
	}
}

// moveBy walks delta selectable rows, reusing moveUp/moveDown so the repo-header
// skipping stays in one place. Used by the half-page keys.
func (t *TreeModel) moveBy(delta int) {
	for range max(delta, -delta) {
		if delta > 0 {
			t.moveDown()
		} else {
			t.moveUp()
		}
	}
}

func (t *TreeModel) skipToNextWorktree(dir int) {
	items := t.items()
	if dir > 0 {
		for i := t.cursor; i < len(items); i++ {
			if t.selectable(items[i]) {
				t.cursor = i
				return
			}
		}
	}
	for i := t.cursor; i >= 0; i-- {
		if t.selectable(items[i]) {
			t.cursor = i
			return
		}
	}
}

// gotoTop / gotoBottom are vim's gg and G. gotoTop pins the scroll to the very
// top as well: clamp pushes the cursor past the first repo header, and without
// this the window would open at that worktree and hide the header above it.
func (t *TreeModel) gotoTop() {
	t.cursor = 0
	t.clamp()
	t.scroll = 0
	t.follow = true
}

func (t *TreeModel) gotoBottom() {
	t.cursor = len(t.items()) - 1
	t.clamp()
	t.follow = true
}

// firstRowOf returns the index of the first selectable row belonging to repoIdx:
// the repo header when the repo is collapsed, otherwise its first worktree (or
// its "no worktrees" placeholder).
func (t *TreeModel) firstRowOf(items []item, repoIdx int) (int, bool) {
	for i, it := range items {
		if it.repoIdx == repoIdx && t.selectable(it) {
			return i, true
		}
	}
	return 0, false
}

// jumpRepo moves to the next (delta > 0) or previous (delta < 0) repo's first
// selectable row. Moving backwards from inside a repo lands on that repo's own
// first row first — "up one level" before "up one repo".
func (t *TreeModel) jumpRepo(delta int) {
	items := t.items()
	if len(items) == 0 || t.cursor >= len(items) {
		return
	}
	cur := items[t.cursor].repoIdx
	if delta < 0 {
		if first, ok := t.firstRowOf(items, cur); ok && first < t.cursor {
			t.cursor = first
			t.follow = true
			return
		}
	}
	for ri := cur + delta; ri >= 0 && ri < len(t.cfg.Repos); ri += delta {
		if first, ok := t.firstRowOf(items, ri); ok {
			t.cursor = first
			t.follow = true
			return
		}
	}
}

// focusRepo parks the cursor on a repo's first selectable row — its header when
// collapsed, its first worktree (or placeholder) when expanded. Folding and
// unfolding go through it so the cursor stays in the repo the user acted on
// instead of being pushed into a neighbouring one when the rows shift.
func (t *TreeModel) focusRepo(alias string) {
	for i, it := range t.items() {
		if it.alias == alias && t.selectable(it) {
			t.cursor = i
			t.follow = true
			return
		}
	}
	t.clamp()
}

// setAllCollapsed folds or unfolds every repo at once (vim's zM / zR), keeping
// the cursor in the repo it was already in.
func (t *TreeModel) setAllCollapsed(collapsed bool) {
	alias := ""
	if it := t.selected(); it != nil {
		alias = it.alias
	}
	for _, r := range t.cfg.Repos {
		t.collapsed[r.Alias] = collapsed
	}
	if alias == "" {
		t.clamp()
		return
	}
	t.focusRepo(alias)
}

// halfPage is the ctrl+d / ctrl+u distance: half the visible rows, or a fixed
// fallback before the pane has reported its size.
func (t *TreeModel) halfPage() int {
	if t.viewHeight <= 0 {
		return fallbackPage
	}
	return max(t.viewHeight/2, 1)
}

// scrollBy pans the viewport without moving the cursor (mouse wheel). Clearing
// follow keeps the view where the user left it instead of snapping back to the
// cursor on the next render or background tick.
func (t *TreeModel) scrollBy(delta int) {
	total := len(t.items())
	if t.viewHeight <= 0 || total <= t.viewHeight {
		return
	}
	t.follow = false
	t.scroll = min(max(t.scroll+delta, 0), total-t.viewHeight)
}

// viewport returns the [start, end) row range to render, recording the visible
// height for the paging and wheel handlers. While follow is set (any cursor
// move) it drags the scroll offset along to keep the cursor visible.
func (t *TreeModel) viewport(total, avail int) (int, int) {
	t.viewHeight = avail
	if avail <= 0 || total <= avail {
		t.scroll = 0
		return 0, total
	}
	if t.follow {
		if t.cursor < t.scroll {
			t.scroll = t.cursor
		}
		if t.cursor >= t.scroll+avail {
			t.scroll = t.cursor - avail + 1
		}
	}
	t.scroll = min(max(t.scroll, 0), total-avail)
	return t.scroll, t.scroll + avail
}

func (t *TreeModel) toggleCollapse() {
	items := t.items()
	if t.cursor >= len(items) {
		return
	}
	alias := items[t.cursor].alias
	t.collapsed[alias] = !t.collapsed[alias]
	t.focusRepo(alias)
}

func (t *TreeModel) collapseContaining() {
	items := t.items()
	if t.cursor >= len(items) {
		return
	}
	alias := items[t.cursor].alias
	if !t.collapsed[alias] {
		t.collapsed[alias] = true
	}
	t.focusRepo(alias)
}

func (t *TreeModel) expandContaining() {
	items := t.items()
	if t.cursor >= len(items) {
		return
	}
	alias := items[t.cursor].alias
	if t.collapsed[alias] {
		t.collapsed[alias] = false
	}
	t.focusRepo(alias)
}

// selectWorktree moves the cursor onto the row for the named worktree, if it
// still exists. Used to preserve the selection across a config reload so a
// concurrent change from another tab can't leave the cursor pointing at a
// different worktree than the user had highlighted.
func (t *TreeModel) selectWorktree(name string) {
	for i, it := range t.items() {
		if !it.isRepo && !it.isPlaceholder && it.worktreeName == name {
			t.cursor = i
			return
		}
	}
}

func (t *TreeModel) selected() *item {
	items := t.items()
	if len(items) == 0 || t.cursor >= len(items) {
		return nil
	}
	it := items[t.cursor]
	return &it
}

// selectByRow acts on the row at a viewport-relative offset (a mouse click).
// Clicking an expanded repo header folds it, mirroring the space key; any other
// selectable row just takes the cursor.
func (t *TreeModel) selectByRow(visible int) {
	items := t.items()
	row := t.scroll + visible
	if row < 0 || row >= len(items) {
		return
	}
	if items[row].isRepo && !t.collapsed[items[row].alias] {
		t.collapsed[items[row].alias] = true
		t.clamp()
	} else {
		t.cursor = row
	}
	t.follow = true
}

func (t *TreeModel) stats() string {
	repos := len(t.cfg.Repos)
	worktrees := 0
	for _, r := range t.cfg.Repos {
		worktrees += len(r.Worktrees)
	}
	running := 0
	for _, r := range t.cfg.Repos {
		for _, w := range r.Worktrees {
			if t.openTabs[w.Name] {
				running++
			}
		}
	}
	dirtyCount := 0
	for _, d := range t.dirty {
		if d {
			dirtyCount++
		}
	}
	prOpen := 0
	if t.prCache != nil {
		for _, r := range t.cfg.Repos {
			for _, w := range r.Worktrees {
				if info := t.prCache.Get(w.Branch); info != nil && (info.Status == github.PROpen || info.Status == github.PRDraft) {
					prOpen++
				}
			}
		}
	}
	parts := []string{
		fmt.Sprintf("%d repo", repos),
		fmt.Sprintf("%d wt", worktrees),
	}
	if running > 0 {
		parts = append(parts, fmt.Sprintf("%d▶", running))
	}
	if dirtyCount > 0 {
		parts = append(parts, fmt.Sprintf("%d*", dirtyCount))
	}
	if prOpen > 0 {
		parts = append(parts, fmt.Sprintf("%d⬆", prOpen))
	}
	return strings.Join(parts, " · ")
}

func refreshRunningCmd() tea.Cmd {
	return func() tea.Msg {
		if !zellij.IsInZellij() {
			return runningMsg{}
		}
		tabs, err := zellij.TabNames()
		if err != nil {
			return runningMsg{err: err}
		}
		return runningMsg{tabs: tabs}
	}
}

func refreshDirtyCmd(cfg *config.Config) tea.Cmd {
	return func() tea.Msg {
		dirty := make(map[string]bool)
		for _, r := range cfg.Repos {
			for _, w := range r.Worktrees {
				if _, err := os.Stat(w.Path); err == nil {
					dirty[w.Name] = git.IsDirty(w.Path)
				}
			}
		}
		return dirtyMsg{dirty: dirty}
	}
}

func (t *TreeModel) view(width, avail int) string {
	items := t.items()
	var sb strings.Builder
	start, end := t.viewport(len(items), avail)
	for i := start; i < end; i++ {
		it := items[i]
		selected := i == t.cursor
		switch {
		case it.isRepo:
			r := t.cfg.Repos[it.repoIdx]
			icon := "▼"
			if t.collapsed[r.Alias] {
				icon = "▶"
			}
			count := fmt.Sprintf(" [%d]", len(r.Worktrees))
			pathLabel := filepath.Base(r.LocalPath)
			line := fmt.Sprintf("%s %s (%s)%s", icon, r.Alias, pathLabel, count)
			sb.WriteString(styleRepo.Render(line))
		case it.isPlaceholder:
			line := "  (no worktrees — press n)"
			if selected {
				padRow(&sb, styleSelected.Render(line), width, styleSelected)
			} else {
				sb.WriteString(styleMuted.Render(line))
			}
		default:
			w := t.cfg.Repos[it.repoIdx].Worktrees[it.worktreeIdx]

			var prSuffix string
			var prStatus github.PRStatus
			if t.prCache != nil {
				if info := t.prCache.Get(w.Branch); info != nil {
					prStatus = info.Status
					if icon, ok := prIcon(info.Status); ok {
						prSuffix = fmt.Sprintf("  %s #%d", icon, info.Number)
					}
				}
			}

			lineStyle, selStyle := prLineStyles(prStatus)

			isDirty := t.dirty[w.Name]
			isRunning := t.openTabs[w.Name]
			isActive := t.activeWorktree != "" && w.Name == t.activeWorktree
			modelLabel := "[" + w.Model + "]"

			// A one-cell gutter marker points at the worktree this sidebar's tab
			// belongs to, regardless of where the cursor is. Inactive rows render
			// a blank cell so the name column stays aligned.
			marker := " "
			if isActive {
				marker = "▸"
			}

			dirty := ""
			if isDirty {
				dirty = styleDirty.Render("*")
			}
			running := ""
			if isRunning {
				running = " ▶"
			}
			model := styleMuted.Render(modelLabel)
			line := fmt.Sprintf(" ● %-18s %s", w.Name, w.Branch)
			suffix := dirty + running + " " + model + prSuffix
			// The marker occupies one leading cell in addition to line.
			if width > 0 && 1+lipgloss.Width(line)+lipgloss.Width(suffix) > width {
				avail := width - 1 - lipgloss.Width(suffix)
				if avail > 0 {
					runes := []rune(line)
					if len(runes) > avail {
						line = string(runes[:avail])
					}
				}
			}

			if selected {
				var buf strings.Builder
				buf.WriteString(styleActiveMarkerSelected.Render(marker))
				buf.WriteString(selStyle.Render(line))
				if isDirty {
					buf.WriteString(styleDirtySelected.Render("*"))
				}
				buf.WriteString(selStyle.Render(running))
				buf.WriteString(styleMutedSelected.Render(" " + modelLabel))
				buf.WriteString(selStyle.Render(prSuffix))
				padRow(&sb, buf.String(), width, selStyle)
			} else {
				sb.WriteString(styleActiveMarker.Render(marker))
				sb.WriteString(lineStyle.Render(line))
				sb.WriteString(dirty)
				sb.WriteString(lineStyle.Render(running))
				sb.WriteString(" ")
				sb.WriteString(model)
				sb.WriteString(lineStyle.Render(prSuffix))
			}
		}
		sb.WriteString("\n")
	}
	if len(items) == 0 {
		sb.WriteString(styleMuted.Render("  no repos — run: workbench add repo <path> --alias=<alias>"))
		sb.WriteString("\n")
	}
	return sb.String()
}

func padRow(sb *strings.Builder, content string, width int, bgStyle lipgloss.Style) {
	sb.WriteString(content)
	if pad := width - lipgloss.Width(content); pad > 0 {
		sb.WriteString(bgStyle.Render(strings.Repeat(" ", pad)))
	}
}

func prIcon(status github.PRStatus) (string, bool) {
	switch status {
	case github.PRDraft:
		return "✎", true
	case github.PROpen:
		return "⬆", true
	case github.PRMerged:
		return "✓", true
	case github.PRClosed:
		return "✗", true
	case github.PRNone:
		return "", false
	}
	return "", false
}

func prLineStyles(status github.PRStatus) (normal lipgloss.Style, sel lipgloss.Style) {
	switch status {
	case github.PRDraft:
		return stylePRDraft, stylePRDraftSelected
	case github.PROpen:
		return stylePROpen, stylePROpenSelected
	case github.PRMerged:
		return stylePRMerged, stylePRMergedSelected
	case github.PRClosed:
		return stylePRClosed, stylePRClosedSelected
	case github.PRNone:
		return styleWorktree, styleSelected
	}
	return styleWorktree, styleSelected
}
