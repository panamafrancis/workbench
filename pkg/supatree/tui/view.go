package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/panamafrancis/workbench/pkg/github"
	"github.com/panamafrancis/workbench/pkg/supatree"
	"github.com/panamafrancis/workbench/pkg/zellij"
)

func (m *Model) View() string {
	footer := m.footer()

	var b strings.Builder
	b.WriteString(styleHeader.Render("supatree"))
	b.WriteString("\n")

	if len(m.rows) == 0 {
		b.WriteString(styleMuted.Render("no supatrees — press n to create one"))
		b.WriteString("\n\n")
		b.WriteString(footer)
		return b.String()
	}

	// Reserve the header line, the blank line, and the (possibly wrapped) footer;
	// the rest is the scrollable row viewport. A height of 0 (size not yet
	// reported) renders every row; a reported-but-tiny pane still windows down to
	// a single row so the cursor stays visible instead of dumping from the top.
	reserved := 1 + 1 + strings.Count(footer, "\n") + 1
	avail := m.height - reserved
	if m.height > 0 && avail < 1 {
		avail = 1
	}
	start, end := m.viewport(avail)
	for i := start; i < end; i++ {
		selected := i == m.cursor && m.mode == modeNormal
		b.WriteString(m.renderRow(m.rows[i], selected))
		b.WriteString("\n")
	}

	b.WriteString("\n")
	b.WriteString(footer)
	return b.String()
}

// viewport adjusts the scroll offset so the cursor stays visible and returns the
// [start, end) row range to render. avail <= 0 (no reported size) or a list that
// fits shows everything.
func (m *Model) viewport(avail int) (int, int) {
	if avail <= 0 || len(m.rows) <= avail {
		m.scroll = 0
		return 0, len(m.rows)
	}
	if m.cursor < m.scroll {
		m.scroll = m.cursor
	}
	if m.cursor >= m.scroll+avail {
		m.scroll = m.cursor - avail + 1
	}
	if maxScroll := len(m.rows) - avail; m.scroll > maxScroll {
		m.scroll = maxScroll
	}
	if m.scroll < 0 {
		m.scroll = 0
	}
	return m.scroll, m.scroll + avail
}

func (m *Model) renderRow(r row, selected bool) string {
	switch r.kind {
	case rowTree:
		badge := m.prBadge(r.tree)
		running := ""
		if m.openTabs[r.tree] {
			running = styleRunning.Render(" ●")
		}
		fold := "▼"
		if m.collapsed[r.tree] {
			fold = "▶"
		}
		line := fold + " " + r.label + running
		if badge != "" {
			line += "  " + badge
		}
		// A gutter marker points at the supatree this sidebar's tab belongs to
		// ("you are here"), regardless of the cursor. Other rows render a blank
		// gutter so the tree names stay aligned.
		marker := "  "
		if m.activeTree != "" && r.tree == m.activeTree {
			marker = styleRunning.Render("▸ ")
		}
		return marker + sel(selected, styleTree.Render(line))
	case rowSubheader:
		return "    " + styleSub.Render(r.label)
	case rowAgent:
		icon := "○"
		if m.openTabs[supatree.TabName(r.tree, r.label)] {
			icon = styleRunning.Render("●")
		}
		return sel(selected, fmt.Sprintf("      %s %s", icon, r.label))
	case rowMember:
		return sel(selected, m.renderMember(r))
	}
	return ""
}

func (m *Model) renderMember(r row) string {
	inst := m.instance(r.tree)
	dirtyMark := " "
	branch := ""
	if inst != nil {
		if mem := inst.FindMember(r.alias); mem != nil {
			branch = mem.Branch
			if m.dirty[mem.Path] {
				dirtyMark = styleDirty.Render("*")
			}
			if !mem.Exists {
				dirtyMark = styleMuted.Render("·")
			}
		}
	}
	pr := ""
	if info := m.prCache.Get(branch); info != nil {
		pr = "  " + prIcon(info.Status)
	}
	return fmt.Sprintf("      %s %-18s%s", dirtyMark, r.alias, pr)
}

func (m *Model) prBadge(tree string) string {
	inst := m.instance(tree)
	if inst == nil {
		return ""
	}
	open, total := 0, 0
	for _, mem := range inst.Members {
		if info := m.prCache.Get(mem.Branch); info != nil && info.Status != github.PRNone {
			total++
			if info.Status == github.PROpen || info.Status == github.PRDraft {
				open++
			}
		}
	}
	if total == 0 {
		return ""
	}
	return styleMuted.Render(fmt.Sprintf("%d/%d PRs", open, total))
}

func (m *Model) footer() string {
	switch m.mode {
	case modeNewAgent:
		return "new agent: " + m.input.View()
	case modeNewTree:
		return "new supatree — stack: " + m.input.View()
	case modeNewTreeName:
		return "new supatree — name: " + m.input.View()
	case modeConfirmDelete:
		return styleDirty.Render(fmt.Sprintf("delete %q? [y/N]", m.actionTree))
	case modeConfirmQuit:
		return styleDirty.Render("quit sidebar? [y/N]")
	case modeNormal:
	}
	if m.err != nil {
		return stylePRClosed.Render("error: " + m.err.Error())
	}
	parts := []string{"enter open", "space fold", "a agent", "n new", "s sync", "d del", "r refresh", "q quit"}
	if m.prHint != "" {
		parts = append(parts, "("+m.prHint+")")
	}
	if m.msg != "" {
		parts = append([]string{m.msg}, parts...)
	}
	return styleStatus.Render(wrapParts(parts, " · ", m.width))
}

// wrapParts joins parts with sep, folding onto multiple lines so nothing is
// clipped in the narrow sidebar. A width of 0 (size not yet reported) keeps it
// on one line.
func wrapParts(parts []string, sep string, width int) string {
	if width <= 0 {
		return strings.Join(parts, sep)
	}
	var lines []string
	cur := ""
	for _, p := range parts {
		cand := p
		if cur != "" {
			cand = cur + sep + p
		}
		if cur != "" && lipgloss.Width(cand) > width {
			lines = append(lines, cur)
			cur = p
		} else {
			cur = cand
		}
	}
	if cur != "" {
		lines = append(lines, cur)
	}
	return strings.Join(lines, "\n")
}

func prIcon(s github.PRStatus) string {
	switch s {
	case github.PROpen:
		return stylePROpen.Render("◉ open")
	case github.PRDraft:
		return stylePRDraft.Render("◌ draft")
	case github.PRMerged:
		return stylePRMerged.Render("✓ merged")
	case github.PRClosed:
		return stylePRClosed.Render("✕ closed")
	case github.PRNone:
		return ""
	default:
		return ""
	}
}

func sel(selected bool, s string) string {
	if selected {
		return styleSelected.Render(strings.TrimRight(s, " "))
	}
	return s
}

func zellijTabs() (map[string]bool, error) {
	return zellij.TabNames()
}

func stackPlaceholder(m *Model) string {
	if len(m.stCfg.Stacks) == 1 {
		return m.stCfg.Stacks[0].Alias + " (enter to use)"
	}
	return "stack alias"
}
