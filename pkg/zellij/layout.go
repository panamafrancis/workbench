package zellij

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/panamafrancis/workbench/pkg/config"
	"github.com/panamafrancis/workbench/pkg/git"
)

func WriteTabLayout(name, cwd, sidebarWidth string, nonoArgs []string, envVars map[string]string) (string, error) {
	// name is spliced into both a KDL pane name and the sidebar pane's bash -c
	// command, so reject anything outside the validated worktree-name charset
	// ([a-z0-9-]) before interpolation. This is fail-safe defense-in-depth:
	// callers already validate at creation, but a hand-edited config must not be
	// able to inject shell or break the layout here.
	if err := git.ValidateName(name, nil); err != nil {
		return "", fmt.Errorf("refusing to write layout for invalid worktree name %q: %w", name, err)
	}

	// The tab/layout identity stays the bare (validated) worktree name — it keys
	// tab lookups and the layout filename. The agent pane's *display* name gets a
	// "{repo}/{worktree}" label for context. The alias comes from config and is
	// not charset-validated, so it goes through quoteKDL to stay KDL-safe (it's
	// a KDL string here, not a shell command).
	displayName := name
	if alias := envVars["WORKBENCH_REPO_ALIAS"]; alias != "" {
		displayName = alias + "/" + name
	}

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
            args "-c" "WORKBENCH_WORKTREE_NAME=%s WORKBENCH_SIDEBAR=1 exec bash -c 'while true; do workbench ls && sleep 2 || sleep 5; done'"
        }
        pane name=%s cwd="%s" focus=true close_on_exit=true {
            %s
            %s
        }
    }
}
`, cwd, sidebarWidth, name, quoteKDL(displayName), cwd, agentCommand, agentArgs)

	path := filepath.Join(dir, name+".kdl")
	if err := os.WriteFile(path, []byte(kdl), 0644); err != nil {
		return "", fmt.Errorf("write layout: %w", err)
	}
	return path, nil
}

func quoteKDL(s string) string {
	// Escape backslashes before quotes so a value containing either (e.g. an
	// un-validated repo alias spliced into the pane display name) can't produce
	// a malformed KDL string that fails to parse and breaks the whole layout.
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	return `"` + s + `"`
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
