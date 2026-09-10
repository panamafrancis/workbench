package config

import (
	"os"
	"path/filepath"
)

func ConfigDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".workbench")
}

func ConfigPath() string {
	return filepath.Join(ConfigDir(), "config.yml")
}

func DefaultWorktreeBase() string {
	return filepath.Join(ConfigDir(), "worktrees")
}

func LayoutsDir() string {
	return filepath.Join(ConfigDir(), "layouts")
}

func CacheDir() string {
	return filepath.Join(ConfigDir(), "cache")
}

func PRCachePath() string {
	return filepath.Join(CacheDir(), "pr-status.json")
}

func StatePath() string {
	return filepath.Join(ConfigDir(), "state.yml")
}

func LogsDir() string {
	return filepath.Join(ConfigDir(), "logs")
}

func WorktreePath(base, alias, name string) string {
	return filepath.Join(base, alias, name)
}
