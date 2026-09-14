package zellij

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/panamafrancis/workbench/pkg/config"
)

const (
	cmdTimeout  = 30 * time.Second
	cbThreshold = 3
	cbCooldown  = 60 * time.Second
)

var ErrCircuitOpen = errors.New("zellij: circuit breaker open (server unresponsive)")

var breaker struct {
	mu          sync.Mutex
	failures    int
	lastFailure time.Time
}

func IsInZellij() bool {
	return os.Getenv("ZELLIJ") != ""
}

func (w Workspace) OpenTab(name, cwd, sidebarWidth string, nonoArgs []string, envVars map[string]string) error {
	layoutPath, err := w.WriteTabLayout(name, cwd, sidebarWidth, nonoArgs, envVars)
	if err != nil {
		return err
	}
	_, stderr, err := runZellij("new-tab",
		"--name", name,
		"--cwd", cwd,
		"--layout", layoutPath,
	)
	if err != nil {
		if s := strings.TrimSpace(stderr); s != "" {
			return fmt.Errorf("zellij: %s", s)
		}
		return fmt.Errorf("zellij: %w", err)
	}
	return nil
}

func RenameTab(oldName, newName string) error {
	if err := GoToTab(oldName); err != nil {
		return err
	}
	_, stderr, err := runZellij("rename-tab", newName)
	if err != nil {
		if s := strings.TrimSpace(stderr); s != "" {
			return fmt.Errorf("zellij rename-tab: %s", s)
		}
		return fmt.Errorf("zellij rename-tab: %w", err)
	}
	return nil
}

func GoToTab(name string) error {
	_, stderr, err := runZellij("go-to-tab-name", name)
	if err != nil {
		if s := strings.TrimSpace(stderr); s != "" {
			return fmt.Errorf("zellij go-to-tab: %s", s)
		}
		return fmt.Errorf("zellij go-to-tab: %w", err)
	}
	return nil
}

func TabNames() (map[string]bool, error) {
	stdout, _, err := runZellij("query-tab-names")
	if err != nil {
		return nil, fmt.Errorf("zellij query-tab-names: %w", err)
	}
	names := make(map[string]bool)
	for _, line := range strings.Split(strings.TrimSpace(stdout), "\n") {
		if line != "" {
			names[line] = true
		}
	}
	return names, nil
}

func tabHasCommandPane(tabName string) bool {
	stdout, _, err := runZellij("dump-layout")
	if err != nil {
		return true
	}
	inTab := false
	for _, line := range strings.Split(stdout, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.Contains(trimmed, "tab name=\""+tabName+"\"") {
			inTab = true
			continue
		}
		if inTab && strings.HasPrefix(trimmed, "tab ") {
			break
		}
		if inTab && strings.Contains(trimmed, "command=") && !strings.Contains(trimmed, "name=\"sidebar\"") {
			return true
		}
	}
	return false
}

// TabIDs maps every tab name to its stable Zellij id. Ids are what let a tab be
// closed without focusing it first — and closing by focus is exactly what a
// sidebar must never do to its own tab (see OpenOrFocusTab).
func TabIDs() (map[string]int, error) {
	stdout, _, err := runZellij("list-tabs")
	if err != nil {
		return nil, fmt.Errorf("zellij list-tabs: %w", err)
	}
	return parseTabIDs(stdout), nil
}

// parseTabIDs reads "list-tabs" output: a "TAB_ID POSITION NAME" header row
// followed by one row per tab. Names may contain spaces, so only the first two
// fields are split off.
func parseTabIDs(out string) map[string]int {
	ids := make(map[string]int)
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}
		id, err := strconv.Atoi(fields[0])
		if err != nil {
			continue // the header row
		}
		rest := strings.TrimSpace(line)
		rest = strings.TrimSpace(strings.TrimPrefix(rest, fields[0]))
		rest = strings.TrimSpace(strings.TrimPrefix(rest, fields[1]))
		if rest != "" {
			ids[rest] = id
		}
	}
	return ids
}

func closeTabByID(id int) error {
	_, stderr, err := runZellij("close-tab-by-id", strconv.Itoa(id))
	if err != nil {
		if s := strings.TrimSpace(stderr); s != "" {
			return fmt.Errorf("zellij close-tab-by-id: %s", s)
		}
		return fmt.Errorf("zellij close-tab-by-id: %w", err)
	}
	return nil
}

// closeTab closes a tab by focusing it first. It is the fallback for when the
// tab's id cannot be resolved; prefer closeTabByID, because focusing is how a
// process ends up closing the tab it is itself running in.
func closeTab(name string) {
	if err := GoToTab(name); err != nil {
		return
	}
	_, _, _ = runZellij("close-tab")
}

func (w Workspace) OpenOrFocusTab(name, cwd, sidebarWidth string, nonoArgs []string, envVars map[string]string) (created bool, err error) {
	tabs, queryErr := TabNames()
	if queryErr != nil {
		if errors.Is(queryErr, ErrCircuitOpen) {
			return false, queryErr
		}
		err = w.OpenTab(name, cwd, sidebarWidth, nonoArgs, envVars)
		return err == nil, err
	}
	if tabs[name] {
		if tabHasCommandPane(name) {
			return false, GoToTab(name)
		}
		// The tab is still there but its agent has exited, so it has to be
		// replaced. Create the replacement FIRST and only then close the husk,
		// by id — the old order (close, then create) killed the caller whenever
		// it was the sidebar of that very tab: closing the tab kills every pane
		// in it, so the process died before it could create anything, and the
		// user was left in a neighbouring tab with nothing opened.
		//
		// Zellij tolerates the two same-named tabs that exist in between. If the
		// id cannot be resolved, fall back to closing by focus rather than
		// leaving a duplicate behind: a duplicate name makes every later tab
		// lookup ambiguous, which is worse.
		ids, idErr := TabIDs()
		staleID, haveID := ids[name]
		if idErr != nil || !haveID {
			closeTab(name)
			err = w.OpenTab(name, cwd, sidebarWidth, nonoArgs, envVars)
			return err == nil, err
		}
		if err = w.OpenTab(name, cwd, sidebarWidth, nonoArgs, envVars); err != nil {
			return false, err
		}
		// Best effort: the replacement is already up, and this may well be the
		// call that kills this process along with the husk.
		_ = closeTabByID(staleID)
		return true, nil
	}
	err = w.OpenTab(name, cwd, sidebarWidth, nonoArgs, envVars)
	return err == nil, err
}

func runZellij(actionArgs ...string) (stdout, stderr string, err error) {
	breaker.mu.Lock()
	if breaker.failures >= cbThreshold {
		if time.Since(breaker.lastFailure) < cbCooldown {
			breaker.mu.Unlock()
			return "", "", ErrCircuitOpen
		}
		breaker.failures = 0
	}
	breaker.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), cmdTimeout)
	defer cancel()

	args := append([]string{"action"}, actionArgs...)
	cmd := exec.CommandContext(ctx, "zellij", args...)
	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf

	err = cmd.Run()
	stdout = outBuf.String()
	stderr = errBuf.String()

	breaker.mu.Lock()
	if err != nil {
		breaker.failures++
		breaker.lastFailure = time.Now()
	} else {
		breaker.failures = 0
	}
	breaker.mu.Unlock()

	if err != nil {
		logFailure(args, stdout, stderr, err)
	}
	return stdout, stderr, err
}

func logFailure(args []string, stdout, stderr string, err error) {
	dir := filepath.Join(config.ConfigDir(), "logs")
	if mkErr := os.MkdirAll(dir, 0755); mkErr != nil {
		return
	}
	path := filepath.Join(dir, "zellij.log")
	f, fErr := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if fErr != nil {
		return
	}
	defer func() { _ = f.Close() }()

	ts := time.Now().Format(time.RFC3339)
	_, _ = fmt.Fprintf(f, "[%s] zellij %s\n", ts, strings.Join(args, " "))
	_, _ = fmt.Fprintf(f, "  error:  %v\n", err)
	if stderr != "" {
		_, _ = fmt.Fprintf(f, "  stderr: %s\n", strings.TrimSpace(stderr))
	}
	if stdout != "" {
		_, _ = fmt.Fprintf(f, "  stdout: %s\n", strings.TrimSpace(stdout))
	}
	_, _ = fmt.Fprintln(f)
}

// OpenOrFocusCommandTab focuses the tab named name if it is alive, and
// otherwise opens a new one running argv. It is the auxiliary-view counterpart
// to OpenOrFocusTab (which opens a worktree's sidebar+agent pair).
func (w Workspace) OpenOrFocusCommandTab(name string, argv []string) error {
	tabs, err := TabNames()
	if err != nil && !errors.Is(err, ErrCircuitOpen) {
		tabs = nil
	} else if err != nil {
		return err
	}
	if tabs[name] {
		if tabHasCommandPane(name) {
			return GoToTab(name)
		}
		// The tab outlived its command (the user quit the dashboard); replace it
		// rather than focusing an empty shell.
		closeTab(name)
	}
	layoutPath, err := w.WriteCommandTabLayout(name, argv)
	if err != nil {
		return err
	}
	_, stderr, err := runZellij("new-tab", "--name", name, "--layout", layoutPath)
	if err != nil {
		if s := strings.TrimSpace(stderr); s != "" {
			return fmt.Errorf("zellij: %s", s)
		}
		return fmt.Errorf("zellij: %w", err)
	}
	return nil
}
