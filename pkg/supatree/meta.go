package supatree

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"gopkg.in/yaml.v3"
)

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
}

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

// MemberBranch returns the branch name for a member repo: st/<slug>/<alias>.
func (m *Meta) MemberBranch(alias string) string {
	return fmt.Sprintf("st/%s/%s", m.Slug, alias)
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
