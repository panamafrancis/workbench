package supatree

import (
	"fmt"
	"os"
	"time"

	"github.com/panamafrancis/workbench/pkg/git"
)

// CreateOptions parameterizes New.
type CreateOptions struct {
	Stack       string    // stack alias; may be empty if exactly one is registered
	Name        string    // explicit supatree name; auto-generated when empty
	Model       string    // model override
	KeepPartial bool      // skip rollback on failure (for debugging)
	Now         time.Time // creation timestamp (injected for determinism)
	Intent      string    // what this supatree is for
	// Review, when set, makes this a review tree: each entry maps a member
	// alias to the pull request its worktree is checked out at. It must be set
	// before the members are created, since it is what decides where they start.
	Review map[string]ReviewRef
}

// New creates a supatree: a worktree of the chosen stack repo at
// <trees-base>/<name> on branch st/<name>, then creates one member worktree per
// repo listed in the stack's supatree.yml. On failure it rolls back everything
// it created unless KeepPartial is set.
func New(c *Config, opts CreateOptions) (*Instance, *SyncReport, error) {
	stack, err := resolveStack(c, opts.Stack)
	if err != nil {
		return nil, nil, err
	}

	name := opts.Name
	if name == "" {
		name, err = git.GenerateName(Names(c))
		if err != nil {
			return nil, nil, err
		}
	} else if err := git.ValidateName(name, Names(c)); err != nil {
		return nil, nil, err
	}

	root := treeRoot(c.ResolveTreesBase(), name)
	if _, statErr := os.Stat(root); statErr == nil {
		return nil, nil, fmt.Errorf("path %s already exists", root)
	}

	// The stack's .git is writable from inside every tree made from it.
	if err := git.RequireSafeRepo(stack.Path); err != nil {
		return nil, nil, err
	}
	// Create the meta-worktree (checks out the stack's tracked files).
	if _, err := git.CreateWorktree(stack.Path, root, "st/"+name); err != nil {
		return nil, nil, fmt.Errorf("create supatree worktree: %w", err)
	}

	inst, report, err := finishCreate(c, stack, root, name, opts)
	if err != nil && !opts.KeepPartial {
		rollback(c, stack.Path, root, name)
		return nil, nil, err
	}
	return inst, report, err
}

func finishCreate(c *Config, stack *Stack, root, name string, opts CreateOptions) (*Instance, *SyncReport, error) {
	spec, err := LoadSpec(root)
	if err != nil {
		return nil, nil, err
	}
	now := opts.Now
	if now.IsZero() {
		now = time.Now()
	}
	meta := &Meta{
		Name:      name,
		Root:      root,
		Slug:      name,
		Stack:     stack.Alias,
		Model:     resolveModel(opts.Model, spec, c),
		CreatedAt: now,
		Intent:    opts.Intent,
		Review:    opts.Review,
	}
	if len(opts.Review) > 0 {
		meta.Mode = ModeReviewing
	}
	if err := meta.Save(root); err != nil {
		return nil, nil, err
	}
	// The .supatree link and repos/ must never show as untracked in the stack,
	// whatever the stack's own .gitignore says (a `.supatree/` pattern matches
	// a directory, not the link that replaced it).
	if err := excludeFromStack(root, "/"+stateDirName, "/"+ReposDirName+"/"); err != nil {
		return nil, nil, err
	}

	report, err := Sync(c, root, false)
	if err != nil {
		return nil, report, err
	}
	inst, err := LoadInstance(root)
	if err != nil {
		return nil, report, err
	}
	if w := RunSetup(inst); w != "" {
		report.Warnings = append(report.Warnings, w)
	}
	return inst, report, nil
}

// rollback tears down a partially-created supatree (member worktrees, then the
// meta-worktree and its branch, then any leftover directory).
func rollback(c *Config, stackPath, root, name string) {
	if inst, err := LoadInstance(root); err == nil {
		for i := len(inst.Members) - 1; i >= 0; i-- {
			m := inst.Members[i]
			if !m.Exists {
				continue
			}
			removeMember(c, root, m.Alias, m.URL, m.Branch, &SyncReport{})
		}
	}
	_ = git.RemoveWorktree(stackPath, root)
	_ = git.DeleteBranch(stackPath, "st/"+name)
	_ = os.RemoveAll(StateDir(root))
	_ = os.RemoveAll(root)
}

func resolveStack(c *Config, alias string) (*Stack, error) {
	if len(c.Stacks) == 0 {
		return nil, fmt.Errorf("no stacks registered — run: supatree scaffold <dir> --repos=<a,b,c>")
	}
	if alias == "" {
		if len(c.Stacks) == 1 {
			return &c.Stacks[0], nil
		}
		return nil, fmt.Errorf("multiple stacks registered; pass --stack=<alias>")
	}
	s := c.FindStack(alias)
	if s == nil {
		return nil, fmt.Errorf("stack %q not found", alias)
	}
	return s, nil
}

func resolveModel(override string, spec *Spec, c *Config) string {
	if override != "" {
		return override
	}
	if spec.Model != "" {
		return spec.Model
	}
	return c.ResolveModel("")
}
