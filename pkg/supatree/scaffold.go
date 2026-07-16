package supatree

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/panamafrancis/workbench/pkg/config"
	"github.com/panamafrancis/workbench/pkg/git"
)

// ScaffoldResult reports what Scaffold produced.
type ScaffoldResult struct {
	Alias string
	Path  string
}

// Scaffold creates a stack repo named name, seeded with supatree.yml (listing
// the given member aliases), AGENTS.md, a scripts/ directory, and a .gitignore,
// then registers it in the supatree registry. It is placed at
// ~/.supatree/stacks/<name>/ unless pathOverride is given. The member aliases
// must be registered with workbench, and the target dir must not already be a
// git repo.
func Scaffold(name, pathOverride string, members []string, wb *config.Config) (*ScaffoldResult, error) {
	if err := git.ValidateName(name, nil); err != nil {
		return nil, fmt.Errorf("invalid stack name %q: %w", name, err)
	}
	dir := DefaultStackPath(name)
	if pathOverride != "" {
		abs, err := filepath.Abs(pathOverride)
		if err != nil {
			return nil, err
		}
		dir = abs
	}
	alias := name
	if len(members) == 0 {
		return nil, fmt.Errorf("no repos selected — pass --repos=<a,b,c> or pick some interactively")
	}
	if _, err := resolveRepos(members, wb); err != nil {
		return nil, err
	}
	if isGitRepo(dir) {
		return nil, fmt.Errorf("%s is already a git repository; scaffold expects a fresh directory", dir)
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("create stack dir: %w", err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "scripts"), 0755); err != nil {
		return nil, fmt.Errorf("create scripts dir: %w", err)
	}

	if err := SaveSpec(dir, &Spec{Members: members, Deps: map[string][]string{}}); err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(dir, AgentsMDName), []byte(scaffoldAgentsMD), 0644); err != nil {
		return nil, fmt.Errorf("write AGENTS.md: %w", err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".gitignore"), []byte(scaffoldGitignore), 0644); err != nil {
		return nil, fmt.Errorf("write .gitignore: %w", err)
	}
	// Keep scripts/ in git even while empty.
	if err := os.WriteFile(filepath.Join(dir, "scripts", ".gitkeep"), nil, 0644); err != nil {
		return nil, fmt.Errorf("write scripts placeholder: %w", err)
	}

	if err := initStackRepo(dir); err != nil {
		return nil, err
	}
	if err := commitAll(dir, "supatree: scaffold stack"); err != nil {
		return nil, err
	}
	if err := AddStack(alias, dir); err != nil {
		return nil, err
	}
	return &ScaffoldResult{Alias: alias, Path: dir}, nil
}
