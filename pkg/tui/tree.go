package tui

import (
	"fmt"
	"os"
	"strings"

	"github.com/panamafrancis/workbench/pkg/config"
	"github.com/panamafrancis/workbench/pkg/git"
)

type item struct {
	isRepo       bool
	repoIdx      int
	worktreeIdx  int
	alias        string
	worktreeName string
}

type TreeModel struct {
	cfg       *config.Config
	collapsed map[string]bool // keyed by repo alias
	cursor    int
	dirty     map[string]bool // keyed by worktree name
}

func newTree(cfg *config.Config) TreeModel {
	return TreeModel{
		cfg:       cfg,
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
		for wi, w := range r.Worktrees {
			out = append(out, item{repoIdx: ri, worktreeIdx: wi, alias: r.Alias, worktreeName: w.Name})
		}
	}
	return out
}

func (t *TreeModel) clamp() {
	items := t.items()
	if t.cursor >= len(items) {
		t.cursor = len(items) - 1
	}
	if t.cursor < 0 {
		t.cursor = 0
	}
}

func (t *TreeModel) moveUp() {
	if t.cursor > 0 {
		t.cursor--
	}
}

func (t *TreeModel) moveDown() {
	if t.cursor < len(t.items())-1 {
		t.cursor++
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
}

func (t *TreeModel) selected() *item {
	items := t.items()
	if len(items) == 0 || t.cursor >= len(items) {
		return nil
	}
	it := items[t.cursor]
	return &it
}

func (t *TreeModel) breadcrumb() string {
	sel := t.selected()
	if sel == nil {
		return "workbench"
	}
	if sel.isRepo {
		return "workbench  ›  " + sel.alias
	}
	return "workbench  ›  " + sel.alias + "  ›  " + sel.worktreeName
}

func (t *TreeModel) refreshDirty() {
	for _, r := range t.cfg.Repos {
		for _, w := range r.Worktrees {
			if _, err := os.Stat(w.Path); err == nil {
				t.dirty[w.Name] = git.IsDirty(w.Path)
			}
		}
	}
}

func (t *TreeModel) view(width int) string {
	items := t.items()
	var sb strings.Builder
	for i, it := range items {
		selected := i == t.cursor
		if it.isRepo {
			r := t.cfg.Repos[it.repoIdx]
			icon := "▼"
			if t.collapsed[r.Alias] {
				icon = "▶"
			}
			count := fmt.Sprintf(" [%d]", len(r.Worktrees))
			line := fmt.Sprintf("%s %s (%s)%s", icon, r.Alias, r.Alias, count)
			if selected {
				sb.WriteString(styleSelected.Render(line))
			} else {
				sb.WriteString(styleRepo.Render(line))
			}
		} else {
			w := t.cfg.Repos[it.repoIdx].Worktrees[it.worktreeIdx]
			dirty := ""
			if t.dirty[w.Name] {
				dirty = styleDirty.Render("*")
			}
			model := styleMuted.Render("[" + w.Model + "]")
			line := fmt.Sprintf("  ● %-18s %s%s %s", w.Name, w.Branch, dirty, model)
			// truncate to width if needed
			if width > 0 && len(line) > width {
				line = line[:width]
			}
			if selected {
				sb.WriteString(styleSelected.Render(line))
			} else {
				sb.WriteString(styleWorktree.Render(line))
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
