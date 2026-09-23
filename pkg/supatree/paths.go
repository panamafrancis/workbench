// Package supatree manages sets of git worktrees — one per repo — for a single
// cross-repo issue. A "stack" is a git repo holding the repo selection
// (supatree.yml), agent instructions (AGENTS.md) and scripts; a supatree is a
// worktree of that stack repo, laid out as ~/.supatree/trees/<name>/ with each
// member repo checked out under repos/<alias>/. Repo definitions (alias →
// local_path, copy_files, scripts) are resolved from workbench's config.
package supatree

import (
	"os"
	"path/filepath"
)

// Dir is the root of all supatree state (~/.supatree).
func Dir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".supatree")
}

// ConfigPath is the registry file listing stacks and defaults.
func ConfigPath() string {
	return filepath.Join(Dir(), "config.yml")
}

// DefaultTreesBase is where supatrees are checked out unless overridden.
func DefaultTreesBase() string {
	return filepath.Join(Dir(), "trees")
}

// StacksDir is where scaffolded stack repos live by default.
func StacksDir() string {
	return filepath.Join(Dir(), "stacks")
}

// DefaultStackPath is the default location of a stack repo named name.
func DefaultStackPath(name string) string {
	return filepath.Join(StacksDir(), name)
}

// LayoutsDir holds generated Zellij layouts (transient).
func LayoutsDir() string {
	return filepath.Join(Dir(), "layouts")
}

// ArchiveDir holds what a removed supatree left behind — agent transcripts
// rescued from deletion so the PM can distil them at its own pace.
func ArchiveDir(tree string) string {
	return filepath.Join(Dir(), "archive", tree)
}

// LogsDir holds diagnostic logs.
func LogsDir() string {
	return filepath.Join(Dir(), "logs")
}

// CacheDir holds transient caches (PR status).
func CacheDir() string {
	return filepath.Join(Dir(), "cache")
}

// PRCachePath is the per-branch PR status cache shared by CLI and sidebar.
func PRCachePath() string {
	return filepath.Join(CacheDir(), "pr-status.json")
}

// PRCacheLockPath serializes gh PR fetches across the independent sidebar
// processes (one per Zellij tab) so they don't all hit the gh API at once.
func PRCacheLockPath() string {
	return PRCachePath() + ".lock"
}

// LockPath is the advisory lock file serializing registry mutations.
func LockPath() string {
	return ConfigPath() + ".lock"
}

// stateDirName is the gitignored per-supatree state directory inside a tree.
const stateDirName = ".supatree"

// StateDir returns the state directory for a supatree rooted at root.
func StateDir(root string) string {
	return filepath.Join(root, stateDirName)
}

// MetaPath returns the per-supatree metadata file inside a tree root.
func MetaPath(root string) string {
	return filepath.Join(StateDir(root), "meta.yml")
}

// AgentsPath returns the per-supatree agents file inside a tree root.
func AgentsPath(root string) string {
	return filepath.Join(StateDir(root), "agents.yml")
}

// BoardPath returns the PM-maintained status board inside a tree root. Like
// info.md it is generated and gitignored: status must not mean "read the PM's
// chat log", because scrollback is a terrible status display and people stop
// reading it by day three.
func BoardPath(root string) string {
	return filepath.Join(StateDir(root), "board.md")
}

// InfoPath returns the generated info file inside a tree root.
func InfoPath(root string) string {
	return filepath.Join(StateDir(root), "info.md")
}

// SpecName is the tracked repo-selection file at a stack/supatree root.
const SpecName = "supatree.yml"

// AgentsMDName is the tracked agent-instructions file at a stack/supatree root.
const AgentsMDName = "AGENTS.md"

// ReposDirName is the gitignored directory holding member worktrees.
const ReposDirName = "repos"

// MemberPath returns the on-disk path of a member repo worktree within a tree.
func MemberPath(root, alias string) string {
	return filepath.Join(root, ReposDirName, alias)
}

// DashTab is the Zellij tab name the dashboard opens into. It is deliberately
// not a plausible supatree name, since tab names key tab lookups and a
// collision would focus the wrong tab.
const DashTab = "supatree-dash"

// EventsPath is the append-only activity ledger the watcher writes and the
// dashboard, the PM's `events` tool and the sidebar's attention marker read.
func EventsPath() string {
	return filepath.Join(Dir(), "events.jsonl")
}

// EventsLockPath serializes appends to the ledger. Appends are O_APPEND and
// line-sized, but the lock also covers the read-modify-write in the notifier's
// dedupe state, which is written in the same step.
func EventsLockPath() string {
	return EventsPath() + ".lock"
}

// LastSummaryPath holds the previous round's Status summary. Events are the
// diff between it and the current one, so it is the watcher's whole memory.
func LastSummaryPath() string {
	return filepath.Join(CacheDir(), "last-status.json")
}

// NotifyStatePath holds the notifier's per-key dedupe and dwell state. It sits
// beside the summary because the two are written together and are equally
// disposable: losing both re-seeds silently on the next round.
func NotifyStatePath() string {
	return filepath.Join(CacheDir(), "notify-state.json")
}

// WatchLockPath elects the single watcher process. It is held for the whole
// life of the daemon, so a concurrent `watch --once` gets ErrLockBusy and
// exits quietly — that is what stops a cron entry double-fetching.
func WatchLockPath() string {
	return filepath.Join(Dir(), "watch.lock")
}

// RequestsPath is the inbound queue anything can append to in order to reach
// the PM agent: the sidebar, the watcher, the scheduler, a shell.
func RequestsPath() string {
	return filepath.Join(Dir(), "requests.jsonl")
}

// NotifyPath is the outbox for processes that cannot notify for themselves.
// Only the watcher runs outside the nono sandbox, so it is the only process
// that can reach the desktop; everything else appends here and the watcher
// delivers it through the same tier policy as its own events.
func NotifyPath() string {
	return filepath.Join(Dir(), "notify.jsonl")
}

// PMDir is the PM agent's own root (~/.supatree/pm). It is deliberately not a
// supatree: a PM that manages many of them cannot be rooted in one.
func PMDir() string {
	return filepath.Join(Dir(), "pm")
}

// PMOffsetPath records how far the PM has read into the request queue. It lives
// with the PM rather than with the queue because it is the reader's position,
// not a property of the history.
func PMOffsetPath() string {
	return filepath.Join(PMDir(), "requests.offset")
}

// RequestsLockPath serializes appends to the request queue.
func RequestsLockPath() string {
	return RequestsPath() + ".lock"
}

// PMTab is the Zellij tab name the PM opens into. Reserved the same way DashTab
// is, so it can never collide with a supatree name — and, because
// OpenOrFocusTab focuses a live tab rather than opening a second one, it is
// also what makes the PM a singleton.
const PMTab = "supatree-pm"

// LaunchPath is the queue of agents the PM has asked to have started. It lives
// in the PM's own directory because that is the one place its sandbox is
// guaranteed to write; the watcher, outside the sandbox, drains it and opens
// the tabs.
func LaunchPath() string {
	return filepath.Join(PMDir(), "launch.jsonl")
}

// LaunchLockPath serializes appends to and drains of the launch queue.
func LaunchLockPath() string {
	return LaunchPath() + ".lock"
}
