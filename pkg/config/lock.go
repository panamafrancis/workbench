package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// ErrLockBusy is returned by TryFileLock when another process already holds the
// lock, so the caller can skip its work rather than blocking.
var ErrLockBusy = errors.New("lock busy")

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
	return flockWith(lockPath, syscall.LOCK_EX, fn)
}

// TryFileLock behaves like WithFileLock but takes the lock non-blockingly: if
// another process already holds it, it returns ErrLockBusy immediately without
// running fn. This lets independent pollers (e.g. one sidebar per Zellij tab)
// elect a single worker per round instead of all firing at once.
func TryFileLock(lockPath string, fn func() error) error {
	return flockWith(lockPath, syscall.LOCK_EX|syscall.LOCK_NB, fn)
}

// flockWith opens (creating as needed) lockPath, acquires an advisory lock with
// the given flock op, and runs fn while holding it. When op carries LOCK_NB and
// the lock is already held, it returns ErrLockBusy without running fn.
func flockWith(lockPath string, op int, fn func() error) error {
	if err := os.MkdirAll(filepath.Dir(lockPath), 0755); err != nil {
		return fmt.Errorf("create lock dir: %w", err)
	}
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0644)
	if err != nil {
		return fmt.Errorf("open lock file: %w", err)
	}
	defer func() { _ = f.Close() }()
	if err := syscall.Flock(int(f.Fd()), op); err != nil {
		if op&syscall.LOCK_NB != 0 && errors.Is(err, syscall.EWOULDBLOCK) {
			return ErrLockBusy
		}
		return fmt.Errorf("acquire lock: %w", err)
	}
	return fn()
}
