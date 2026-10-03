package config

import (
	"os"
	"path/filepath"

	"github.com/panamafrancis/workbench/pkg/xdg"
)

// toolName names workbench's directory under each XDG base.
const toolName = "workbench"

// ConfigDir holds what a person edits: config.yml.
func ConfigDir() string {
	return filepath.Join(xdg.ConfigHome(), toolName)
}

// ConfigPath is workbench's config file.
func ConfigPath() string {
	return filepath.Join(ConfigDir(), "config.yml")
}

// StateDir holds what workbench accumulates: state.yml, logs and locks.
func StateDir() string {
	return filepath.Join(xdg.StateHome(), toolName)
}

// StatePath is the run state (stats, update check, reserved names).
func StatePath() string {
	return filepath.Join(StateDir(), "state.yml")
}

// LogsDir holds diagnostic logs.
func LogsDir() string {
	return filepath.Join(StateDir(), "logs")
}

// CacheDir holds what can be rebuilt: the PR status cache and layouts.
func CacheDir() string {
	return filepath.Join(xdg.CacheHome(), toolName)
}

// AgentCacheDir is the one part of the cache a sandboxed agent may write: the
// PR status cache its create_pr tool records into. Layouts, which zellij runs
// outside any sandbox, are deliberately elsewhere.
func AgentCacheDir() string {
	return filepath.Join(CacheDir(), "agent")
}

// PRCachePath is the per-branch PR status cache.
func PRCachePath() string {
	return filepath.Join(AgentCacheDir(), "pr-status.json")
}

// LayoutsDir holds generated Zellij layouts (transient).
func LayoutsDir() string {
	return filepath.Join(CacheDir(), "layouts")
}

// DefaultWorktreeBase is where worktrees are checked out unless worktree_base
// says otherwise. Visible, because people cd into it.
func DefaultWorktreeBase() string {
	return filepath.Join(xdg.Home(), toolName)
}

// LegacyDir is the pre-XDG home of everything (~/.workbench). Only the
// old-layout guard and `workbench migrate` look at it.
func LegacyDir() string {
	return filepath.Join(xdg.Home(), ".workbench")
}

// LegacyConfigPath is the pre-XDG config file.
func LegacyConfigPath() string {
	return filepath.Join(LegacyDir(), "config.yml")
}

func WorktreePath(base, alias, name string) string {
	return filepath.Join(base, alias, name)
}

// OldLayoutMessage is what every command says while the old layout is live.
const OldLayoutMessage = "workbench's files are still in ~/.workbench from an older version — remove every " +
	"worktree (workbench rm worktree), quit every wb-* zellij session, then run: workbench migrate"

// OldLayout reports whether ~/.workbench is still where workbench's files are:
// it exists and nothing has been migrated to the XDG config path yet.
func OldLayout() bool {
	if _, err := os.Stat(LegacyDir()); err != nil {
		return false
	}
	_, err := os.Stat(ConfigPath())
	return os.IsNotExist(err)
}
