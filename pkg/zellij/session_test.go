package zellij

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteSessionLayoutPath(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	path, err := WriteSessionLayout("wb-main", "15%")
	if err != nil {
		t.Fatalf("WriteSessionLayout() error = %v", err)
	}
	if filepath.Base(path) != "session-wb-main.kdl" {
		t.Errorf("layout path = %q, want session-wb-main.kdl", filepath.Base(path))
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("layout file not found: %v", err)
	}
}

func TestWriteSessionLayoutContent(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	path, err := WriteSessionLayout("wb-main", "20%")
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
		{"sidebar width", `size="20%"`},
		{"sidebar restart loop", `while true; do workbench ls && sleep 0.2 || sleep 2; done`},
		{"sidebar env", `WORKBENCH_SIDEBAR "1"`},
		{"shell pane", `name="shell"`},
	}
	for _, c := range checks {
		if !strings.Contains(kdl, c.contain) {
			t.Errorf("%s: %q not found in KDL:\n%s", c.desc, c.contain, kdl)
		}
	}
}

func TestSessionPrefix(t *testing.T) {
	if got := SessionPrefix(); got != "wb-" {
		t.Errorf("SessionPrefix() = %q, want %q", got, "wb-")
	}
}
