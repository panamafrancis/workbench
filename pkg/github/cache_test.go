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
