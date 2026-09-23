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
func Sync(root string, wb *config.Config, prune bool) (*SyncReport, error) {
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
	repos, err := resolveRepos(ordered, wb)
	if err != nil {
		return nil, err
	}

	report := &SyncReport{}
	for _, alias := range ordered {
		path := MemberPath(root, alias)
		if _, statErr := os.Stat(path); statErr == nil {
			continue
		}
		if err := createMember(repos[alias], path, meta, alias, report); err != nil {
			return report, err
		}
		report.Created = append(report.Created, alias)
	}

	if prune {
		if err := pruneMembers(root, ordered, wb, report); err != nil {
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

func createMember(repo *config.Repo, path string, meta *Meta, alias string, report *SyncReport) error {
	branch := meta.MemberBranch(alias)
	// A review member starts at the pull request's head rather than at the
	// default branch. The commits live on refs/pull/<n>/head, which is also the
	// only ref that reaches a PR opened from a fork.
	if ref, ok := meta.Review[alias]; ok {
		sha, err := git.FetchRef(repo.LocalPath, fmt.Sprintf("refs/pull/%d/head", ref.Number))
		if err != nil {
			return fmt.Errorf("fetch %s#%d: %w", ref.Repo, ref.Number, err)
		}
		if err := git.CreateWorktreeAt(repo.LocalPath, path, branch, sha); err != nil {
			return fmt.Errorf("create member %q at %s#%d: %w", repo.Alias, ref.Repo, ref.Number, err)
		}
		return repoCopyFiles(repo, path)
	}
	offline, err := git.CreateWorktree(repo.LocalPath, path, branch)
	if err != nil {
		return fmt.Errorf("create member %q: %w", repo.Alias, err)
	}
	if offline {
		report.Warnings = append(report.Warnings, fmt.Sprintf("%s: offline — branched from last-fetched origin", repo.Alias))
	}
	return repoCopyFiles(repo, path)
}

func repoCopyFiles(repo *config.Repo, path string) error {
	if err := repo.RunCopyFiles(path); err != nil {
		return fmt.Errorf("copy_files for %q: %w", repo.Alias, err)
	}
	return nil
}

func pruneMembers(root string, keep []string, wb *config.Config, report *SyncReport) error {
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
		if e.IsDir() && !keepSet[e.Name()] {
			stale = append(stale, e.Name())
		}
	}
	sort.Strings(stale)
	meta, err := LoadMeta(root)
	if err != nil {
		return err
	}
	for _, alias := range stale {
		removeMember(root, alias, meta.MemberBranch(alias), wb, report)
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
func removeMember(root, alias, want string, wb *config.Config, report *SyncReport) {
	path := MemberPath(root, alias)
	if repo, _ := wb.FindRepo(alias); repo != nil {
		branch, _ := git.CurrentBranch(path)
		if err := git.RemoveWorktree(repo.LocalPath, path); err != nil {
			report.Warnings = append(report.Warnings, fmt.Sprintf("%s: %v", alias, err))
		}
		// Delete the branch this tree created, whether or not the worktree was
		// still on it — leaving it behind would litter the clone every time a
		// member had been checked out elsewhere.
		if want != "" {
			_ = git.DeleteBranch(repo.LocalPath, want)
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
