package tui

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/panamafrancis/workbench/pkg/config"
	"github.com/panamafrancis/workbench/pkg/git"
	"github.com/panamafrancis/workbench/pkg/github"
	"github.com/panamafrancis/workbench/pkg/sandbox"
	"github.com/panamafrancis/workbench/pkg/zellij"
)

const (
	tickInterval  = 60 * time.Second
	visibleMaxAge = 15 * time.Minute
	// wheelStep is how many rows one mouse-wheel notch scrolls.
	wheelStep = 3
	// fallbackPage is the half-page distance used by ctrl+d/ctrl+u before the
	// pane has reported its size.
	fallbackPage = 5
	// rowsTopOffset is the number of lines View renders above the first tree row
	// (the "workbench" header and its rule). Mouse Y coordinates go through it.
	rowsTopOffset = 2
)

type refreshMsg struct{}
type openErrMsg struct{ err error }
type createWorktreeMsg struct {
	name string
	err  error
}
type deleteWorktreeMsg struct {
	name string
	err  error
}
type openDoneMsg struct{}
type tickMsg time.Time
type dirtyMsg struct {
	dirty map[string]bool
}
type runningMsg struct {
	tabs map[string]bool
	err  error
}

type prBatchDoneMsg struct {
	// deferred counts branches the round left unresolved to stay above the
	// shared rate-limit reserve, so the footer can say why a status is missing
	// rather than leaving the user to wonder.
	deferred int
	ghErr    error
}

// prSkippedMsg is emitted when a fetch round was ceded to another sidebar
// process holding the PR-cache lock. It only clears the in-flight flag — the
// hints stay as they were, since nothing was learned this round.
type prSkippedMsg struct{}

type inputMode int

const (
	modeNormal        inputMode = iota
	modeAddRepoPath             // waiting for repo path
	modeAddRepoAlias            // waiting for alias
	modeNewWorktree             // waiting for worktree name (empty = auto)
	modeConfirmDelete           // waiting for y/n
	modeConfirmQuit             // waiting for y/n to quit sidebar
	modeOpenWith                // waiting for model name
	modeHelp                    // showing keybinding help
)

type Model struct {
	cfg                *config.Config
	state              *config.State
	tree               TreeModel
	prCache            *github.Cache
	ghAvailable        bool
	ghHint             string
	fetching           bool
	width              int
	height             int
	keys               KeyMap
	err                error
	msg                string
	mode               inputMode
	input              textinput.Model
	pendingPath        string
	pendingRepoIdx     int
	pendingWorktreeIdx int
	isSidebar          bool
	zellijHint         string
	// pending holds a half-typed multi-key sequence ("g" or "z").
	pending        string
	refreshingTabs bool
	creating       map[string]bool
	pendingOpen    *pendingOpenRequest
	ws             zellij.Workspace
}

type pendingOpenRequest struct {
	name          string
	modelOverride string
}

func New(cfg *config.Config) *Model {
	cache := github.NewCache(config.PRCachePath())
	_ = cache.Load()

	ws := zellij.WorkbenchWorkspace()
	validNames := make(map[string]bool)
	for _, name := range cfg.AllWorktreeNames() {
		validNames[name] = true
	}
	ws.CleanupStaleLayouts(validNames)

	state, _ := config.LoadState()

	t := newTree(cfg, cache)
	// Injected into the sidebar pane by WriteTabLayout; empty for the root
	// session sidebar. Drives the passive "you are here" marker.
	t.activeWorktree = os.Getenv("WORKBENCH_WORKTREE_NAME")
	return &Model{
		cfg:         cfg,
		state:       state,
		tree:        t,
		prCache:     cache,
		ghAvailable: true,
		keys:        DefaultKeyMap,
		input:       textinput.New(),
		isSidebar:   os.Getenv("WORKBENCH_SIDEBAR") == "1",
		ws:          ws,
	}
}

// reloadLocalState re-reads config.yml and state.yml (the shared on-disk cache)
// so the sidebar reflects current state without triggering a full refresh's
// network PR fetch. It is a no-op while a worktree is being created (an
// optimistic in-memory entry isn't persisted yet) or while an input/confirm
// mode is active (pendingRepoIdx/pendingWorktreeIdx point into the current
// slice and must not shift under it).
func (m *Model) reloadLocalState() {
	if m.mode != modeNormal || len(m.creating) > 0 {
		return
	}
	if newCfg, err := config.Load(); err == nil {
		// Preserve the selection by worktree name across the cfg swap: a
		// concurrent change from another tab can reorder rows, so re-pinning by
		// index alone would silently move the cursor to a different worktree.
		var selectedName string
		if sel := m.tree.selected(); sel != nil && !sel.isRepo && !sel.isPlaceholder {
			selectedName = sel.worktreeName
		}
		m.cfg = newCfg
		m.tree.cfg = newCfg
		if selectedName != "" {
			m.tree.selectWorktree(selectedName)
		}
		m.tree.clamp()
	}
	if newState, err := config.LoadState(); err == nil {
		m.state = newState
	}
}

func (m *Model) refreshRunningGuarded() tea.Cmd {
	if m.refreshingTabs {
		return nil
	}
	m.refreshingTabs = true
	return refreshRunningCmd()
}

func (m *Model) Init() tea.Cmd {
	return tea.Batch(
		m.tickCmd(),
		// Non-forced: respect the on-disk PR cache. The sidebar runs in a
		// restart loop, so forcing here would re-hit the GitHub GraphQL API
		// for every worktree on every restart and exhaust the rate limit.
		m.fetchStaleCmd(),
		refreshDirtyCmd(m.cfg),
		m.refreshRunningGuarded(),
	)
}

func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height

	case tea.FocusMsg:
		// Regaining focus (e.g. switching back to this tab) reloads the shared
		// on-disk state so a long-lived sidebar doesn't show a stale snapshot
		// that another tab has since changed — without the network PR fetch a
		// full refresh does.
		m.reloadLocalState()
		return m, tea.Batch(m.refreshRunningGuarded(), refreshDirtyCmd(m.cfg))

	case tea.MouseMsg:
		// Written with ifs rather than a switch on tea.MouseButton so it needn't
		// enumerate every button the exhaustive linter knows about.
		switch {
		case msg.Button == tea.MouseButtonWheelUp:
			m.tree.scrollBy(-wheelStep)
		case msg.Button == tea.MouseButtonWheelDown:
			m.tree.scrollBy(wheelStep)
		case msg.Button == tea.MouseButtonLeft && msg.Action == tea.MouseActionRelease:
			m.tree.selectByRow(msg.Y - rowsTopOffset)
		}

	case tickMsg:
		// Periodically re-sync the on-disk state so tabs converge even if no
		// focus event fires; the network PR fetch stays on its own staleness
		// schedule below.
		m.reloadLocalState()
		var cmds []tea.Cmd
		cmds = append(cmds, m.tickCmd(), m.refreshRunningGuarded())
		if m.ghAvailable && !m.fetching {
			cmds = append(cmds, m.fetchStaleCmd())
		}
		return m, tea.Batch(cmds...)

	case dirtyMsg:
		m.tree.dirty = msg.dirty

	case runningMsg:
		m.refreshingTabs = false
		if msg.err != nil {
			if errors.Is(msg.err, zellij.ErrCircuitOpen) {
				m.zellijHint = "zellij unreachable"
			} else {
				m.zellijHint = "tab sync error"
			}
		} else {
			m.tree.openTabs = msg.tabs
			m.zellijHint = ""
		}

	case prSkippedMsg:
		m.fetching = false

	case prBatchDoneMsg:
		m.fetching = false
		switch {
		case msg.ghErr == nil:
			m.ghAvailable = true
			m.ghHint = ""
			if msg.deferred > 0 {
				m.ghHint = "gh quota low"
			}
		case github.IsPermanentError(msg.ghErr):
			m.ghAvailable = false
			if errors.Is(msg.ghErr, github.ErrGHNotFound) {
				m.ghHint = "gh CLI not found"
			} else {
				m.ghHint = "gh auth required"
			}
		case github.IsRateLimited(msg.ghErr):
			// Leave ghAvailable true so fetches resume once the persisted
			// cooldown (InBackoff) expires.
			m.ghHint = "gh rate limited"
		default:
			m.ghHint = "sync error"
		}

	case refreshMsg:
		newCfg, err := config.Load()
		if err != nil {
			m.err = err
		} else {
			m.cfg = newCfg
			m.tree.cfg = newCfg
		}
		if newState, stateErr := config.LoadState(); stateErr == nil {
			m.state = newState
		}
		cmds := []tea.Cmd{refreshDirtyCmd(m.cfg), m.refreshRunningGuarded()}
		fetchCmd := m.fetchVisibleCmd(true)
		if fetchCmd != nil {
			m.msg = "refreshing..."
			cmds = append(cmds, fetchCmd)
		} else {
			m.msg = "refreshed (sync in progress)"
		}
		return m, tea.Batch(cmds...)

	case openDoneMsg:
		return m, m.refreshRunningGuarded()

	case openErrMsg:
		m.err = msg.err

	case createWorktreeMsg:
		delete(m.creating, msg.name)
		if msg.err != nil {
			if m.pendingOpen != nil && m.pendingOpen.name == msg.name {
				m.pendingOpen = nil
			}
			m.removeWorktreeFromConfig(msg.name)
			m.err = msg.err
		} else {
			// Reload from disk so the tree reflects the persisted state (and
			// drops any optimistic entry that another process removed) rather
			// than the in-memory snapshot, mirroring the delete handler.
			if newCfg, err := config.Load(); err == nil {
				m.cfg = newCfg
				m.tree.cfg = newCfg
				m.tree.clamp()
			}
			if newState, stateErr := config.LoadState(); stateErr == nil {
				m.state = newState
			}
			if m.pendingOpen != nil && m.pendingOpen.name == msg.name {
				openCmd := m.openWorktreeByName(msg.name, m.pendingOpen.modelOverride)
				m.pendingOpen = nil
				return m, tea.Batch(openCmd, refreshDirtyCmd(m.cfg))
			}
			m.msg = fmt.Sprintf("created worktree %q", msg.name)
			return m, refreshDirtyCmd(m.cfg)
		}

	case deleteWorktreeMsg:
		if msg.err != nil {
			m.err = msg.err
		} else {
			newCfg, err := config.Load()
			if err != nil {
				m.err = err
			} else {
				m.cfg = newCfg
				m.tree.cfg = newCfg
			}
			if newState, stateErr := config.LoadState(); stateErr == nil {
				m.state = newState
			}
			m.tree.clamp()
			m.msg = fmt.Sprintf("deleted worktree %q", msg.name)
			return m, refreshDirtyCmd(m.cfg)
		}

	case tea.KeyMsg:
		if m.mode == modeHelp {
			m.mode = modeNormal
			return m, nil
		}
		if m.mode == modeConfirmDelete {
			return m.updateConfirmDelete(msg)
		}
		if m.mode == modeConfirmQuit {
			return m.updateConfirmQuit(msg)
		}
		if m.mode != modeNormal {
			return m.updateInput(msg)
		}
		m.err = nil
		m.msg = ""

		// Two-key vim sequences: gg (first row), zM (fold all), zR (unfold all).
		// A pending prefix consumes exactly one more key; an unrecognized pair
		// clears the prefix and the key is handled on its own below, so a
		// mistyped g never swallows the next command.
		if m.pending != "" {
			seq := m.pending + msg.String()
			m.pending = ""
			switch seq {
			case "gg":
				m.tree.gotoTop()
				return m, nil
			case "zM":
				m.tree.setAllCollapsed(true)
				return m, nil
			case "zR":
				m.tree.setAllCollapsed(false)
				return m, nil
			}
		}
		if s := msg.String(); s == "g" || s == "z" {
			m.pending = s
			return m, nil
		}

		switch {
		case key.Matches(msg, m.keys.Quit):
			if m.isSidebar {
				m.mode = modeConfirmQuit
				return m, nil
			}
			return m, tea.Quit
		case key.Matches(msg, m.keys.Up):
			m.tree.moveUp()
		case key.Matches(msg, m.keys.Down):
			m.tree.moveDown()
		case key.Matches(msg, m.keys.HalfDown):
			m.tree.moveBy(m.tree.halfPage())
			return m, nil
		case key.Matches(msg, m.keys.HalfUp):
			m.tree.moveBy(-m.tree.halfPage())
			return m, nil
		case key.Matches(msg, m.keys.Bottom):
			m.tree.gotoBottom()
			return m, nil
		case key.Matches(msg, m.keys.NextRepo):
			m.tree.jumpRepo(1)
			return m, nil
		case key.Matches(msg, m.keys.PrevRepo):
			m.tree.jumpRepo(-1)
			return m, nil
		case key.Matches(msg, m.keys.Collapse):
			m.tree.collapseContaining()
		case key.Matches(msg, m.keys.Expand):
			m.tree.expandContaining()
		case key.Matches(msg, m.keys.Toggle):
			m.tree.toggleCollapse()
		case key.Matches(msg, m.keys.Refresh):
			return m, func() tea.Msg { return refreshMsg{} }
		case key.Matches(msg, m.keys.Open):
			return m, m.openSelected("")
		case key.Matches(msg, m.keys.New):
			sel := m.tree.selected()
			if sel == nil {
				m.err = fmt.Errorf("no repo selected")
				return m, nil
			}
			m.pendingRepoIdx = sel.repoIdx
			ti := textinput.New()
			ti.Placeholder = "auto-generate"
			ti.Focus()
			m.input = ti
			m.mode = modeNewWorktree
		case key.Matches(msg, m.keys.Delete):
			sel := m.tree.selected()
			if sel == nil || sel.isRepo || sel.isPlaceholder {
				m.err = fmt.Errorf("select a worktree to delete")
				return m, nil
			}
			m.pendingRepoIdx = sel.repoIdx
			m.pendingWorktreeIdx = sel.worktreeIdx
			m.mode = modeConfirmDelete
		case key.Matches(msg, m.keys.OpenWith):
			sel := m.tree.selected()
			if sel == nil || sel.isRepo || sel.isPlaceholder {
				m.err = fmt.Errorf("select a worktree to open")
				return m, nil
			}
			m.pendingRepoIdx = sel.repoIdx
			m.pendingWorktreeIdx = sel.worktreeIdx
			ti := textinput.New()
			ti.Placeholder = m.cfg.ResolveModel("")
			ti.Focus()
			m.input = ti
			m.mode = modeOpenWith
		case key.Matches(msg, m.keys.Help):
			m.mode = modeHelp
		case key.Matches(msg, m.keys.AddRepo):
			ti := textinput.New()
			ti.Placeholder = "/path/to/repo"
			ti.Focus()
			m.input = ti
			m.mode = modeAddRepoPath
		}
	}
	return m, nil
}

func (m *Model) updateInput(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.mode = modeNormal
		m.pendingPath = ""
		return m, nil

	case "enter":
		val := strings.TrimSpace(m.input.Value())

		if m.mode == modeOpenWith {
			m.mode = modeNormal
			if val == "" {
				return m, nil
			}
			return m, m.openSelected(val)
		}

		if m.mode == modeNewWorktree {
			return m.createWorktreeOptimistic(val)
		}

		if m.mode == modeAddRepoPath {
			if val == "" {
				m.err = fmt.Errorf("path cannot be empty")
				m.mode = modeNormal
				return m, nil
			}
			abs, err := filepath.Abs(val)
			if err != nil {
				m.err = fmt.Errorf("invalid path: %w", err)
				m.mode = modeNormal
				return m, nil
			}
			if _, err := os.Stat(abs); err != nil {
				m.err = fmt.Errorf("path does not exist: %s", abs)
				m.mode = modeNormal
				return m, nil
			}
			m.pendingPath = abs
			ti := textinput.New()
			defaultAlias := filepath.Base(abs)
			ti.Placeholder = defaultAlias
			ti.SetValue(defaultAlias)
			ti.Focus()
			m.input = ti
			m.mode = modeAddRepoAlias
			return m, nil
		}

		// modeAddRepoAlias
		alias := val
		if alias == "" {
			alias = filepath.Base(m.pendingPath)
		}
		if r, _ := m.cfg.FindRepo(alias); r != nil {
			m.err = fmt.Errorf("alias %q already registered", alias)
			m.mode = modeNormal
			m.pendingPath = ""
			return m, nil
		}
		m.cfg.Repos = append(m.cfg.Repos, config.Repo{
			Alias:     alias,
			LocalPath: m.pendingPath,
		})
		if err := m.cfg.Save(); err != nil {
			m.err = err
		} else {
			m.tree.cfg = m.cfg
			m.msg = fmt.Sprintf("added repo %q", alias)
		}
		m.mode = modeNormal
		m.pendingPath = ""
		return m, nil
	}

	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

func (m *Model) createWorktreeOptimistic(nameInput string) (tea.Model, tea.Cmd) {
	repoIdx := m.pendingRepoIdx
	m.mode = modeNormal

	repo := m.cfg.Repos[repoIdx]
	existing := m.cfg.AllWorktreeNames()

	name := nameInput
	if name == "" {
		// Skip names still reserved by worktrees whose Claude history lingers,
		// mirroring the CLI add-worktree flow, so a new tab can't collide with a
		// stale session of the same name.
		genState, _ := config.LoadState()
		genState.ReclaimReservedCities(m.cfg.WorktreeNameSet(), sandbox.HasPriorSession)
		excluded := append(append([]string{}, existing...), genState.ReservedNames()...)
		var err error
		name, err = git.GenerateName(excluded)
		if err != nil {
			m.err = err
			return m, nil
		}
	} else {
		if err := git.ValidateName(name, existing); err != nil {
			m.err = err
			return m, nil
		}
	}

	branch := fmt.Sprintf("wt/%s/%s", repo.Alias, name)
	base := m.cfg.ResolveWorktreeBase()
	wtPath := config.WorktreePath(base, repo.Alias, name)
	modelKey := m.cfg.ResolveModel("")

	wt := config.Worktree{
		Name:      name,
		Branch:    branch,
		Path:      wtPath,
		CreatedAt: time.Now(),
		Model:     modelKey,
	}
	m.cfg.Repos[repoIdx].Worktrees = append(m.cfg.Repos[repoIdx].Worktrees, wt)
	m.tree.cfg = m.cfg
	if err := os.MkdirAll(wtPath, 0755); err != nil {
		m.removeWorktreeFromConfig(name)
		m.err = fmt.Errorf("create dir: %w", err)
		return m, nil
	}
	if m.creating == nil {
		m.creating = make(map[string]bool)
	}
	m.creating[name] = true
	m.msg = fmt.Sprintf("creating %q...", name)

	alias := repo.Alias
	repoPath := repo.LocalPath
	createRepo := repo
	return m, func() tea.Msg {
		_, err := git.CreateWorktree(repoPath, wtPath, branch)
		if err != nil {
			os.Remove(wtPath) //nolint:errcheck
			return createWorktreeMsg{name: name, err: err}
		}
		// Copy gitignored files (copy_files) from the repo into the fresh
		// worktree before persisting, matching the CLI add-worktree flow.
		if err := createRepo.RunCopyFiles(wtPath); err != nil {
			os.RemoveAll(wtPath) //nolint:errcheck
			return createWorktreeMsg{name: name, err: err}
		}
		// Persist via a fresh read-modify-write so a stale in-memory snapshot
		// can't resurrect worktrees deleted by another process or instance.
		if err := config.AddWorktree(alias, wt); err != nil {
			return createWorktreeMsg{name: name, err: err}
		}

		state, _ := config.LoadState()
		// Reserve the new name and prune stale reserved entries (see the CLI
		// add-worktree flow).
		state.ReserveAndReclaim(name, wtPath, sandbox.HasPriorSession)
		state.RecordWorktreeCreated(name)
		_ = state.CheckAndUnlockAchievements()
		_ = state.Save()

		return createWorktreeMsg{name: name}
	}
}

func (m *Model) removeWorktreeFromConfig(name string) {
	for ri := range m.cfg.Repos {
		for wi := range m.cfg.Repos[ri].Worktrees {
			if m.cfg.Repos[ri].Worktrees[wi].Name == name {
				m.cfg.Repos[ri].Worktrees = slices.Delete(m.cfg.Repos[ri].Worktrees, wi, wi+1)
				m.tree.cfg = m.cfg
				return
			}
		}
	}
}

func (m *Model) updateConfirmDelete(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "y", "Y":
		m.mode = modeNormal
		return m, m.deleteWorktree()
	default:
		m.mode = modeNormal
		m.msg = "delete cancelled"
		return m, nil
	}
}

func (m *Model) updateConfirmQuit(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "y", "Y":
		return m, tea.Quit
	default:
		m.mode = modeNormal
		return m, nil
	}
}

func (m *Model) deleteWorktree() tea.Cmd {
	repoIdx := m.pendingRepoIdx
	wtIdx := m.pendingWorktreeIdx
	cfg := m.cfg
	prCache := m.prCache

	return func() tea.Msg {
		repo := cfg.Repos[repoIdx]
		wt := repo.Worktrees[wtIdx]

		_ = repo.RunCleanup(wt.Path, wt.Name)

		if err := git.RemoveWorktree(repo.LocalPath, wt.Path); err != nil {
			return deleteWorktreeMsg{err: err}
		}

		_ = git.DeleteBranch(repo.LocalPath, wt.Branch)
		_ = sandbox.ClearSessionCache(wt.Path)

		// Read-modify-write against current disk state so the removal can't
		// clobber worktrees created concurrently by another process.
		if err := config.RemoveWorktreeEntry(wt.Name); err != nil {
			return deleteWorktreeMsg{err: err}
		}

		state, _ := config.LoadState()
		// Keep the name reserved until its Claude history is cleaned up;
		// ReserveAndReclaim releases it once ClearSessionCache has removed it.
		state.ReserveAndReclaim(wt.Name, wt.Path, sandbox.HasPriorSession)
		if prCache != nil {
			if info := prCache.Get(wt.Branch); info != nil && info.Status == github.PRMerged {
				state.RecordWorktreeMerged()
				if !info.UpdatedAt.IsZero() && info.UpdatedAt.Sub(wt.CreatedAt) < time.Hour {
					state.UnlockAchievement("speed-demon")
				}
			}
		}
		_ = state.CheckAndUnlockAchievements()
		_ = state.Save()

		m.ws.CleanupLayout(wt.Name)
		return deleteWorktreeMsg{name: wt.Name}
	}
}

func (m *Model) openSelected(modelOverride string) tea.Cmd {
	sel := m.tree.selected()
	if sel == nil || sel.isRepo || sel.isPlaceholder {
		return nil
	}
	wt := m.cfg.Repos[sel.repoIdx].Worktrees[sel.worktreeIdx]
	if m.creating[wt.Name] {
		m.pendingOpen = &pendingOpenRequest{name: wt.Name, modelOverride: modelOverride}
		m.msg = fmt.Sprintf("waiting for %q to finish creating...", wt.Name)
		return nil
	}
	return m.openWorktree(wt, m.cfg.Repos[sel.repoIdx], modelOverride)
}

func (m *Model) openWorktreeByName(name, modelOverride string) tea.Cmd {
	wt, repo := m.cfg.FindWorktree(name)
	if wt == nil {
		return nil
	}
	return m.openWorktree(*wt, *repo, modelOverride)
}

func (m *Model) openWorktree(wt config.Worktree, repo config.Repo, modelOverride string) tea.Cmd {
	modelKey := m.cfg.ResolveModel(modelOverride)
	if wt.Model != "" {
		modelKey = m.cfg.ResolveModel(wt.Model)
	}
	if modelOverride != "" {
		modelKey = modelOverride
	}

	return func() tea.Msg {
		if !zellij.IsInZellij() {
			return openErrMsg{fmt.Errorf("not inside a Zellij session")}
		}
		nonoArgs, err := sandbox.BuildNonoArgs(wt.Path, modelKey, m.cfg)
		if err != nil {
			return openErrMsg{err}
		}
		envVars := map[string]string{
			"WORKBENCH":               "1",
			"WORKBENCH_WORKTREE_NAME": wt.Name,
			"WORKBENCH_REPO_ALIAS":    repo.Alias,
			"WORKBENCH_BRANCH":        wt.Branch,
		}
		created, err := m.ws.OpenOrFocusTab(wt.Name, wt.Path, m.cfg.ResolveSidebarWidth(), nonoArgs, envVars)
		if err != nil {
			return openErrMsg{err}
		}
		if created {
			if err := repo.RunStartup(wt.Path, wt.Name); err != nil {
				return openErrMsg{fmt.Errorf("startup script: %w", err)}
			}
		}
		return openDoneMsg{}
	}
}

func (m *Model) statsBox() string {
	if m.state == nil || !m.cfg.ResolveShowStats() || m.height < 15 {
		return ""
	}
	var sb strings.Builder
	cityCount := len(m.state.CitiesVisited)
	totalCities := len(git.Cities)
	streak := m.state.CurrentStreak()

	sb.WriteString(styleMuted.Render(fmt.Sprintf(" Cities: %d/%d", cityCount, totalCities)))
	sb.WriteString("\n")

	parts := []string{
		fmt.Sprintf("Created: %d", m.state.WorktreesCreated),
		fmt.Sprintf("Merged: %d", m.state.WorktreesMerged),
	}
	if streak > 0 {
		parts = append(parts, fmt.Sprintf("Streak: %dd", streak))
	}
	sb.WriteString(styleMuted.Render(" " + strings.Join(parts, "  ")))
	sb.WriteString("\n")

	if len(m.state.Achievements) > 0 {
		latest := m.state.Achievements[len(m.state.Achievements)-1]
		sb.WriteString(styleMuted.Render(fmt.Sprintf(" Latest: %s", config.AchievementDescription(latest.ID))))
		sb.WriteString("\n")
	}
	return sb.String()
}

func (m *Model) View() string {
	// Everything below the tree is rendered first: its line count is what's left
	// over for the scrollable tree viewport.
	var sb strings.Builder
	tail := m.viewTail()

	sb.WriteString(styleHeader.Render("workbench"))
	sb.WriteString("\n")
	sb.WriteString(styleMuted.Render(strings.Repeat("─", m.width)))
	sb.WriteString("\n")

	avail := m.height - rowsTopOffset - strings.Count(tail, "\n") - 1
	if m.height > 0 && avail < 1 {
		avail = 1
	}
	sb.WriteString(m.tree.view(m.width, avail))
	sb.WriteString(tail)
	return sb.String()
}

// viewTail renders everything below the tree: the closing rule, the optional
// stats box, and the status/footer line for the current mode.
func (m *Model) viewTail() string {
	var sb strings.Builder

	sb.WriteString(styleMuted.Render(strings.Repeat("─", m.width)))
	sb.WriteString("\n")

	if box := m.statsBox(); box != "" {
		sb.WriteString(box)
		sb.WriteString(styleMuted.Render(strings.Repeat("─", m.width)))
		sb.WriteString("\n")
	}

	switch m.mode {
	case modeNormal:
		sb.WriteString(styleMuted.Render(m.tree.stats()))
		sb.WriteString("\n")
		switch {
		case m.err != nil:
			sb.WriteString(styleDirty.Render("error: " + m.err.Error()))
		case m.msg != "":
			sb.WriteString(styleMuted.Render(m.msg))
		default:
			footer := m.contextFooter()
			sb.WriteString(styleStatusBar.Render(footer))
		}
	case modeNewWorktree:
		repo := m.cfg.Repos[m.pendingRepoIdx]
		sb.WriteString(styleMuted.Render(fmt.Sprintf("new worktree [%s] name: ", repo.Alias)) + m.input.View())
	case modeAddRepoPath:
		sb.WriteString(styleMuted.Render("repo path: ") + m.input.View())
	case modeAddRepoAlias:
		sb.WriteString(styleMuted.Render("alias: ") + m.input.View())
	case modeConfirmDelete:
		wt := m.cfg.Repos[m.pendingRepoIdx].Worktrees[m.pendingWorktreeIdx]
		prompt := fmt.Sprintf("delete %q?", wt.Name)
		if m.tree.dirty[wt.Name] {
			prompt += " (dirty)"
		}
		if m.tree.openTabs[wt.Name] {
			prompt += " (running)"
		}
		prompt += " [y/n]"
		sb.WriteString(styleDirty.Render(prompt))
	case modeConfirmQuit:
		sb.WriteString(styleDirty.Render("quit sidebar? [y/n]"))
	case modeOpenWith:
		models := make([]string, 0, len(m.cfg.Models))
		for k := range m.cfg.Models {
			models = append(models, k)
		}
		slices.Sort(models)
		sb.WriteString(styleMuted.Render(fmt.Sprintf("model (%s): ", strings.Join(models, "/"))) + m.input.View())
	case modeHelp:
		sb.WriteString(m.helpView())
	}

	return sb.String()
}

func (m *Model) contextFooter() string {
	sel := m.tree.selected()
	var parts []string

	if sel != nil && !sel.isRepo {
		if sel.isPlaceholder {
			parts = append(parts, "[n]ew")
		} else {
			parts = append(parts, "[o]pen", "[n]ew", "[d]el")
		}
	} else if sel != nil && sel.isRepo {
		parts = append(parts, "[space]unfold")
	}

	parts = append(parts, "[A]dd repo", "[r]efresh", "[?]help")

	if m.fetching {
		parts = append(parts, "⟳")
	} else if m.ghHint != "" {
		parts = append(parts, "("+m.ghHint+")")
	}
	if m.zellijHint != "" {
		parts = append(parts, "("+m.zellijHint+")")
	}

	footer := strings.Join(parts, " ")
	for m.width > 0 && lipgloss.Width(footer) > m.width && len(parts) > 1 {
		parts = parts[:len(parts)-1]
		footer = strings.Join(parts, " ")
	}
	return footer
}

func (m *Model) helpView() string {
	lines := []string{
		styleHeader.Render("Navigation"),
		"  j/k ↑↓   move down/up",
		"  ctrl+d/u half page down/up",
		"  gg / G   first / last row",
		"  } / {    next / prev repo",
		"  wheel    scroll  ·  click  select",
		"",
		styleHeader.Render("Worktree commands"),
		"  enter/o  open worktree",
		"  O        open with model",
		"  n        new worktree",
		"  d        delete worktree",
		"  space    fold/unfold repo",
		"  h/←      collapse  l/→  expand",
		"  zM / zR  fold / unfold all",
		"",
		styleHeader.Render("Global"),
		"  A        add repo",
		"  r        refresh",
		"  ?        this help",
		"  q/esc    quit",
		"",
		styleHeader.Render("Zellij basics"),
		"  Alt+←/→     switch panes",
		"  Ctrl+t ←/→  switch tabs",
		"  Ctrl+o d    detach session",
		"  Ctrl+q      quit session",
		"",
		styleMuted.Render("press any key to close"),
	}
	return strings.Join(lines, "\n")
}

func (m *Model) tickCmd() tea.Cmd {
	return tea.Tick(tickInterval, func(t time.Time) tea.Msg {
		return tickMsg(t)
	})
}

// fetchVisibleCmd refreshes PR status for the worktrees on screen. The round
// itself lives in github.Sync — one conditional poll per repo, which costs
// nothing when the repo has not changed — and runs inside Cache.TryMutate, so
// only one sidebar per machine fetches per round and its writes merge into
// whatever peers wrote meanwhile.
func (m *Model) fetchVisibleCmd(force bool) tea.Cmd {
	if m.fetching {
		return nil
	}
	// Re-read the on-disk cache first so this long-lived sidebar picks up the
	// statuses (and backoff) another tab's sidebar persisted.
	if m.prCache != nil {
		_ = m.prCache.Load()
		if m.prCache.InBackoff(github.ResourceCore, time.Now()) {
			// A peer tab may have armed the backoff; surface the hint here too so
			// every tab signals that fetches are paused, not just the one that hit
			// the limit.
			m.ghHint = "gh rate limited"
			return nil
		}
	}

	// Targets are simply what the user can see: which of them actually need a
	// request is Sync's decision, not the sidebar's.
	var targets []github.Target
	for _, r := range m.cfg.Repos {
		if m.tree.collapsed[r.Alias] {
			continue
		}
		for _, w := range r.Worktrees {
			targets = append(targets, github.Target{RepoPath: r.LocalPath, Branch: w.Branch})
		}
	}
	if len(targets) == 0 {
		return nil
	}

	m.fetching = true
	cache := m.prCache
	return func() tea.Msg {
		var report github.SyncReport
		lockErr := cache.TryMutate(func(w *github.Writable) error {
			report = github.Sync(w, targets, github.SyncOptions{
				MaxAge: visibleMaxAge,
				Force:  force,
			})
			return nil
		})
		if errors.Is(lockErr, github.ErrLockBusy) {
			// Another sidebar owns this round; cede and let the next tick pick up
			// the cache it writes.
			return prSkippedMsg{}
		}
		if lockErr != nil {
			return prBatchDoneMsg{ghErr: lockErr}
		}
		return prBatchDoneMsg{ghErr: report.Err, deferred: report.Deferred}
	}
}

func (m *Model) fetchStaleCmd() tea.Cmd {
	return m.fetchVisibleCmd(false)
}
