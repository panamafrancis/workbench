// Package supatree manages sets of git worktrees — one per repo — for a single
// cross-repo issue. A "stack" is a git repo holding the repo selection
// (supatree.yml), agent instructions (AGENTS.md) and scripts; a supatree is a
// worktree of that stack repo, laid out as ~/supatree/trees/<name>/ with each
// member repo checked out under repos/<alias>/. Configuration, state and cache
// live under the XDG base directories (see paths.go).
package supatree

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/panamafrancis/workbench/pkg/xdg"
)

// toolName names supatree's directory under each XDG base.
const toolName = "supatree"

// ConfigDir holds what a person edits: config.yml and schedule.yml.
func ConfigDir() string {
	return filepath.Join(xdg.ConfigHome(), toolName)
}

// ConfigPath is supatree's config: stacks, defaults, models, repo settings.
func ConfigPath() string {
	return filepath.Join(ConfigDir(), "config.yml")
}

// StateRoot holds what supatree accumulates and would rather not lose: per-tree
// state, the ledger, the PM's queues and home, logs, locks.
func StateRoot() string {
	return filepath.Join(xdg.StateHome(), toolName)
}

// CacheDir holds what can be rebuilt: PR status, the watcher's last summary,
// generated layouts.
func CacheDir() string {
	return filepath.Join(xdg.CacheHome(), toolName)
}

// WorkspaceDir is the visible home of trees, stacks and the repo cache — the
// directories people cd into.
func WorkspaceDir() string {
	return filepath.Join(xdg.Home(), toolName)
}

// DefaultTreesBase is where supatrees are checked out unless overridden.
func DefaultTreesBase() string {
	return filepath.Join(WorkspaceDir(), "trees")
}

// StacksDir is where scaffolded stack repos live by default.
func StacksDir() string {
	return filepath.Join(WorkspaceDir(), "stacks")
}

// DefaultStackPath is the default location of a stack repo named name.
func DefaultStackPath(name string) string {
	return filepath.Join(StacksDir(), name)
}

// LayoutsDir holds generated Zellij layouts (transient).
func LayoutsDir() string {
	return filepath.Join(CacheDir(), "layouts")
}

// ArchiveDir holds what a removed supatree left behind — agent transcripts
// rescued from deletion so the PM can distil them at its own pace.
func ArchiveDir(tree string) string {
	return filepath.Join(StateRoot(), "archive", tree)
}

// LogsDir holds diagnostic logs.
func LogsDir() string {
	return filepath.Join(StateRoot(), "logs")
}

// AgentCacheDir is the one part of the cache a sandboxed agent may write: the
// PR status and comment caches its MCP tools refresh, and the recall log. The
// rest of the cache — layouts above all, which zellij runs outside any sandbox,
// and the watcher's own state — is never granted.
func AgentCacheDir() string {
	return filepath.Join(CacheDir(), "agent")
}

// PRCachePath is the per-branch PR status cache shared by CLI and sidebar.
func PRCachePath() string {
	return filepath.Join(AgentCacheDir(), "pr-status.json")
}

// LockPath is the advisory lock file serializing registry mutations.
func LockPath() string {
	return filepath.Join(StateRoot(), "config.lock")
}

// LegacyDir is the pre-XDG home of everything (~/.supatree). Only the
// old-layout guard and `supatree migrate` look at it.
func LegacyDir() string {
	return filepath.Join(xdg.Home(), ".supatree")
}

// stateDirName is the gitignored link, inside a tree, to that tree's state.
const stateDirName = ".supatree"

// TreesStateDir holds every tree's state, one directory per tree. It is what
// List scans, and what the PM is granted as a whole: a tree created after the
// PM started is inside it already, so nothing has to be relaunched.
func TreesStateDir() string {
	return filepath.Join(StateRoot(), "trees")
}

// StateDir returns the state directory for the supatree rooted at root.
//
// It lives outside the tree, under TreesStateDir, so a sandbox can be granted
// all tree state without being granted anything under any tree's repos/. The
// tree keeps a .supatree symlink to it (LinkState), so every path agents and
// docs use — .supatree/info.md — reads the same as it always did.
//
// A tree that already has its link resolves through it, so a tree root whose
// directory name is not its name (or that was moved) still finds its state.
// The PM is rooted in no tree; its own home is its state directory.
func StateDir(root string) string {
	if filepath.Clean(root) == PMDir() {
		return PMDir()
	}
	if target, err := os.Readlink(StateLink(root)); err == nil && filepath.IsAbs(target) {
		return target
	}
	return filepath.Join(TreesStateDir(), filepath.Base(root))
}

// StateLink is the .supatree symlink inside a tree root.
func StateLink(root string) string {
	return filepath.Join(root, stateDirName)
}

// LinkState creates the tree's state directory and points the tree's
// .supatree link at it. Idempotent.
func LinkState(root string) error {
	dir := StateDir(root)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("create state dir: %w", err)
	}
	if dir == root {
		return nil
	}
	link := StateLink(root)
	if target, err := os.Readlink(link); err == nil {
		if target == dir {
			return nil
		}
		return fmt.Errorf("%s points at %s, not this tree's state (%s)", link, target, dir)
	}
	if _, err := os.Lstat(link); err == nil {
		return fmt.Errorf("%s exists and is not a link to this tree's state", link)
	}
	if err := os.MkdirAll(root, 0755); err != nil {
		return err
	}
	return os.Symlink(dir, link)
}

// MetaPath returns the per-supatree metadata file.
func MetaPath(root string) string {
	return filepath.Join(StateDir(root), "meta.yml")
}

// AgentsPath returns the per-supatree agents file.
func AgentsPath(root string) string {
	return filepath.Join(StateDir(root), "agents.yml")
}

// BoardPath returns the PM-maintained status board. Like info.md it is
// generated and gitignored: status must not mean "read the PM's chat log",
// because scrollback is a terrible status display and people stop reading it
// by day three.
func BoardPath(root string) string {
	return filepath.Join(StateDir(root), "board.md")
}

// InfoPath returns the generated info file.
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
	return filepath.Join(LedgerDir(), "events.jsonl")
}

// LedgerDir holds the activity ledger. A directory of its own so the PM can be
// granted read access to it as a whole: rotation renames the file, and a grant
// on the file would follow the old inode on Linux.
func LedgerDir() string {
	return filepath.Join(StateRoot(), "ledger")
}

// EventsLockPath serializes appends to the ledger. Appends are O_APPEND and
// line-sized, but the lock also covers the read-modify-write in the notifier's
// dedupe state, which is written in the same step.
func EventsLockPath() string {
	return filepath.Join(LedgerDir(), "events.lock")
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
	return filepath.Join(StateRoot(), "watch.lock")
}

// RequestsPath is the inbound queue anything can append to in order to reach
// the PM agent: the sidebar, the watcher, the scheduler, a shell.
func RequestsPath() string {
	return filepath.Join(RequestsDir(), "requests.jsonl")
}

// RequestsDir holds the PM's inbound queue, which the PM may read and nothing
// sandboxed may write.
func RequestsDir() string {
	return filepath.Join(StateRoot(), "requests")
}

// NotifyPath is the outbox for processes that cannot notify for themselves.
// Only the watcher runs outside the nono sandbox, so it is the only process
// that can reach the desktop; everything else appends here and the watcher
// delivers it through the same tier policy as its own events.
func NotifyPath() string {
	return filepath.Join(OutboxDir(), "notify.jsonl")
}

// OutboxDir holds the notification outbox and its lock, which the PM is
// granted as a directory.
func OutboxDir() string {
	return filepath.Join(StateRoot(), "outbox")
}

// NotifyLockPath serializes appends to and drains of the outbox.
func NotifyLockPath() string {
	return filepath.Join(OutboxDir(), "notify.lock")
}

// PMDir is the PM agent's own root (<state>/pm). It is deliberately not a
// supatree: a PM that manages many of them cannot be rooted in one.
func PMDir() string {
	return filepath.Join(StateRoot(), "pm")
}

// PMOffsetPath records how far the PM has read into the request queue. It lives
// with the PM rather than with the queue because it is the reader's position,
// not a property of the history.
func PMOffsetPath() string {
	return filepath.Join(PMDir(), "requests.offset")
}

// RequestsLockPath serializes appends to the request queue.
func RequestsLockPath() string {
	return filepath.Join(RequestsDir(), "requests.lock")
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

// OldLayoutMessage is what every command says while the old layout is live.
const OldLayoutMessage = "supatree's files are still in ~/.supatree from an older version — close every supatree " +
	"(supatree rm), quit every st-* zellij session, then run: supatree migrate"

// OldLayout reports whether ~/.supatree is still where supatree's files are:
// it exists and nothing has been migrated to the XDG config path yet.
func OldLayout() bool {
	if _, err := os.Stat(LegacyDir()); err != nil {
		return false
	}
	_, err := os.Stat(ConfigPath())
	return os.IsNotExist(err)
}
