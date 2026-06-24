package config

import (
	"fmt"
	"os"
	"syscall"
)

// withConfigLock runs fn while holding an exclusive advisory lock on the config
// lock file. This serializes read-modify-write sequences (Load → mutate → Save)
// both across goroutines in one process and across separate workbench processes
// (e.g. a sidebar and a CLI invocation). flock locks are associated with the
// open file description, so each caller's own fd blocks until the holder closes
// it. Without this, two concurrent Load→Save sequences can interleave and the
// later Save resurrects an entry the other removed.
func withConfigLock(fn func() error) error {
	if err := os.MkdirAll(ConfigDir(), 0755); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}
	f, err := os.OpenFile(ConfigPath()+".lock", os.O_CREATE|os.O_RDWR, 0644)
	if err != nil {
		return fmt.Errorf("open config lock: %w", err)
	}
	// Closing the descriptor releases the flock, so the deferred close is the
	// unlock; no explicit LOCK_UN needed.
	defer func() { _ = f.Close() }()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		return fmt.Errorf("lock config: %w", err)
	}
	return fn()
}
