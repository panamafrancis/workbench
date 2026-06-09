package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/panamafrancis/workbench/pkg/config"
	"github.com/panamafrancis/workbench/pkg/git"
	"github.com/panamafrancis/workbench/pkg/sandbox"
	"github.com/panamafrancis/workbench/pkg/zellij"
)

type refreshMsg struct{}
type openErrMsg struct{ err error }
type createWorktreeMsg struct {
	name string
	err  error
}

type inputMode int

const (
	modeNormal       inputMode = iota
	modeAddRepoPath            // waiting for repo path
	modeAddRepoAlias           // waiting for alias
	modeNewWorktree            // waiting for worktree name (empty = auto)
)

type Model struct {
	cfg         *config.Config
	tree        TreeModel
	width       int
	height      int
	keys        KeyMap
	err         error
	msg         string
	mode           inputMode
	input          textinput.Model
	pendingPath    string
	pendingRepoIdx int
}

func New(cfg *config.Config) *Model {
	t := newTree(cfg)
	t.refreshDirty()
	return &Model{
		cfg:   cfg,
		tree:  t,
		keys:  DefaultKeyMap,
		input: textinput.New(),
	}
}

func (m *Model) Init() tea.Cmd {
	return nil
}

func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height

	case refreshMsg:
		newCfg, err := config.Load()
		if err != nil {
			m.err = err
		} else {
			m.cfg = newCfg
			m.tree.cfg = newCfg
		}
		m.tree.refreshDirty()
		m.msg = "refreshed"

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

	case tea.KeyMsg:
		if m.mode != modeNormal {
			return m.updateInput(msg)
		}
		m.err = nil
		m.msg = ""
		switch {
		case key.Matches(msg, m.keys.Quit):
			return m, tea.Quit
		case key.Matches(msg, m.keys.Up):
			m.tree.moveUp()
		case key.Matches(msg, m.keys.Down):
			m.tree.moveDown()
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
	default:
		sb.WriteString(styleSelected.Render(m.tree.breadcrumb()))
		sb.WriteString("\n")
		if m.err != nil {
			sb.WriteString(styleDirty.Render("error: " + m.err.Error()))
		} else if m.msg != "" {
			sb.WriteString(styleMuted.Render(m.msg))
		} else {
			sb.WriteString(styleStatusBar.Render("[n]ew  [o]pen  [d]el  [A]dd repo  [r]efresh  [q]uit"))
		}
	}

	return sb.String()
}
