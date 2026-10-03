package supatree

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/panamafrancis/workbench/pkg/git"
)

// ScaffoldResult reports what Scaffold produced.
type ScaffoldResult struct {
	Alias string
	Path  string
}

// Scaffold creates a stack repo named name, seeded with supatree.yml (the
// given members, alias → git URL), AGENTS.md, a scripts/ directory, and a
// .gitignore, then registers it. It is placed at ~/supatree/stacks/<name>/
// unless pathOverride is given, and the target dir must not already be a git
// repo. Each member is cloned into the repo cache up front, so the first tree
// does not pay for it.
func Scaffold(c *Config, name, pathOverride string, members map[string]string) (*ScaffoldResult, error) {
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
		return nil, fmt.Errorf("no repos selected — pass --repos=<owner/repo,...> or pick some interactively")
	}
	for alias, url := range members {
		if err := git.ValidateName(alias, nil); err != nil {
			return nil, fmt.Errorf("invalid member alias %q: %w", alias, err)
		}
		if _, err := c.ResolveMember(alias, url); err != nil {
			return nil, err
		}
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
	// Durable memory for this stack, in git so it is reviewed like code. Created
	// here rather than on first write, because a directory that does not exist
	// is one nobody writes to.
	if err := ScaffoldNotes(dir); err != nil {
		return nil, err
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

// ParseMemberArg reads how a person names a member on the command line:
// "owner/repo" (a GitHub repository, over ssh), a git URL, or either with an
// explicit alias as "alias=…". The alias defaults to the repository name.
func ParseMemberArg(arg string) (alias, url string, err error) {
	arg = strings.TrimSpace(arg)
	if a, rest, ok := strings.Cut(arg, "="); ok && !strings.Contains(a, "/") && !strings.Contains(a, ":") {
		alias, arg = strings.TrimSpace(a), strings.TrimSpace(rest)
	}
	url = arg
	if !strings.Contains(arg, ":") && !filepath.IsAbs(arg) && strings.Count(arg, "/") == 1 {
		url = "git@github.com:" + strings.TrimSuffix(arg, ".git") + ".git"
	}
	if _, err := CacheKey(url); err != nil {
		return "", "", err
	}
	if alias == "" {
		alias = repoName(url)
	}
	return alias, url, nil
}

// repoName is the last path element of a git URL, without .git.
func repoName(url string) string {
	url = strings.TrimSuffix(strings.TrimSuffix(url, "/"), ".git")
	if i := strings.LastIndexAny(url, "/:"); i >= 0 {
		url = url[i+1:]
	}
	return strings.ToLower(url)
}
