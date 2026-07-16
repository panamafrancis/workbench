// Package supatree manages sets of git worktrees — one per repo — for a single
// cross-repo issue. A "stack" is a git repo holding the repo selection
// (supatree.yml), agent instructions (AGENTS.md) and scripts; a supatree is a
// worktree of that stack repo, laid out as ~/.supatree/trees/<name>/ with each
// member repo checked out under repos/<alias>/. Repo definitions (alias →
// local_path, copy_files, scripts) are resolved from workbench's config.
package supatree

import (
	"os"
	"path/filepath"
)

// Dir is the root of all supatree state (~/.supatree).
func Dir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".supatree")
}

// ConfigPath is the registry file listing stacks and defaults.
func ConfigPath() string {
	return filepath.Join(Dir(), "config.yml")
}

// DefaultTreesBase is where supatrees are checked out unless overridden.
func DefaultTreesBase() string {
	return filepath.Join(Dir(), "trees")
}

// StacksDir is where scaffolded stack repos live by default.
func StacksDir() string {
	return filepath.Join(Dir(), "stacks")
}

// DefaultStackPath is the default location of a stack repo named name.
func DefaultStackPath(name string) string {
	return filepath.Join(StacksDir(), name)
}

// LayoutsDir holds generated Zellij layouts (transient).
func LayoutsDir() string {
	return filepath.Join(Dir(), "layouts")
}

// LogsDir holds diagnostic logs.
func LogsDir() string {
	return filepath.Join(Dir(), "logs")
}

// CacheDir holds transient caches (PR status).
func CacheDir() string {
	return filepath.Join(Dir(), "cache")
}

// PRCachePath is the per-branch PR status cache shared by CLI and sidebar.
func PRCachePath() string {
	return filepath.Join(CacheDir(), "pr-status.json")
}

// LockPath is the advisory lock file serializing registry mutations.
func LockPath() string {
	return ConfigPath() + ".lock"
}

// stateDirName is the gitignored per-supatree state directory inside a tree.
const stateDirName = ".supatree"

// StateDir returns the state directory for a supatree rooted at root.
func StateDir(root string) string {
	return filepath.Join(root, stateDirName)
}

// MetaPath returns the per-supatree metadata file inside a tree root.
func MetaPath(root string) string {
	return filepath.Join(StateDir(root), "meta.yml")
}

// AgentsPath returns the per-supatree agents file inside a tree root.
func AgentsPath(root string) string {
	return filepath.Join(StateDir(root), "agents.yml")
}

// InfoPath returns the generated info file inside a tree root.
func InfoPath(root string) string {
	return filepath.Join(StateDir(root), "info.md")
}

// SpecName is the tracked repo-selection file at a stack/supatree root.
const SpecName = "supatree.yml"

// AgentsMDName is the tracked agent-instructions file at a stack/supatree root.
const AgentsMDName = "AGENTS.md"

// ReposDirName is the gitignored directory holding member worktrees.
const ReposDirName = "repos"

// MemberPath returns the on-disk path of a member repo worktree within a tree.
func MemberPath(root, alias string) string {
	return filepath.Join(root, ReposDirName, alias)
}
