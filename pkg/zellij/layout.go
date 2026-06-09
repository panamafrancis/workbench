package zellij

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/panamafrancis/workbench/pkg/config"
)

func WriteTabLayout(name, cwd, sidebarWidth string, nonoArgs []string) (string, error) {
	dir := config.LayoutsDir()
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", fmt.Errorf("create layouts dir: %w", err)
	}

	// Build KDL args list: "arg1" "arg2" ...
	quotedArgs := make([]string, len(nonoArgs))
	for i, a := range nonoArgs {
		quotedArgs[i] = `"` + strings.ReplaceAll(a, `"`, `\"`) + `"`
	}

	kdl := fmt.Sprintf(`layout {
    pane split_direction="vertical" {
        pane size="%s" name="sidebar" {
            command "workbench"
            args "ls"
        }
        pane name="%s" cwd="%s" focus=true close_on_exit=true {
            command "nono"
            args %s
        }
    }
}
`, sidebarWidth, name, cwd, strings.Join(quotedArgs, " "))

	path := filepath.Join(dir, name+".kdl")
	if err := os.WriteFile(path, []byte(kdl), 0644); err != nil {
		return "", fmt.Errorf("write layout: %w", err)
	}
	return path, nil
}

func CleanupLayout(name string) {
	path := filepath.Join(config.LayoutsDir(), name+".kdl")
	os.Remove(path)
}

func CleanupStaleLayouts(validNames map[string]bool) {
	entries, err := os.ReadDir(config.LayoutsDir())
	if err != nil {
		return
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".kdl") {
			continue
		}
		name := strings.TrimSuffix(e.Name(), ".kdl")
		if !validNames[name] {
			os.Remove(filepath.Join(config.LayoutsDir(), e.Name()))
		}
	}
}
