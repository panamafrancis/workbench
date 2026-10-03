package supatree

import (
	"os"
	"path/filepath"
	"sort"

	"github.com/panamafrancis/workbench/pkg/config"
	"github.com/panamafrancis/workbench/pkg/sandbox"
	"github.com/panamafrancis/workbench/pkg/setup"
)

// AgentProfileName is the nono profile `supatree init` writes and the default
// models run under. Supatree's own, so no profile file has two writers.
const AgentProfileName = "supatree-agent"

// DefaultModels is the built-in model map with the claude entry pointed at
// supatree's own profile.
func DefaultModels() map[string]config.Model {
	models := config.DefaultModels()
	if m, ok := models[defaultModelKey]; ok {
		m.NonoProfile = AgentProfileName
		models[defaultModelKey] = m
	}
	return models
}

// AgentProfile is the static half of every supatree sandbox: what is the same
// for every launch. Everything specific to one tree is a per-launch flag
// (TreeGrants, MemberGrants, PMGrants), because a static file cannot know
// where a given tree, its stack or its clones live.
//
// It grants supatree's config read-only and the agent cache read-write, and
// deliberately nothing else of supatree's: not the state root (whose pm/
// queue the unsandboxed watcher executes), not the rest of the cache (whose
// layouts zellij runs unsandboxed).
func AgentProfile() setup.Profile {
	return setup.Profile{
		Name:        AgentProfileName,
		Description: "claude-code for supatree agents: toolchain and SSH agent; tree access is granted per launch",
		Extends:     []string{"claude-code"},
		Read:        []string{ConfigDir()},
		Allow:       []string{AgentCacheDir()},
	}.WithToolchain()
}

// TreeGrants is a root agent's per-launch reach: its own tree, its own state,
// and the git directories its commits land in — the stack's (the tree is a
// worktree of it) and each member's base clone (each member is a worktree of
// one). Only this tree's: a coding agent is never granted another tree's state
// or the whole repo cache.
//
// Grants are fixed when nono starts, so a member that sync adds later is not
// committable until the agent restarts.
func TreeGrants(c *Config, inst *Instance) sandbox.Grants {
	g := sandbox.Grants{Allow: []string{inst.Root, StateDir(inst.Root)}}
	if dir, err := GitCommonDir(inst.Root); err == nil {
		g.Allow = append(g.Allow, dir)
	}
	g.Allow = append(g.Allow, memberGitDirs(c, inst, "")...)
	g.Allow = dedupe(g.Allow)
	return g
}

// MemberGrants is a member-scoped agent's reach: that member's worktree and its
// base clone's git dir, the tree's state (mail, meta), and the tree root to
// read the spec and instructions.
func MemberGrants(c *Config, inst *Instance, alias string) sandbox.Grants {
	m := inst.FindMember(alias)
	if m == nil {
		return sandbox.Grants{}
	}
	g := sandbox.Grants{
		Allow: []string{m.Path, StateDir(inst.Root)},
		Read:  []string{inst.Root},
	}
	g.Allow = append(g.Allow, memberGitDirs(c, inst, alias)...)
	g.Allow = dedupe(g.Allow)
	return g
}

// memberGitDirs returns the git common dir of each member's base clone, or of
// just the one named by only.
func memberGitDirs(c *Config, inst *Instance, only string) []string {
	var out []string
	for _, m := range inst.Members {
		if only != "" && m.Alias != only {
			continue
		}
		if m.Exists {
			if dir, err := GitCommonDir(m.Path); err == nil {
				out = append(out, dir)
				continue
			}
		}
		if r, err := c.baseClone(m.Alias); err == nil {
			if dir, err := GitCommonDir(r.Clone); err == nil {
				out = append(out, dir)
			}
		}
	}
	return out
}

// EnsureLayout creates the directories every sandbox grant points at. nono
// refuses a grant on a path that does not exist, and a grant on a directory
// created later would not be the one granted.
func EnsureLayout() error {
	for _, dir := range []string{
		ConfigDir(), StateRoot(), CacheDir(), AgentCacheDir(),
		TreesStateDir(), PMDir(), LedgerDir(), OutboxDir(), RequestsDir(), LogsDir(),
	} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return err
		}
	}
	return nil
}

func dedupe(paths []string) []string {
	seen := make(map[string]bool, len(paths))
	out := paths[:0]
	for _, p := range paths {
		p = filepath.Clean(p)
		if seen[p] {
			continue
		}
		seen[p] = true
		out = append(out, p)
	}
	return out
}

// sortedKeys returns m's keys in order, for stable grant lists.
func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
