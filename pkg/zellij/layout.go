package zellij

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/panamafrancis/workbench/pkg/git"
)

func (w Workspace) WriteTabLayout(name, cwd, sidebarWidth string, nonoArgs []string, envVars map[string]string) (string, error) {
	// name is spliced into both a KDL pane name and the sidebar pane's bash -c
	// command, so reject anything outside the validated worktree-name charset
	// ([a-z0-9-]) before interpolation. This is fail-safe defense-in-depth:
	// callers already validate at creation, but a hand-edited config must not be
	// able to inject shell or break the layout here. Supatree agent tabs use a
	// "<name>:<agent>" identity, so allow a single colon-separated suffix —
	// validating BOTH halves with the same charset (the suffix reaches the
	// sidebar bash command via SidebarActiveEnvVar, so it must be sanitized too).
	base, suffix, hasSuffix := strings.Cut(name, ":")
	if err := git.ValidateName(base, nil); err != nil {
		return "", fmt.Errorf("refusing to write layout for invalid tab name %q: %w", name, err)
	}
	if hasSuffix {
		if err := git.ValidateName(suffix, nil); err != nil {
			return "", fmt.Errorf("refusing to write layout for invalid tab name %q: %w", name, err)
		}
	}

	// The tab/layout identity stays the bare (validated) name — it keys tab
	// lookups and the layout filename. The agent pane's *display* name gets a
	// "{repo}/{worktree}" label for context. The alias comes from config and is
	// not charset-validated, so it goes through quoteKDL to stay KDL-safe (it's
	// a KDL string here, not a shell command).
	displayName := name
	if alias := envVars["WORKBENCH_REPO_ALIAS"]; alias != "" {
		displayName = alias + "/" + name
	}

	dir := w.LayoutsDir
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
            args "-c" "%s"
        }
        pane name=%s cwd="%s" focus=true close_on_exit=true {
            %s
            %s
        }
    }
}
`, cwd, sidebarWidth, w.sidebarBashArg(name), quoteKDL(displayName), cwd, agentCommand, agentArgs)

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

// sidebarBashArg builds the bash -c argument that runs the sidebar CLI in a
// restart loop (fast retry on clean exit, slow retry on failure). When
// activeName is non-empty and the workspace defines SidebarActiveEnvVar, that
// env var is injected so the sidebar can mark the current tab ("you are here").
func (w Workspace) sidebarBashArg(activeName string) string {
	prefix := ""
	if activeName != "" && w.SidebarActiveEnvVar != "" {
		prefix = w.SidebarActiveEnvVar + "=" + activeName + " "
	}
	return fmt.Sprintf(
		"%s%s=1 exec bash -c 'while true; do %s && sleep 2 || sleep 5; done'",
		prefix, w.SidebarEnvVar, w.SidebarCommand,
	)
}

func (w Workspace) CleanupLayout(name string) {
	path := filepath.Join(w.LayoutsDir, name+".kdl")
	_ = os.Remove(path)
}

func (w Workspace) CleanupStaleLayouts(validNames map[string]bool) {
	entries, err := os.ReadDir(w.LayoutsDir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".kdl") {
			continue
		}
		name := strings.TrimSuffix(e.Name(), ".kdl")
		if !validNames[name] {
			_ = os.Remove(filepath.Join(w.LayoutsDir, e.Name()))
		}
	}
}

// WriteCommandTabLayout writes a single-pane layout that runs argv in its own
// tab. The pane closes when the command exits — unlike the sidebar, an
// auxiliary view (the supatree dashboard) is something the user quits on
// purpose, so a restart loop would fight them.
func (w Workspace) WriteCommandTabLayout(name string, argv []string) (string, error) {
	// name is spliced into a KDL pane/tab identity and used as the layout
	// filename, so hold it to the same validated charset as a worktree name.
	if err := git.ValidateName(name, nil); err != nil {
		return "", fmt.Errorf("refusing to write layout for invalid tab name %q: %w", name, err)
	}
	if len(argv) == 0 {
		return "", fmt.Errorf("no command for tab %q", name)
	}
	if err := os.MkdirAll(w.LayoutsDir, 0755); err != nil {
		return "", fmt.Errorf("create layouts dir: %w", err)
	}

	args := ""
	if len(argv) > 1 {
		quoted := make([]string, 0, len(argv)-1)
		for _, a := range argv[1:] {
			quoted = append(quoted, quoteKDL(a))
		}
		args = "\n            args " + strings.Join(quoted, " ")
	}
	kdl := fmt.Sprintf(`layout {
    tab name=%s focus=true {
        pane name=%s focus=true close_on_exit=true {
            command %s%s
        }
    }
}
`, quoteKDL(name), quoteKDL(name), quoteKDL(argv[0]), args)

	path := filepath.Join(w.LayoutsDir, name+".kdl")
	if err := os.WriteFile(path, []byte(kdl), 0644); err != nil {
		return "", fmt.Errorf("write layout: %w", err)
	}
	return path, nil
}
