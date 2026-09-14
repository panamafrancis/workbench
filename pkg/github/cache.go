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

// Rename moves a cached entry to a new branch key after a local branch rename.
// The PR itself does not necessarily move: GitHub keys a PR on its head ref,
// and that ref only follows a rename for a PR still open when the new branch is
// pushed. A PR that merged or closed first keeps the old head forever, as does
// one whose push failed or was never made (--push=false).
//
// So the entry keeps its Number — the identity that survives a rename, and what
// ResolvePR uses to find the PR again — but its FetchedAt is zeroed. That
// status was verified against a different key and must not be trusted (or held
// for TerminalMaxAge) until the next round re-verifies it under the new one.
func (c *Cache) Rename(oldBranch, newBranch string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	info, ok := c.entries[oldBranch]
	if !ok {
		return
	}
	moved := *info
	moved.FetchedAt = time.Time{}
	c.entries[newBranch] = &moved
	delete(c.entries, oldBranch)
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

// TerminalMaxAge is the floor on how long a merged or closed PR status is
// trusted. Those states are final, so re-checking them on the ordinary
// staleness schedule is pure GitHub-quota burn — most cached branches sit in a
// terminal state, and each sidebar re-fetched every one of them several times
// an hour. A forced refresh bypasses IsStale entirely, so the user can still
// pull a fresh status for a reused branch name.
const TerminalMaxAge = 24 * time.Hour

func isTerminal(s PRStatus) bool {
	return s == PRMerged || s == PRClosed
}

// KnowsPR reports whether the cache holds an entry for branch that names an
// actual PR. Callers use it to keep refreshing a PR they have already seen even
// when the local repo has no remote-tracking ref for its branch (e.g. it was
// pushed from another clone and never fetched here).
func (c *Cache) KnowsPR(branch string) bool {
	return c.Ref(branch).Number != 0
}

// Ref returns what the cache knows about branch's PR — its number and URL, both
// zero if the branch is uncached or names no PR. Fetchers hand it to
// github.ResolvePR so a PR whose head ref has moved away from branch can still
// be resolved by its stable number, and so a number recorded against a
// different repo is not mistaken for this one's.
func (c *Cache) Ref(branch string) PRRef {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if info, ok := c.entries[branch]; ok {
		return PRRef{Number: info.Number, URL: info.URL}
	}
	return PRRef{}
}

func (c *Cache) IsStale(branch string, maxAge time.Duration) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	info, ok := c.entries[branch]
	if !ok {
		return true
	}
	if isTerminal(info.Status) && maxAge < TerminalMaxAge {
		maxAge = TerminalMaxAge
	}
	return time.Since(info.FetchedAt) > maxAge
}
