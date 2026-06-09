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
type tickMsg time.Time

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
}

func New(cfg *config.Config) *Model {
	cache := github.NewCache(config.PRCachePath())
	_ = cache.Load()

	t := newTree(cfg, cache)
	t.refreshDirty()
	return &Model{
		cfg:         cfg,
		tree:        t,
		prCache:     cache,
		ghAvailable: true,
		keys:        DefaultKeyMap,
		input:       textinput.New(),
	}
}

func (m *Model) Init() tea.Cmd {
	return tea.Batch(m.tickCmd(), m.fetchVisibleCmd(true))
}

func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height

	case tickMsg:
		var cmds []tea.Cmd
		cmds = append(cmds, m.tickCmd())
		if m.ghAvailable && !m.fetching {
			cmds = append(cmds, m.fetchStaleCmd())
		}
		return m, tea.Batch(cmds...)

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
		m.tree.refreshDirty()
		cmd := m.fetchVisibleCmd(true)
		if cmd != nil {
			m.msg = "refreshing..."
		} else {
			m.msg = "refreshed (sync in progress)"
		}
		return m, cmd

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
			m.tree.refreshDirty()
			m.msg = fmt.Sprintf("created worktree %q", msg.name)
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
			m.tree.refreshDirty()
			m.msg = fmt.Sprintf("deleted worktree %q", msg.name)
		}

	case tea.KeyMsg:
		if m.mode == modeHelp {
			m.mode = modeNormal
			return m, nil
		}
		if m.mode == modeConfirmDelete {
			return m.updateConfirmDelete(msg)
		}
		if m.mode != modeNormal {
			return m.updateInput(msg)
		}
		m.err = nil
		m.msg = ""
		switch {
		case key.Matches(msg, m.keys.Quit):
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
			if sel == nil || sel.isRepo {
				m.err = fmt.Errorf("select a worktree to delete")
				return m, nil
			}
			m.pendingRepoIdx = sel.repoIdx
			m.pendingWorktreeIdx = sel.worktreeIdx
			m.mode = modeConfirmDelete
		case key.Matches(msg, m.keys.OpenWith):
			sel := m.tree.selected()
			if sel == nil || sel.isRepo {
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

		if err := git.CreateWorktree(repo.LocalPath, wtPath, branch); err != nil {
			return createWorktreeMsg{err: err}
		}

		modelKey := cfg.ResolveModel("")
		repo.Worktrees = append(repo.Worktrees, config.Worktree{
			Name:   name,
			Branch: branch,
			Path:   wtPath,
			Model:  modelKey,
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
	if sel == nil || sel.isRepo {
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
		if err := repo.RunStartup(wt.Path, wt.Name); err != nil {
			return openErrMsg{fmt.Errorf("startup script: %w", err)}
		}
		nonoArgs, err := sandbox.BuildNonoArgs(wt.Path, modelKey, m.cfg)
		if err != nil {
			return openErrMsg{err}
		}
		if err := zellij.OpenTab(wt.Name, wt.Path, nonoArgs); err != nil {
			return openErrMsg{err}
		}
		return nil
	}
}

func (m *Model) View() string {
	var sb strings.Builder

	title := styleHeader.Render("workbench")
	hint := styleMuted.Render("[?] j/k=nav  enter=open  space=collapse  r=refresh  q=quit")
	header := lipgloss.JoinHorizontal(lipgloss.Top, title, "  ", hint)
	sb.WriteString(header)
	sb.WriteString("\n")
	sb.WriteString(styleMuted.Render(strings.Repeat("─", m.width)))
	sb.WriteString("\n")

	sb.WriteString(m.tree.view(m.width))

	sb.WriteString(styleMuted.Render(strings.Repeat("─", m.width)))
	sb.WriteString("\n")

	switch m.mode {
	case modeNewWorktree:
		repo := m.cfg.Repos[m.pendingRepoIdx]
		sb.WriteString(styleMuted.Render(fmt.Sprintf("new worktree [%s] name: ", repo.Alias)) + m.input.View())
	case modeAddRepoPath:
		sb.WriteString(styleMuted.Render("repo path: ") + m.input.View())
	case modeAddRepoAlias:
		sb.WriteString(styleMuted.Render("alias: ") + m.input.View())
	case modeConfirmDelete:
		wt := m.cfg.Repos[m.pendingRepoIdx].Worktrees[m.pendingWorktreeIdx]
		sb.WriteString(styleDirty.Render(fmt.Sprintf("delete worktree %q? [y/n]", wt.Name)))
	case modeOpenWith:
		models := make([]string, 0, len(m.cfg.Models))
		for k := range m.cfg.Models {
			models = append(models, k)
		}
		slices.Sort(models)
		sb.WriteString(styleMuted.Render(fmt.Sprintf("model (%s): ", strings.Join(models, "/"))) + m.input.View())
	case modeHelp:
		help := strings.Join([]string{
			"j/↑      up",
			"k/↓      down",
			"enter/o  open worktree",
			"O        open with model",
			"n        new worktree",
			"d        delete worktree",
			"space    expand/collapse",
			"A        add repo",
			"r        refresh",
			"?        this help",
			"q/esc    quit",
			"",
			styleMuted.Render("press any key to close"),
		}, "\n")
		sb.WriteString(help)
	default:
		sb.WriteString(styleSelected.Render(m.tree.breadcrumb()))
		sb.WriteString("\n")
		if m.err != nil {
			sb.WriteString(styleDirty.Render("error: " + m.err.Error()))
		} else if m.msg != "" {
			sb.WriteString(styleMuted.Render(m.msg))
		} else {
			status := "[n]ew  [o]pen  [d]el  [A]dd repo  [r]efresh  [q]uit"
			if m.fetching {
				status += "  ⟳ syncing..."
			} else if m.ghHint != "" {
				status += "  (" + m.ghHint + ")"
			}
			sb.WriteString(styleStatusBar.Render(status))
		}
	}

	return sb.String()
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
	if sel == nil || sel.isRepo {
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
