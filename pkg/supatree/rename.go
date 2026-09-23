package supatree

import (
	"errors"
	"fmt"

	"github.com/panamafrancis/workbench/pkg/config"
	"github.com/panamafrancis/workbench/pkg/git"
	"github.com/panamafrancis/workbench/pkg/github"
)

// renamedMember records one completed local branch rename, so it can be rolled
// back if a later member fails or pushed once the whole set has succeeded.
type renamedMember struct {
	alias     string
	path      string
	oldBranch string
	newBranch string
}

// RenameBranchSlug changes the branch slug for every member of a supatree from
// st/<oldslug>/<alias> to st/<newSlug>/<alias>, updates the tree meta, migrates
// PR-cache keys, and regenerates info.md. The supatree name, directory, and
// stack branch (st/<name>) are left unchanged. With push, each member's new
// branch is pushed and the old remote branch deleted.
//
// The work is phased so the tree is never left half-renamed. Every local rename
// happens first: a failure part-way rolls the earlier ones back and returns with
// nothing changed, because meta.Slug is what derives Member.Branch — a member
// left on the other slug falls outside its own tree and loses its PR and status.
// Only once all members are on the new slug is meta saved — and a failure there
// rolls them back too, since meta is the first thing written and the renames are
// still the only change made. The PR cache, info.md and the pushes all follow a
// saved meta, so a failure in any of them cannot desync meta from the branches;
// it is reported, but the rename itself stands.
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
	if meta.Reviewing() {
		return fmt.Errorf("%s is a review tree: %w", name, ErrReviewTree)
	}
	if meta.Slug == newSlug {
		return fmt.Errorf("slug is already %q", newSlug)
	}

	renamed, err := renameMemberBranches(inst, newSlug)
	if err != nil {
		return err
	}

	// Meta is saved before anything else is touched. If it fails, the local
	// renames are the only change made so far and must be undone: meta.Slug is
	// what derives Member.Branch, so leaving them in place strands every member
	// outside its own tree, and a retry would then die on "branch already
	// exists" with no way forward.
	meta.Slug = newSlug
	if err := meta.Save(inst.Root); err != nil {
		return undoRenames(err, renamed)
	}

	_ = github.NewCache(PRCachePath()).Mutate(func(w *github.Writable) error {
		for _, r := range renamed {
			w.Rename(r.oldBranch, r.newBranch)
		}
		return nil
	})

	// info.md is generated convenience and the pushes are independent of it, so
	// a failure to rewrite it is collected rather than returned: bailing here
	// would silently skip the --push the caller asked for and leave the remote
	// on the old branch names with nothing in the error to say so.
	var errs []error
	if updated, err := LoadInstance(inst.Root); err != nil {
		errs = append(errs, err)
	} else if err := WriteInfo(updated); err != nil {
		errs = append(errs, err)
	}
	if push {
		if err := pushRenamedMembers(renamed, newSlug); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// renameMemberBranches renames every checked-out member onto newSlug. On the
// first failure it undoes the renames already made and reports both the cause
// and any member the undo could not restore.
func renameMemberBranches(inst *Instance, newSlug string) ([]renamedMember, error) {
	var done []renamedMember
	for _, m := range inst.Members {
		if !m.Exists {
			continue
		}
		newBranch := fmt.Sprintf("st/%s/%s", newSlug, m.Alias)
		if err := git.RenameBranch(m.Path, newBranch); err != nil {
			return nil, undoRenames(fmt.Errorf("rename %s: %w", m.Alias, err), done)
		}
		done = append(done, renamedMember{alias: m.Alias, path: m.Path, oldBranch: m.Branch, newBranch: newBranch})
	}
	return done, nil
}

// undoRenames rolls back the renames already made because of cause, and returns
// the error to report: cause alone when everything was restored, or cause plus
// whichever members are stuck on the new slug.
func undoRenames(cause error, done []renamedMember) error {
	if stuck := rollbackRenames(done); len(stuck) > 0 {
		return fmt.Errorf("%w (rolled back, except: %w)", cause, errors.Join(stuck...))
	}
	return cause
}

// rollbackRenames puts each member back on its old branch, newest first, and
// returns the members it could not restore.
func rollbackRenames(done []renamedMember) []error {
	var stuck []error
	for i := len(done) - 1; i >= 0; i-- {
		r := done[i]
		if err := git.RenameBranch(r.path, r.oldBranch); err != nil {
			stuck = append(stuck, fmt.Errorf("%s left on %s: %w", r.alias, r.newBranch, err))
		}
	}
	return stuck
}

// pushRenamedMembers pushes every renamed branch and deletes its old remote
// counterpart. Failures are collected rather than aborting: the local rename has
// already been committed to meta, and stopping at the first failure would leave
// the remaining members unpushed with no report of it.
func pushRenamedMembers(renamed []renamedMember, newSlug string) error {
	var errs []error
	for _, r := range renamed {
		if err := pushBranchAndDeleteOld(r.path, r.oldBranch); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", r.alias, err))
		}
	}
	if len(errs) == 0 {
		return nil
	}
	return fmt.Errorf("branches renamed to st/%s/<alias>, but pushing failed: %w", newSlug, errors.Join(errs...))
}
