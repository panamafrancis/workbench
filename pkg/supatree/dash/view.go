package dash

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/panamafrancis/workbench/pkg/github"
	"github.com/panamafrancis/workbench/pkg/supatree"
)

// rowsTopOffset is the number of lines View renders above the first row (title
// + blank + column header). Mouse Y coordinates translate through it.
const rowsTopOffset = 3

// wideAt is the terminal width from which the stack column earns its space.
const wideAt = 100

func (m *Model) View() string {
	var b strings.Builder
	b.WriteString(m.header())
	b.WriteString("\n\n")

	if !m.loaded {
		b.WriteString(styleMuted.Render("loading…"))
		return b.String()
	}
	if len(m.summary.Trees) == 0 {
		b.WriteString(styleMuted.Render("no supatrees — create one with: supatree new"))
		return b.String()
	}

	b.WriteString(m.columns())
	b.WriteString("\n")

	footer := m.footer()
	reserved := rowsTopOffset + 1 + strings.Count(footer, "\n") + 1
	avail := m.height - reserved
	if m.height > 0 && avail < 1 {
		avail = 1
	}
	start, end := m.viewport(avail)
	for i := start; i < end; i++ {
		b.WriteString(m.renderRow(m.rows[i], i == m.cursor))
		b.WriteString("\n")
	}

	b.WriteString("\n")
	b.WriteString(footer)
	return b.String()
}

// viewport returns the [start, end) row range to render, recording the visible
// height for paging and wheel clamping. While follow is set (any cursor move)
// it drags the scroll offset along to keep the cursor visible; the wheel clears
// the flag so a scrolled-away view stays put across background reloads.
func (m *Model) viewport(avail int) (int, int) {
	m.viewRows = avail
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

func (m *Model) header() string {
	s := m.summary
	parts := []string{fmt.Sprintf("%d supatrees", len(s.Trees))}
	if s.OpenPRs > 0 {
		parts = append(parts, fmt.Sprintf("%d open PRs", s.OpenPRs))
	}
	if s.ApprovedPRs > 0 {
		parts = append(parts, fmt.Sprintf("%d approved", s.ApprovedPRs))
	}
	if s.Stale > 0 {
		parts = append(parts, fmt.Sprintf("%d stale", s.Stale))
	}
	if s.Done > 0 {
		parts = append(parts, fmt.Sprintf("%d done", s.Done))
	}
	left := styleTitle.Render("supatree") + styleMuted.Render("  "+strings.Join(parts, " · "))

	right := styleMuted.Render(m.freshness())
	gap := m.width - lipgloss.Width(left) - lipgloss.Width(right)
	if m.width <= 0 || gap < 2 {
		return left + "  " + right
	}
	return left + strings.Repeat(" ", gap) + right
}

// freshness says how old the GitHub half of the picture is — the dashboard is
// cache-backed, so a reader must be able to tell live data from a snapshot.
func (m *Model) freshness() string {
	switch {
	case m.fetching:
		return "fetching…"
	case m.prHint != "":
		return m.prHint
	case m.summary.LastFetch.IsZero():
		return "PR status never fetched — press r"
	default:
		if d := m.summary.At.Sub(m.summary.LastFetch); d >= time.Minute {
			return "PRs fetched " + ago(d) + " ago"
		}
		return "PRs just fetched"
	}
}

func (m *Model) columns() string {
	cols := "  " + pad("STATE", 10) + pad("SUPATREE", 24)
	if m.width >= wideAt {
		cols += pad("STACK", 14)
	}
	cols += pad("PRS", 7) + pad("REVIEW", 12) + pad("SEEN", 7) + "NOTES"
	return styleColumns.Render(truncate(cols, m.width))
}

// renderRow builds the row's plain text first and styles it last: truncation
// slices runes, so it must never run over a string that already carries ANSI
// escapes.
func (m *Model) renderRow(r row, selected bool) string {
	t := m.summary.Trees[r.tree]
	line, style := m.treeLine(t), stateStyle(t)
	if r.kind == rowMember {
		line, style = m.memberLine(t.Members[r.member]), styleMuted
	}
	line = truncate(line, m.width)
	if selected {
		return styleSelected.Render(padTo(line, m.width))
	}
	return style.Render(line)
}

func (m *Model) treeLine(t supatree.TreeStatus) string {
	fold := "▸"
	if m.expanded[t.Name] {
		fold = "▾"
	}
	prs := "-"
	if t.TotalPRs > 0 {
		prs = fmt.Sprintf("%d/%d", t.OpenPRs, t.TotalPRs)
	}
	seen := "-"
	if !t.LastActivity.IsZero() {
		seen = ago(m.summary.At.Sub(t.LastActivity))
	}
	line := fold + " " + pad(string(t.State), 10) + pad(t.Name, 24)
	if m.width >= wideAt {
		line += pad(t.Stack, 14)
	}
	return line + pad(prs, 7) + pad(reviewCell(t), 12) + pad(seen, 7) + strings.Join(notes(t), " · ")
}

// reviewCell condenses the review verdicts across a supatree's open PRs into
// one column: approvals, changes requested, and PRs still waiting.
func reviewCell(t supatree.TreeStatus) string {
	approved, changes, waiting := 0, 0, 0
	for _, mem := range t.Members {
		if mem.PR == nil || (mem.PR.Status != github.PROpen && mem.PR.Status != github.PRDraft) {
			continue
		}
		switch mem.PR.Review {
		case github.ReviewApproved:
			approved++
		case github.ReviewChanges:
			changes++
		case github.ReviewRequired, github.ReviewNone:
			waiting++
		}
	}
	var parts []string
	if approved > 0 {
		parts = append(parts, fmt.Sprintf("%d✓", approved))
	}
	if changes > 0 {
		parts = append(parts, fmt.Sprintf("%d✗", changes))
	}
	if waiting > 0 {
		parts = append(parts, fmt.Sprintf("%d·", waiting))
	}
	if len(parts) == 0 {
		return "-"
	}
	return strings.Join(parts, " ")
}

// notes are the reasons a supatree wants attention, in the order they matter.
func notes(t supatree.TreeStatus) []string {
	var out []string
	if t.Blocked {
		out = append(out, "blocked")
	}
	if t.State == supatree.TreeSetup {
		out = append(out, "run sync")
	}
	if t.Stale {
		out = append(out, "stale")
	}
	if t.Dirty {
		out = append(out, "uncommitted")
	}
	if t.Reap() {
		out = append(out, "rm me")
	}
	return out
}

func (m *Model) memberLine(mem supatree.MemberStatus) string {
	pr := "-"
	if mem.PR != nil && mem.PR.Number > 0 {
		pr = fmt.Sprintf("#%d", mem.PR.Number)
	}
	review := "-"
	if mem.PR != nil {
		switch mem.PR.Review {
		case github.ReviewApproved:
			review = "approved"
		case github.ReviewChanges:
			review = "changes"
		case github.ReviewRequired:
			review = "waiting"
		case github.ReviewNone:
		}
		if mem.PR.Checks == github.CheckFailing {
			review += " ✗ci"
		}
	}
	seen := "-"
	if !mem.LastCommit.IsZero() {
		seen = ago(m.summary.At.Sub(mem.LastCommit))
	}
	line := "    " + pad(string(mem.State), 10) + pad(mem.Alias, 22)
	if m.width >= wideAt {
		line += pad("", 14)
	}
	return line + pad(pr, 7) + pad(review, 12) + pad(seen, 7) + memberNotes(mem)
}

func memberNotes(mem supatree.MemberStatus) string {
	var out []string
	if mem.Dirty {
		out = append(out, "uncommitted")
	}
	if mem.Unpushed > 0 {
		out = append(out, fmt.Sprintf("%d unpushed", mem.Unpushed))
	} else if mem.PR == nil && mem.Ahead > 0 {
		out = append(out, fmt.Sprintf("%d commits", mem.Ahead))
	}
	if !mem.Exists {
		out = append(out, "not checked out")
	}
	return strings.Join(out, " · ")
}

// stateStyle colors a supatree row by what it needs: red when blocked, green
// when ready to merge, magenta when finished, yellow when it has gone quiet.
func stateStyle(t supatree.TreeStatus) lipgloss.Style {
	switch {
	case t.Blocked:
		return styleRed
	case t.State == supatree.TreeApproved:
		return styleGreen
	case t.State == supatree.TreeDone:
		return styleMagenta
	case t.Stale:
		return styleYellow
	default:
		return styleTree
	}
}

func (m *Model) footer() string {
	if m.err != nil {
		return styleRed.Render("error: " + m.err.Error())
	}
	parts := []string{"j/k move", "space expand", "}/{ tree", "enter focus tab", "r refresh", "q quit"}
	if m.msg != "" {
		parts = append([]string{m.msg}, parts...)
	}
	return styleMuted.Render(truncate(strings.Join(parts, " · "), m.width))
}

// ago renders a duration in a single unit so the column stays narrow.
func ago(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "now"
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}

// pad right-pads (or truncates) a cell to width, always leaving one space so
// neighbouring columns never run together.
func pad(s string, width int) string {
	if lipgloss.Width(s) >= width {
		return truncate(s, width-1) + " "
	}
	return s + strings.Repeat(" ", width-lipgloss.Width(s))
}

func padTo(s string, width int) string {
	if width <= 0 || lipgloss.Width(s) >= width {
		return s
	}
	return s + strings.Repeat(" ", width-lipgloss.Width(s))
}

// truncate cuts a string to width runes with an ellipsis. width <= 0 (no size
// reported yet) leaves it untouched.
func truncate(s string, width int) string {
	if width <= 0 || lipgloss.Width(s) <= width {
		return s
	}
	runes := []rune(s)
	if width <= 1 {
		return string(runes[:width])
	}
	return string(runes[:width-1]) + "…"
}
