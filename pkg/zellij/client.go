package zellij

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/panamafrancis/workbench/pkg/config"
)

const cmdTimeout = 30 * time.Second

func IsInZellij() bool {
	return os.Getenv("ZELLIJ") != ""
}

func OpenTab(name, cwd, sidebarWidth string, nonoArgs []string) error {
	layoutPath, err := WriteTabLayout(name, cwd, sidebarWidth, nonoArgs)
	if err != nil {
		return err
	}
	_, stderr, err := runZellij("new-tab",
		"--name", name,
		"--cwd", cwd,
		"--layout", layoutPath,
	)
	if err != nil {
		return fmt.Errorf("zellij: %s", strings.TrimSpace(stderr))
	}
	return nil
}

func GoToTab(name string) error {
	_, stderr, err := runZellij("go-to-tab-name", name)
	if err != nil {
		return fmt.Errorf("zellij go-to-tab: %s", strings.TrimSpace(stderr))
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

func closeTab(name string) {
	if err := GoToTab(name); err != nil {
		return
	}
	_, _, _ = runZellij("close-tab")
}

func OpenOrFocusTab(name, cwd, sidebarWidth string, nonoArgs []string) (created bool, err error) {
	tabs, queryErr := TabNames()
	if queryErr != nil {
		err = OpenTab(name, cwd, sidebarWidth, nonoArgs)
		return err == nil, err
	}
	if tabs[name] {
		if tabHasCommandPane(name) {
			return false, GoToTab(name)
		}
		closeTab(name)
	}
	err = OpenTab(name, cwd, sidebarWidth, nonoArgs)
	return err == nil, err
}

func runZellij(actionArgs ...string) (stdout, stderr string, err error) {
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

	if err != nil {
		logFailure(args, stdout, stderr, err)
	}
	return
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
