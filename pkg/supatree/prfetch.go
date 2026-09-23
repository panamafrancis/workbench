package supatree

import (
	"errors"
	"time"

	"github.com/panamafrancis/workbench/pkg/config"
	"github.com/panamafrancis/workbench/pkg/git"
	"github.com/panamafrancis/workbench/pkg/github"
)

// Default fetch tuning shared by every supatree surface that talks to GitHub
// (the sidebar and the dashboard). They are deliberately in one place: a second
// poller with its own numbers is exactly how the shared GraphQL quota drains.
const (
	// PRStaleAge bounds how old a cached PR status may be before a fetch is
	// allowed.
	PRStaleAge = 10 * time.Minute
	// RateLimitCooldown suppresses all GitHub fetches after a rate-limit
	// response. Persisted via the cache so it survives process restarts.
	RateLimitCooldown = 15 * time.Minute
)

// FetchTarget is one member worth asking GitHub about.
type FetchTarget struct {
	Path   string
	Branch string
	// Key is the cache entry this target writes, which is the branch name for
	// an authoring member and the PR reference for a review one.
	Key string
	// Ref pins a review member to its PR. When set, the lookup goes straight to
	// the number instead of searching for a PR whose head is Branch — there is
	// none, because the branch is tree-local.
	Ref github.PRRef
}

// FetchTargets selects the member branches a fetch round should query. It skips
// members that are not checked out, statuses that are still fresh (unless
// forced), and branches with no origin ref that we have never seen a PR for —
// an unpushed branch cannot have a PR, so asking is a guaranteed-empty round
// trip and the dominant source of wasted quota.
func FetchTargets(insts []*Instance, cache *github.Cache, force bool, staleAge time.Duration) []FetchTarget {
	var targets []FetchTarget
	for _, inst := range insts {
		for _, mem := range inst.Members {
			if !mem.Exists {
				continue
			}
			key := mem.CacheKey()
			if !force && !cache.IsStale(key, staleAge) {
				continue
			}
			// A review member's PR is known by construction, so the
			// unpushed-branch skip below must not apply to it: its branch is
			// tree-local and has no origin ref *by design*, and skipping on
			// that would mean a review tree never asked about its own PRs.
			if mem.Review != nil {
				targets = append(targets, FetchTarget{
					Path:   mem.Path,
					Branch: mem.Branch,
					Key:    key,
					Ref:    github.PRRef{Number: mem.Review.Number, URL: mem.Review.URL},
				})
				continue
			}
			if !cache.KnowsPR(key) && !git.HasRemoteBranch(mem.Path, mem.Branch) {
				continue
			}
			targets = append(targets, FetchTarget{Path: mem.Path, Branch: mem.Branch, Key: key})
		}
	}
	return targets
}

// FetchOutcome reports what a fetch round did. Skipped means another process
// owned the round (the cross-process try-lock was busy) and this one ceded —
// distinct from an error, and from a successful but empty round.
type FetchOutcome struct {
	Skipped bool
	Err     error
}

// FetchPRs looks up each target and writes the results to the cache. The gh
// calls run under a cross-process try-lock (PRCacheLockPath): with one sidebar
// per Zellij tab plus a dashboard all polling independently, only the process
// that wins the lock fetches each round while the rest cede and pick up the
// cache it writes. That, not per-process throttling alone, is what stops a
// burst of concurrent gh calls from tripping GitHub's rate limit. A rate-limit
// response arms a cooldown that survives restarts.
//
// The caller is expected to have already re-read the cache and checked
// InBackoff before building targets; FetchPRs re-checks both under the lock,
// since a peer may have armed a backoff while this round queued.
func FetchPRs(targets []FetchTarget, cache *github.Cache, force bool, staleAge time.Duration) FetchOutcome {
	if len(targets) == 0 {
		return FetchOutcome{}
	}
	outcome := FetchOutcome{Skipped: true}
	lockErr := config.TryFileLock(PRCacheLockPath(), func() error {
		// Under the lock, re-read the cache: while we queued to build targets
		// another process may have populated statuses or armed a backoff.
		_ = cache.Load()
		if cache.InBackoff(time.Now()) {
			return nil
		}
		var lastErr error
		for _, t := range targets {
			// A peer that just held the lock may have refreshed this entry;
			// don't re-fetch what is already fresh.
			if !force && !cache.IsStale(t.Key, staleAge) {
				continue
			}
			info, err := resolveTarget(t, cache)
			if err != nil {
				lastErr = err
				if github.IsPermanentError(err) || github.IsRateLimited(err) {
					if github.IsRateLimited(err) {
						cache.SetRetryAfter(time.Now().Add(RateLimitCooldown))
					}
					_ = cache.Save()
					outcome = FetchOutcome{Err: err}
					return nil
				}
				continue
			}
			cache.Set(t.Key, info)
		}
		_ = cache.Save()
		outcome = FetchOutcome{Err: lastErr}
		return nil
	})
	if errors.Is(lockErr, config.ErrLockBusy) {
		// Another process owns this round; cede and pick up the cache it writes.
		return FetchOutcome{Skipped: true}
	}
	if lockErr != nil {
		return FetchOutcome{Err: lockErr}
	}
	return outcome
}

// resolveTarget looks one target up, by number when it is pinned to a PR and by
// branch head otherwise.
func resolveTarget(t FetchTarget, cache *github.Cache) (*github.PRInfo, error) {
	if t.Ref.Number != 0 {
		return github.ResolvePRByNumber(t.Path, t.Ref)
	}
	return github.ResolvePR(t.Path, t.Branch, cache.Ref(t.Key))
}
