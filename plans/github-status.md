# GitHub PR Status for Worktrees

## Goal

Show GitHub PR status alongside each worktree in the TUI — whether a PR has been opened, is in draft, has been merged, or is closed. Use `gh` CLI for data, with a caching layer that avoids constant polling.

---

## Data Model

### PR Status

```go
// pkg/github/status.go

type PRStatus string

const (
    PRNone   PRStatus = ""       // no PR exists for this branch
    PRDraft  PRStatus = "draft"
    PROpen   PRStatus = "open"
    PRMerged PRStatus = "merged"
    PRClosed PRStatus = "closed"
)

type PRInfo struct {
    Number    int      `json:"number"`
    Status    PRStatus `json:"status"`
    Title     string   `json:"title"`
    URL       string   `json:"url"`
    UpdatedAt time.Time `json:"updated_at"`   // from GitHub
    FetchedAt time.Time `json:"fetched_at"`   // when we last queried
}
```

### Cache File

Stored at `~/.workbench/cache/pr-status.json`. Separate from `config.yml` — this is ephemeral derived data, not user config.

```json
{
  "entries": {
    "wt/ss/swift-atlanta": {
      "number": 142,
      "status": "open",
      "title": "Add rate limiting to scoring endpoint",
      "url": "https://github.com/org/repo/pull/142",
      "updated_at": "2026-06-09T10:00:00Z",
      "fetched_at": "2026-06-09T10:30:00Z"
    },
    "wt/pay/calm-tokyo": {
      "status": "",
      "fetched_at": "2026-06-09T10:30:00Z"
    }
  }
}
```

Key is the git branch name (not worktree name), since that's what maps to GitHub PRs.

---

## `gh` CLI Integration

### Package: `pkg/github/`

Two files:

**`pkg/github/gh.go`** — wraps `gh` CLI calls.

```go
func LookupPR(repoPath, branch string) (*PRInfo, error)
```

Implementation: runs `gh pr list --head <branch> --state all --json number,state,title,url,isDraft,updatedAt --limit 1` from within the repo's `LocalPath` directory (the original clone, not the worktree path — more reliable for `gh` remote resolution, though worktrees share the same git config). Parses JSON output. If no results, returns `PRInfo{Status: PRNone}`. If `gh` is not installed or not authenticated, returns a clear error on the first call and degrades gracefully (no PR badges shown, no retries until manual refresh).

**Important:** `--state all` is required — without it, `gh pr list` only returns open PRs, so merged/closed PRs would never appear.

**`pkg/github/cache.go`** — cache layer.

```go
type Cache struct {
    path    string
    entries map[string]*PRInfo // keyed by branch name
    mu      sync.RWMutex
}

func NewCache(path string) *Cache
func (c *Cache) Load() error                              // read from disk
func (c *Cache) Save() error                              // write to disk (atomic)
func (c *Cache) Get(branch string) *PRInfo                // nil if missing
func (c *Cache) Set(branch string, info *PRInfo)
func (c *Cache) IsStale(branch string, maxAge time.Duration) bool
```

Cache is loaded once at TUI startup. Saved to disk after each batch of fetches completes (not per-entry — avoids excessive I/O).

---

## Refresh Strategy

Three tiers of freshness, designed to minimize `gh` calls:

| Tier | What | Refresh interval | Trigger |
|------|------|------------------|---------|
| **Active** | Currently selected worktree in TUI | 5 minutes | Bubble Tea ticker |
| **Visible** | All expanded (non-collapsed) worktrees | 15 minutes | Piggyback on active refresh cycle |
| **Background** | Collapsed/off-screen worktrees | Never auto-refreshed | Manual `r` key only |

### How it works in practice

1. **TUI startup**: Load cache from disk. Display cached values immediately (instant render, no blocking). Fire a one-shot command to refresh all visible worktrees in the background.

2. **Ticker (every 60 seconds)**: A Bubble Tea `tea.Tick` fires every 60s. On tick:
   - Check if the active worktree's cache entry is older than 5 minutes. If so, fetch it.
   - Check if any visible worktree's cache entry is older than 15 minutes. If so, fetch those too.
   - All fetches run in a single goroutine that processes them sequentially (not parallel — avoid hammering `gh` rate limits).

3. **Manual refresh (`r` key)**: Force-refresh all visible worktrees immediately, ignoring cache age. Shows "refreshing..." in status bar.

4. **Cursor movement**: When the user navigates to a different worktree, if that worktree has no cached PR data at all (never fetched), trigger a single fetch for it.

5. **`gh` unavailable**: Distinguish permanent vs transient failures. If `gh` binary is not found (`exec.ErrNotFound`) or auth fails (exit code 4 / "not logged in"), set `ghAvailable = false` — skip all future automatic fetches. For transient errors (network timeout, rate limit), do NOT disable `gh` — just skip that fetch and retry on the next tick. The `r` key always attempts a refresh regardless of the flag (in case the user just ran `gh auth login`), and resets `ghAvailable = true` on success. Show a hint in the status bar: "gh not found" or "gh auth required".

### Concurrency

All `gh` calls happen in Bubble Tea commands (goroutines that return messages). The TUI model itself is single-threaded as Bubble Tea requires. Pattern:

```go
type prStatusMsg struct {
    branch string
    info   *PRInfo
    err    error
}

type prBatchDoneMsg struct{}

func fetchPRStatus(repoPath, branch string) tea.Cmd {
    return func() tea.Msg {
        info, err := github.LookupPR(repoPath, branch)
        return prStatusMsg{branch: branch, info: info, err: err}
    }
}
```

Batch fetches are sequential within a single goroutine to avoid spawning N `gh` processes:

```go
type fetchTarget struct {
    repoPath string // Repo.LocalPath — the original clone, not worktree.Path
    branch   string
}

func fetchBatch(worktrees []fetchTarget, cache *github.Cache) tea.Cmd {
    return func() tea.Msg {
        for _, wt := range worktrees {
            info, err := github.LookupPR(wt.repoPath, wt.branch)
            if err != nil { continue }
            cache.Set(wt.branch, info)
        }
        cache.Save()
        return prBatchDoneMsg{}
    }
}
```

---

## TUI Display

### Worktree line format

Current:
```
  ● swift-atlanta    wt/ss/swift-atlanta*  [claude]
```

New:
```
  ● swift-atlanta    wt/ss/swift-atlanta*  [claude]  ⬆ #142    ← entire line green
  ● calm-tokyo       wt/pay/calm-tokyo     [codex]   ✎ #87     ← entire line grey
  ● wide-berlin      wt/ss/wide-berlin     [claude]  ✓ #201    ← entire line magenta
  ● old-paris        wt/ss/old-paris       [claude]             ← default (white)
```

**The PR status color applies to the entire worktree line**, not just the icon. This makes it easy to scan at a glance which worktrees have open PRs, which are merged, etc. When a worktree is selected (cursor on it), the line uses bold + the PR status color (or bold cyan if no PR, preserving the current selected style).

Status indicators (single character + optional PR number):

| Status | Icon | Line Color | Meaning |
|--------|------|------------|---------|
| No PR | (nothing) | white (default) | No PR opened yet |
| Draft | `✎` | grey/muted | Draft PR exists |
| Open | `⬆` | green | PR is open for review |
| Merged | `✓` | magenta/purple | PR has been merged |
| Closed | `✗` | red | PR was closed without merging |

### New styles in `styles.go`

The PR status styles replace the base worktree line style entirely:

```go
colorGreen   = lipgloss.Color("10")
colorRed     = lipgloss.Color("9")
colorMagenta = lipgloss.Color("13")

stylePRNone   = styleWorktree                                          // white — no PR
stylePRDraft  = lipgloss.NewStyle().Foreground(colorMuted)             // grey
stylePROpen   = lipgloss.NewStyle().Foreground(colorGreen)             // green
stylePRMerged = lipgloss.NewStyle().Foreground(colorMagenta)           // magenta
stylePRClosed = lipgloss.NewStyle().Foreground(colorRed)               // red

// Selected variants: bold + PR color
stylePRNoneSelected   = styleSelected                                  // bold cyan — preserved
stylePRDraftSelected  = lipgloss.NewStyle().Foreground(colorMuted).Bold(true)
stylePROpenSelected   = lipgloss.NewStyle().Foreground(colorGreen).Bold(true)
stylePRMergedSelected = lipgloss.NewStyle().Foreground(colorMagenta).Bold(true)
stylePRClosedSelected = lipgloss.NewStyle().Foreground(colorRed).Bold(true)
```

In `tree.go:view()`, instead of always using `styleWorktree`/`styleSelected`, pick the style based on the worktree's cached PR status first, then apply selected variant if the cursor is on that line.

**Dirty indicator:** The `*` dirty marker currently uses `styleDirty` (yellow) rendered inline. With whole-line PR coloring, the line-level `Render()` call would override it. To preserve the yellow dirty signal, build the line in two segments: render the dirty `*` with `styleDirty` first, then render the rest of the line with the PR status style. Lipgloss inline styles compose correctly — the `*` keeps its yellow foreground even when the surrounding text is green/magenta/etc.

### Status bar

When a fetch is in progress, the status bar shows:
```
[n]ew  [o]pen  [d]el  [r]efresh  [q]uit  ⟳ syncing...
```

If `gh` is not available:
```
[n]ew  [o]pen  [d]el  [r]efresh  [q]uit  (gh CLI not found)
```

---

## Changes to Existing Code

### `pkg/tui/model.go`

- Add `prCache *github.Cache` field to `Model`.
- Add `ghAvailable bool` field (defaults true, set false on first `gh` error).
- Add `fetching bool` field for status bar display.
- In `New()`: create cache, load from disk, fire initial background fetch command.
- In `Init()`: return `tea.Batch(tickCmd(), initialFetchCmd)`. The tick handler in `Update()` must return a new `tickCmd()` to keep the 60-second cycle alive (`tea.Tick` fires once, not repeatedly).
- In `Update()`: handle `prStatusMsg`, `prBatchDoneMsg`, `tickMsg`.
- On cursor move to a worktree with no cache entry: fire single fetch.

### `pkg/tui/tree.go`

- `view()`: after the model badge, append PR status icon + number using cache lookup.
- Add `prCache *github.Cache` field to `TreeModel` (or pass cache via view method parameter).

### `pkg/tui/styles.go`

- Add green, red, magenta colors and corresponding PR status styles.

### `pkg/config/paths.go`

- Add `CacheDir() string` returning `~/.workbench/cache/`.
- Add `PRCachePath() string` returning `~/.workbench/cache/pr-status.json`.

---

## New Files

```
pkg/github/
├── gh.go           # LookupPR — wraps `gh pr list` CLI call
├── gh_test.go      # Tests with mock gh output
├── cache.go        # Cache struct, load/save, staleness checks
└── cache_test.go   # Cache tests
```

---

## Implementation Phases

### Phase 1: `pkg/github/` package

1. `pkg/github/gh.go` — `LookupPR(repoPath, branch string) (*PRInfo, error)`
   - Runs: `gh pr list --head <branch> --state all --json number,state,title,url,isDraft,updatedAt --limit 1`
   - Sets working directory to `Repo.LocalPath` (original clone) so `gh` picks up the right remote
   - Parses JSON, maps `state`+`isDraft` to `PRStatus`
   - Returns `PRInfo{Status: PRNone}` when no PR found
   - Returns error if `gh` not found or auth fails

2. `pkg/github/cache.go` — `Cache` with `Load`, `Save`, `Get`, `Set`, `IsStale`
   - JSON file at `~/.workbench/cache/pr-status.json`
   - Atomic save (write tmp + rename)
   - `IsStale` checks `FetchedAt` against provided `maxAge`

3. Tests for both.

### Phase 2: TUI integration — display

1. Add PR status styles to `styles.go`.
2. Add `CacheDir`/`PRCachePath` to `pkg/config/paths.go`.
3. Pass cache into `TreeModel`. Update `tree.go:view()` to render PR status icon + number after the model badge.
4. Load cache in `tui.New()` and display immediately.

### Phase 3: TUI integration — refresh logic

1. Add tick infrastructure to `model.go` (60-second `tea.Tick`).
2. On tick: check staleness of active + visible worktrees, fire batch fetch command.
3. Handle `prStatusMsg` / `prBatchDoneMsg` in `Update()`.
4. On cursor move to uncached worktree: fire single fetch.
5. `r` key: force-refresh all visible worktrees.
6. Handle `gh` unavailable gracefully — set flag, skip auto-fetches, show hint.
7. Show "syncing..." in status bar during fetch.

### Phase 4: Edge cases and polish

1. Worktrees whose branch has been deleted from remote (PR merged + branch deleted) — cache the merged status, don't re-fetch.
2. Multiple worktrees with the same branch (unusual but possible) — cache handles this naturally since key is branch name.
3. Config reload (`r` key currently refreshes dirty status) — also refresh PR status.
4. New worktree created — has no cache entry, will be fetched on first view.
5. `gh` auth token expiry mid-session — next fetch fails, set `ghAvailable = false`, user can `r` to retry.

---

## Verification Checklist

1. TUI starts instantly with cached PR data (no blocking on `gh` calls).
2. Active worktree PR status refreshes within 5 minutes.
3. Visible worktrees refresh within 15 minutes.
4. Collapsed worktrees are never auto-refreshed.
5. `r` key force-refreshes and shows "syncing..." feedback.
6. `gh` not installed → graceful degradation, hint shown, no crashes or retries.
7. `gh` not authenticated → same graceful degradation.
8. PR opened/merged/closed → status updates on next refresh cycle.
9. No more than 1 `gh` process running at a time (sequential batch).
10. Cache persists across TUI restarts.
11. Cache file is created automatically on first use.

---

## Key Decisions

- **`gh` CLI, not GitHub API**: Avoids token management — `gh` handles auth. Users already have it for PR workflows. If they don't, the feature degrades gracefully.
- **Branch-keyed cache, not worktree-keyed**: A branch maps 1:1 to a PR. If a worktree is deleted and recreated with the same branch, the cache still applies.
- **Sequential fetches, not parallel**: One `gh` call at a time prevents rate limiting and process spam. With <20 worktrees typical, sequential is fast enough (~0.5s per call).
- **60-second tick, not per-entry timers**: Single timer simplifies the model. The tick checks staleness — entries that are fresh are skipped. This means actual `gh` calls are rare (once every 5 min for active, once every 15 min for visible).
- **Separate cache file, not in config.yml**: PR status is ephemeral derived data. Keeping it out of config avoids config churn and keeps the cache independently deletable.
- **No CI/checks status in v1**: PR status (open/merged/closed/draft) is the highest-value signal. CI status is a separate `gh` call per PR and adds complexity. Can be added later as a follow-up.
