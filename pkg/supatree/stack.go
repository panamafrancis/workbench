package supatree

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/panamafrancis/workbench/pkg/git"
)

// Editing a stack. Each change is one commit to the stack repo's supatree.yml:
// the stack is shared, so its history is how a teammate sees why a repo joined
// or left. Only supatree.yml is staged — whatever else is uncommitted in the
// stack stays as it was — and a supatree.yml with uncommitted edits of its own
// is refused rather than folded into a commit that says something else.

// StackAdd adds a member to a stack. The repository is cloned into the cache
// first, when clone is set, so the first tree does not pay for it.
func (c *Config) StackAdd(stackAlias, alias, url string, clone bool) error {
	stack := c.FindStack(stackAlias)
	if stack == nil {
		return fmt.Errorf("stack %q not found", stackAlias)
	}
	if err := git.ValidateName(alias, nil); err != nil {
		return fmt.Errorf("invalid member alias %q: %w", alias, err)
	}
	if _, err := CacheKey(url); err != nil {
		return err
	}
	return editSpec(stack.Path, "supatree: add "+alias, func(s *Spec) error {
		if prev, ok := s.Members[alias]; ok {
			if prev == url {
				return errNoChange
			}
			return fmt.Errorf("%q is already a member (%s) — pick another alias with --as", alias, prev)
		}
		for a, u := range s.Members {
			if sameRepo(u, url) {
				return fmt.Errorf("%s is already a member, as %q", url, a)
			}
		}
		if clone {
			if _, err := c.ResolveMember(alias, url); err != nil {
				return err
			}
		}
		if s.Members == nil {
			s.Members = map[string]string{}
		}
		s.Members[alias] = url
		return nil
	})
}

// StackRm removes a member from a stack, and every dependency edge that names
// it. Existing trees keep the member until they sync with prune.
func (c *Config) StackRm(stackAlias, alias string) error {
	stack := c.FindStack(stackAlias)
	if stack == nil {
		return fmt.Errorf("stack %q not found", stackAlias)
	}
	return editSpec(stack.Path, "supatree: remove "+alias, func(s *Spec) error {
		if _, ok := s.Members[alias]; !ok {
			return fmt.Errorf("%q is not a member of %s (members: %s)", alias, stackAlias, strings.Join(s.Aliases(), ", "))
		}
		delete(s.Members, alias)
		delete(s.Deps, alias)
		for from, tos := range s.Deps {
			kept := tos[:0]
			for _, to := range tos {
				if to != alias {
					kept = append(kept, to)
				}
			}
			if len(kept) == 0 {
				delete(s.Deps, from)
			} else {
				s.Deps[from] = kept
			}
		}
		return nil
	})
}

// StackDep records that from depends on to: to's pull request merges first.
func (c *Config) StackDep(stackAlias, from, to string) error {
	stack := c.FindStack(stackAlias)
	if stack == nil {
		return fmt.Errorf("stack %q not found", stackAlias)
	}
	return editSpec(stack.Path, fmt.Sprintf("supatree: %s depends on %s", from, to), func(s *Spec) error {
		for _, a := range []string{from, to} {
			if _, ok := s.Members[a]; !ok {
				return fmt.Errorf("%q is not a member of %s", a, stackAlias)
			}
		}
		if from == to {
			return fmt.Errorf("a member cannot depend on itself")
		}
		for _, d := range s.Deps[from] {
			if d == to {
				return errNoChange
			}
		}
		if s.Deps == nil {
			s.Deps = map[string][]string{}
		}
		s.Deps[from] = append(s.Deps[from], to)
		sort.Strings(s.Deps[from])
		if _, err := s.OrderedMembers(); err != nil {
			return err
		}
		return nil
	})
}

// errNoChange makes an edit that would change nothing succeed quietly.
var errNoChange = errors.New("no change")

func editSpec(stackPath, message string, edit func(*Spec) error) error {
	if out, err := gitOutput(stackPath, "status", "--porcelain", "--", SpecName); err != nil {
		return err
	} else if out != "" {
		return fmt.Errorf("%s has uncommitted changes in %s — commit or discard them first", SpecName, stackPath)
	}
	spec, err := LoadSpec(stackPath)
	if err != nil {
		return err
	}
	if err := edit(spec); err != nil {
		if errors.Is(err, errNoChange) {
			return nil
		}
		return err
	}
	if err := SaveSpec(stackPath, spec); err != nil {
		return err
	}
	if err := runGit(stackPath, "add", "--", SpecName); err != nil {
		return err
	}
	return runGit(stackPath, "commit", "-q", "-m", message, "--", SpecName)
}

// sameRepo reports whether two URLs name one repository.
func sameRepo(a, b string) bool {
	ka, errA := CacheKey(a)
	kb, errB := CacheKey(b)
	return errA == nil && errB == nil && ka == kb
}

// StackCloneResult reports what StackClone did.
type StackCloneResult struct {
	Alias string
	Path  string
	// Setup is the stack's scripts/setup, if it has one. It runs for every
	// tree made from this stack, so whoever adopts a stack should read it.
	Setup    string
	Warnings []string
}

// StackClone is teammate onboarding: clone a shared stack repo, register it,
// and fill the repo cache with its members.
func (c *Config) StackClone(url, alias, pathOverride string) (*StackCloneResult, error) {
	if alias == "" {
		alias = repoName(url)
	}
	if err := git.ValidateName(alias, nil); err != nil {
		return nil, fmt.Errorf("invalid stack name %q (pass --as): %w", alias, err)
	}
	if c.FindStack(alias) != nil {
		return nil, fmt.Errorf("a stack named %q is already registered", alias)
	}
	dest := DefaultStackPath(alias)
	if pathOverride != "" {
		abs, err := filepath.Abs(pathOverride)
		if err != nil {
			return nil, err
		}
		dest = abs
	}
	if _, err := os.Stat(dest); err == nil {
		return nil, fmt.Errorf("%s already exists", dest)
	}
	if err := cloneInto(url, dest); err != nil {
		return nil, fmt.Errorf("clone stack: %w", err)
	}
	spec, err := LoadSpec(dest)
	if err != nil {
		_ = os.RemoveAll(dest)
		return nil, err
	}
	if err := AddStack(alias, dest); err != nil {
		return nil, err
	}
	res := &StackCloneResult{Alias: alias, Path: dest}
	for _, a := range spec.Aliases() {
		if _, err := c.ResolveMember(a, spec.Members[a]); err != nil {
			res.Warnings = append(res.Warnings, err.Error())
		}
	}
	if data, err := os.ReadFile(filepath.Join(dest, SetupScript)); err == nil {
		res.Setup = string(data)
	}
	return res, nil
}

// ScanOrigins reads the origin of every git repository directly under dir. It
// only reads them: the scanned checkouts are never used, only their URLs.
func ScanOrigins(dir string) (map[string]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		path := filepath.Join(dir, e.Name())
		if !isClone(path) {
			continue
		}
		url, err := gitOutput(path, "remote", "get-url", "origin")
		if err != nil || url == "" {
			continue
		}
		out[repoName(url)] = url
	}
	return out, nil
}

// OrgRepos lists an organization's repositories through gh, skipping archived
// repositories and forks: neither is somewhere new work goes.
func OrgRepos(org string) (map[string]string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	out, err := exec.CommandContext(ctx, "gh", "repo", "list", org, "--limit", "1000",
		"--json", "name,sshUrl,isArchived,isFork").Output()
	if err != nil {
		return nil, fmt.Errorf("gh repo list %s: %w", org, err)
	}
	var repos []struct {
		Name       string `json:"name"`
		SSHURL     string `json:"sshUrl"`
		IsArchived bool   `json:"isArchived"`
		IsFork     bool   `json:"isFork"`
	}
	if err := json.Unmarshal(out, &repos); err != nil {
		return nil, fmt.Errorf("parse gh repo list: %w", err)
	}
	res := map[string]string{}
	for _, r := range repos {
		if r.IsArchived || r.IsFork {
			continue
		}
		res[strings.ToLower(r.Name)] = r.SSHURL
	}
	return res, nil
}

// RepoUse is a tree whose member hangs off a cached clone.
type RepoUse struct {
	Tree  string
	Alias string
}

// CacheUsage maps each cache clone to the trees with a worktree of it, keyed
// by the clone's resolved path (see UsageOf).
func CacheUsage(insts []*Instance) map[string][]RepoUse {
	out := map[string][]RepoUse{}
	for _, inst := range insts {
		for _, m := range inst.Members {
			if !m.Exists {
				continue
			}
			if clone, ok := cloneOf(m.Path); ok {
				key := realPath(clone)
				out[key] = append(out[key], RepoUse{Tree: inst.Name, Alias: m.Alias})
			}
		}
	}
	return out
}

// FindCachedRepo resolves what a person calls a cached repository — its cache
// key, owner/repo, or a URL — to the clone.
func FindCachedRepo(name string) (*CachedRepo, error) {
	repos, err := ListCachedRepos()
	if err != nil {
		return nil, err
	}
	want := strings.ToLower(strings.TrimSuffix(name, ".git"))
	if key, err := CacheKey(name); err == nil {
		want = strings.ToLower(key)
	}
	var matches []CachedRepo
	for _, r := range repos {
		k := strings.ToLower(r.Key)
		if k == want || strings.HasSuffix(k, "/"+want) {
			matches = append(matches, r)
		}
	}
	switch len(matches) {
	case 0:
		return nil, fmt.Errorf("%q is not in the repo cache — see: supatree repo ls", name)
	case 1:
		return &matches[0], nil
	default:
		keys := make([]string, len(matches))
		for i, m := range matches {
			keys[i] = m.Key
		}
		return nil, fmt.Errorf("%q matches several cached repos: %s", name, strings.Join(keys, ", "))
	}
}

// RemoveCachedRepo deletes a clone from the cache. It refuses while any tree
// has a worktree of it: that worktree's .git points into the clone.
func RemoveCachedRepo(r *CachedRepo, insts []*Instance) error {
	if uses := UsageOf(CacheUsage(insts), r.Path); len(uses) > 0 {
		trees := make([]string, 0, len(uses))
		for _, u := range uses {
			trees = append(trees, u.Tree)
		}
		return fmt.Errorf("%s is in use by %s — remove those trees first", r.Key, strings.Join(trees, ", "))
	}
	return os.RemoveAll(r.Path)
}

// UsageOf looks a clone up in a CacheUsage map. git reports paths with
// symlinks resolved (/private/var for /var on macOS), so both sides are.
func UsageOf(usage map[string][]RepoUse, clone string) []RepoUse {
	return usage[realPath(clone)]
}

func realPath(p string) string {
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	return filepath.Clean(p)
}
