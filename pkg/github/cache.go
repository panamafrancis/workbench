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
	Entries map[string]*PRInfo `json:"entries"`
}

type Cache struct {
	path    string
	entries map[string]*PRInfo
	mu      sync.RWMutex
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
	return nil
}

func (c *Cache) Save() error {
	c.mu.RLock()
	f := cacheFile{Entries: c.entries}
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

func (c *Cache) IsStale(branch string, maxAge time.Duration) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	info, ok := c.entries[branch]
	if !ok {
		return true
	}
	return time.Since(info.FetchedAt) > maxAge
}
