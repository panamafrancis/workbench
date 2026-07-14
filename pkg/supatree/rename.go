package supatree

import (
	"fmt"

	"github.com/panamafrancis/workbench/pkg/config"
	"github.com/panamafrancis/workbench/pkg/git"
	"github.com/panamafrancis/workbench/pkg/github"
)

// RenameBranchSlug changes the branch slug for every member of a supatree from
// st/<oldslug>/<alias> to st/<newSlug>/<alias>, updates the tree meta, migrates
// PR-cache keys, and regenerates info.md. The supatree name, directory, and
// stack branch (st/<name>) are left unchanged. With push, each member's new
// branch is pushed and the old remote branch deleted.
func RenameBranchSlug(c *Config, wb *config.Config, name, newSlug string, push bool) error {
	if err := git.ValidateName(newSlug, nil); err != nil {
		return err
	}
	inst, err := Get(c, name)
	if err != nil {
		return err
	}
	meta, err := LoadMeta(inst.Root)
	if err != nil {
		return err
	}
	if meta.Slug == newSlug {
		return fmt.Errorf("slug is already %q", newSlug)
	}

	prCache := github.NewCache(PRCachePath())
	_ = prCache.Load()

	for _, m := range inst.Members {
		if !m.Exists {
			continue
		}
		oldBranch := m.Branch
		newBranch := fmt.Sprintf("st/%s/%s", newSlug, m.Alias)
		if err := git.RenameBranch(m.Path, newBranch); err != nil {
			return fmt.Errorf("rename %s: %w", m.Alias, err)
		}
		prCache.Rename(oldBranch, newBranch)
		if push {
			if err := pushBranchAndDeleteOld(m.Path, oldBranch); err != nil {
				return fmt.Errorf("push %s: %w", m.Alias, err)
			}
		}
	}
	_ = prCache.Save()

	meta.Slug = newSlug
	if err := meta.Save(inst.Root); err != nil {
		return err
	}
	updated, err := LoadInstance(inst.Root)
	if err != nil {
		return err
	}
	return WriteInfo(updated)
}
