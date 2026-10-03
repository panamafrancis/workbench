// Package xdg resolves the XDG base directories every tool in this module
// keeps its files under. It follows the spec on every platform, macOS
// included, the way gh and git do: the environment variable when it is set to
// an absolute path, else the spec's default under $HOME. It deliberately does
// not use os.UserConfigDir, which answers ~/Library/Application Support on
// darwin.
package xdg

import (
	"os"
	"path/filepath"
)

// ConfigHome is $XDG_CONFIG_HOME, default ~/.config: what a person edits.
func ConfigHome() string { return resolve("XDG_CONFIG_HOME", ".config") }

// StateHome is $XDG_STATE_HOME, default ~/.local/state: what a program
// accumulates and would rather not lose.
func StateHome() string { return resolve("XDG_STATE_HOME", filepath.Join(".local", "state")) }

// CacheHome is $XDG_CACHE_HOME, default ~/.cache: what can be rebuilt.
func CacheHome() string { return resolve("XDG_CACHE_HOME", ".cache") }

// Home is the user's home directory, "" if it cannot be determined.
func Home() string {
	home, _ := os.UserHomeDir()
	return home
}

func resolve(env, fallback string) string {
	// The spec says a relative value is invalid and must be ignored.
	if v := os.Getenv(env); v != "" && filepath.IsAbs(v) {
		return v
	}
	return filepath.Join(Home(), fallback)
}
