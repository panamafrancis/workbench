# Worktree Process Lifecycle Management

## Problem

Three issues with how worktree processes are managed:

1. **Crash on re-open**: Opening a worktree that already has a running Zellij tab creates a duplicate tab (or crashes). There's no check for existing tabs — `GoToTab()` exists but is never called.

2. **No cleanup on tab close**: When a user closes a Zellij tab, workbench doesn't know. The process dies (Zellij sends SIGTERM), but layout files persist and state is stale.

3. **No cleanup on session exit**: When Zellij exits, all nono processes are killed by the OS. This is fine — but `workbench rm worktree` doesn't check if a tab is still open, risking file corruption.

## Available Zellij APIs

| Command | What it does | Limitation |
|---------|-------------|------------|
| `zellij action query-tab-names` | Lists all tab names, one per line | Read-only |
| `zellij action go-to-tab-name <name>` | Navigates to tab by name | Tab must exist |
| `zellij action new-tab --name <name> --layout <kdl>` | Creates a new tab | No duplicate check |
| `zellij action close-tab` | Closes the **current** tab | Cannot close by name |

**Key constraint:** There is no `close-tab-by-name`. We can only close the tab we're currently on. This means workbench cannot remotely kill a worktree's tab — it can only navigate to it first, then close it.

---

## Design

### Principle: Zellij owns process lifecycle, workbench owns navigation

Zellij already handles process termination correctly:
- Tab close → SIGTERM to pane process → nono terminates → LLM exits
- Session exit → all processes killed

Workbench should not try to replicate this. Instead, workbench should:
1. **Know which tabs are alive** (query Zellij)
2. **Navigate to existing tabs** instead of creating duplicates
3. **Show tab state in the TUI** so the user knows what's running
4. **Warn before destructive operations** on active worktrees

### Change 1: `TabNames` + `OpenOrFocusTab` (returns created bool)

Replace the current "always create" behavior with "focus if exists, create if not." The key design choice: `OpenOrFocusTab` returns `(created bool, err error)` so callers can skip startup scripts and nonoArgs construction when focusing an existing tab.

```go
// pkg/zellij/client.go

func TabNames() (map[string]bool, error) {
    cmd := exec.Command("zellij", "action", "query-tab-names")
    out, err := cmd.Output()
    if err != nil {
        return nil, fmt.Errorf("zellij query-tab-names: %w", err)
    }
    names := make(map[string]bool)
    for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
        if line != "" {
            names[line] = true
        }
    }
    return names, nil
}

func OpenOrFocusTab(name, cwd string, nonoArgs []string) (created bool, err error) {
    tabs, err := TabNames()
    if err != nil {
        // Fall through to create on query error — better than hard-failing
        err = OpenTab(name, cwd, nonoArgs)
        return err == nil, err
    }
    if tabs[name] {
        return false, GoToTab(name)
    }
    err = OpenTab(name, cwd, nonoArgs)
    return err == nil, err
}
```

Callers use the `created` return to skip startup scripts:

```go
// cmd/open.go — restructured flow:
nonoArgs, err := sandbox.BuildNonoArgs(...)
created, err := zellij.OpenOrFocusTab(wt.Name, wt.Path, nonoArgs)
if created {
    repo.RunStartup(wt.Path, wt.Name)  // only on fresh tab
}
```

**Behavior change:**
- First open → creates tab, runs startup, starts nono/claude
- Second open → focuses existing tab (instant, no startup, no new process)
- Tab closed by user, then re-opened → creates fresh tab
- TabNames() fails → falls through to OpenTab (best-effort, not hard failure)

### Change 2: Show running state in the TUI

The TUI should show which worktrees have active Zellij tabs. This helps the user understand what's running.

```
  ● swift-atlanta    wt/ss/swift-atlanta   ▶ [claude]  ⬆ #142
  ● calm-tokyo       wt/pay/calm-tokyo       [codex]   ✎ #87
```

The `▶` indicator shows "this worktree has a running tab." This uses `query-tab-names` — same approach as the PR cache: query on startup, refresh periodically.

**Implementation in `TreeModel`:**

Store the raw `TabNames()` result directly — no intermediate copy needed. Look up `w.Name` against it in `view()`, same pattern as `prCache.Get()`.

```go
type TreeModel struct {
    // ...existing fields...
    openTabs map[string]bool // raw result from zellij.TabNames()
}

func (t *TreeModel) refreshRunning() {
    if !zellij.IsInZellij() {
        return
    }
    tabs, err := zellij.TabNames()
    if err != nil {
        return
    }
    t.openTabs = tabs
}
```

Guard with `IsInZellij()` so the TUI works in plain terminals without spawning failing `zellij` processes every 60s.

**Refresh strategy:** Piggyback on the existing tick (every 60s) and on the `r` key refresh. Also refresh after `openSelected` returns (the tab just opened, so running state changed). Since `query-tab-names` is a fast local IPC call (not a network request), it can run alongside the PR cache tick without concern.

### Change 3: Warn on `rm worktree` if tab is running

`cmd/rm_worktree.go` should check if the worktree has a running Zellij tab before deleting.

Merge the running-tab warning into the existing confirmation prompt (not a separate one):

```go
// Replace the existing single prompt with a context-aware one:
prompt := fmt.Sprintf("Remove worktree %q at %s?", name, wt.Path)
if zellij.IsInZellij() {
    tabs, _ := zellij.TabNames()
    if tabs[name] {
        prompt = fmt.Sprintf("Worktree %q has a running Zellij tab. Remove anyway?", name)
    }
}
fmt.Printf("%s [y/N] ", prompt)
```

Single prompt, not two. We cannot close the tab programmatically (no close-by-name API), but the warning tells the user what will happen.

### Change 4: Clean up stale layout files

Layout KDL files in `~/.workbench/layouts/` accumulate forever. Clean them up when:
- `rm worktree` succeeds → delete `layouts/<name>.kdl`
- On TUI startup → delete layout files for worktrees that no longer exist in config

```go
// pkg/zellij/layout.go

func CleanupLayout(name string) {
    path := filepath.Join(config.LayoutsDir(), name+".kdl")
    os.Remove(path)
}

func CleanupStaleLayouts(validNames map[string]bool) {
    entries, err := os.ReadDir(config.LayoutsDir())
    if err != nil {
        return
    }
    for _, e := range entries {
        name := strings.TrimSuffix(e.Name(), ".kdl")
        if !validNames[name] {
            os.Remove(filepath.Join(config.LayoutsDir(), e.Name()))
        }
    }
}
```

---

## What about Zellij exit killing everything?

**This already works correctly.** When Zellij exits:
1. All tab pane processes receive SIGTERM
2. nono forwards the signal to the sandboxed LLM
3. Processes exit

There's nothing workbench needs to do here. The user's concern about "processes running forever" is already handled by Zellij's own lifecycle management. The real issue was the crash on re-open (duplicate tabs), which Change 1 fixes.

**Edge case:** If the user runs `workbench open --no-zellij`, the nono process runs in their terminal — not managed by Zellij. This is intentional (the flag exists for non-Zellij use), and the user owns that process.

---

## Changes to Existing Code

### `pkg/zellij/client.go`
- Add `TabNames() (map[string]bool, error)` — queries `zellij action query-tab-names`
- Add `OpenOrFocusTab(name, cwd string, nonoArgs []string) error` — focus if exists, create if not
- Keep `OpenTab` and `GoToTab` as lower-level primitives

### `cmd/open.go`
- Restructure flow: build nonoArgs first, call `zellij.OpenOrFocusTab(...)`, only run startup script if `created == true`
- This avoids running startup scripts when merely focusing an existing tab

### `pkg/tui/model.go`
- Restructure `openSelected()`: call `zellij.OpenOrFocusTab(...)`, only run startup if `created == true`
- Add `runningMsg` type and handler
- Refresh running state (call `refreshRunning()`) after open, on tick, and on manual refresh
- Guard all `zellij.TabNames()` calls with `zellij.IsInZellij()` so TUI works in plain terminals

### `pkg/tui/tree.go`
- Add `openTabs map[string]bool` to `TreeModel` (raw TabNames result, not a copy)
- Add `refreshRunning()` method (guarded by `IsInZellij()`)
- Show `▶` indicator in `view()` for worktrees where `openTabs[w.Name]` is true

### `cmd/rm_worktree.go`
- Merge running-tab warning into existing confirmation prompt (single prompt, not two)
- Query `TabNames()` only when `IsInZellij()`

### `pkg/zellij/layout.go`
- Add `CleanupLayout(name string)` and `CleanupStaleLayouts(validNames map[string]bool)`

### `cmd/rm_worktree.go`
- Call `CleanupLayout(name)` after successful removal

---

## Implementation Phases

### Phase 1: Fix the crash (OpenOrFocusTab)
1. Add `TabNames()` to `pkg/zellij/client.go`
2. Add `OpenOrFocusTab()` to `pkg/zellij/client.go`
3. Update `cmd/open.go` to call `OpenOrFocusTab`
4. Update `pkg/tui/model.go:openSelected()` to call `OpenOrFocusTab`
5. Skip startup script when focusing existing tab

### Phase 2: Show running state in TUI
1. Add `running` map and `refreshRunning()` to `TreeModel`
2. Call `refreshRunning()` alongside `refreshDirty()`
3. Show `▶` indicator in `view()` for worktrees with active tabs
4. Refresh after successful open

### Phase 3: Safe deletion + layout cleanup
1. Add running-tab warning to `cmd/rm_worktree.go`
2. Add `CleanupLayout()` to `pkg/zellij/layout.go`
3. Call layout cleanup from `rm_worktree.go`
4. Add stale layout cleanup on TUI startup

---

## Verification Checklist

1. Open worktree → creates tab and starts nono
2. Open same worktree again → focuses existing tab (no crash, no duplicate)
3. Close tab in Zellij, then open again → creates fresh tab
4. TUI shows `▶` for running worktrees
5. `▶` disappears after tab is closed and TUI refreshes
6. `rm worktree` with active tab → shows warning
7. `rm worktree` without active tab → no warning, normal flow
8. Layout files cleaned up after `rm worktree`
9. Stale layout files cleaned up on TUI startup

---

## Key Decisions

- **Zellij owns process lifecycle**: We don't try to manage SIGTERM/SIGKILL. Zellij handles this correctly. Workbench just needs to know *what's running*.
- **`query-tab-names` is the source of truth**: It's a fast local IPC call. We query it rather than maintaining our own state (which would drift).
- **No close-by-name**: Zellij's `close-tab` only works on the current tab. We warn instead of trying to navigate+close (which would yank the user's focus). The user should close tabs themselves.
- **Startup script skipped on focus**: If the tab already exists, the LLM is already running. Re-running the startup script would be wrong (it might re-install deps, reset state, etc.).
- **Layout cleanup is best-effort**: These are tiny KDL files. If cleanup fails, no harm done.
