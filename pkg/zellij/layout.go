package zellij

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/panamafrancis/workbench/pkg/config"
)

func WriteTabLayout(name, cwd, sidebarWidth string, nonoArgs []string, envVars map[string]string) (string, error) {
	dir := config.LayoutsDir()
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", fmt.Errorf("create layouts dir: %w", err)
	}

	quotedArgs := make([]string, len(nonoArgs))
	for i, a := range nonoArgs {
		quotedArgs[i] = `"` + strings.ReplaceAll(a, `"`, `\"`) + `"`
	}

	var envBlock string
	if len(envVars) > 0 {
		keys := make([]string, 0, len(envVars))
		for k := range envVars {
			keys = append(keys, k)
		}
		slices.Sort(keys)
		var lines []string
		for _, k := range keys {
			lines = append(lines, fmt.Sprintf("                %s %q", k, envVars[k]))
		}
		envBlock = fmt.Sprintf("\n            env {\n%s\n            }", strings.Join(lines, "\n"))
	}

	kdl := fmt.Sprintf(`layout {
    pane split_direction="vertical" {
        pane size="%s" name="sidebar" {
            command "bash"
            args "-c" "while true; do workbench ls && sleep 0.2 || sleep 2; done"
            env {
                WORKBENCH_SIDEBAR "1"
            }
        }
        pane name="%s" cwd="%s" focus=true close_on_exit=true {
            command "nono"
            args %s%s
        }
    }
}
`, sidebarWidth, name, cwd, strings.Join(quotedArgs, " "), envBlock)

	path := filepath.Join(dir, name+".kdl")
	if err := os.WriteFile(path, []byte(kdl), 0644); err != nil {
		return "", fmt.Errorf("write layout: %w", err)
	}
	return path, nil
}

func CleanupLayout(name string) {
	path := filepath.Join(config.LayoutsDir(), name+".kdl")
	_ = os.Remove(path)
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
			_ = os.Remove(filepath.Join(config.LayoutsDir(), e.Name()))
		}
	}
}
