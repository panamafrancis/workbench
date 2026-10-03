package supatree

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"gopkg.in/yaml.v3"

	"github.com/panamafrancis/workbench/pkg/git"
)

// Spec is the tracked repo-selection file (supatree.yml) at a stack/supatree
// root. It names each member by a stack-local alias and the git URL it is
// cloned from, plus their dependency edges — everything a teammate needs to
// use the stack, and nothing personal: no local paths, no ssh aliases, no
// hooks. Editing it and running sync reconciles the member worktrees.
//
//	members:
//	  keystone: git@github.com:fraud-zero/keystone.git
//	  admin-frontend: git@github.com:fraud-zero/admin-frontend.git
//	deps:
//	  admin-frontend: [keystone]
//
// The alias names repos/<alias>/ in a tree and the st/<slug>/<alias> branch;
// two stacks may alias the same repository differently.
type Spec struct {
	Members map[string]string   `yaml:"members"`
	Deps    map[string][]string `yaml:"deps,omitempty"`
	Model   string              `yaml:"model,omitempty"`
}

// ErrOldSpec is what reading a supatree.yml written in the old format says: it
// listed members by workbench alias, and supatree no longer reads workbench's
// config to resolve them.
var ErrOldSpec = errors.New("supatree.yml lists members by alias, the format before members carried their git URL — run: supatree migrate")

// LoadSpec reads supatree.yml from a stack/supatree root.
func LoadSpec(root string) (*Spec, error) {
	path := filepath.Join(root, SpecName)
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", SpecName, err)
	}
	return parseSpec(data)
}

func parseSpec(data []byte) (*Spec, error) {
	var probe struct {
		Members yaml.Node `yaml:"members"`
	}
	if err := yaml.Unmarshal(data, &probe); err != nil {
		return nil, fmt.Errorf("parse %s: %w", SpecName, err)
	}
	if probe.Members.Kind == yaml.SequenceNode {
		return nil, ErrOldSpec
	}
	var s Spec
	if err := yaml.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("parse %s: %w", SpecName, err)
	}
	// Aliases become paths (repos/<alias>) that unsandboxed processes create
	// and delete, and the spec is writable from inside the tree — so an alias
	// is held to the same charset as every other name supatree turns into a
	// path, and a "../" can never reach a RemoveAll.
	for alias, url := range s.Members {
		if err := git.ValidateName(alias, nil); err != nil {
			return nil, fmt.Errorf("%s: member alias %q: %w", SpecName, alias, err)
		}
		if url == "" {
			return nil, fmt.Errorf("%s: member %q has no git URL", SpecName, alias)
		}
	}
	for from, tos := range s.Deps {
		for _, a := range append([]string{from}, tos...) {
			if err := git.ValidateName(a, nil); err != nil {
				return nil, fmt.Errorf("%s: deps: %q: %w", SpecName, a, err)
			}
		}
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

// Aliases returns the member aliases, sorted.
func (s *Spec) Aliases() []string {
	out := make([]string, 0, len(s.Members))
	for a := range s.Members {
		out = append(out, a)
	}
	sort.Strings(out)
	return out
}

// OrderedMembers returns the members in dependency (topological) order.
func (s *Spec) OrderedMembers() ([]string, error) {
	return TopoSort(s.Aliases(), s.Deps)
}
