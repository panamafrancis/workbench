package tui

import (
	"fmt"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/panamafrancis/workbench/pkg/git"
	"github.com/panamafrancis/workbench/pkg/github"
	"github.com/panamafrancis/workbench/pkg/supatree"
	"github.com/panamafrancis/workbench/pkg/zellij"
)

func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
	case tea.FocusMsg:
		// Switching back to this tab reloads live state so a long-lived sidebar
		// doesn't show a snapshot another tab has since changed.
		if m.mode == modeNormal {
			m.reloadWithSelection()
		}
		return m, tea.Batch(m.backgroundCmds()...)
	case tickMsg:
		// Periodically re-read live state so new/removed supatrees appear across
		// tabs without a manual refresh; the PR fetch stays on its staleness gate.
		if m.mode == modeNormal {
			m.reloadWithSelection()
		}
		return m, tea.Batch(append(m.backgroundCmds(), m.tickCmd())...)
	case dirtyMsg:
		m.dirty = msg.dirty
	case attentionMsg:
		m.attention = msg.attention
	case runningMsg:
		m.openTabs = msg.tabs
	case prSkippedMsg:
		m.fetching = false
	case prMsg:
		m.fetching = false
		switch {
		case msg.err == nil:
			m.ghAvailable = true
			m.prHint = ""
		case github.IsRateLimited(msg.err):
			// Leave ghAvailable true: the persisted backoff (InBackoff) gates
			// retries and lifts on its own.
			m.prHint = "gh rate limited"
		case github.IsPermanentError(msg.err):
			// No backoff is armed for auth/not-found, so stop the tick fetch loop
			// from retrying every tick forever; a manual `r` still forces a retry.
			m.ghAvailable = false
			m.prHint = "gh auth required"
		default:
			// Transient error (network blip): clear any stale hint since the
			// successful branches refreshed and nothing is persistently wrong.
			m.prHint = ""
		}
	case actionDoneMsg:
		m.msg = msg.msg
		m.err = msg.err
		m.reloadWithSelection()
		if msg.reveal != "" {
			// Land the cursor on the just-created tree so the viewport scrolls to
			// it, rather than leaving it pinned to the prior selection off-screen.
			m.selectRow(row{kind: rowTree, tree: msg.reveal, label: msg.reveal})
		}
		return m, tea.Batch(m.refreshDirtyCmd(), m.refreshRunningCmd(), m.refreshAttentionCmd())
	case tea.MouseMsg:
		return m.updateMouse(msg)
	case tea.KeyMsg:
		if m.mode == modeHelp {
			// Any key dismisses the reference — it is a read-only overlay, so
			// there is nothing to confirm or cancel.
			m.mode = modeNormal
			return m, nil
		}
		if m.mode != modeNormal {
			return m.updateInput(msg)
		}
		return m.updateNormal(msg)
	}
	return m, nil
}

// updateMouse handles wheel scrolling (pans the viewport, leaving the cursor
// where it is) and click-to-select. Written with ifs rather than a switch on
// tea.MouseButton so it needn't enumerate every button the linter knows about.
func (m *Model) updateMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	if m.mode != modeNormal {
		return m, nil
	}
	switch {
	case msg.Button == tea.MouseButtonWheelUp:
		m.scrollBy(-wheelStep)
	case msg.Button == tea.MouseButtonWheelDown:
		m.scrollBy(wheelStep)
	case msg.Button == tea.MouseButtonLeft && msg.Action == tea.MouseActionRelease:
		m.selectByRow(msg.Y - rowsTopOffset)
	}
	return m, nil
}

func (m *Model) updateNormal(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// The footer carries the last action's result, and an error there outlives
	// the moment it describes — a rejected supatree name sat under the next
	// create prompt until some other action happened to replace it. Any
	// deliberate keystroke means the user has read it, so clear it here and let
	// the action about to run post its own.
	m.err = nil
	m.msg = ""

	// Two-key vim sequences: gg (top), zM (fold all), zR (unfold all). A pending
	// prefix consumes exactly one more key; an unrecognized pair cancels the
	// prefix and the key is handled on its own below.
	if m.pending != "" {
		seq := m.pending + msg.String()
		m.pending = ""
		switch seq {
		case "gg":
			m.gotoTop()
			return m, nil
		case "zM":
			m.setAllCollapsed(true)
			return m, nil
		case "zR":
			m.setAllCollapsed(false)
			return m, nil
		}
	}

	switch msg.String() {
	case "q", "ctrl+c":
		if m.isSidebar && msg.String() == "q" {
			m.mode = modeConfirmQuit
			return m, nil
		}
		return m, tea.Quit
	case "j", "down":
		m.moveCursor(1)
	case "k", "up":
		m.moveCursor(-1)
	case "g", "z":
		m.pending = msg.String()
	case "G":
		m.gotoBottom()
	case "}", "]":
		m.jumpTree(1)
	case "{", "[":
		m.jumpTree(-1)
	case "ctrl+d":
		m.moveCursor(m.halfPage())
	case "ctrl+u":
		m.moveCursor(-m.halfPage())
	case "r":
		m.reloadWithSelection()
		return m, tea.Batch(m.refreshDirtyCmd(), m.refreshRunningCmd(), m.fetchPRCmd(true))
	case " ":
		// Fold the innermost section the cursor is in: the repositories list on a
		// repo or section row, the whole supatree anywhere else.
		if r := m.selected(); r != nil {
			if inRepos(r.kind) {
				m.setReposCollapse(r.tree, !m.ui.ReposCollapsed(r.tree))
			} else {
				m.setCollapse(r.tree, !m.ui.TreeCollapsed(r.tree))
			}
		}
	case "h", "left":
		// Vim's fold-close: shut the repositories section first, and only once it
		// is already shut does another h close the supatree around it.
		if r := m.selected(); r != nil {
			if inRepos(r.kind) && !m.ui.ReposCollapsed(r.tree) {
				m.setReposCollapse(r.tree, true)
			} else {
				m.setCollapse(r.tree, true)
			}
		}
	case "l", "right":
		if r := m.selected(); r != nil {
			if inRepos(r.kind) {
				m.setReposCollapse(r.tree, false)
			} else {
				m.setCollapse(r.tree, false)
			}
		}
	case "enter", "o":
		// On the repositories header "open" means open the section, since it is
		// neither a place to stand in nor a process to focus.
		if r := m.selected(); r != nil && r.kind == rowRepos {
			m.setReposCollapse(r.tree, !m.ui.ReposCollapsed(r.tree))
			return m, nil
		}
		return m, m.openSelected()
	case "a":
		if r := m.selected(); r != nil {
			// `a` reads the same everywhere — "give me an agent here" — so on a
			// member row it opens that repo's scoped agent (nono allows only that
			// repo) instead of prompting for a name at the tree root.
			if r.kind == rowMember {
				return m, m.openMember(r.tree, r.alias)
			}
			m.mode = modeNewAgent
			m.actionTree = r.tree
			m.input.SetValue("")
			m.inputErr = nil
			m.input.Placeholder = "agent name"
			m.input.Focus()
		}
	case "n":
		m.actionStack = ""
		m.input.SetValue("")
		m.inputErr = nil
		if len(m.stCfg.Stacks) > 1 {
			// Ambiguous: pick the stack from a list first, then name the tree.
			m.mode = modeNewTree
			m.stackCursor = 0
		} else {
			m.mode = modeNewTreeName
			m.input.Placeholder = "name (blank = auto)"
			m.input.Focus()
		}
	case "s":
		if r := m.selected(); r != nil {
			return m, m.syncTree(r.tree)
		}
	case "?":
		m.mode = modeHelp
	case "D":
		return m, m.openDashboard()
	case "P":
		return m, m.openPM()
	case "m":
		return m, m.handToPM()
	case "d":
		if r := m.selected(); r != nil {
			m.mode = modeConfirmDelete
			m.actionTree = r.tree
		}
	}
	return m, nil
}

func (m *Model) updateInput(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.mode == modeConfirmDelete || m.mode == modeConfirmQuit {
		confirmed := msg.String() == "y" || msg.String() == "Y"
		wasDelete := m.mode == modeConfirmDelete
		tree := m.actionTree
		m.mode = modeNormal
		if !confirmed {
			return m, nil
		}
		if wasDelete {
			return m, m.removeTree(tree)
		}
		return m, tea.Quit
	}

	// The new-tree stack picker is a selectable list, not a text field.
	if m.mode == modeNewTree {
		return m.updateStackPick(msg)
	}

	// Text-input modes (new agent / new tree name).
	switch msg.String() {
	case "esc":
		m.mode = modeNormal
		m.inputErr = nil
		m.input.Blur()
		return m, nil
	case "enter":
		val := m.input.Value()
		switch m.mode {
		case modeNewAgent:
			tree := m.actionTree
			m.mode = modeNormal
			m.input.Blur()
			if val == "" {
				return m, nil
			}
			return m, m.openAgent(tree, val)
		case modeNewTreeName:
			// A name the creator would reject keeps the prompt open with the
			// reason attached, rather than tearing it down and leaving the
			// complaint behind in the footer for the user to clear.
			if err := m.validateTreeName(val); err != nil {
				m.inputErr = err
				return m, nil
			}
			stack := m.actionStack
			m.mode = modeNormal
			m.inputErr = nil
			m.input.Blur()
			return m, m.newTree(stack, val)
		case modeNormal, modeNewTree, modeConfirmDelete, modeConfirmQuit, modeHelp:
			// Not text-input modes; handled earlier in updateInput.
		}
		m.mode = modeNormal
		m.input.Blur()
		return m, nil
	default:
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(msg)
		if m.mode == modeNewTreeName {
			// Re-validate on every keystroke so the warning tracks what is in the
			// field: it appears the moment the name goes bad and is gone again by
			// the time the offending character has been deleted.
			m.inputErr = m.validateTreeName(m.input.Value())
		}
		return m, cmd
	}
}

// validateTreeName reports why a typed supatree name would be rejected, or nil
// if it is fine. An empty name is fine — the creator generates one.
//
// It checks against the names already in memory rather than re-scanning the
// trees base, because it runs on every keystroke; supatree.New re-validates
// authoritatively against disk when the name is actually submitted.
func (m *Model) validateTreeName(name string) error {
	if name == "" {
		return nil
	}
	existing := make([]string, 0, len(m.insts))
	for _, inst := range m.insts {
		existing = append(existing, inst.Name)
	}
	existing = append(existing, m.wbCfg.AllWorktreeNames()...)
	return git.ValidateName(name, existing)
}

// inRepos reports whether a row kind sits inside a supatree's repositories
// section, and so whether the fold keys should act on that section rather than
// on the whole supatree.
func inRepos(k rowKind) bool {
	return k == rowRepos || k == rowMember
}

// updateStackPick drives the inline stack picker shown when more than one stack
// is registered: j/k (or arrows) move, enter chooses and advances to the name
// prompt, esc cancels.
func (m *Model) updateStackPick(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc", "q":
		m.mode = modeNormal
	case "up", "k":
		if m.stackCursor > 0 {
			m.stackCursor--
		}
	case "down", "j":
		if m.stackCursor < len(m.stCfg.Stacks)-1 {
			m.stackCursor++
		}
	case "enter":
		m.actionStack = m.stCfg.Stacks[m.stackCursor].Alias
		m.mode = modeNewTreeName
		m.input.SetValue("")
		m.inputErr = nil
		m.input.Placeholder = "name (blank = auto)"
		m.input.Focus()
	}
	return m, nil
}

func (m *Model) moveCursor(delta int) {
	m.follow = true
	m.cursor += delta
	for m.cursor >= 0 && m.cursor < len(m.rows) && m.rows[m.cursor].kind == rowSubheader {
		m.cursor += delta
	}
	if m.cursor < 0 {
		m.cursor = 0
		m.clampCursor()
	}
	if m.cursor >= len(m.rows) {
		m.cursor = len(m.rows) - 1
		m.clampCursor()
	}
}

// --- actions (return tea.Cmd producing actionDoneMsg) ---

func (m *Model) openSelected() tea.Cmd {
	r := m.selected()
	if r == nil {
		return nil
	}
	switch r.kind {
	case rowMember:
		// A member row is a place, not a process: enter stands in it. The
		// repo-scoped agent lives on `a`, alongside the tree-level one.
		return m.shellMember(r.tree, r.alias)
	case rowAgent:
		return m.openAgent(r.tree, r.label)
	case rowTree, rowSubheader, rowRepos:
		// rowRepos never reaches here — updateNormal folds it instead — but the
		// tree and agents headers stand for the supatree's main agent.
		return m.openAgent(r.tree, "main")
	}
	return nil
}

func (m *Model) openAgent(tree, agent string) tea.Cmd {
	return func() tea.Msg {
		inst := m.instance(tree)
		if inst == nil {
			return actionDoneMsg{err: fmt.Errorf("supatree %q gone", tree)}
		}
		_, err := supatree.OpenRootAgent(inst, m.wbCfg, m.ws, m.stCfg.ResolveSidebarWidth(), agent, "", nil)
		// Going to a supatree is what "I have seen this" means, so it clears the
		// attention marker. Doing it here rather than per render keeps the
		// per-tab sidebars off the ui.yml lock. Only on success: an open that
		// failed is one you never got to look at.
		if err == nil {
			supatree.MarkSeen(tree, time.Now())
		}
		return actionDoneMsg{msg: "opened " + supatree.TabName(tree, agent), err: err}
	}
}

func (m *Model) openMember(tree, alias string) tea.Cmd {
	return func() tea.Msg {
		inst := m.instance(tree)
		if inst == nil {
			return actionDoneMsg{err: fmt.Errorf("supatree %q gone", tree)}
		}
		_, err := supatree.OpenMemberAgent(inst, m.wbCfg, m.ws, m.stCfg.ResolveSidebarWidth(), alias, "")
		return actionDoneMsg{msg: "opened " + supatree.TabName(tree, alias), err: err}
	}
}

func (m *Model) shellMember(tree, alias string) tea.Cmd {
	return func() tea.Msg {
		inst := m.instance(tree)
		if inst == nil {
			return actionDoneMsg{err: fmt.Errorf("supatree %q gone", tree)}
		}
		if err := supatree.OpenMemberShell(inst, alias); err != nil {
			return actionDoneMsg{err: err}
		}
		return actionDoneMsg{msg: "shell " + tree + "/" + alias}
	}
}

func (m *Model) syncTree(tree string) tea.Cmd {
	return func() tea.Msg {
		inst := m.instance(tree)
		if inst == nil {
			return actionDoneMsg{err: fmt.Errorf("supatree %q gone", tree)}
		}
		report, err := supatree.Sync(inst.Root, m.wbCfg, false)
		if err != nil {
			return actionDoneMsg{err: err}
		}
		return actionDoneMsg{msg: fmt.Sprintf("synced %s (+%d)", tree, len(report.Created))}
	}
}

func (m *Model) removeTree(tree string) tea.Cmd {
	return func() tea.Msg {
		res, err := supatree.Remove(m.stCfg, m.wbCfg, tree, supatree.RemoveOptions{Force: true})
		if err != nil {
			return actionDoneMsg{err: err}
		}
		m.ws.CleanupLayout(tree)
		for _, a := range res.Agents {
			m.ws.CleanupLayout(supatree.TabName(tree, a.Name))
		}
		return actionDoneMsg{msg: "removed " + tree}
	}
}

// openDashboard opens (or focuses) the dashboard tab. It lives in its own tab
// rather than a pane under the sidebar so it costs no rows in every supatree
// tab and can be quit when it is not wanted.
func (m *Model) openDashboard() tea.Cmd {
	ws := m.ws
	return func() tea.Msg {
		if !zellij.IsInZellij() {
			return actionDoneMsg{msg: "not inside zellij — run: supatree dash"}
		}
		if err := ws.OpenOrFocusCommandTab(supatree.DashTab, []string{"supatree", "dash"}); err != nil {
			return actionDoneMsg{err: err}
		}
		return actionDoneMsg{msg: "dashboard"}
	}
}

// handToPM queues the selected row for the PM and focuses its tab, so you land
// in the chat with it already reading about that supatree.
//
// One verb on every row kind, which is what makes it learnable: what differs is
// only how much context the row carries. It does not *open* the PM if it is not
// running — the request waits in the queue, which is the whole point of the
// queue being read from a stored offset.
func (m *Model) handToPM() tea.Cmd {
	r := m.selected()
	if r == nil {
		return nil
	}
	ctx := *r
	prNumber := 0
	if ctx.kind == rowMember {
		if inst := m.instance(ctx.tree); inst != nil {
			if mem := inst.FindMember(ctx.alias); mem != nil {
				if pr := m.prCache.Get(mem.Branch); pr != nil {
					prNumber = pr.Number
				}
			}
		}
	}
	return func() tea.Msg {
		req := supatree.Request{
			From:   "sidebar",
			Tree:   ctx.tree,
			Member: ctx.alias,
			PR:     prNumber,
			Text:   handoffText(ctx),
		}
		if err := supatree.AppendRequest(req); err != nil {
			return actionDoneMsg{err: err}
		}
		if zellij.IsInZellij() {
			// Best effort: the request is queued either way, and failing to
			// focus a tab that is not open is not a failure to hand over.
			_ = zellij.GoToTab(supatree.PMTab)
		}
		return actionDoneMsg{msg: "handed " + ctx.tree + " to the PM"}
	}
}

// handoffText says what the human was looking at when they pressed m. The PM
// gets the row's identity as structured fields; this is the part it reads.
func handoffText(r row) string {
	switch r.kind {
	case rowMember:
		return fmt.Sprintf("Look at %s/%s.", r.tree, r.alias)
	case rowAgent:
		return fmt.Sprintf("Look at the %s agent in %s.", r.label, r.tree)
	case rowTree, rowSubheader, rowRepos:
		return fmt.Sprintf("Look at %s.", r.tree)
	}
	return "Look at " + r.tree + "."
}

// openPM opens or focuses the PM agent's tab. Unlike the dashboard it is a
// sandboxed agent rather than a command pane, so it goes through OpenOrFocusTab
// with its own grants rather than OpenOrFocusCommandTab.
func (m *Model) openPM() tea.Cmd {
	stCfg, wbCfg, ws, width := m.stCfg, m.wbCfg, m.ws, m.stCfg.ResolveSidebarWidth()
	return func() tea.Msg {
		if !zellij.IsInZellij() {
			return actionDoneMsg{msg: "not inside zellij — run: supatree pm"}
		}
		if _, err := supatree.OpenPM(stCfg, wbCfg, ws, width); err != nil {
			return actionDoneMsg{err: err}
		}
		return actionDoneMsg{msg: "PM"}
	}
}

func (m *Model) newTree(stack, name string) tea.Cmd {
	return func() tea.Msg {
		inst, _, err := supatree.New(m.stCfg, m.wbCfg, supatree.CreateOptions{Stack: stack, Name: name})
		if err != nil {
			return actionDoneMsg{err: err}
		}
		return actionDoneMsg{msg: "created " + inst.Name, reveal: inst.Name}
	}
}

// --- background polling ---

// refreshAttentionCmd recomputes which supatrees hold news you have not seen.
//
// Off the main loop, like every other disk read here. The ledger is append-only
// and grows without bound, and one sidebar runs per Zellij tab: reading it
// synchronously on each tick froze every sidebar for as long as the file took
// to scan. The dashboard already reads it in a tea.Cmd for exactly this reason.
func (m *Model) refreshAttentionCmd() tea.Cmd {
	ui := m.ui
	return func() tea.Msg {
		evs, err := supatree.ReadEvents(time.Now().Add(-attentionWindow))
		if err != nil {
			return attentionMsg{attention: map[string]bool{}}
		}
		return attentionMsg{attention: supatree.Attention(evs, ui)}
	}
}

func (m *Model) refreshDirtyCmd() tea.Cmd {
	insts := m.insts
	return func() tea.Msg {
		dirty := map[string]bool{}
		for _, inst := range insts {
			for _, mem := range inst.Members {
				if mem.Exists && git.IsDirty(mem.Path) {
					dirty[mem.Path] = true
				}
			}
		}
		return dirtyMsg{dirty: dirty}
	}
}

func (m *Model) refreshRunningCmd() tea.Cmd {
	return func() tea.Msg {
		tabs, err := zellijTabs()
		if err != nil {
			return runningMsg{tabs: map[string]bool{}}
		}
		return runningMsg{tabs: tabs}
	}
}

// backgroundCmds is the dirty/running/PR refresh triple shared by the focus and
// tick handlers. The PR fetch is skipped while gh is known-unavailable (a
// permanent error), so a broken auth doesn't spawn a fetch every tick forever.
func (m *Model) backgroundCmds() []tea.Cmd {
	cmds := []tea.Cmd{m.refreshDirtyCmd(), m.refreshRunningCmd(), m.refreshAttentionCmd()}
	if m.ghAvailable {
		cmds = append(cmds, m.fetchPRCmd(false))
	}
	return cmds
}

// fetchPRCmd fetches PR status for member branches. When force is false it only
// fetches entries older than prStaleAge, and it always respects the persisted
// backoff window. Target selection and the locked gh calls live in
// supatree.FetchTargets/FetchPRs so the sidebar and the dashboard share one
// implementation of the quota discipline — see the comments there.
//
// All of it runs inside the returned command: selecting targets asks git
// whether each member branch has been pushed (one process per member), which
// would otherwise stall the sidebar on every tick.
func (m *Model) fetchPRCmd(force bool) tea.Cmd {
	if m.fetching {
		return nil
	}
	m.fetching = true
	insts, cache := m.insts, m.prCache
	return func() tea.Msg {
		// Re-read the on-disk cache first so this long-lived sidebar picks up the
		// backoff (and freshly cached statuses) another tab's sidebar persisted —
		// otherwise each tab would independently keep hitting a rate-limited API.
		// Safe here because the m.fetching guard above rules out an in-flight
		// writer.
		_ = cache.Load()
		if cache.InBackoff(time.Now()) {
			// A peer tab may have armed the backoff; report it here too so every
			// tab (not just the one that hit the limit) signals that fetches are
			// paused.
			return prMsg{err: github.ErrGHRateLimited}
		}
		targets := supatree.FetchTargets(insts, cache, force, prStaleAge)
		if len(targets) == 0 {
			return prSkippedMsg{}
		}
		out := supatree.FetchPRs(targets, cache, force, prStaleAge)
		if out.Skipped {
			return prSkippedMsg{}
		}
		return prMsg{err: out.Err}
	}
}
