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

	var agentCommand, agentArgs string
	if len(envVars) > 0 {
		keys := make([]string, 0, len(envVars))
		for k := range envVars {
			keys = append(keys, k)
		}
		slices.Sort(keys)
		var allArgs []string
		for _, k := range keys {
			allArgs = append(allArgs, quoteKDL(k+"="+envVars[k]))
		}
		allArgs = append(allArgs, quoteKDL("nono"))
		for _, a := range nonoArgs {
			allArgs = append(allArgs, quoteKDL(a))
		}
		agentCommand = `command "env"`
		agentArgs = "args " + strings.Join(allArgs, " ")
	} else {
		var quotedArgs []string
		for _, a := range nonoArgs {
			quotedArgs = append(quotedArgs, quoteKDL(a))
		}
		agentCommand = `command "nono"`
		agentArgs = "args " + strings.Join(quotedArgs, " ")
	}

	kdl := fmt.Sprintf(`layout {
    cwd "%s"
    pane split_direction="vertical" {
        pane size="%s" name="sidebar" {
            command "bash"
            args "-c" "WORKBENCH_SIDEBAR=1 exec bash -c 'while true; do workbench ls && sleep 0.2 || sleep 2; done'"
        }
        pane name="%s" cwd="%s" focus=true close_on_exit=true {
            %s
            %s
        }
    }
}
`, cwd, sidebarWidth, name, cwd, agentCommand, agentArgs)

	path := filepath.Join(dir, name+".kdl")
	if err := os.WriteFile(path, []byte(kdl), 0644); err != nil {
		return "", fmt.Errorf("write layout: %w", err)
	}
	return path, nil
}

func quoteKDL(s string) string {
	return `"` + strings.ReplaceAll(s, `"`, `\"`) + `"`
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
