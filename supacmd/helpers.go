package supacmd

import (
	"fmt"
	"os"
)

// resolveTreeName returns the supatree name from the first positional arg, or —
// when none is given — from the SUPATREE_NAME env var (set inside a supatree
// agent pane). It errors if neither is available.
func resolveTreeName(args []string) (string, error) {
	if len(args) > 0 && args[0] != "" {
		return args[0], nil
	}
	if name := os.Getenv("SUPATREE_NAME"); name != "" {
		return name, nil
	}
	return "", fmt.Errorf("supatree name required (pass it as an argument, or run inside a supatree)")
}

// isInteractive reports whether stdout is a terminal.
func isInteractive() bool {
	fi, err := os.Stdout.Stat()
	if err != nil {
		return false
	}
	return (fi.Mode() & os.ModeCharDevice) != 0
}
