package supatree

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"gopkg.in/yaml.v3"
)

// Mode is a supatree's lifecycle: authoring your own cross-repo change, or
// reviewing someone else's. The zero value is authoring, so every meta.yml
// written before this field existed keeps its meaning.
type Mode string

const (
	// ModeAuthoring is the original flow: branch, commit, rename, open PRs.
	ModeAuthoring Mode = ""
	// ModeReviewing tracks foreign PRs read-only. The authoring commands are
	// refused in this mode because the branches belong to someone else.
	ModeReviewing Mode = "reviewing"
)

// ReviewRef is the pull request one member of a review tree is checked out at.
//
// It exists because a review tree cannot be keyed on the branch slug the way an
// authoring tree is: the branch is the author's, the PR is the fact. Recording
// Head is what makes "the author pushed since you looked" detectable rather
// than something you discover by accident.
type ReviewRef struct {
	Repo    string `yaml:"repo"`     // owner/name, e.g. fraud-zero/keystone-api
	Number  int    `yaml:"number"`   // PR number
	URL     string `yaml:"url"`      // canonical PR URL
	Head    string `yaml:"head"`     // head SHA checked out
	HeadRef string `yaml:"head_ref"` // the author's branch name, for display
	Base    string `yaml:"base"`     // the PR's base branch
}

// Meta is the gitignored per-supatree metadata (.supatree/meta.yml). It records
// the facts about a supatree that are not derivable from the tracked files or
// git: which stack it belongs to, the branch slug for member branches, the
// default model, and when it was created.
type Meta struct {
	Name      string    `yaml:"name"`
	Slug      string    `yaml:"slug"`
	Stack     string    `yaml:"stack"`
	Model     string    `yaml:"model"`
	CreatedAt time.Time `yaml:"created_at"`
	// Autonomy is how much the PM may do here unasked ("off", "nudge", "auto").
	// Empty inherits the workspace default.
	Autonomy string `yaml:"autonomy,omitempty"`
	// Outward allows actions a third party sees. A pointer so "unset" is
	// distinguishable from "explicitly false" and can inherit the default.
	Outward *bool `yaml:"outward,omitempty"`
	// Intent is what this supatree was created for — the issue, ticket or
	// prompt. Captured at creation because the rename flow deliberately
	// discards the city name for a slug, so without it nothing three weeks
	// later says what "canberra" was *for*.
	Intent string `yaml:"intent,omitempty"`
	// Mode is authoring (the zero value) or reviewing.
	Mode Mode `yaml:"mode,omitempty"`
	// Review maps a member alias to the pull request it is checked out at.
	// Only set in ModeReviewing.
	Review map[string]ReviewRef `yaml:"review,omitempty"`
	// Base maps a member alias to the branch its pull request should target.
	// Empty means the repository's default branch, which is the ordinary case;
	// a tree forked from a review targets the author's branch instead, so the
	// change is proposed *to them* rather than opened as a rival PR.
	Base map[string]string `yaml:"base,omitempty"`
}

// MemberBase returns the branch a member's pull request should target, or ""
// for the repository default.
func (m *Meta) MemberBase(alias string) string { return m.Base[alias] }

// Reviewing reports whether this is a review tree.
func (m *Meta) Reviewing() bool { return m.Mode == ModeReviewing }

// LoadMeta reads .supatree/meta.yml from a supatree root.
func LoadMeta(root string) (*Meta, error) {
	data, err := os.ReadFile(MetaPath(root))
	if err != nil {
		return nil, fmt.Errorf("read meta: %w", err)
	}
	var m Meta
	if err := yaml.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("parse meta: %w", err)
	}
	return &m, nil
}

// Save writes .supatree/meta.yml atomically, creating the state dir.
func (m *Meta) Save(root string) error {
	if err := os.MkdirAll(StateDir(root), 0755); err != nil {
		return fmt.Errorf("create state dir: %w", err)
	}
	data, err := yaml.Marshal(m)
	if err != nil {
		return fmt.Errorf("marshal meta: %w", err)
	}
	path := MetaPath(root)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return fmt.Errorf("write meta: %w", err)
	}
	return os.Rename(tmp, path)
}

// MemberBranch returns the branch name for a member repo.
//
// Review trees use their own prefix. The author's branch name cannot be used —
// members share one clone across many worktrees and git refuses a branch
// already checked out elsewhere — and a detached HEAD would make
// rev-parse --abbrev-ref report the literal "HEAD", which every surface that
// reads a branch name would then believe. A tree-local ref is also
// unambiguously supatree's to delete, which is what keeps teardown from
// touching a branch the author owns.
func (m *Meta) MemberBranch(alias string) string {
	return fmt.Sprintf("%s/%s/%s", m.branchPrefix(), m.Slug, alias)
}

// branchPrefix is the first segment of every member branch in this tree. It is
// also what the teardown guard matches on, so a member sitting on anything else
// is known to be foreign.
func (m *Meta) branchPrefix() string {
	if m.Reviewing() {
		return "review"
	}
	return "st"
}

// isSupatreeRoot reports whether dir looks like a supatree (has meta.yml).
func isSupatreeRoot(dir string) bool {
	_, err := os.Stat(MetaPath(dir))
	return err == nil
}

// treeRoot joins the trees base and a supatree name.
func treeRoot(base, name string) string {
	return filepath.Join(base, name)
}
