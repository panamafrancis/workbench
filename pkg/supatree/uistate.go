package supatree

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"

	"github.com/panamafrancis/workbench/pkg/config"
)

// UIStatePath is where the sidebar's fold state lives (~/.supatree/ui.yml).
func UIStatePath() string {
	return filepath.Join(Dir(), "ui.yml")
}

// UIStateLockPath serializes the read-modify-write cycles of the fold state
// across the independent sidebar processes (one per Zellij tab).
func UIStateLockPath() string {
	return UIStatePath() + ".lock"
}

// UIState is the sidebar fold state, shared by every sidebar process. One
// sidebar runs per Zellij tab, so keeping folds in memory made each tab render a
// different shape of the same list — folding a supatree in one tab left it open
// in all the others. Persisting them here, and re-reading on every reload, keeps
// the tabs in step.
//
// Both maps are keyed on supatree name and both default to their zero value: a
// supatree is expanded unless listed collapsed, and its repositories section is
// collapsed unless listed expanded. That way a name nobody has touched yet gets
// the intended default without an entry having to exist.
type UIState struct {
	CollapsedTrees map[string]bool `yaml:"collapsed_trees,omitempty"`
	ExpandedRepos  map[string]bool `yaml:"expanded_repos,omitempty"`
}

// NewUIState returns an empty (all-defaults) fold state.
func NewUIState() *UIState {
	return &UIState{CollapsedTrees: map[string]bool{}, ExpandedRepos: map[string]bool{}}
}

// LoadUIState reads the fold state, returning the defaults when the file is
// absent or unreadable. Fold state is a convenience, never worth failing a
// render over, so errors are swallowed deliberately.
func LoadUIState() *UIState {
	u := NewUIState()
	data, err := os.ReadFile(UIStatePath())
	if err != nil {
		return u
	}
	var on UIState
	if err := yaml.Unmarshal(data, &on); err != nil {
		return u
	}
	for k, v := range on.CollapsedTrees {
		u.CollapsedTrees[k] = v
	}
	for k, v := range on.ExpandedRepos {
		u.ExpandedRepos[k] = v
	}
	return u
}

// UpdateUIState applies mutate to the on-disk fold state under the lock and
// returns the result. Going back to disk rather than writing a snapshot held in
// memory means a fold made in another tab between two folds made here survives.
func UpdateUIState(mutate func(*UIState)) (*UIState, error) {
	out := NewUIState()
	err := config.WithFileLock(UIStateLockPath(), func() error {
		u := LoadUIState()
		mutate(u)
		out = u
		return u.save()
	})
	return out, err
}

// TreeCollapsed reports whether the named supatree is folded (agents and
// repositories hidden). Supatrees are expanded by default.
func (u *UIState) TreeCollapsed(tree string) bool {
	return u.CollapsedTrees[tree]
}

// ReposCollapsed reports whether the named supatree's repositories section is
// folded. Repositories are collapsed by default: the section's status summary
// is enough for most glances, and a dozen supatrees' members would otherwise
// bury the list.
func (u *UIState) ReposCollapsed(tree string) bool {
	return !u.ExpandedRepos[tree]
}

// SetTreeCollapsed folds or unfolds a supatree.
func (u *UIState) SetTreeCollapsed(tree string, collapsed bool) {
	u.CollapsedTrees[tree] = collapsed
}

// SetReposCollapsed folds or unfolds a supatree's repositories section.
func (u *UIState) SetReposCollapsed(tree string, collapsed bool) {
	u.ExpandedRepos[tree] = !collapsed
}

// save writes the fold state atomically (temp + rename). Entries that match the
// default are dropped first, so the file stays small and a supatree that is
// removed and recreated comes back with the default shape.
func (u *UIState) save() error {
	out := UIState{CollapsedTrees: map[string]bool{}, ExpandedRepos: map[string]bool{}}
	for k, v := range u.CollapsedTrees {
		if v {
			out.CollapsedTrees[k] = true
		}
	}
	for k, v := range u.ExpandedRepos {
		if v {
			out.ExpandedRepos[k] = true
		}
	}
	if err := os.MkdirAll(filepath.Dir(UIStatePath()), 0755); err != nil {
		return fmt.Errorf("create supatree dir: %w", err)
	}
	data, err := yaml.Marshal(out)
	if err != nil {
		return fmt.Errorf("marshal ui state: %w", err)
	}
	tmp := UIStatePath() + ".tmp"
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return fmt.Errorf("write ui state: %w", err)
	}
	return os.Rename(tmp, UIStatePath())
}
