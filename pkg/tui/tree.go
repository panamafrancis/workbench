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
}

func newTree(cfg *config.Config, prCache *github.Cache) TreeModel {
	return TreeModel{
		cfg:       cfg,
		prCache:   prCache,
		collapsed: map[string]bool{},
		dirty:     map[string]bool{},
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
	if items[t.cursor].isRepo {
		t.skipToNextWorktree(1)
	}
}

func (t *TreeModel) moveUp() {
	items := t.items()
	for i := t.cursor - 1; i >= 0; i-- {
		if !items[i].isRepo {
			t.cursor = i
			return
		}
	}
}

func (t *TreeModel) moveDown() {
	items := t.items()
	for i := t.cursor + 1; i < len(items); i++ {
		if !items[i].isRepo {
			t.cursor = i
			return
		}
	}
}

func (t *TreeModel) skipToNextWorktree(dir int) {
	items := t.items()
	if dir > 0 {
		for i := t.cursor; i < len(items); i++ {
			if !items[i].isRepo {
				t.cursor = i
				return
			}
		}
	}
	for i := t.cursor; i >= 0; i-- {
		if !items[i].isRepo {
			t.cursor = i
			return
		}
	}
}

func (t *TreeModel) toggleCollapse() {
	items := t.items()
	if t.cursor >= len(items) {
		return
	}
	cur := items[t.cursor]
	alias := cur.alias
	t.collapsed[alias] = !t.collapsed[alias]
	t.clamp()
}

func (t *TreeModel) collapseContaining() {
	items := t.items()
	if t.cursor >= len(items) {
		return
	}
	alias := items[t.cursor].alias
	if !t.collapsed[alias] {
		t.collapsed[alias] = true
		t.clamp()
	}
}

func (t *TreeModel) expandContaining() {
	items := t.items()
	if t.cursor >= len(items) {
		return
	}
	alias := items[t.cursor].alias
	if t.collapsed[alias] {
		t.collapsed[alias] = false
		t.clamp()
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

func (t *TreeModel) selectByRow(row int) {
	items := t.items()
	if row >= 0 && row < len(items) {
		if items[row].isRepo {
			t.collapsed[items[row].alias] = !t.collapsed[items[row].alias]
			t.clamp()
		} else {
			t.cursor = row
		}
	}
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
			return runningMsg{}
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

func (t *TreeModel) view(width int) string {
	items := t.items()
	var sb strings.Builder
	for i, it := range items {
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
				sb.WriteString(styleSelected.Render(line))
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

			dirty := ""
			if t.dirty[w.Name] {
				dirty = styleDirty.Render("*")
			}

			running := ""
			if t.openTabs[w.Name] {
				running = " ▶"
			}
			model := styleMuted.Render("[" + w.Model + "]")
			line := fmt.Sprintf("  ● %-18s %s", w.Name, w.Branch)
			suffix := dirty + running + " " + model + prSuffix
			if width > 0 && lipgloss.Width(line)+lipgloss.Width(suffix) > width {
				avail := width - lipgloss.Width(suffix)
				if avail > 0 {
					runes := []rune(line)
					if len(runes) > avail {
						line = string(runes[:avail])
					}
				}
			}

			if selected {
				sb.WriteString(selStyle.Render(line))
				sb.WriteString(dirty)
				sb.WriteString(selStyle.Render(running))
				sb.WriteString(" ")
				sb.WriteString(model)
				sb.WriteString(selStyle.Render(prSuffix))
			} else {
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
