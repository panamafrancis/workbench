package xdg

import (
	"path/filepath"
	"testing"
)

func TestDefaultsUnderHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("XDG_STATE_HOME", "")
	t.Setenv("XDG_CACHE_HOME", "")
	for got, want := range map[string]string{
		ConfigHome(): filepath.Join(home, ".config"),
		StateHome():  filepath.Join(home, ".local", "state"),
		CacheHome():  filepath.Join(home, ".cache"),
	} {
		if got != want {
			t.Errorf("got %s, want %s", got, want)
		}
	}
}

func TestEnvWinsWhenAbsolute(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", "/x/config")
	if got := ConfigHome(); got != "/x/config" {
		t.Errorf("ConfigHome = %s, want the env value", got)
	}
	// A relative value is invalid per the spec and ignored.
	t.Setenv("XDG_CONFIG_HOME", "rel/config")
	if got := ConfigHome(); got != filepath.Join(Home(), ".config") {
		t.Errorf("ConfigHome = %s, want the default for a relative value", got)
	}
}
