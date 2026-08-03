package tui

import (
	"errors"
	"fmt"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/panamafrancis/workbench/pkg/config"
	"github.com/panamafrancis/workbench/pkg/git"
	"github.com/panamafrancis/workbench/pkg/github"
	"github.com/panamafrancis/workbench/pkg/supatree"
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
		return m, tea.Batch(m.refreshDirtyCmd(), m.refreshRunningCmd())
	case tea.KeyMsg:
		if m.mode != modeNormal {
			return m.updateInput(msg)
		}
		return m.updateNormal(msg)
	}
	return m, nil
}

func (m *Model) updateNormal(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
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
	case "r":
		m.reloadWithSelection()
		return m, tea.Batch(m.refreshDirtyCmd(), m.refreshRunningCmd(), m.fetchPRCmd(true))
	case " ":
		if r := m.selected(); r != nil {
			m.setCollapse(r.tree, !m.collapsed[r.tree])
		}
	case "h", "left":
		if r := m.selected(); r != nil {
			m.setCollapse(r.tree, true)
		}
	case "l", "right":
		if r := m.selected(); r != nil {
			m.setCollapse(r.tree, false)
		}
	case "enter", "o":
		return m, m.openSelected()
	case "a":
		if r := m.selected(); r != nil {
			m.mode = modeNewAgent
			m.actionTree = r.tree
			m.input.SetValue("")
			m.input.Placeholder = "agent name"
			m.input.Focus()
		}
	case "n":
		m.actionStack = ""
		m.input.SetValue("")
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
			stack := m.actionStack
			m.mode = modeNormal
			m.input.Blur()
			return m, m.newTree(stack, val)
		case modeNormal, modeNewTree, modeConfirmDelete, modeConfirmQuit:
			// Not text-input modes; handled earlier in updateInput.
		}
		m.mode = modeNormal
		m.input.Blur()
		return m, nil
	default:
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(msg)
		return m, cmd
	}
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
		m.input.Placeholder = "name (blank = auto)"
		m.input.Focus()
	}
	return m, nil
}

func (m *Model) moveCursor(delta int) {
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
		return m.openMember(r.tree, r.alias)
	case rowAgent:
		return m.openAgent(r.tree, r.label)
	case rowTree, rowSubheader:
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
	cmds := []tea.Cmd{m.refreshDirtyCmd(), m.refreshRunningCmd()}
	if m.ghAvailable {
		cmds = append(cmds, m.fetchPRCmd(false))
	}
	return cmds
}

// fetchPRCmd fetches PR status for member branches. When force is false it only
// fetches entries older than prStaleAge, and it always respects the persisted
// backoff window. Crucially, the actual gh calls run under a cross-process
// try-lock (PRCacheLockPath): with one sidebar per Zellij tab all polling
// independently, only the tab that wins the lock fetches each round while the
// rest cede and pick up the cache it writes — this, not per-process throttling
// alone, is what stops a burst of concurrent gh calls from tripping GitHub's
// rate limit. A rate-limit response arms a cooldown that survives restarts.
func (m *Model) fetchPRCmd(force bool) tea.Cmd {
	if m.fetching {
		return nil
	}
	// Re-read the on-disk cache first so this long-lived sidebar picks up the
	// backoff (and freshly cached statuses) another tab's sidebar persisted —
	// otherwise each tab would independently keep hitting a rate-limited API.
	// Safe here because the m.fetching guard above rules out an in-flight writer.
	_ = m.prCache.Load()
	if m.prCache.InBackoff(time.Now()) {
		// A peer tab may have armed the backoff; surface the hint here too so every
		// tab (not just the one that hit the limit) signals that fetches are paused.
		m.prHint = "gh rate limited"
		return nil
	}

	type target struct{ path, branch string }
	var targets []target
	for _, inst := range m.insts {
		for _, mem := range inst.Members {
			if !mem.Exists {
				continue
			}
			if force || m.prCache.IsStale(mem.Branch, prStaleAge) {
				targets = append(targets, target{mem.Path, mem.Branch})
			}
		}
	}
	if len(targets) == 0 {
		return nil
	}

	m.fetching = true
	cache := m.prCache
	return func() tea.Msg {
		// prSkippedMsg unless we win the lock and actually run a fetch below.
		var result tea.Msg = prSkippedMsg{}
		lockErr := config.TryFileLock(supatree.PRCacheLockPath(), func() error {
			// Under the lock, re-read the cache: while we queued to build targets
			// another tab may have populated statuses or armed a backoff.
			_ = cache.Load()
			if cache.InBackoff(time.Now()) {
				return nil
			}
			var lastErr error
			for _, t := range targets {
				// A peer that just held the lock may have refreshed this branch;
				// don't re-fetch what is already fresh.
				if !force && !cache.IsStale(t.branch, prStaleAge) {
					continue
				}
				info, err := github.LookupPR(t.path, t.branch)
				if err != nil {
					lastErr = err
					if github.IsPermanentError(err) {
						_ = cache.Save()
						result = prMsg{err: err}
						return nil
					}
					if github.IsRateLimited(err) {
						cache.SetRetryAfter(time.Now().Add(rateLimitCooldown))
						_ = cache.Save()
						result = prMsg{err: err}
						return nil
					}
					continue
				}
				cache.Set(t.branch, info)
			}
			_ = cache.Save()
			result = prMsg{err: lastErr}
			return nil
		})
		if errors.Is(lockErr, config.ErrLockBusy) {
			// Another sidebar owns this round; cede and let the next tick pick up
			// the cache it writes.
			return prSkippedMsg{}
		}
		if lockErr != nil {
			return prMsg{err: lockErr}
		}
		return result
	}
}
