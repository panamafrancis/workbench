package supatree

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/panamafrancis/workbench/pkg/config"
)

// Instance is a live supatree: a worktree of a stack repo with its member repo
// worktrees checked out underneath. It is reconstructed from the tracked spec,
// the gitignored meta file, and workbench's repo definitions.
type Instance struct {
	Name    string   // city name; the dir under the trees base and the st/<name> branch
	Slug    string   // branch slug for members: st/<slug>/<alias>
	Stack   string   // stack alias this supatree was created from
	Root    string   // absolute path to the tree root
	Model   string   // default model for agents
	Members []Member // in dependency order
}

// Member is one repo participating in a supatree.
type Member struct {
	Alias     string   // workbench repo alias
	Path      string   // <root>/repos/<alias>
	Branch    string   // st/<slug>/<alias>
	DependsOn []string // in-set dependency aliases
	Exists    bool     // whether the member worktree is checked out on disk
}

// LoadInstance reconstructs the supatree rooted at root from its meta + spec
// files. Members are derived purely from the spec so a supatree lists even when
// a repo alias has gone missing from workbench's config.
func LoadInstance(root string) (*Instance, error) {
	meta, err := LoadMeta(root)
	if err != nil {
		return nil, err
	}
	spec, err := LoadSpec(root)
	if err != nil {
		return nil, err
	}
	ordered, err := spec.OrderedMembers()
	if err != nil {
		return nil, err
	}

	inSet := make(map[string]bool, len(ordered))
	for _, a := range ordered {
		inSet[a] = true
	}

	members := make([]Member, 0, len(ordered))
	for _, alias := range ordered {
		var deps []string
		for _, d := range spec.Deps[alias] {
			if inSet[d] && d != alias {
				deps = append(deps, d)
			}
		}
		sort.Strings(deps)
		path := MemberPath(root, alias)
		_, statErr := os.Stat(path)
		members = append(members, Member{
			Alias:     alias,
			Path:      path,
			Branch:    meta.MemberBranch(alias),
			DependsOn: deps,
			Exists:    statErr == nil,
		})
	}

	return &Instance{
		Name:    meta.Name,
		Slug:    meta.Slug,
		Stack:   meta.Stack,
		Root:    root,
		Model:   meta.Model,
		Members: members,
	}, nil
}

// List discovers every supatree under the configured trees base.
func List(c *Config) ([]*Instance, error) {
	base := c.ResolveTreesBase()
	entries, err := os.ReadDir(base)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read trees base: %w", err)
	}
	var out []*Instance
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		root := treeRoot(base, e.Name())
		if !isSupatreeRoot(root) {
			continue
		}
		inst, err := LoadInstance(root)
		if err != nil {
			// Skip trees we cannot parse rather than failing the whole listing.
			continue
		}
		out = append(out, inst)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// Get returns the named supatree, or an error if it does not exist.
func Get(c *Config, name string) (*Instance, error) {
	root := treeRoot(c.ResolveTreesBase(), name)
	if !isSupatreeRoot(root) {
		return nil, fmt.Errorf("supatree %q not found", name)
	}
	return LoadInstance(root)
}

// Names returns the names of all existing supatrees (best effort).
func Names(c *Config) []string {
	insts, _ := List(c)
	names := make([]string, 0, len(insts))
	for _, i := range insts {
		names = append(names, i.Name)
	}
	return names
}

// MemberAliases returns member aliases in dependency order.
func (inst *Instance) MemberAliases() []string {
	out := make([]string, len(inst.Members))
	for i, m := range inst.Members {
		out[i] = m.Alias
	}
	return out
}

// FindMember returns the member with the given alias, or nil.
func (inst *Instance) FindMember(alias string) *Member {
	for i := range inst.Members {
		if inst.Members[i].Alias == alias {
			return &inst.Members[i]
		}
	}
	return nil
}

// AgentEnv returns the environment variables injected into an agent's pane so
// the agent knows it is inside a supatree and can locate its siblings.
func (inst *Instance) AgentEnv(agentName string) map[string]string {
	return map[string]string{
		"SUPATREE":             "1",
		"SUPATREE_NAME":        inst.Name,
		"SUPATREE_ROOT":        inst.Root,
		"SUPATREE_MEMBERS":     strings.Join(inst.MemberAliases(), ","),
		"SUPATREE_BRANCH_SLUG": inst.Slug,
		"SUPATREE_AGENT":       agentName,
	}
}

// resolveRepos maps member aliases to their workbench repo definitions,
// erroring if any alias is not registered with workbench.
func resolveRepos(aliases []string, wb *config.Config) (map[string]*config.Repo, error) {
	repos := make(map[string]*config.Repo, len(aliases))
	for _, alias := range aliases {
		r, _ := wb.FindRepo(alias)
		if r == nil {
			return nil, fmt.Errorf("repo %q is not registered with workbench — run: workbench add repo <path> --alias=%s", alias, alias)
		}
		repos[alias] = r
	}
	return repos, nil
}
