package github

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type cacheFile struct {
	Entries    map[string]*PRInfo `json:"entries"`
	RetryAfter time.Time          `json:"retry_after,omitzero"`
}

type Cache struct {
	path       string
	entries    map[string]*PRInfo
	retryAfter time.Time
	mu         sync.RWMutex
}

func NewCache(path string) *Cache {
	return &Cache{
		path:    path,
		entries: make(map[string]*PRInfo),
	}
}

func (c *Cache) Load() error {
	data, err := os.ReadFile(c.path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read pr cache: %w", err)
	}
	var f cacheFile
	if err := json.Unmarshal(data, &f); err != nil {
		return fmt.Errorf("parse pr cache: %w", err)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if f.Entries != nil {
		c.entries = f.Entries
	}
	c.retryAfter = f.RetryAfter
	return nil
}

func (c *Cache) Save() error {
	c.mu.RLock()
	f := cacheFile{Entries: c.entries, RetryAfter: c.retryAfter}
	data, err := json.MarshalIndent(f, "", "  ")
	c.mu.RUnlock()
	if err != nil {
		return fmt.Errorf("marshal pr cache: %w", err)
	}

	if err := os.MkdirAll(filepath.Dir(c.path), 0755); err != nil {
		return fmt.Errorf("create cache dir: %w", err)
	}
	tmp := c.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return fmt.Errorf("write pr cache: %w", err)
	}
	return os.Rename(tmp, c.path)
}

func (c *Cache) Get(branch string) *PRInfo {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.entries[branch]
}

func (c *Cache) Set(branch string, info *PRInfo) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries[branch] = info
}

func (c *Cache) Rename(oldBranch, newBranch string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if info, ok := c.entries[oldBranch]; ok {
		c.entries[newBranch] = info
		delete(c.entries, oldBranch)
	}
}

func (c *Cache) Delete(branch string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.entries, branch)
}

// SetRetryAfter records a timestamp before which no GitHub fetches should be
// attempted. It persists via Save so the backoff survives process restarts —
// important for the sidebar's restart loop.
func (c *Cache) SetRetryAfter(t time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.retryAfter = t
}

// InBackoff reports whether the fetch backoff window (set by SetRetryAfter) is
// still active as of now.
func (c *Cache) InBackoff(now time.Time) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return now.Before(c.retryAfter)
}

func (c *Cache) IsStale(branch string, maxAge time.Duration) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	info, ok := c.entries[branch]
	if !ok {
		return true
	}
	return time.Since(info.FetchedAt) > maxAge
}
