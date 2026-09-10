package github

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/panamafrancis/workbench/pkg/config"
)

type cacheFile struct {
	Entries map[string]*PRInfo `json:"entries"`
	// Repos keys a repo ("owner/name") to what we remember about its last
	// PR-list poll.
	Repos      map[string]RepoState `json:"repos,omitempty"`
	RetryAfter time.Time            `json:"retry_after,omitzero"`
}

// Cache is a process-local snapshot of the on-disk PR status cache, used by the
// render path (sidebars read it every tick) and refreshed with Load.
//
// The cache is shared by every workbench and supatree process on the machine —
// a sidebar per Zellij tab, the CLI, the MCP server. It is therefore read-only
// here: the snapshot can go stale but never diverge destructively, because the
// only way to change the file is Mutate/TryMutate, which re-read it under an
// advisory lock. That matters more than it sounds: a whole-file write from a
// stale snapshot silently reverts every entry another process wrote since this
// one loaded, including the rate-limit cooldown, which is how a single
// `pr_status` call used to un-pause every sidebar on a drained quota.
// RepoState is what a poll of one repo leaves behind for the next round. The
// ETag is what makes the next poll free when nothing changed; PolledAt is how a
// caller decides whether one page of results still covers everything that has
// happened since it last looked.
type RepoState struct {
	ETag     string    `json:"etag,omitempty"`
	PolledAt time.Time `json:"polled_at,omitzero"`
	// Unavailable marks a repo the authenticated account cannot see (private to
	// another org, renamed, deleted). Polling it again would 404 again, once per
	// round forever, so it is remembered and skipped until a forced refresh.
	Unavailable bool `json:"unavailable,omitempty"`
	// Truncated records whether the last listing was only the first page. A 304
	// says nothing has changed, not that the listing covered the repo's whole
	// history, so this has to be remembered across rounds — without it a
	// not-modified poll would read as proof that a branch has no PR.
	Truncated bool `json:"truncated,omitempty"`
}

type Cache struct {
	path       string
	entries    map[string]*PRInfo
	repos      map[string]RepoState
	retryAfter time.Time
	mu         sync.RWMutex
}

// ErrLockBusy is returned by TryMutate when another process holds the cache
// lock, so a caller can cede its round instead of blocking.
var ErrLockBusy = config.ErrLockBusy

func NewCache(path string) *Cache {
	return &Cache{
		path:    path,
		entries: make(map[string]*PRInfo),
		repos:   make(map[string]RepoState),
	}
}

// lockPath is the advisory lock serializing cache mutations across processes.
// Deriving it from the cache path keeps the two in step by construction; no
// caller needs to know (or agree on) the convention.
func (c *Cache) lockPath() string { return c.path + ".lock" }

func (c *Cache) Load() error {
	f, err := readCacheFile(c.path)
	if err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries = f.Entries
	c.repos = f.Repos
	c.retryAfter = f.RetryAfter
	return nil
}

// Writable is the mutable view of the cache passed to Mutate's callback. It is
// loaded from disk while the lock is held, so mutations always apply to the
// newest state: nothing another process wrote can be lost, and nothing it armed
// can be cleared.
type Writable struct {
	entries    map[string]*PRInfo
	repos      map[string]RepoState
	retryAfter time.Time
}

// Mutate runs fn against the current on-disk cache while holding the cache
// lock, then writes the result atomically. It is the only way to change the
// cache — there is deliberately no exported Save, so a read-modify-write from a
// stale snapshot cannot be expressed.
//
// This process's snapshot is refreshed to the written state on success, so a
// caller that also renders from the cache sees its own writes immediately.
func (c *Cache) Mutate(fn func(*Writable) error) error {
	return c.mutate(config.WithFileLock, fn)
}

// TryMutate behaves like Mutate but takes the lock non-blockingly, returning
// ErrLockBusy without running fn when another process holds it. Independent
// pollers (one sidebar per Zellij tab) use this to elect a single worker per
// round: the winner fetches and writes, the rest cede and pick up its results.
// fn may therefore be long-running — it holds the lock for the whole round.
func (c *Cache) TryMutate(fn func(*Writable) error) error {
	return c.mutate(config.TryFileLock, fn)
}

func (c *Cache) mutate(lock func(string, func() error) error, fn func(*Writable) error) error {
	return lock(c.lockPath(), func() error {
		f, err := readCacheFile(c.path)
		if err != nil {
			return err
		}
		w := &Writable{entries: f.Entries, repos: f.Repos, retryAfter: f.RetryAfter}
		if err := fn(w); err != nil {
			return err
		}
		if err := writeCacheFile(c.path, cacheFile{Entries: w.entries, Repos: w.repos, RetryAfter: w.retryAfter}); err != nil {
			return err
		}
		c.mu.Lock()
		defer c.mu.Unlock()
		c.entries = w.entries
		c.repos = w.repos
		c.retryAfter = w.retryAfter
		return nil
	})
}

func readCacheFile(path string) (cacheFile, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return cacheFile{
			Entries: make(map[string]*PRInfo),
			Repos:   make(map[string]RepoState),
		}, nil
	}
	if err != nil {
		return cacheFile{}, fmt.Errorf("read pr cache: %w", err)
	}
	var f cacheFile
	if err := json.Unmarshal(data, &f); err != nil {
		return cacheFile{}, fmt.Errorf("parse pr cache: %w", err)
	}
	if f.Entries == nil {
		f.Entries = make(map[string]*PRInfo)
	}
	if f.Repos == nil {
		f.Repos = make(map[string]RepoState)
	}
	return f, nil
}

func writeCacheFile(path string, f cacheFile) error {
	data, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal pr cache: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf("create cache dir: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return fmt.Errorf("write pr cache: %w", err)
	}
	return os.Rename(tmp, path)
}

// RepoState returns what the last poll of repo left behind, zero when the repo
// has never been polled.
func (c *Cache) RepoState(repo string) RepoState {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.repos[repo]
}

func (w *Writable) RepoState(repo string) RepoState { return w.repos[repo] }

func (w *Writable) SetRepoState(repo string, state RepoState) { w.repos[repo] = state }

// Touch marks an entry as confirmed current as of at, without changing what it
// says. A repo poll that comes back without a given branch has established that
// the branch's PR did not change — which is just as good as re-fetching it, and
// free. Without this the entry would age out and be re-fetched one branch at a
// time, which is the cost this design exists to avoid.
func (w *Writable) Touch(branch string, at time.Time) {
	if info, ok := w.entries[branch]; ok {
		info.FetchedAt = at
	}
}

func (c *Cache) Get(branch string) *PRInfo {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.entries[branch]
}

// Get reads an entry from the mutation's view of the cache, which includes both
// what other processes have written and this mutation's own changes so far.
func (w *Writable) Get(branch string) *PRInfo { return w.entries[branch] }

func (w *Writable) Set(branch string, info *PRInfo) { w.entries[branch] = info }

func (w *Writable) Delete(branch string) { delete(w.entries, branch) }

func (w *Writable) Rename(oldBranch, newBranch string) {
	if info, ok := w.entries[oldBranch]; ok {
		w.entries[newBranch] = info
		delete(w.entries, oldBranch)
	}
}

// SetRetryAfter records a timestamp before which no GitHub fetches should be
// attempted, so a rate-limit response pauses every process rather than just the
// one that hit it. It only ever moves the deadline later: a mutation that
// started before a peer armed a longer cooldown must not shorten it, and there
// is no way to clear one early — the window simply expires.
func (w *Writable) SetRetryAfter(t time.Time) {
	if t.After(w.retryAfter) {
		w.retryAfter = t
	}
}

// InBackoff reports whether the fetch backoff window (set by SetRetryAfter) is
// still active as of now.
func (c *Cache) InBackoff(now time.Time) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return now.Before(c.retryAfter)
}

func (w *Writable) InBackoff(now time.Time) bool { return now.Before(w.retryAfter) }

// RetryAfter is the currently armed backoff deadline, zero when none is armed.
// Callers use it to report when fetches resume.
func (c *Cache) RetryAfter() time.Time {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.retryAfter
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
	c.mu.RLock()
	defer c.mu.RUnlock()
	return knowsPR(c.entries, branch)
}

func (w *Writable) KnowsPR(branch string) bool { return knowsPR(w.entries, branch) }

func knowsPR(entries map[string]*PRInfo, branch string) bool {
	info, ok := entries[branch]
	return ok && info.Number != 0
}

func (c *Cache) IsStale(branch string, maxAge time.Duration) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return isStale(c.entries, branch, maxAge)
}

func (w *Writable) IsStale(branch string, maxAge time.Duration) bool {
	return isStale(w.entries, branch, maxAge)
}

func isStale(entries map[string]*PRInfo, branch string, maxAge time.Duration) bool {
	info, ok := entries[branch]
	if !ok {
		return true
	}
	if isTerminal(info.Status) && maxAge < TerminalMaxAge {
		maxAge = TerminalMaxAge
	}
	return time.Since(info.FetchedAt) > maxAge
}
