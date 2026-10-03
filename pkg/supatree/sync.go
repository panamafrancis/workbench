package supatree

import (
	"fmt"
	"os"
	"sort"

	"github.com/panamafrancis/workbench/pkg/config"
	"github.com/panamafrancis/workbench/pkg/git"
)

// SyncReport summarizes a reconciliation.
type SyncReport struct {
	Created  []string // member aliases whose worktrees were newly created
	Pruned   []string // member aliases whose worktrees were removed
	Warnings []string
}

// Sync reconciles the member worktrees under a supatree root with its
// supatree.yml: it creates any missing member worktrees (in dependency order)
// and, when prune is true, removes member worktrees no longer listed. It then
// regenerates .supatree/info.md. Members are created off the branch scheme in
// the tree's meta (st/<slug>/<alias>).
func Sync(c *Config, root string, prune bool) (*SyncReport, error) {
	meta, err := LoadMeta(root)
	if err != nil {
		return nil, err
	}
	spec, err := LoadSpec(root)
	if err != nil {
		return nil, err
	}
	ordered, err := spec.OrderedMembers()
	if err != nil {
		return nil, err
	}
	report := &SyncReport{}
	for _, alias := range ordered {
		path := MemberPath(root, alias)
		if _, statErr := os.Stat(path); statErr == nil {
			continue
		}
		// The one network call creation may make: cloning a repository the
		// cache does not have yet.
		repo, err := c.ResolveMember(alias, spec.Members[alias])
		if err != nil {
			return report, err
		}
		if err := createMember(repo, path, meta, alias, report); err != nil {
			return report, err
		}
		report.Created = append(report.Created, alias)
	}

	if prune {
		if err := pruneMembers(c, root, ordered, report); err != nil {
			return report, err
		}
	}

	inst, err := LoadInstance(root)
	if err != nil {
		return report, err
	}
	if err := WriteInfo(inst); err != nil {
		return report, err
	}
	return report, nil
}

func createMember(repo *baseClone, path string, meta *Meta, alias string, report *SyncReport) error {
	branch := meta.MemberBranch(alias)
	// A review member starts at the pull request's head rather than at the
	// default branch. The commits live on refs/pull/<n>/head, which is also the
	// only ref that reaches a PR opened from a fork.
	if ref, ok := meta.Review[alias]; ok {
		sha, err := git.FetchRef(repo.Clone, fmt.Sprintf("refs/pull/%d/head", ref.Number))
		if err != nil {
			return fmt.Errorf("fetch %s#%d: %w", ref.Repo, ref.Number, err)
		}
		if err := git.CreateWorktreeAt(repo.Clone, path, branch, sha); err != nil {
			return fmt.Errorf("create member %q at %s#%d: %w", repo.Alias, ref.Repo, ref.Number, err)
		}
		return repoCopyFiles(repo, path, report)
	}
	offline, err := git.CreateWorktree(repo.Clone, path, branch)
	if err != nil {
		return fmt.Errorf("create member %q: %w", repo.Alias, err)
	}
	if offline {
		report.Warnings = append(report.Warnings, fmt.Sprintf("%s: offline — branched from last-fetched origin", repo.Alias))
	}
	return repoCopyFiles(repo, path, report)
}

// repoCopyFiles copies the member's copy_files from its base clone into the new
// worktree. A listed file that is missing is a warning, not a failure: a member
// without its .env is still a member, and the warning says what to put where.
func repoCopyFiles(repo *baseClone, path string, report *SyncReport) error {
	missing, err := config.CopyFiles(repo.Clone, path, repo.CopyFiles)
	if err != nil {
		return fmt.Errorf("copy_files for %q: %w", repo.Alias, err)
	}
	for _, f := range missing {
		report.Warnings = append(report.Warnings, fmt.Sprintf(
			"%s: copy_files: %s not found in %s — put it there once and every new tree gets a copy", repo.Alias, f, repo.Clone))
	}
	return nil
}

func pruneMembers(c *Config, root string, keep []string, report *SyncReport) error {
	keepSet := make(map[string]bool, len(keep))
	for _, a := range keep {
		keepSet[a] = true
	}
	reposDir := MemberPath(root, "")
	entries, err := os.ReadDir(reposDir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read repos dir: %w", err)
	}
	var stale []string
	for _, e := range entries {
		if e.IsDir() && !keepSet[e.Name()] && git.ValidateName(e.Name(), nil) == nil {
			stale = append(stale, e.Name())
		}
	}
	sort.Strings(stale)
	meta, err := LoadMeta(root)
	if err != nil {
		return err
	}
	for _, alias := range stale {
		removeMember(c, root, alias, "", meta.MemberBranch(alias), report)
		report.Pruned = append(report.Pruned, alias)
	}
	return nil
}

// removeMember tears down a single member worktree, tolerating missing pieces.
//
// want is the branch this tree created for the member. The branch actually
// checked out is deleted only when it matches: this runs `git branch -D`, and a
// member that has been pointed at someone else's branch — by a `gh pr checkout`,
// or by a review tree gone wrong — would otherwise have that branch destroyed by
// an ordinary teardown. Anything else is left behind and reported, which is the
// recoverable direction.
//
// The clone is read from the worktree itself, so a member that has already
// left the spec can still be torn down; url is the fallback when the worktree
// is too broken to say.
func removeMember(c *Config, root, alias, url, want string, report *SyncReport) {
	if err := git.ValidateName(alias, nil); err != nil {
		report.Warnings = append(report.Warnings, fmt.Sprintf("refusing to remove member %q: %v", alias, err))
		return
	}
	path := MemberPath(root, alias)
	clone, ok := cloneOf(path)
	if !ok && url != "" {
		if bc, err := c.cachedClone(alias, url); err == nil && isClone(bc.Clone) {
			clone, ok = bc.Clone, true
		}
	}
	if ok {
		if err := git.RequireSafeRepo(clone); err != nil {
			report.Warnings = append(report.Warnings, fmt.Sprintf("%s: %v — removed the directory only", alias, err))
			ok = false
		}
	}
	if ok {
		branch, _ := git.CurrentBranch(path)
		if err := git.RemoveWorktree(clone, path); err != nil {
			report.Warnings = append(report.Warnings, fmt.Sprintf("%s: %v", alias, err))
		}
		// Delete the branch this tree created, whether or not the worktree was
		// still on it — leaving it behind would litter the clone every time a
		// member had been checked out elsewhere.
		if want != "" {
			_ = git.DeleteBranch(clone, want)
		}
		switch {
		case branch == "" || branch == want:
		case want == "":
			// Nothing said which branch this tree owned, so nothing here is
			// known to be safe to delete.
			report.Warnings = append(report.Warnings, fmt.Sprintf(
				"%s: left branch %q in place", alias, branch))
		default:
			report.Warnings = append(report.Warnings, fmt.Sprintf(
				"%s: left branch %q in place — this tree owns %q, and deleting a branch it did not create is not ours to do",
				alias, branch, want))
		}
	}
	_ = os.RemoveAll(path)
}
