package supatree

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// Spec is the tracked repo-selection file (supatree.yml) at a stack/supatree
// root. It names the member repos (by workbench alias) and their dependency
// edges. Editing it and running sync reconciles the member worktrees.
type Spec struct {
	Members []string            `yaml:"members"`
	Deps    map[string][]string `yaml:"deps,omitempty"`
	Model   string              `yaml:"model,omitempty"`
}

// LoadSpec reads supatree.yml from a stack/supatree root.
func LoadSpec(root string) (*Spec, error) {
	path := filepath.Join(root, SpecName)
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", SpecName, err)
	}
	var s Spec
	if err := yaml.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("parse %s: %w", SpecName, err)
	}
	return &s, nil
}

// SaveSpec writes supatree.yml to a stack/supatree root atomically.
func SaveSpec(root string, s *Spec) error {
	data, err := yaml.Marshal(s)
	if err != nil {
		return fmt.Errorf("marshal %s: %w", SpecName, err)
	}
	path := filepath.Join(root, SpecName)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return fmt.Errorf("write %s: %w", SpecName, err)
	}
	return os.Rename(tmp, path)
}

// OrderedMembers returns the members in dependency (topological) order.
func (s *Spec) OrderedMembers() ([]string, error) {
	return TopoSort(s.Members, s.Deps)
}
