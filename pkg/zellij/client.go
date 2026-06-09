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

func OpenTab(name, cwd string, nonoArgs []string) error {
	layoutPath, err := WriteTabLayout(name, cwd, nonoArgs)
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

func OpenOrFocusTab(name, cwd string, nonoArgs []string) (created bool, err error) {
	tabs, queryErr := TabNames()
	if queryErr != nil {
		err = OpenTab(name, cwd, nonoArgs)
		return err == nil, err
	}
	if tabs[name] {
		return false, GoToTab(name)
	}
	err = OpenTab(name, cwd, nonoArgs)
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
	defer f.Close()

	ts := time.Now().Format(time.RFC3339)
	fmt.Fprintf(f, "[%s] zellij %s\n", ts, strings.Join(args, " "))
	fmt.Fprintf(f, "  error:  %v\n", err)
	if stderr != "" {
		fmt.Fprintf(f, "  stderr: %s\n", strings.TrimSpace(stderr))
	}
	if stdout != "" {
		fmt.Fprintf(f, "  stdout: %s\n", strings.TrimSpace(stdout))
	}
	fmt.Fprintln(f)
}
