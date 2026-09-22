package supatree

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

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
	// SeenEvents records when you last looked at each supatree, which is what
	// makes the sidebar's attention marker mean "unacted" rather than merely
	// "recent". It lives here rather than in the event ledger because it is a
	// property of the reader, not of the history.
	SeenEvents map[string]time.Time `yaml:"seen_events,omitempty"`
}

// NewUIState returns an empty (all-defaults) fold state.
func NewUIState() *UIState {
	return &UIState{
		CollapsedTrees: map[string]bool{},
		ExpandedRepos:  map[string]bool{},
		SeenEvents:     map[string]time.Time{},
	}
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
	for k, v := range on.SeenEvents {
		u.SeenEvents[k] = v
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

// MarkSeen records that you have just looked at a supatree, clearing its
// attention marker. Called when a tree's tab is opened or focused — a discrete
// action, deliberately not every render, since one sidebar runs per Zellij tab
// and a per-render write would have them all contending for the lock.
func MarkSeen(tree string, at time.Time) {
	_, _ = UpdateUIState(func(u *UIState) {
		if u.SeenEvents == nil {
			u.SeenEvents = map[string]time.Time{}
		}
		u.SeenEvents[tree] = at
	})
}

// Attention reports, per supatree, whether the ledger holds an event worth your
// attention that postdates the last time you looked at that tree.
//
// Only board- and desktop-tier events count: a glyph-tier event is already
// visible in the row it changed, and marking the tree for it would leave the
// marker permanently on.
func Attention(evs []Event, u *UIState) map[string]bool {
	out := make(map[string]bool)
	for _, ev := range evs {
		switch TierOf(ev.Kind) {
		case TierBoard, TierDesktop:
		case TierGlyph, TierNever:
			continue
		}
		if seen, ok := u.SeenEvents[ev.Tree]; ok && !ev.At.After(seen) {
			continue
		}
		out[ev.Tree] = true
	}
	return out
}
