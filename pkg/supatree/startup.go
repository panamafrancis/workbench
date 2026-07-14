package supatree

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/panamafrancis/workbench/pkg/config"
)

// RunStartupScripts runs each member repo's workbench startup script and, if
// present, the stack's scripts/startup, once (on first agent open). Failures are
// reported to w but never abort — a startup hook must not block opening a tab.
func RunStartupScripts(inst *Instance, wb *config.Config, w io.Writer) {
	for _, m := range inst.Members {
		if !m.Exists {
			continue
		}
		if repo, _ := wb.FindRepo(m.Alias); repo != nil {
			if err := repo.RunStartup(m.Path, inst.Name); err != nil {
				_, _ = fmt.Fprintf(w, "warning: %s startup: %v\n", m.Alias, err)
			}
		}
	}
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
