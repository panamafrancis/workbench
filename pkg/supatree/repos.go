package supatree

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/panamafrancis/workbench/pkg/config"
	"github.com/panamafrancis/workbench/pkg/git"
	"github.com/panamafrancis/workbench/pkg/github"
)

// The repo cache: supatree owns its clones. Every member repo is cloned once,
// into ~/supatree/repos/<host>/<owner>/<name>, from the URL a stack gives for
// it, and every tree's member worktree hangs off that clone. The path is the
// registry — there is no alias table, so no way to clone one repository twice
// and no duplicate to reconcile. Supatree never adopts a clone it did not make.
//
// The clone's own checkout is never worked in. It exists to hold worktrees,
// and to hold the personal untracked files copy_files copies into each new one.

// ReposDir is the repo cache.
func ReposDir() string {
	return filepath.Join(WorkspaceDir(), "repos")
}

// resolveRemote is github.ResolveRemote, swappable in tests (it shells out to
// ssh to resolve host aliases).
var resolveRemote = github.ResolveRemote

// CacheKey is where a URL's clone lives under ReposDir: host/owner/name, with
// an ssh alias resolved to its real host so both spellings share a clone. A
// local path or file:// URL — a repository with no remote at all — is keyed
// under local/ by its absolute path.
func CacheKey(url string) (string, error) {
	if r, ok := resolveRemote(url); ok {
		return r.Key(), nil
	}
	if p, ok := localRepoPath(url); ok {
		return filepath.Join("local", strings.TrimPrefix(p, string(filepath.Separator))), nil
	}
	return "", fmt.Errorf("%q is not a git URL supatree can clone (expected git@host:owner/name.git, https://host/owner/name, or a local path)", url)
}

func localRepoPath(url string) (string, bool) {
	p := strings.TrimPrefix(url, "file://")
	if !filepath.IsAbs(p) {
		return "", false
	}
	return filepath.Clean(p), true
}

// ClonePath is the cache clone for url.
func ClonePath(url string) (string, error) {
	key, err := CacheKey(url)
	if err != nil {
		return "", err
	}
	return filepath.Join(ReposDir(), key), nil
}

// baseClone is what supatree needs to know about one member repo: the cache
// clone its worktrees hang off, and what to copy into a new worktree.
type baseClone struct {
	Alias     string
	URL       string
	Key       string
	Clone     string
	CopyFiles []string
}

// cachedClone resolves a member to its cache clone without creating it.
func (c *Config) cachedClone(alias, url string) (*baseClone, error) {
	key, err := CacheKey(url)
	if err != nil {
		return nil, fmt.Errorf("member %q: %w", alias, err)
	}
	return &baseClone{
		Alias:     alias,
		URL:       url,
		Key:       key,
		Clone:     filepath.Join(ReposDir(), key),
		CopyFiles: c.Repos[key].CopyFiles,
	}, nil
}

// cloneTimeout bounds one clone. Generous: it is the one network call tree
// creation is allowed to make, and a large repository on a slow link is slow.
const cloneTimeout = 30 * time.Minute

// ResolveMember returns the cache clone for a member, cloning it first if the
// cache does not have it yet. It is the only place supatree clones.
func (c *Config) ResolveMember(alias, url string) (*baseClone, error) {
	bc, err := c.cachedClone(alias, url)
	if err != nil {
		return nil, err
	}
	if isClone(bc.Clone) {
		return bc, git.RequireSafeRepo(bc.Clone)
	}
	lock := filepath.Join(StateRoot(), "locks", strings.ReplaceAll(bc.Key, "/", "_")+".lock")
	err = config.WithFileLock(lock, func() error {
		// Someone else may have cloned it while we waited for the lock.
		if isClone(bc.Clone) {
			return nil
		}
		return cloneInto(url, bc.Clone)
	})
	if err != nil {
		return nil, fmt.Errorf("clone %s for %q: %w", url, alias, err)
	}
	return bc, git.RequireSafeRepo(bc.Clone)
}

// cloneInto clones url to dest by way of a sibling temp dir, so an interrupted
// clone is never mistaken for a finished one.
func cloneInto(url, dest string) error {
	if err := os.MkdirAll(filepath.Dir(dest), 0755); err != nil {
		return err
	}
	tmp, err := os.MkdirTemp(filepath.Dir(dest), "."+filepath.Base(dest)+".clone-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(tmp) }()
	ctx, cancel := context.WithTimeout(context.Background(), cloneTimeout)
	defer cancel()
	// Stdin closed: tree creation must never stop to ask a human anything.
	cmd := exec.CommandContext(ctx, "git", "clone", "--quiet", "--", url, tmp)
	cmd.Stdin = nil
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(string(out)))
	}
	return os.Rename(tmp, dest)
}

// isClone reports whether dir holds a finished clone.
func isClone(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, ".git"))
	return err == nil
}

// cloneOf is the clone a member worktree hangs off, read from the worktree
// itself — so a member can be torn down even after it has left the spec.
func cloneOf(worktree string) (string, bool) {
	common, err := GitCommonDir(worktree)
	if err != nil || filepath.Base(common) != ".git" {
		return "", false
	}
	return filepath.Dir(common), true
}

// CachedRepo is one clone in the repo cache.
type CachedRepo struct {
	Key  string // host/owner/name, or local/<path>
	Path string
	URL  string // its origin
}

// ListCachedRepos lists every clone in the cache, sorted by key.
func ListCachedRepos() ([]CachedRepo, error) {
	var out []CachedRepo
	root := ReposDir()
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			if os.IsNotExist(err) {
				return filepath.SkipDir
			}
			return err
		}
		if !d.IsDir() || path == root {
			return nil
		}
		if strings.HasPrefix(d.Name(), ".") {
			return filepath.SkipDir
		}
		if isClone(path) {
			rel, _ := filepath.Rel(root, path)
			url, _ := gitOutput(path, "remote", "get-url", "origin")
			out = append(out, CachedRepo{Key: rel, Path: path, URL: url})
			return filepath.SkipDir
		}
		return nil
	})
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	return out, nil
}
