package supatree

import (
	"fmt"
	"os"
	"strings"

	"github.com/panamafrancis/workbench/pkg/git"
	"github.com/panamafrancis/workbench/pkg/github"
	"github.com/panamafrancis/workbench/pkg/sandbox"
	"github.com/panamafrancis/workbench/pkg/zellij"
)

// RemoveOptions parameterizes Remove.
type RemoveOptions struct {
	Force bool // remove even when the stack worktree has uncommitted changes
	Push  bool // push the st/<name> branch before deleting it locally
}

// RemoveResult reports what Remove did.
type RemoveResult struct {
	Warnings []string
	Agents   []Agent  // agents that existed (so the caller can clean their tabs)
	Members  []string // member aliases (a member agent's tab is <tree>:<alias>)
	Archived int      // transcripts rescued from deletion into ArchiveDir
}

// Remove tears down a supatree: every member worktree (reverse dependency
// order), then the stack meta-worktree and its branch,
// then leftover files and PR-cache entries. It tolerates a half-created tree.
// It refuses when the stack worktree has uncommitted tracked changes unless
// Force or Push is set (Push preserves the work on the remote first).
func Remove(c *Config, name string, opts RemoveOptions) (*RemoveResult, error) {
	inst, err := Get(c, name)
	if err != nil {
		return nil, err
	}
	res := &RemoveResult{Members: inst.MemberAliases()}
	res.Agents, _ = LoadAgents(inst.Root)

	if dirty, _ := hasUncommittedChanges(inst.Root); dirty && !opts.Force && !opts.Push {
		return nil, fmt.Errorf("supatree %q has uncommitted changes to its stack files; re-run with --push to save them or --force to discard", name)
	}

	stack := c.FindStack(inst.Stack)

	// Members, reverse dependency order.
	var removed []string
	for i := len(inst.Members) - 1; i >= 0; i-- {
		m := inst.Members[i]
		report := &SyncReport{}
		removeMember(c, inst.Root, m.Alias, m.URL, m.Branch, report)
		res.Warnings = append(res.Warnings, report.Warnings...)
		archiveSessions(m.Path, name, &res.Archived)
		_ = sandbox.ClearSessionCache(m.Path)
		removed = append(removed, m.CacheKey())
	}
	// One mutation once the slow work is done: holding the cache lock across
	// worktree removal would stall every sidebar's round.
	_ = github.NewCache(PRCachePath()).Mutate(func(w *github.Writable) error {
		for _, branch := range removed {
			w.Delete(branch)
		}
		return nil
	})

	// Stack meta-worktree.
	if opts.Push {
		if err := pushBranchAndDeleteOld(inst.Root, ""); err != nil {
			res.Warnings = append(res.Warnings, fmt.Sprintf("push %s: %v", name, err))
		}
	}
	archiveSessions(inst.Root, name, &res.Archived)
	_ = sandbox.ClearSessionCache(inst.Root)
	if stack != nil {
		if err := git.RemoveWorktree(stack.Path, inst.Root); err != nil {
			res.Warnings = append(res.Warnings, fmt.Sprintf("remove worktree: %v", err))
		}
		_ = git.DeleteBranch(stack.Path, "st/"+name)
	}
	// State before the root: the root's .supatree link is how StateDir finds
	// it, and state left behind is a tree List would keep reporting.
	if err := os.RemoveAll(StateDir(inst.Root)); err != nil {
		res.Warnings = append(res.Warnings, fmt.Sprintf("remove state: %v", err))
	}
	if err := os.RemoveAll(inst.Root); err != nil {
		res.Warnings = append(res.Warnings, fmt.Sprintf("remove dir: %v", err))
	}
	return res, nil
}

// hasUncommittedChanges reports whether the stack worktree has staged or
// unstaged changes to tracked files (member worktrees and .supatree/ are
// gitignored, so they do not count).
func hasUncommittedChanges(root string) (bool, error) {
	out, err := gitOutput(root, "status", "--porcelain", "--untracked-files=no")
	if err != nil {
		return false, err
	}
	return out != "", nil
}

// archiveSessions rescues an agent's transcripts before the cache is cleared.
//
// Silent and best effort on purpose. Removing a supatree is the user's
// instruction and must not fail because a transcript could not be copied; the
// cost of a miss is one lost history, and the cost of a hard failure is a
// half-removed supatree.
func archiveSessions(path, tree string, total *int) {
	n, err := sandbox.ArchiveSessionCache(path, ArchiveDir(tree))
	if err != nil {
		return
	}
	*total += n
}

// CloseTree closes every tab a removed tree had open, in every live session of
// ws, and deletes the layouts they were opened from.
//
// Closing a tab ends the processes in it, agents included: this is what makes
// removing a tree stop its agents rather than leave them running against a
// directory that no longer exists (supatree#2). It runs after the removal,
// because the caller may be a sidebar inside one of those tabs and does not
// survive the close.
func CloseTree(ws zellij.Workspace, tree string, res *RemoveResult) []string {
	names := map[string]bool{tree: true}
	if res != nil {
		for _, a := range res.Agents {
			names[TabName(tree, a.Name)] = true
		}
		for _, alias := range res.Members {
			names[TabName(tree, alias)] = true
		}
	}
	for name := range names {
		ws.CleanupLayout(name)
	}
	// Any "<tree>:…" tab is this tree's, whether or not an agent was ever
	// recorded for it — a tab opened by hand counts too.
	match := func(name string) bool { return names[name] || strings.HasPrefix(name, tree+":") }

	var warnings []string
	sessions, err := zellij.ListSessions()
	if err != nil {
		return []string{fmt.Sprintf("could not list zellij sessions to close %s's tabs: %v", tree, err)}
	}
	for _, s := range sessions {
		if s.Exited || !strings.HasPrefix(s.Name, ws.SessionPrefix) {
			continue
		}
		if _, err := zellij.CloseTabsIn(s.Name, match); err != nil {
			warnings = append(warnings, fmt.Sprintf("%s: %v", s.Name, err))
		}
	}
	return warnings
}
