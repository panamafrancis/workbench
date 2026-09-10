package supatree

import (
	"fmt"
	"os"

	"github.com/panamafrancis/workbench/pkg/config"
	"github.com/panamafrancis/workbench/pkg/git"
	"github.com/panamafrancis/workbench/pkg/github"
	"github.com/panamafrancis/workbench/pkg/sandbox"
)

// RemoveOptions parameterizes Remove.
type RemoveOptions struct {
	Force bool // remove even when the stack worktree has uncommitted changes
	Push  bool // push the st/<name> branch before deleting it locally
}

// RemoveResult reports what Remove did.
type RemoveResult struct {
	Warnings []string
	Agents   []Agent // agents that existed (so the caller can clean their tabs)
}

// Remove tears down a supatree: every member worktree (reverse dependency
// order, running cleanup scripts), then the stack meta-worktree and its branch,
// then leftover files and PR-cache entries. It tolerates a half-created tree.
// It refuses when the stack worktree has uncommitted tracked changes unless
// Force or Push is set (Push preserves the work on the remote first).
func Remove(c *Config, wb *config.Config, name string, opts RemoveOptions) (*RemoveResult, error) {
	inst, err := Get(c, name)
	if err != nil {
		return nil, err
	}
	res := &RemoveResult{}
	res.Agents, _ = LoadAgents(inst.Root)

	if dirty, _ := hasUncommittedChanges(inst.Root); dirty && !opts.Force && !opts.Push {
		return nil, fmt.Errorf("supatree %q has uncommitted changes to its stack files; re-run with --push to save them or --force to discard", name)
	}

	stack := c.FindStack(inst.Stack)

	// Members, reverse dependency order.
	var removed []string
	for i := len(inst.Members) - 1; i >= 0; i-- {
		m := inst.Members[i]
		if repo, _ := wb.FindRepo(m.Alias); repo != nil && m.Exists {
			if err := repo.RunCleanup(m.Path, name); err != nil {
				res.Warnings = append(res.Warnings, fmt.Sprintf("%s cleanup: %v", m.Alias, err))
			}
		}
		removeMember(inst.Root, m.Alias, wb, &SyncReport{Warnings: res.Warnings})
		_ = sandbox.ClearSessionCache(m.Path)
		removed = append(removed, m.Branch)
	}
	// One mutation once the slow work is done: holding the cache lock across
	// cleanup scripts and worktree removal would stall every sidebar's round.
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
	_ = sandbox.ClearSessionCache(inst.Root)
	if stack != nil {
		if err := git.RemoveWorktree(stack.Path, inst.Root); err != nil {
			res.Warnings = append(res.Warnings, fmt.Sprintf("remove worktree: %v", err))
		}
		_ = git.DeleteBranch(stack.Path, "st/"+name)
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
