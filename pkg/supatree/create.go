package supatree

import (
	"fmt"
	"os"
	"time"

	"github.com/panamafrancis/workbench/pkg/config"
	"github.com/panamafrancis/workbench/pkg/git"
)

// CreateOptions parameterizes New.
type CreateOptions struct {
	Stack       string    // stack alias; may be empty if exactly one is registered
	Name        string    // explicit supatree name; auto-generated when empty
	Model       string    // model override
	KeepPartial bool      // skip rollback on failure (for debugging)
	Now         time.Time // creation timestamp (injected for determinism)
}

// New creates a supatree: a worktree of the chosen stack repo at
// <trees-base>/<name> on branch st/<name>, then creates one member worktree per
// repo listed in the stack's supatree.yml. On failure it rolls back everything
// it created unless KeepPartial is set.
func New(c *Config, wb *config.Config, opts CreateOptions) (*Instance, *SyncReport, error) {
	stack, err := resolveStack(c, opts.Stack)
	if err != nil {
		return nil, nil, err
	}

	name := opts.Name
	if name == "" {
		name, err = git.GenerateName(existingNames(c, wb))
		if err != nil {
			return nil, nil, err
		}
	} else if err := git.ValidateName(name, existingNames(c, wb)); err != nil {
		return nil, nil, err
	}

	root := treeRoot(c.ResolveTreesBase(), name)
	if _, statErr := os.Stat(root); statErr == nil {
		return nil, nil, fmt.Errorf("path %s already exists", root)
	}

	// Create the meta-worktree (checks out the stack's tracked files).
	if _, err := git.CreateWorktree(stack.Path, root, "st/"+name); err != nil {
		return nil, nil, fmt.Errorf("create supatree worktree: %w", err)
	}

	inst, report, err := finishCreate(c, wb, stack, root, name, opts)
	if err != nil && !opts.KeepPartial {
		rollback(stack.Path, root, name, wb)
		return nil, nil, err
	}
	return inst, report, err
}

func finishCreate(c *Config, wb *config.Config, stack *Stack, root, name string, opts CreateOptions) (*Instance, *SyncReport, error) {
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
		Slug:      name,
		Stack:     stack.Alias,
		Model:     resolveModel(opts.Model, spec, c, wb),
		CreatedAt: now,
	}
	if err := meta.Save(root); err != nil {
		return nil, nil, err
	}

	report, err := Sync(root, wb, false)
	if err != nil {
		return nil, report, err
	}
	inst, err := LoadInstance(root)
	if err != nil {
		return nil, report, err
	}
	return inst, report, nil
}

// rollback tears down a partially-created supatree (member worktrees, then the
// meta-worktree and its branch, then any leftover directory).
func rollback(stackPath, root, name string, wb *config.Config) {
	if inst, err := LoadInstance(root); err == nil {
		for i := len(inst.Members) - 1; i >= 0; i-- {
			m := inst.Members[i]
			if !m.Exists {
				continue
			}
			removeMember(root, m.Alias, wb, &SyncReport{})
		}
	}
	_ = git.RemoveWorktree(stackPath, root)
	_ = git.DeleteBranch(stackPath, "st/"+name)
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

func resolveModel(override string, spec *Spec, c *Config, wb *config.Config) string {
	if override != "" {
		return override
	}
	if spec.Model != "" {
		return spec.Model
	}
	return c.ResolveModel("", wb)
}

// existingNames is the union of supatree names and workbench worktree names, so
// a generated city name never collides across the two tools.
func existingNames(c *Config, wb *config.Config) []string {
	names := Names(c)
	return append(names, wb.AllWorktreeNames()...)
}
