package github

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func newTestCache(t *testing.T) *Cache {
	t.Helper()
	return NewCache(filepath.Join(t.TempDir(), "pr-status.json"))
}

func mustMutate(t *testing.T, c *Cache, fn func(*Writable)) {
	t.Helper()
	if err := c.Mutate(func(w *Writable) error {
		fn(w)
		return nil
	}); err != nil {
		t.Fatalf("mutate: %v", err)
	}
}

func TestCacheRoundtrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pr-status.json")

	c := NewCache(path)
	mustMutate(t, c, func(w *Writable) {
		w.Set("wt/ss/atlanta", &PRInfo{
			Number:    42,
			Status:    PROpen,
			Title:     "test pr",
			URL:       "https://github.com/org/repo/pull/42",
			UpdatedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
			FetchedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		})
	})

	// A mutation also refreshes the mutating process's own snapshot, so a
	// sidebar renders its own writes without an explicit Load.
	if info := c.Get("wt/ss/atlanta"); info == nil || info.Number != 42 {
		t.Errorf("writer's snapshot = %+v, want the entry it just wrote", info)
	}

	c2 := NewCache(path)
	if err := c2.Load(); err != nil {
		t.Fatalf("load: %v", err)
	}
	info := c2.Get("wt/ss/atlanta")
	if info == nil {
		t.Fatal("expected cached entry")
	}
	if info.Number != 42 {
		t.Errorf("number = %d, want 42", info.Number)
	}
	if info.Status != PROpen {
		t.Errorf("status = %q, want %q", info.Status, PROpen)
	}
	if info.Title != "test pr" {
		t.Errorf("title = %q, want %q", info.Title, "test pr")
	}
}

// TestMutateKeepsWritesFromOtherProcesses pins the failure this API exists to
// prevent. The old Save() marshalled the caller's whole in-memory snapshot over
// the file, so a process that had loaded the cache earlier silently reverted
// every entry written since — those branches then looked uncached and were
// re-fetched, multiplying GitHub quota use by the number of sidebars.
func TestMutateKeepsWritesFromOtherProcesses(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pr-status.json")

	// Two processes, both holding a snapshot from before either wrote.
	stale := NewCache(path)
	if err := stale.Load(); err != nil {
		t.Fatalf("load: %v", err)
	}
	peer := NewCache(path)
	mustMutate(t, peer, func(w *Writable) {
		w.Set("peer-branch", &PRInfo{Number: 1, Status: PROpen, FetchedAt: time.Now()})
	})

	// The stale process writes an unrelated entry.
	mustMutate(t, stale, func(w *Writable) {
		w.Set("own-branch", &PRInfo{Number: 2, Status: PROpen, FetchedAt: time.Now()})
	})

	final := NewCache(path)
	if err := final.Load(); err != nil {
		t.Fatalf("load: %v", err)
	}
	if final.Get("peer-branch") == nil {
		t.Error("entry written by the peer was lost")
	}
	if final.Get("own-branch") == nil {
		t.Error("entry written by the stale process is missing")
	}
}

// TestMutateCannotDisarmAnotherProcessesCooldown is the same hazard applied to
// the rate-limit backoff, which is how a single `pr_status` call used to
// un-pause every sidebar on a drained quota.
func TestMutateCannotDisarmAnotherProcessesCooldown(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pr-status.json")
	now := time.Now()

	stale := NewCache(path)
	if err := stale.Load(); err != nil { // snapshot taken before any cooldown
		t.Fatalf("load: %v", err)
	}

	peer := NewCache(path)
	mustMutate(t, peer, func(w *Writable) {
		w.SetRetryAfter(now.Add(15 * time.Minute))
	})

	mustMutate(t, stale, func(w *Writable) {
		w.Set("some-branch", &PRInfo{Status: PRNone, FetchedAt: now})
	})

	final := NewCache(path)
	if err := final.Load(); err != nil {
		t.Fatalf("load: %v", err)
	}
	if !final.InBackoff(now) {
		t.Error("cooldown armed by the peer was disarmed by a stale writer")
	}
}

func TestSetRetryAfterOnlyMovesLater(t *testing.T) {
	c := newTestCache(t)
	now := time.Now()

	mustMutate(t, c, func(w *Writable) { w.SetRetryAfter(now.Add(15 * time.Minute)) })
	mustMutate(t, c, func(w *Writable) { w.SetRetryAfter(now.Add(time.Minute)) })

	if !c.InBackoff(now.Add(10 * time.Minute)) {
		t.Error("a shorter cooldown must not shorten the armed window")
	}

	mustMutate(t, c, func(w *Writable) { w.SetRetryAfter(now.Add(30 * time.Minute)) })
	if !c.InBackoff(now.Add(20 * time.Minute)) {
		t.Error("a longer cooldown should extend the armed window")
	}
}

func TestTryMutateCedesWhenLockHeld(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pr-status.json")
	held := make(chan struct{})
	release := make(chan struct{})
	done := make(chan error, 1)

	go func() {
		done <- NewCache(path).Mutate(func(*Writable) error {
			close(held)
			<-release
			return nil
		})
	}()

	<-held
	err := NewCache(path).TryMutate(func(*Writable) error {
		t.Error("TryMutate ran fn while the lock was held")
		return nil
	})
	if !errors.Is(err, ErrLockBusy) {
		t.Errorf("TryMutate error = %v, want ErrLockBusy", err)
	}

	close(release)
	if err := <-done; err != nil {
		t.Fatalf("holder mutate: %v", err)
	}
}

func TestMutateAbortsWithoutWriting(t *testing.T) {
	c := newTestCache(t)
	mustMutate(t, c, func(w *Writable) {
		w.Set("kept", &PRInfo{Number: 5, Status: PROpen, FetchedAt: time.Now()})
	})

	sentinel := errors.New("abort")
	err := c.Mutate(func(w *Writable) error {
		w.Delete("kept")
		return sentinel
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("mutate error = %v, want the callback's error", err)
	}

	reloaded := NewCache(c.path)
	if err := reloaded.Load(); err != nil {
		t.Fatalf("load: %v", err)
	}
	if reloaded.Get("kept") == nil {
		t.Error("an aborted mutation must not be written")
	}
}

func TestWritableRenameAndDelete(t *testing.T) {
	c := newTestCache(t)
	mustMutate(t, c, func(w *Writable) {
		w.Set("old", &PRInfo{Number: 3, Status: PROpen, FetchedAt: time.Now()})
		w.Set("doomed", &PRInfo{Number: 4, Status: PROpen, FetchedAt: time.Now()})
	})
	mustMutate(t, c, func(w *Writable) {
		w.Rename("old", "new")
		w.Delete("doomed")
	})

	if c.Get("old") != nil {
		t.Error("renamed-from key should be gone")
	}
	if info := c.Get("new"); info == nil || info.Number != 3 {
		t.Errorf("renamed-to entry = %+v, want the original", info)
	}
	if c.Get("doomed") != nil {
		t.Error("deleted key should be gone")
	}
}

func TestCacheGetMissing(t *testing.T) {
	c := newTestCache(t)
	if info := c.Get("nonexistent"); info != nil {
		t.Errorf("expected nil for missing key, got %+v", info)
	}
}

func TestCacheIsStale(t *testing.T) {
	c := newTestCache(t)
	if !c.IsStale("missing", time.Minute) {
		t.Error("missing entry should be stale")
	}

	mustMutate(t, c, func(w *Writable) {
		w.Set("branch", &PRInfo{FetchedAt: time.Now().Add(-10 * time.Minute)})
	})
	if !c.IsStale("branch", 5*time.Minute) {
		t.Error("old entry should be stale with 5m max age")
	}
	if c.IsStale("branch", 15*time.Minute) {
		t.Error("old entry should not be stale with 15m max age")
	}
}

func TestCacheLoadMissingFile(t *testing.T) {
	c := NewCache(filepath.Join(t.TempDir(), "nonexistent.json"))
	if err := c.Load(); err != nil {
		t.Fatalf("load missing file should not error: %v", err)
	}
	if info := c.Get("anything"); info != nil {
		t.Error("expected nil from empty cache")
	}
}

func TestCacheBackoff(t *testing.T) {
	now := time.Now()
	c := newTestCache(t)
	if c.InBackoff(now) {
		t.Error("fresh cache should not be in backoff")
	}

	mustMutate(t, c, func(w *Writable) { w.SetRetryAfter(now.Add(15 * time.Minute)) })
	if !c.InBackoff(now) {
		t.Error("should be in backoff before retry_after")
	}
	if c.InBackoff(now.Add(16 * time.Minute)) {
		t.Error("should not be in backoff after retry_after")
	}
	if got := c.RetryAfter(); !got.Equal(now.Add(15 * time.Minute)) {
		t.Errorf("RetryAfter() = %v, want the armed deadline", got)
	}
}

func TestCacheBackoffPersists(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pr-status.json")
	retryAfter := time.Date(2026, 7, 9, 12, 44, 0, 0, time.UTC)

	c := NewCache(path)
	mustMutate(t, c, func(w *Writable) { w.SetRetryAfter(retryAfter) })

	c2 := NewCache(path)
	if err := c2.Load(); err != nil {
		t.Fatalf("load: %v", err)
	}
	if !c2.InBackoff(retryAfter.Add(-time.Second)) {
		t.Error("backoff should survive reload")
	}
	if c2.InBackoff(retryAfter.Add(time.Second)) {
		t.Error("backoff should have expired after retry_after")
	}
}

func TestCacheMutateCreatesDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "sub", "dir")
	path := filepath.Join(dir, "cache.json")

	c := NewCache(path)
	mustMutate(t, c, func(w *Writable) { w.Set("b", &PRInfo{Status: PRMerged}) })

	if _, err := os.Stat(path); err != nil {
		t.Fatalf("cache file not created: %v", err)
	}
}

func TestIsStaleTerminalStatusHeldLonger(t *testing.T) {
	c := newTestCache(t)
	old := time.Now().Add(-time.Hour)

	mustMutate(t, c, func(w *Writable) {
		w.Set("merged", &PRInfo{Status: PRMerged, FetchedAt: old})
		w.Set("closed", &PRInfo{Status: PRClosed, FetchedAt: old})
		w.Set("open", &PRInfo{Status: PROpen, FetchedAt: old})
		w.Set("none", &PRInfo{Status: PRNone, FetchedAt: old})
	})

	for _, branch := range []string{"merged", "closed"} {
		if c.IsStale(branch, 15*time.Minute) {
			t.Errorf("%s: terminal status should stay fresh past the ordinary maxAge", branch)
		}
	}
	for _, branch := range []string{"open", "none"} {
		if !c.IsStale(branch, 15*time.Minute) {
			t.Errorf("%s: non-terminal status should be stale after maxAge", branch)
		}
	}
}

func TestIsStaleTerminalStatusExpires(t *testing.T) {
	c := newTestCache(t)
	mustMutate(t, c, func(w *Writable) {
		w.Set("merged", &PRInfo{Status: PRMerged, FetchedAt: time.Now().Add(-TerminalMaxAge - time.Minute)})
	})
	if !c.IsStale("merged", 15*time.Minute) {
		t.Error("terminal status should go stale past TerminalMaxAge")
	}
}

func TestKnowsPR(t *testing.T) {
	c := newTestCache(t)
	mustMutate(t, c, func(w *Writable) {
		w.Set("has-pr", &PRInfo{Number: 7, Status: PROpen, FetchedAt: time.Now()})
		w.Set("no-pr", &PRInfo{Status: PRNone, FetchedAt: time.Now()})
	})

	if !c.KnowsPR("has-pr") {
		t.Error("branch with a cached PR number should be known")
	}
	if c.KnowsPR("no-pr") {
		t.Error("a cached PRNone entry names no PR")
	}
	if c.KnowsPR("never-seen") {
		t.Error("uncached branch names no PR")
	}
}

// TestWritableViewMatchesCache guards the two read paths (snapshot and
// in-mutation view) staying in step, since callers pick staleness targets with
// one and re-check them with the other.
func TestWritableViewMatchesCache(t *testing.T) {
	c := newTestCache(t)
	now := time.Now()
	mustMutate(t, c, func(w *Writable) {
		w.Set("fresh", &PRInfo{Number: 9, Status: PROpen, FetchedAt: now})
	})

	mustMutate(t, c, func(w *Writable) {
		if w.IsStale("fresh", time.Hour) != c.IsStale("fresh", time.Hour) {
			t.Error("IsStale differs between the snapshot and the mutation view")
		}
		if w.KnowsPR("fresh") != c.KnowsPR("fresh") {
			t.Error("KnowsPR differs between the snapshot and the mutation view")
		}
		if w.Get("fresh") == nil {
			t.Error("mutation view should see committed entries")
		}
	})
}
