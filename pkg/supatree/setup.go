package supatree

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// SetupScript is the stack's per-tree setup hook, relative to a tree root.
// It is team-shared — it lives in the stack repo — and runs for every tree
// made from the stack; personal per-repo files are copy_files instead.
const SetupScript = "scripts/setup"

// setupTimeout bounds the hook. Long enough for an install; it is not a place
// to run a server.
const setupTimeout = 15 * time.Minute

// SetupLogPath is where the hook's output goes.
func SetupLogPath(root string) string {
	return filepath.Join(StateDir(root), "setup.log")
}

// RunSetup runs the stack's scripts/setup for a newly created tree: once, after
// the members exist and copy_files has run, before any agent tab opens.
//
// It can never need a human: stdin is closed, so a script that prompts reads
// end-of-file instead of waiting. A failure is a warning, not a failed create —
// the tree is still usable, and the log says what went wrong. Whether it ran is
// recorded in meta, so nothing runs it twice.
func RunSetup(inst *Instance) (warning string) {
	script := filepath.Join(inst.Root, SetupScript)
	if _, err := os.Stat(script); err != nil {
		return ""
	}
	meta, err := LoadMeta(inst.Root)
	if err != nil || !meta.SetupAt.IsZero() {
		return ""
	}

	logPath := SetupLogPath(inst.Root)
	log, err := os.Create(logPath)
	if err != nil {
		return fmt.Sprintf("%s: %v", SetupScript, err)
	}
	defer func() { _ = log.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), setupTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "bash", "--", script)
	cmd.Dir = inst.Root
	cmd.Stdin = nil
	cmd.Stdout = log
	cmd.Stderr = log
	cmd.Env = append(os.Environ(),
		"SUPATREE=1",
		"SUPATREE_NAME="+inst.Name,
		"SUPATREE_ROOT="+inst.Root,
		"SUPATREE_MEMBERS="+strings.Join(inst.MemberAliases(), ","),
		"GIT_TERMINAL_PROMPT=0",
	)
	runErr := cmd.Run()

	meta.SetupAt = time.Now().UTC()
	if runErr != nil {
		meta.SetupErr = runErr.Error()
		warning = fmt.Sprintf("%s failed (%v) — the tree is usable; see .supatree/setup.log", SetupScript, runErr)
	}
	if err := meta.Save(inst.Root); err != nil && warning == "" {
		warning = fmt.Sprintf("%s ran, but recording it failed: %v", SetupScript, err)
	}
	return warning
}
