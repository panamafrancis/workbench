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
	activeMaxAge  = 5 * time.Minute
	visibleMaxAge = 15 * time.Minute
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
	ghErr error
}

type fetchTarget struct {
	repoPath string
	branch   string
}

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
	refreshingTabs     bool
}

func New(cfg *config.Config) *Model {
	cache := github.NewCache(config.PRCachePath())
	_ = cache.Load()

	validNames := make(map[string]bool)
	for _, name := range cfg.AllWorktreeNames() {
		validNames[name] = true
	}
	zellij.CleanupStaleLayouts(validNames)

	t := newTree(cfg, cache)
	return &Model{
		cfg:         cfg,
		tree:        t,
		prCache:     cache,
		ghAvailable: true,
		keys:        DefaultKeyMap,
		input:       textinput.New(),
		isSidebar:   os.Getenv("WORKBENCH_SIDEBAR") == "1",
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
		m.fetchVisibleCmd(true),
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
		return m, tea.Batch(m.refreshRunningGuarded(), refreshDirtyCmd(m.cfg))

	case tea.MouseMsg:
		if msg.Action == tea.MouseActionRelease && msg.Button == tea.MouseButtonLeft {
			row := msg.Y - 2
			m.tree.selectByRow(row)
		}

	case tickMsg:
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

	case prBatchDoneMsg:
		m.fetching = false
		if msg.ghErr != nil {
			if github.IsPermanentError(msg.ghErr) {
				m.ghAvailable = false
				if errors.Is(msg.ghErr, github.ErrGHNotFound) {
					m.ghHint = "gh CLI not found"
				} else {
					m.ghHint = "gh auth required"
				}
			} else {
				m.ghHint = "sync error"
			}
		} else {
			m.ghAvailable = true
			m.ghHint = ""
		}

	case refreshMsg:
		newCfg, err := config.Load()
		if err != nil {
			m.err = err
		} else {
			m.cfg = newCfg
			m.tree.cfg = newCfg
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
		switch {
		case key.Matches(msg, m.keys.Quit):
			if m.isSidebar {
				m.mode = modeConfirmQuit
				return m, nil
			}
			return m, tea.Quit
		case key.Matches(msg, m.keys.Up):
			prev := m.tree.cursor
			m.tree.moveUp()
			if m.tree.cursor != prev {
				return m, m.fetchIfUncached()
			}
		case key.Matches(msg, m.keys.Down):
			prev := m.tree.cursor
			m.tree.moveDown()
			if m.tree.cursor != prev {
				return m, m.fetchIfUncached()
			}
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
			return m, m.createWorktree(val)
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

func (m *Model) createWorktree(nameInput string) tea.Cmd {
	repoIdx := m.pendingRepoIdx
	m.mode = modeNormal
	cfg := m.cfg

	return func() tea.Msg {
		repo := cfg.Repos[repoIdx]
		existing := cfg.AllWorktreeNames()

		name := nameInput
		if name == "" {
			var err error
			name, err = git.GenerateName(existing)
			if err != nil {
				return createWorktreeMsg{err: err}
			}
		} else {
			if err := git.ValidateName(name, existing); err != nil {
				return createWorktreeMsg{err: err}
			}
		}

		branch := fmt.Sprintf("wt/%s/%s", repo.Alias, name)
		base := cfg.ResolveWorktreeBase()
		wtPath := config.WorktreePath(base, repo.Alias, name)

		if err := os.MkdirAll(wtPath, 0755); err != nil {
			return createWorktreeMsg{err: fmt.Errorf("create dir: %w", err)}
		}
		_ = os.Remove(wtPath)

		_, err := git.CreateWorktree(repo.LocalPath, wtPath, branch)
		if err != nil {
			return createWorktreeMsg{err: err}
		}

		modelKey := cfg.ResolveModel("")
		repo.Worktrees = append(repo.Worktrees, config.Worktree{
			Name:      name,
			Branch:    branch,
			Path:      wtPath,
			CreatedAt: time.Now(),
			Model:     modelKey,
		})
		for i := range cfg.Repos {
			if cfg.Repos[i].Alias == repo.Alias {
				cfg.Repos[i] = repo
				break
			}
		}
		if err := cfg.Save(); err != nil {
			return createWorktreeMsg{err: err}
		}
		return createWorktreeMsg{name: name}
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

	return func() tea.Msg {
		repo := cfg.Repos[repoIdx]
		wt := repo.Worktrees[wtIdx]

		_ = repo.RunCleanup(wt.Path, wt.Name)

		if err := git.RemoveWorktree(repo.LocalPath, wt.Path); err != nil {
			return deleteWorktreeMsg{err: err}
		}

		_ = git.DeleteBranch(repo.LocalPath, wt.Branch)

		repo.Worktrees = slices.Delete(repo.Worktrees, wtIdx, wtIdx+1)
		cfg.Repos[repoIdx] = repo
		if err := cfg.Save(); err != nil {
			return deleteWorktreeMsg{err: err}
		}
		return deleteWorktreeMsg{name: wt.Name}
	}
}

func (m *Model) openSelected(modelOverride string) tea.Cmd {
	sel := m.tree.selected()
	if sel == nil || sel.isRepo || sel.isPlaceholder {
		return nil
	}
	wt := m.cfg.Repos[sel.repoIdx].Worktrees[sel.worktreeIdx]
	repo := m.cfg.Repos[sel.repoIdx]
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
		created, err := zellij.OpenOrFocusTab(wt.Name, wt.Path, m.cfg.ResolveSidebarWidth(), nonoArgs, envVars)
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

func (m *Model) View() string {
	var sb strings.Builder

	sb.WriteString(styleHeader.Render("workbench"))
	sb.WriteString("\n")
	sb.WriteString(styleMuted.Render(strings.Repeat("─", m.width)))
	sb.WriteString("\n")

	sb.WriteString(m.tree.view(m.width))

	sb.WriteString(styleMuted.Render(strings.Repeat("─", m.width)))
	sb.WriteString("\n")

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
		styleHeader.Render("Worktree commands"),
		"  j/k ↑↓   navigate",
		"  enter/o  open worktree",
		"  O        open with model",
		"  n        new worktree",
		"  d        delete worktree",
		"  space    fold/unfold repo",
		"  h/←      collapse  l/→  expand",
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

func (m *Model) fetchVisibleCmd(force bool) tea.Cmd {
	if m.fetching {
		return nil
	}

	maxAge := visibleMaxAge
	if force {
		maxAge = 0
	}

	var targets []fetchTarget
	sel := m.tree.selected()
	for _, r := range m.cfg.Repos {
		if m.tree.collapsed[r.Alias] {
			continue
		}
		for wi, w := range r.Worktrees {
			age := maxAge
			if sel != nil && !sel.isRepo && sel.repoIdx < len(m.cfg.Repos) && sel.worktreeIdx == wi {
				repo := m.cfg.Repos[sel.repoIdx]
				if repo.Alias == r.Alias {
					age = activeMaxAge
				}
			}
			if force || m.prCache.IsStale(w.Branch, age) {
				targets = append(targets, fetchTarget{
					repoPath: r.LocalPath,
					branch:   w.Branch,
				})
			}
		}
	}

	if len(targets) == 0 {
		return nil
	}

	m.fetching = true
	cache := m.prCache
	return func() tea.Msg {
		var lastErr error
		for _, t := range targets {
			info, err := github.LookupPR(t.repoPath, t.branch)
			if err != nil {
				lastErr = err
				if github.IsPermanentError(err) {
					_ = cache.Save()
					return prBatchDoneMsg{ghErr: err}
				}
				continue
			}
			cache.Set(t.branch, info)
		}
		_ = cache.Save()
		return prBatchDoneMsg{ghErr: lastErr}
	}
}

func (m *Model) fetchStaleCmd() tea.Cmd {
	return m.fetchVisibleCmd(false)
}

func (m *Model) fetchIfUncached() tea.Cmd {
	if m.fetching || !m.ghAvailable {
		return nil
	}
	sel := m.tree.selected()
	if sel == nil || sel.isRepo || sel.isPlaceholder {
		return nil
	}
	w := m.cfg.Repos[sel.repoIdx].Worktrees[sel.worktreeIdx]
	if m.prCache.Get(w.Branch) != nil {
		return nil
	}
	r := m.cfg.Repos[sel.repoIdx]
	m.fetching = true
	cache := m.prCache
	branch := w.Branch
	repoPath := r.LocalPath
	return func() tea.Msg {
		info, err := github.LookupPR(repoPath, branch)
		if err != nil {
			return prBatchDoneMsg{ghErr: err}
		}
		cache.Set(branch, info)
		_ = cache.Save()
		return prBatchDoneMsg{}
	}
}
