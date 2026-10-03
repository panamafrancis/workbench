// Package testutil holds helpers shared by tests across the module.
package testutil

import (
	"os"
	"path/filepath"
	"testing"
)

// IsolateHome points HOME and every XDG base directory at a fresh temp dir and
// returns it.
//
// Setting HOME alone is not isolation once paths come from XDG: a developer
// whose shell exports XDG_CONFIG_HOME would have tests write into their real
// ~/.config. So the four go together, always.
func IsolateHome(t testing.TB) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("XDG_STATE_HOME", filepath.Join(home, ".local", "state"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, ".cache"))
	return home
}

// IsolateProcess does what IsolateHome does for a whole test binary, from
// TestMain, so a test that forgets to isolate itself still cannot reach the
// real home. It returns a cleanup func.
func IsolateProcess() func() {
	home, err := os.MkdirTemp("", "testhome")
	if err != nil {
		panic(err)
	}
	for k, v := range map[string]string{
		"HOME":            home,
		"XDG_CONFIG_HOME": filepath.Join(home, ".config"),
		"XDG_STATE_HOME":  filepath.Join(home, ".local", "state"),
		"XDG_CACHE_HOME":  filepath.Join(home, ".cache"),
	} {
		if err := os.Setenv(k, v); err != nil {
			panic(err)
		}
	}
	return func() { _ = os.RemoveAll(home) }
}
