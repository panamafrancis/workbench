package config

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// withConfigLock runs fn while holding an exclusive advisory lock on the config
// lock file. This serializes read-modify-write sequences (Load → mutate → Save)
// both across goroutines in one process and across separate workbench processes
// (e.g. a sidebar and a CLI invocation). Without this, two concurrent Load→Save
// sequences can interleave and the later Save resurrects an entry the other
// removed.
func withConfigLock(fn func() error) error {
	return WithFileLock(ConfigPath()+".lock", fn)
}

// WithFileLock runs fn while holding an exclusive advisory (flock) lock on
// lockPath, creating the parent directory and lock file as needed. flock locks
// are associated with the open file description, so each caller's own fd blocks
// until the holder closes it; the deferred close is the unlock, so no explicit
// LOCK_UN is needed. Used by config mutations and by sibling tools (supatree)
// that need the same cross-process serialization for their own state files.
func WithFileLock(lockPath string, fn func() error) error {
	if err := os.MkdirAll(filepath.Dir(lockPath), 0755); err != nil {
		return fmt.Errorf("create lock dir: %w", err)
	}
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0644)
	if err != nil {
		return fmt.Errorf("open lock file: %w", err)
	}
	defer func() { _ = f.Close() }()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		return fmt.Errorf("acquire lock: %w", err)
	}
	return fn()
}
