package zellij

import (
	"bytes"
	"context"
	_ "embed"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"text/template"

	"github.com/panamafrancis/workbench/pkg/config"
)

//go:embed session.kdl.tmpl
var sessionLayoutTmpl string

type SessionInfo struct {
	Name   string
	Exited bool
}

func ListSessions() ([]SessionInfo, error) {
	ctx, cancel := context.WithTimeout(context.Background(), cmdTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "zellij", "list-sessions")
	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		if outBuf.Len() == 0 && errBuf.Len() == 0 {
			return nil, nil
		}
		return nil, fmt.Errorf("zellij list-sessions: %s", strings.TrimSpace(errBuf.String()))
	}
	var sessions []SessionInfo
	for _, line := range strings.Split(strings.TrimSpace(outBuf.String()), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		info := SessionInfo{Name: line}
		if strings.Contains(line, "EXITED") {
			parts := strings.Fields(line)
			if len(parts) > 0 {
				info.Name = parts[0]
			}
			info.Exited = true
		} else {
			parts := strings.Fields(line)
			if len(parts) > 0 {
				info.Name = parts[0]
			}
		}
		sessions = append(sessions, info)
	}
	return sessions, nil
}

func WriteSessionLayout(name, sidebarWidth string) (string, error) {
	dir := config.LayoutsDir()
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", fmt.Errorf("create layouts dir: %w", err)
	}
	tmpl, err := template.New("session").Parse(sessionLayoutTmpl)
	if err != nil {
		return "", fmt.Errorf("parse session template: %w", err)
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, struct{ SidebarWidth string }{sidebarWidth}); err != nil {
		return "", fmt.Errorf("render session template: %w", err)
	}
	path := filepath.Join(dir, "session-"+name+".kdl")
	if err := os.WriteFile(path, buf.Bytes(), 0644); err != nil {
		return "", fmt.Errorf("write session layout: %w", err)
	}
	return path, nil
}

func DeleteSession(name string) error {
	ctx, cancel := context.WithTimeout(context.Background(), cmdTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "zellij", "delete-session", name, "--force")
	var errBuf bytes.Buffer
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("zellij delete-session: %s", strings.TrimSpace(errBuf.String()))
	}
	return nil
}

func CreateBackgroundSession(name, layoutPath string) error {
	ctx, cancel := context.WithTimeout(context.Background(), cmdTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "zellij", "attach", "--create-background", name,
		"options", "--default-layout", layoutPath)
	var errBuf bytes.Buffer
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("zellij create-background: %s", strings.TrimSpace(errBuf.String()))
	}
	return nil
}

func SessionPrefix() string {
	return "wb-"
}
