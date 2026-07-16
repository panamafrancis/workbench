package supacmd

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/panamafrancis/workbench/pkg/zellij"
)

var (
	startList       bool
	startGC         bool
	startBackground bool
)

var startCmd = &cobra.Command{
	Use:   "start [session-name]",
	Short: "Start or attach to a supatree Zellij session",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if zellij.IsInZellij() {
			fmt.Fprintln(os.Stderr, "Already inside a Zellij session. Use supatree open to open a supatree.")
			return nil
		}
		ws := supatreeWorkspace()
		prefix := ws.SessionPrefix
		if startGC {
			return gcDeadSessions(prefix)
		}
		if startList {
			return listSessions(prefix)
		}

		name := "main"
		if len(args) > 0 {
			name = args[0]
		}
		sessionName := prefix + name

		layoutPath, err := ws.WriteSessionLayout(sessionName, stCfg.ResolveSidebarWidth())
		if err != nil {
			return err
		}
		if startBackground {
			return zellij.CreateBackgroundSession(sessionName, layoutPath)
		}

		sessions, err := zellij.ListSessions()
		if err != nil {
			sessions = nil
		}
		for i := range sessions {
			if sessions[i].Name == sessionName {
				if sessions[i].Exited {
					_ = zellij.DeleteSession(sessionName)
					break
				}
				return execZellij("attach", sessionName)
			}
		}
		return execZellij("--session", sessionName, "--new-session-with-layout", layoutPath)
	},
}

func execZellij(args ...string) error {
	zellijPath, err := exec.LookPath("zellij")
	if err != nil {
		return fmt.Errorf("zellij not found in PATH: %w", err)
	}
	argv := append([]string{"zellij"}, args...)
	return syscall.Exec(zellijPath, argv, os.Environ())
}

func listSessions(prefix string) error {
	sessions, err := zellij.ListSessions()
	if err != nil {
		return err
	}
	found := false
	for _, s := range sessions {
		if !strings.HasPrefix(s.Name, prefix) {
			continue
		}
		found = true
		status := "running"
		if s.Exited {
			status = "exited"
		}
		fmt.Printf("  %-20s %s\n", strings.TrimPrefix(s.Name, prefix), status)
	}
	if !found {
		fmt.Println("No supatree sessions found.")
	}
	return nil
}

func gcDeadSessions(prefix string) error {
	sessions, err := zellij.ListSessions()
	if err != nil {
		return err
	}
	count := 0
	for _, s := range sessions {
		if strings.HasPrefix(s.Name, prefix) && s.Exited {
			if err := zellij.DeleteSession(s.Name); err != nil {
				fmt.Fprintf(os.Stderr, "failed to delete %s: %v\n", s.Name, err)
				continue
			}
			count++
		}
	}
	fmt.Printf("Deleted %d dead session(s).\n", count)
	return nil
}

func init() {
	startCmd.Flags().BoolVar(&startList, "ls", false, "list supatree sessions")
	startCmd.Flags().BoolVar(&startGC, "gc", false, "delete dead supatree sessions")
	startCmd.Flags().BoolVar(&startBackground, "background", false, "create a detached session (for CI/scripting)")
}
