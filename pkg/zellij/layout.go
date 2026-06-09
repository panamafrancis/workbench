package zellij

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/panamafrancis/workbench/pkg/config"
)

func WriteTabLayout(name, cwd string, nonoArgs []string) (string, error) {
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
        pane size="15%%" name="sidebar" {
            command "workbench"
            args "ls"
        }
        pane name="%s" cwd="%s" focus=true {
            command "nono"
            args %s
        }
    }
}
`, name, cwd, strings.Join(quotedArgs, " "))

	path := filepath.Join(dir, name+".kdl")
	if err := os.WriteFile(path, []byte(kdl), 0644); err != nil {
		return "", fmt.Errorf("write layout: %w", err)
	}
	return path, nil
}
