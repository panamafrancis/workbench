package supatree

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// RunStartupScripts runs the stack's scripts/startup, if present, when a tab
// is created. Failures are reported to w but never abort — a startup hook must
// not block opening a tab.
func RunStartupScripts(inst *Instance, w io.Writer) {
	script := filepath.Join(inst.Root, "scripts", "startup")
	if _, err := os.Stat(script); err != nil {
		return
	}
	cmd := exec.CommandContext(context.Background(), "bash", "--", script)
	cmd.Dir = inst.Root
	cmd.Env = append(os.Environ(),
		"SUPATREE=1",
		"SUPATREE_NAME="+inst.Name,
		"SUPATREE_ROOT="+inst.Root,
		"SUPATREE_MEMBERS="+strings.Join(inst.MemberAliases(), ","),
	)
	cmd.Stdout = w
	cmd.Stderr = w
	if err := cmd.Run(); err != nil {
		_, _ = fmt.Fprintf(w, "warning: stack startup: %v\n", err)
	}
}
