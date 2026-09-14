package github

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCacheRoundtrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pr-status.json")

	c := NewCache(path)
	c.Set("wt/ss/atlanta", &PRInfo{
		Number:    42,
		Status:    PROpen,
		Title:     "test pr",
		URL:       "https://github.com/org/repo/pull/42",
		UpdatedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		FetchedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	})
	if err := c.Save(); err != nil {
		t.Fatalf("save: %v", err)
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

func TestCacheGetMissing(t *testing.T) {
	c := NewCache("")
	if info := c.Get("nonexistent"); info != nil {
		t.Errorf("expected nil for missing key, got %+v", info)
	}
}

func TestCacheIsStale(t *testing.T) {
	c := NewCache("")
	if !c.IsStale("missing", time.Minute) {
		t.Error("missing entry should be stale")
	}

	c.Set("branch", &PRInfo{
		FetchedAt: time.Now().Add(-10 * time.Minute),
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
	c := NewCache("")
	if c.InBackoff(now) {
		t.Error("fresh cache should not be in backoff")
	}

	c.SetRetryAfter(now.Add(15 * time.Minute))
	if !c.InBackoff(now) {
		t.Error("should be in backoff before retry_after")
	}
	if c.InBackoff(now.Add(16 * time.Minute)) {
		t.Error("should not be in backoff after retry_after")
	}
}

func TestCacheBackoffPersists(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pr-status.json")
	retryAfter := time.Date(2026, 7, 9, 12, 44, 0, 0, time.UTC)

	c := NewCache(path)
	c.SetRetryAfter(retryAfter)
	if err := c.Save(); err != nil {
		t.Fatalf("save: %v", err)
	}

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

func TestCacheSaveCreatesDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "sub", "dir")
	path := filepath.Join(dir, "cache.json")

	c := NewCache(path)
	c.Set("b", &PRInfo{Status: PRMerged})
	if err := c.Save(); err != nil {
		t.Fatalf("save: %v", err)
	}

	if _, err := os.Stat(path); err != nil {
		t.Fatalf("cache file not created: %v", err)
	}
}

func TestIsStaleTerminalStatusHeldLonger(t *testing.T) {
	c := NewCache(filepath.Join(t.TempDir(), "pr.json"))
	old := time.Now().Add(-time.Hour)

	c.Set("merged", &PRInfo{Status: PRMerged, FetchedAt: old})
	c.Set("closed", &PRInfo{Status: PRClosed, FetchedAt: old})
	c.Set("open", &PRInfo{Status: PROpen, FetchedAt: old})
	c.Set("none", &PRInfo{Status: PRNone, FetchedAt: old})

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
	c := NewCache(filepath.Join(t.TempDir(), "pr.json"))
	c.Set("merged", &PRInfo{Status: PRMerged, FetchedAt: time.Now().Add(-TerminalMaxAge - time.Minute)})
	if !c.IsStale("merged", 15*time.Minute) {
		t.Error("terminal status should go stale past TerminalMaxAge")
	}
}

func TestKnowsPR(t *testing.T) {
	c := NewCache(filepath.Join(t.TempDir(), "pr.json"))
	c.Set("has-pr", &PRInfo{Number: 7, Status: PROpen, FetchedAt: time.Now()})
	c.Set("no-pr", &PRInfo{Status: PRNone, FetchedAt: time.Now()})

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

func TestCacheRenameKeepsNumberButForcesRefetch(t *testing.T) {
	c := NewCache(filepath.Join(t.TempDir(), "pr.json"))
	c.Set("st/old-slug/ads", &PRInfo{Number: 485, Status: PROpen, FetchedAt: time.Now()})

	c.Rename("st/old-slug/ads", "st/new-slug/ads")

	if c.Get("st/old-slug/ads") != nil {
		t.Error("old key should be gone after a rename")
	}
	info := c.Get("st/new-slug/ads")
	if info == nil {
		t.Fatal("entry should have moved to the new key")
	}
	if info.Number != 485 {
		t.Errorf("number = %d, want 485 — it is the identity that survives a rename", info.Number)
	}
	// The carried status was verified against the old branch name; GitHub may
	// never have heard of the new one, so it must be re-verified next round.
	if !c.IsStale("st/new-slug/ads", 10*time.Minute) {
		t.Error("renamed entry must be stale so the next round re-verifies it")
	}
}

func TestCacheRenameTerminalEntryIsStale(t *testing.T) {
	// TerminalMaxAge must not keep a merged status alive under a key that was
	// never checked: that is how a merged PR kept showing as open for days.
	c := NewCache(filepath.Join(t.TempDir(), "pr.json"))
	c.Set("old", &PRInfo{Number: 1, Status: PRMerged, FetchedAt: time.Now()})
	c.Rename("old", "new")
	if !c.IsStale("new", 10*time.Minute) {
		t.Error("renamed terminal entry must be stale")
	}
}

func TestCacheRenameDoesNotMutateOriginal(t *testing.T) {
	c := NewCache(filepath.Join(t.TempDir(), "pr.json"))
	fetched := time.Now()
	info := &PRInfo{Number: 9, Status: PROpen, FetchedAt: fetched}
	c.Set("old", info)
	c.Rename("old", "new")
	if !info.FetchedAt.Equal(fetched) {
		t.Error("Rename must copy the entry, not zero the caller's PRInfo in place")
	}
}

func TestCacheRenameMissingEntry(t *testing.T) {
	c := NewCache(filepath.Join(t.TempDir(), "pr.json"))
	c.Rename("absent", "new")
	if c.Get("new") != nil {
		t.Error("renaming an uncached branch should not invent an entry")
	}
}

func TestPRNumber(t *testing.T) {
	c := NewCache(filepath.Join(t.TempDir(), "pr.json"))
	c.Set("has-pr", &PRInfo{Number: 7, Status: PROpen})
	c.Set("no-pr", &PRInfo{Status: PRNone})

	if got := c.PRNumber("has-pr"); got != 7 {
		t.Errorf("PRNumber = %d, want 7", got)
	}
	if got := c.PRNumber("no-pr"); got != 0 {
		t.Errorf("PRNumber = %d, want 0", got)
	}
	if got := c.PRNumber("never-seen"); got != 0 {
		t.Errorf("PRNumber = %d, want 0", got)
	}
}
