package supacmd

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/panamafrancis/workbench/pkg/supatree"
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
		// Spawn the watcher BEFORE handing the terminal over: execZellij ends in
		// syscall.Exec, which replaces this process, so nothing written after it
		// ever runs. It is a singleton by file lock, so spawning one per start is
		// safe — the loser exits quietly.
		spawnWatcher(sessionName)
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

// spawnWatcher starts `supatree watch` detached, so it survives this process
// being replaced by zellij and keeps running when the terminal closes. Setsid
// puts it in its own session — without it the watcher dies with the terminal
// that started the zellij session, which is exactly the window overnight
// notifications need to cover.
//
// Best effort throughout: a missing binary or an unwritable log directory costs
// notifications, and must never stop a session from starting.
func spawnWatcher(sessionName string) {
	exe, err := os.Executable()
	if err != nil {
		return
	}
	if err := os.MkdirAll(supatree.LogsDir(), 0755); err != nil {
		return
	}
	// Raw stdio only — a panic, or a line from a library. The watcher writes
	// its own log, watch.log, through a rotating writer once it has won the
	// singleton election; pointing stdio there too would leave the live
	// watcher's stderr on a file rotation had renamed away.
	logPath := filepath.Join(supatree.LogsDir(), "watch.out")
	log, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return
	}
	defer func() { _ = log.Close() }()

	// context.Background: the watcher must outlive this process entirely, so
	// there is deliberately no cancellation tied to the caller.
	cmd := exec.CommandContext(context.Background(), exe, "watch", "--session", sessionName, "--quiet")
	cmd.Stdin = nil
	cmd.Stdout = log
	cmd.Stderr = log
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return
	}
	// Detach: we are about to be replaced by zellij, so there is nobody left to
	// wait on the child. Releasing it now avoids leaving a zombie in the brief
	// window before the exec.
	_ = cmd.Process.Release()
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
