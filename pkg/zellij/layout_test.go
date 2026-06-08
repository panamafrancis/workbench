package zellij

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteTabLayoutPath(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	path, err := WriteTabLayout("myworktree", "/wt/path", []string{"run", "--profile", "claude-code", "--allow", "/wt/path", "--", "claude"})
	if err != nil {
		t.Fatalf("WriteTabLayout() error = %v", err)
	}
	if filepath.Base(path) != "myworktree.kdl" {
		t.Errorf("layout path base = %q, want myworktree.kdl", filepath.Base(path))
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("layout file not found at %q: %v", path, err)
	}
}

func TestWriteTabLayoutContent(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	nonoArgs := []string{"run", "--profile", "claude-code", "--allow", "/wt/path", "--", "claude"}
	path, err := WriteTabLayout("atlanta", "/wt/path", nonoArgs)
	if err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	kdl := string(data)

	checks := []struct {
		desc    string
		contain string
	}{
		{"pane name", `pane name="atlanta"`},
		{"cwd", `cwd="/wt/path"`},
		{"command nono", `command "nono"`},
		{"profile arg", `"claude-code"`},
		{"binary arg", `"claude"`},
		{"separator arg", `"--"`},
	}
	for _, c := range checks {
		if !strings.Contains(kdl, c.contain) {
			t.Errorf("%s: %q not found in KDL:\n%s", c.desc, c.contain, kdl)
		}
	}
}

func TestWriteTabLayoutQuotesArgs(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	// Arg containing a double-quote must be escaped.
	path, err := WriteTabLayout("tab", "/wt", []string{`has"quote`})
	if err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), `"has\"quote"`) {
		t.Errorf("quote not escaped in KDL:\n%s", string(data))
	}
}

func TestWriteTabLayoutCreatesDir(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	path, err := WriteTabLayout("x", "/wt", []string{"nono"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Dir(path)); err != nil {
		t.Errorf("layouts dir not created: %v", err)
	}
}

func TestWriteTabLayoutOverwrites(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	_, err := WriteTabLayout("tab", "/old", []string{"old"})
	if err != nil {
		t.Fatal(err)
	}
	path, err := WriteTabLayout("tab", "/new", []string{"new"})
	if err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	if strings.Contains(string(data), "/old") {
		t.Error("second write should overwrite the first")
	}
	if !strings.Contains(string(data), "/new") {
		t.Error("second write did not write new cwd")
	}
}
