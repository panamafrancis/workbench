package config

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

// TryFileLock returns ErrLockBusy (without running fn) when the lock is already
// held, and runs fn once the holder releases it.
func TestTryFileLockBusyThenFree(t *testing.T) {
	lockPath := filepath.Join(t.TempDir(), "test.lock")

	// Hold the lock via a raw flock on a separate fd, mimicking another process.
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0644)
	if err != nil {
		t.Fatal(err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		t.Fatal(err)
	}

	ran := false
	err = TryFileLock(lockPath, func() error { ran = true; return nil })
	if !errors.Is(err, ErrLockBusy) {
		t.Fatalf("held lock: err = %v, want ErrLockBusy", err)
	}
	if ran {
		t.Error("fn ran while the lock was held")
	}

	// Release the holder; the try-lock should now succeed and run fn.
	_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	_ = f.Close()

	if err := TryFileLock(lockPath, func() error { ran = true; return nil }); err != nil {
		t.Fatalf("free lock: err = %v, want nil", err)
	}
	if !ran {
		t.Error("fn did not run after the lock was released")
	}
}
