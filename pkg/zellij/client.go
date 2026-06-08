package zellij

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

func IsInZellij() bool {
	return os.Getenv("ZELLIJ") != ""
}

func OpenTab(name, cwd string, nonoArgs []string) error {
	layoutPath, err := WriteTabLayout(name, cwd, nonoArgs)
	if err != nil {
		return err
	}
	cmd := exec.Command("zellij", "action", "new-tab",
		"--name", name,
		"--cwd", cwd,
		"--layout", layoutPath,
	)
	var errBuf bytes.Buffer
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("zellij: %s", strings.TrimSpace(errBuf.String()))
	}
	return nil
}

func GoToTab(name string) error {
	cmd := exec.Command("zellij", "action", "go-to-tab-name", name)
	var errBuf bytes.Buffer
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("zellij go-to-tab: %s", strings.TrimSpace(errBuf.String()))
	}
	return nil
}
