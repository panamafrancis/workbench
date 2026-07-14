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
		if err := createMember(repos[alias], path, meta.MemberBranch(alias), report); err != nil {
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

func createMember(repo *config.Repo, path, branch string, report *SyncReport) error {
	offline, err := git.CreateWorktree(repo.LocalPath, path, branch)
	if err != nil {
		return fmt.Errorf("create member %q: %w", repo.Alias, err)
	}
	if offline {
		report.Warnings = append(report.Warnings, fmt.Sprintf("%s: offline — branched from last-fetched origin", repo.Alias))
	}
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
	for _, alias := range stale {
		removeMember(root, alias, wb, report)
		report.Pruned = append(report.Pruned, alias)
	}
	return nil
}

// removeMember tears down a single member worktree, tolerating missing pieces.
func removeMember(root, alias string, wb *config.Config, report *SyncReport) {
	path := MemberPath(root, alias)
	if repo, _ := wb.FindRepo(alias); repo != nil {
		branch, _ := git.CurrentBranch(path)
		if err := git.RemoveWorktree(repo.LocalPath, path); err != nil {
			report.Warnings = append(report.Warnings, fmt.Sprintf("%s: %v", alias, err))
		}
		if branch != "" {
			_ = git.DeleteBranch(repo.LocalPath, branch)
		}
	}
	_ = os.RemoveAll(path)
}
