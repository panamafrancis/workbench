package supatree

import (
	"fmt"
	"os"

	"github.com/panamafrancis/workbench/pkg/config"
)

// This file is the temporary bridge to workbench's config. Supatree used to
// resolve member repos and models through ~/.workbench/config.yml; it now owns
// both, and until `supatree migrate` has moved a user's setup across, these
// fall back to what workbench's config says. Nothing outside this file reads
// workbench's config, and nothing here ever writes it.

// loadLegacy reads workbench's config if there is one. A missing or broken
// file is not an error: supatree runs without workbench.
func loadLegacy() *config.Config {
	if _, err := os.Stat(config.ConfigPath()); err != nil {
		return nil
	}
	wb, err := config.Load()
	if err != nil {
		return nil
	}
	return wb
}

// legacyModels is workbench's config when supatree's own has no models yet.
func (c *Config) legacyModels() *config.Config {
	if len(c.Models) > 0 {
		return nil
	}
	return c.legacy
}

// baseClone is what supatree needs to know about one member repo: the base
// clone its worktrees hang off, and what to copy into a new worktree.
type baseClone struct {
	Alias     string
	Clone     string
	CopyFiles []string
}

// baseClone resolves a stack member alias to its base clone.
func (c *Config) baseClone(alias string) (*baseClone, error) {
	if c.legacy != nil {
		if r, _ := c.legacy.FindRepo(alias); r != nil {
			return &baseClone{Alias: alias, Clone: r.LocalPath, CopyFiles: r.CopyFiles}, nil
		}
	}
	return nil, fmt.Errorf("repo %q is not registered with workbench — run: workbench add repo <path> --alias=%s", alias, alias)
}

// baseClones resolves every alias, failing on the first unknown one.
func (c *Config) baseClones(aliases []string) (map[string]*baseClone, error) {
	out := make(map[string]*baseClone, len(aliases))
	for _, alias := range aliases {
		r, err := c.baseClone(alias)
		if err != nil {
			return nil, err
		}
		out[alias] = r
	}
	return out, nil
}

// LegacyRepoAliases lists the repos workbench knows, for the scaffold picker.
func (c *Config) LegacyRepoAliases() []string {
	if c.legacy == nil {
		return nil
	}
	out := make([]string, 0, len(c.legacy.Repos))
	for _, r := range c.legacy.Repos {
		out = append(out, r.Alias)
	}
	return out
}
