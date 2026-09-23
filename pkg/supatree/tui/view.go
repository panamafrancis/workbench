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
	var b strings.Builder
	b.WriteString(styleHeader.Render("supatree"))
	b.WriteString("\n")

	// The reference replaces the list rather than overlaying it: the sidebar is
	// a narrow column, so there is nowhere to float a panel over.
	if m.mode == modeHelp {
		b.WriteString(helpView())
		return b.String()
	}

	footer := m.footer()
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

// viewport returns the [start, end) row range to render, recording the visible
// height for the paging and wheel handlers. While m.follow is set (any cursor
// movement) it pulls the scroll offset along to keep the cursor visible; the
// mouse wheel clears the flag so a scrolled-away view stays put across renders
// and background ticks. avail <= 0 (no reported size) or a list that fits shows
// everything.
func (m *Model) viewport(avail int) (int, int) {
	m.viewHeight = avail
	if avail <= 0 || len(m.rows) <= avail {
		m.scroll = 0
		return 0, len(m.rows)
	}
	if m.follow {
		if m.cursor < m.scroll {
			m.scroll = m.cursor
		}
		if m.cursor >= m.scroll+avail {
			m.scroll = m.cursor - avail + 1
		}
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
		// The PR summary belongs to the repositories section; it is lifted onto
		// the supatree row only when that section is out of sight, so a folded
		// supatree still reports where its members stand without the count being
		// printed twice when everything is open.
		badge := ""
		collapsed := m.ui.TreeCollapsed(r.tree)
		if collapsed {
			badge = m.prCounts(r.tree)
		}
		running := ""
		if m.openTabs[r.tree] {
			running = styleRunning.Render(" ●")
		}
		fold := "▼"
		if collapsed {
			fold = "▶"
		}
		line := fold + " " + r.label + running
		if m.attention[r.tree] {
			// Something happened in this supatree that you have not looked at.
			// It sits next to the name rather than in the gutter, which the
			// "you are here" marker already owns.
			line += styleAttention.Render(" !")
		}
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
	case rowRepos:
		fold := "▼"
		if m.ui.ReposCollapsed(r.tree) {
			fold = "▶"
		}
		// Indented one level past the supatree's own fold arrow and one level
		// short of its members, so the nesting reads at a glance.
		line := "    " + styleSub.Render(fold+" "+r.label)
		if counts := m.prCounts(r.tree); counts != "" {
			line += "  " + counts
		}
		return sel(selected, line)
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
	key := ""
	reviewed := ""
	if inst != nil {
		if mem := inst.FindMember(r.alias); mem != nil {
			// Keyed on the member, not its branch: a review member's branch is
			// tree-local and its status lives under the pull request instead.
			key = mem.CacheKey()
			if m.dirty[mem.Path] {
				dirtyMark = styleDirty.Render("*")
			}
			if !mem.Exists {
				dirtyMark = styleMuted.Render("·")
			}
			if mem.Review != nil {
				reviewed = styleMuted.Render(" " + mem.Review.HeadRef)
			}
		}
	}
	pr := ""
	if info := m.prCache.Get(key); info != nil {
		if icon := prIcon(info.Status); icon != "" {
			pr = "  " + icon
			if info.Number > 0 {
				pr += styleMuted.Render(fmt.Sprintf(" #%d", info.Number))
			}
		}
	}
	return fmt.Sprintf("      %s %-18s%s%s", dirtyMark, r.alias, pr, reviewed)
}

// prCountStatuses is the order counts are rendered in: roughly the order a PR
// travels through, with the members that have no PR yet last.
var prCountStatuses = []github.PRStatus{
	github.PRDraft, github.PROpen, github.PRMerged, github.PRClosed, github.PRNone,
}

// prCounts summarises a supatree's members as one coloured glyph-and-count per
// PR status present ("◉2 ✓1"), so a folded repositories section still says how
// far along the tree is. The colour carries the status — spelling it out would
// not fit the sidebar's width.
func (m *Model) prCounts(tree string) string {
	inst := m.instance(tree)
	if inst == nil || len(inst.Members) == 0 {
		return ""
	}
	counts := make(map[github.PRStatus]int, len(prCountStatuses))
	for _, mem := range inst.Members {
		status := github.PRNone
		if info := m.prCache.Get(mem.CacheKey()); info != nil {
			status = info.Status
		}
		counts[status]++
	}
	parts := make([]string, 0, len(prCountStatuses))
	for _, status := range prCountStatuses {
		if n := counts[status]; n > 0 {
			parts = append(parts, prStyle(status).Render(fmt.Sprintf("%s%d", prGlyph(status), n)))
		}
	}
	return strings.Join(parts, " ")
}

func (m *Model) footer() string {
	switch m.mode {
	case modeNewAgent:
		return "new agent: " + m.input.View()
	case modeNewTree:
		var b strings.Builder
		b.WriteString(styleSub.Render("new supatree — pick stack (↑/↓, enter, esc):"))
		for i, s := range m.stCfg.Stacks {
			b.WriteString("\n")
			if i == m.stackCursor {
				b.WriteString(styleSelected.Render("› " + s.Alias))
			} else {
				b.WriteString("  " + styleRow.Render(s.Alias))
			}
		}
		return b.String()
	case modeNewTreeName:
		prompt := "new supatree — name: " + m.input.View()
		if m.inputErr != nil {
			// Live validation: the complaint sits under the field the user is
			// still typing in, and goes away with the character that caused it.
			prompt += "\n" + styleDirty.Render(wrapText(m.inputErr.Error(), m.width))
		}
		return prompt
	case modeConfirmDelete:
		return styleDirty.Render(fmt.Sprintf("delete %q? [y/N]", m.actionTree))
	case modeConfirmQuit:
		return styleDirty.Render("quit sidebar? [y/N]")
	case modeHelp:
		return styleMuted.Render("press any key to close")
	case modeNormal:
	}
	if m.err != nil {
		return stylePRClosed.Render(wrapText("error: "+m.err.Error(), m.width))
	}
	// Enter is contextual (a member row is a place, an agent row is a process),
	// so the hint says which one the cursor is on rather than a generic "open".
	openHint := "enter open"
	if r := m.selected(); r != nil && r.kind == rowMember {
		openHint = "enter shell"
	}
	// The motions moved into `?` — the footer keeps the actions, which are the
	// ones that are not guessable from vim habits.
	parts := []string{openHint, "space fold", "a agent", "n new", "s sync", "d del", "D dash", "r refresh", "? help", "q quit"}
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

// prIcon is the per-member badge: the status glyph plus its name, in the status
// colour. PRStatus's own string is the name ("open", "merged", ...).
// wrapText word-wraps one message to the sidebar width — an error or a
// validation complaint is a sentence, and a narrow pane would otherwise clip it.
func wrapText(s string, width int) string {
	return wrapParts(strings.Fields(s), " ", width)
}

func prIcon(s github.PRStatus) string {
	if s == github.PRNone {
		return ""
	}
	return prStyle(s).Render(prGlyph(s) + " " + string(s))
}

// prGlyph and prStyle are the one place a PR status is turned into a symbol and
// a colour, shared by the per-member badge and the folded-section counts.
func prGlyph(s github.PRStatus) string {
	switch s {
	case github.PROpen:
		return "◉"
	case github.PRDraft:
		return "◌"
	case github.PRMerged:
		return "✓"
	case github.PRClosed:
		return "✕"
	case github.PRNone:
		return "·"
	default:
		return "·"
	}
}

func prStyle(s github.PRStatus) lipgloss.Style {
	switch s {
	case github.PROpen:
		return stylePROpen
	case github.PRDraft:
		return stylePRDraft
	case github.PRMerged:
		return stylePRMerged
	case github.PRClosed:
		return stylePRClosed
	case github.PRNone:
		return styleMuted
	default:
		return styleMuted
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

// helpView is the `?` reference. It is a plain block rather than the footer's
// wrapped one-liner because the sidebar is narrow: every line is kept short
// enough to survive a 25%-width pane without folding.
func helpView() string {
	lines := []string{
		styleHeader.Render("Navigation"),
		"  j/k ↑↓   move",
		"  ctrl+d/u half page",
		"  gg / G   first / last",
		"  } / {    next / prev tree",
		"  wheel    scroll",
		"  click    select",
		"",
		styleHeader.Render("Folding"),
		"  space    fold this section",
		"  h / l    close / open",
		"  zM / zR  fold / unfold all",
		"",
		styleHeader.Render("Open"),
		"  enter/o  agent, or shell",
		"           on a repo row",
		"  a        new named agent,",
		"           repo agent on a repo",
		"  D        dashboard",
		"  P        PM agent",
		"  m        hand this row to the PM",
		"",
		styleHeader.Render("Supatrees"),
		"  n        new supatree",
		"  s        sync members",
		"  d        delete supatree",
		"",
		styleHeader.Render("Global"),
		"  r        refresh",
		"  ?        this help",
		"  q        quit",
		"",
		styleHeader.Render("Zellij"),
		"  Alt+←/→     panes",
		"  Ctrl+t ←/→  tabs",
		"  Ctrl+o d    detach",
		"  Ctrl+q      quit session",
		"",
		styleMuted.Render("press any key to close"),
	}
	return strings.Join(lines, "\n")
}
