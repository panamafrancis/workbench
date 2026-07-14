package tui

import (
	"fmt"
	"strings"

	"github.com/panamafrancis/workbench/pkg/github"
	"github.com/panamafrancis/workbench/pkg/supatree"
	"github.com/panamafrancis/workbench/pkg/zellij"
)

func (m *Model) View() string {
	var b strings.Builder
	b.WriteString(styleHeader.Render("supatree"))
	b.WriteString("\n")

	if len(m.rows) == 0 {
		b.WriteString(styleMuted.Render("no supatrees — press n to create one"))
		b.WriteString("\n")
	}

	for i, r := range m.rows {
		selected := i == m.cursor && m.mode == modeNormal
		b.WriteString(m.renderRow(r, selected))
		b.WriteString("\n")
	}

	b.WriteString("\n")
	b.WriteString(m.footer())
	return b.String()
}

func (m *Model) renderRow(r row, selected bool) string {
	switch r.kind {
	case rowTree:
		badge := m.prBadge(r.tree)
		running := ""
		if m.openTabs[r.tree] {
			running = styleRunning.Render(" ●")
		}
		line := "▾ " + r.label + running
		if badge != "" {
			line += "  " + badge
		}
		return sel(selected, styleTree.Render(line))
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
		return "new supatree (stack): " + m.input.View()
	case modeConfirmDelete:
		return styleDirty.Render(fmt.Sprintf("delete %q? [y/N]", m.actionTree))
	case modeConfirmQuit:
		return styleDirty.Render("quit sidebar? [y/N]")
	case modeNormal:
	}
	if m.err != nil {
		return stylePRClosed.Render("error: " + m.err.Error())
	}
	help := "enter open · a agent · n new · s sync · d del · r refresh · q quit"
	if m.msg != "" {
		return styleStatus.Render(m.msg + "  ·  " + help)
	}
	return styleStatus.Render(help)
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
