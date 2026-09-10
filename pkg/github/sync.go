package github

import (
	"errors"
	"time"

	"github.com/panamafrancis/workbench/pkg/git"
)

// Sync is the single path from "branches a sidebar wants statuses for" to a
// filled cache, shared by both sidebars and the MCP tools. It polls each repo's
// PR list once — free when the repo has not changed — and only falls back to a
// per-branch lookup for branches a poll cannot answer.
//
// It runs inside a cache mutation, so the whole round holds the cache lock:
// that is what elects one fetcher among the sidebars, and what guarantees the
// round's writes merge into whatever peers wrote in the meantime.

// Target is a branch whose PR status the caller wants kept current. RepoPath is
// any clone or worktree of the repo — gh resolves the remote from it.
type Target struct {
	RepoPath string
	Branch   string
}

// SyncOptions tunes one round.
type SyncOptions struct {
	// MaxAge is how old an entry may be before a per-branch fallback lookup is
	// allowed for it. Repo polls ignore it: they are free and refresh everything.
	MaxAge time.Duration
	// Force re-polls unconditionally (no If-None-Match, so no 304) and ignores
	// MaxAge. This is what the user's explicit refresh does.
	Force bool
	// MaxLookups caps the per-branch fallback lookups in one round, so a cold
	// cache with many unresolved branches spends a bounded amount of quota and
	// finishes over several rounds instead of in one burst.
	MaxLookups int
	// Reserve is the rate-limit headroom this round must leave for everyone
	// else. Background rounds keep it; a user-forced refresh passes a smaller
	// one, because the person waiting for it outranks the background. Zero
	// means DefaultReserve.
	Reserve int
}

// Reserve floors. Background polling is nearly free, but the fallback lookups
// are real requests, and this cache is shared with whatever else on the machine
// is spending the same bucket — so a round stops short rather than taking the
// last of it.
const (
	DefaultReserve = 500
	ForcedReserve  = 50
)

// SyncReport describes what a round did, for the status line and for tests.
type SyncReport struct {
	Polled      int // repos polled and changed
	NotModified int // repos that answered 304, costing nothing
	Updated     int // branch entries written from poll results
	Confirmed   int // entries a poll proved unchanged
	Lookups     int // per-branch fallback lookups spent
	Skipped     int // repos skipped as unavailable to this account
	Deferred    int // branches left unresolved to stay above the reserve
	Paused      bool
	Err         error
}

// Swappable for tests; production wiring is the real gh calls.
var (
	pollRepo     = PollRepo
	lookupREST   = LookupBranchPR
	lookupBranch = LookupPR
	originURL    = git.OriginURL
	hasRemote    = git.HasRemoteBranch
)

const defaultMaxLookups = 10

// Sync brings w up to date for targets. It never returns an error separately —
// everything the caller needs is in the report, so a partial round still
// persists what it learned.
func Sync(w *Writable, targets []Target, opts SyncOptions) SyncReport {
	now := time.Now()
	report := SyncReport{}
	// Only the core bucket gates the round: that is what the polls spend, and
	// an exhausted graphql bucket must not stop work that costs nothing.
	if w.InBackoff(ResourceCore, now) {
		report.Paused = true
		return report
	}
	if opts.MaxLookups == 0 {
		opts.MaxLookups = defaultMaxLookups
	}
	if opts.Reserve == 0 {
		opts.Reserve = DefaultReserve
		if opts.Force {
			opts.Reserve = ForcedReserve
		}
	}

	var unresolved []unresolvedTarget
	for _, group := range groupByRepo(targets) {
		done, err := syncRepo(w, group, opts, now, &report)
		if err != nil {
			report.Err = err
			// A rate limit or a broken gh applies to every repo, not just this
			// one: stop the round rather than proving it repo by repo.
			if armCooldown(w, err, now, ResourceCore) || IsPermanentError(err) {
				return report
			}
			continue
		}
		unresolved = append(unresolved, done...)
	}

	// Whatever the polls could not answer — a branch with no PR in a repo whose
	// listing was truncated, or a repo with no usable GitHub remote — costs one
	// lookup each, capped so a cold cache spreads over rounds.
	for _, u := range unresolved {
		t := u.target
		if report.Lookups >= opts.MaxLookups {
			break
		}
		// A lookup's bucket depends on how it has to be made: REST for a repo we
		// can address by owner/name, gh's own GraphQL resolution otherwise.
		resource := ResourceCore
		if !u.ref.Valid() {
			resource = ResourceGraphQL
		}
		if w.InBackoff(resource, now) {
			report.Deferred++
			continue
		}
		// Unlike a conditional poll, a lookup is always charged. Stop before
		// eating into the headroom the user's own gh calls need; the branch
		// stays unresolved and the next round picks it up.
		if resource == ResourceCore && !w.Budget().Allows(opts.Reserve, now) {
			report.Deferred++
			continue
		}
		if !opts.Force && !w.IsStale(t.Branch, opts.MaxAge) {
			continue
		}
		// An unpushed branch cannot have a PR, so asking is a guaranteed-empty
		// round trip.
		if !w.KnowsPR(t.Branch) && !hasRemote(t.RepoPath, t.Branch) {
			continue
		}
		report.Lookups++
		info, err := lookupOne(u)
		if err != nil {
			report.Err = err
			if armCooldown(w, err, now, resource) {
				// The bucket this lookup needed is gone; the others may still be
				// usable, so keep going rather than abandoning the round.
				continue
			}
			if IsPermanentError(err) {
				return report
			}
			continue
		}
		w.Set(t.Branch, info)
		report.Updated++
	}
	return report
}

// unresolvedTarget is a branch a poll could not settle, carrying the repo it
// belongs to so the fallback can use the REST lookup rather than the GraphQL
// one.
type unresolvedTarget struct {
	target Target
	ref    RepoRef
}

// lookupOne resolves a single branch, preferring REST — it spends from the core
// bucket, which background work has to itself, rather than the GraphQL bucket
// the agents live on. Only a repo we cannot address by owner/name (an
// enterprise host, no origin) falls back to `gh pr list`, which resolves the
// remote from the working directory itself.
func lookupOne(u unresolvedTarget) (*PRInfo, error) {
	if u.ref.Valid() {
		return lookupREST(u.target.RepoPath, u.ref, u.target.Branch)
	}
	return lookupBranch(u.target.RepoPath, u.target.Branch)
}

// syncRepo polls one repo and applies the result, returning the targets the
// poll could not settle.
func syncRepo(w *Writable, group repoGroup, opts SyncOptions, now time.Time, report *SyncReport) ([]unresolvedTarget, error) {
	if !group.ref.Valid() {
		// Not a GitHub remote we can address by owner/name (an enterprise host,
		// or no origin at all). Let the per-branch path handle it — gh still
		// resolves those from the working directory.
		return unresolvedFor(group), nil
	}

	repo := group.ref.String()
	state := w.RepoState(repo)
	if state.Unavailable && !opts.Force {
		// We already learned this repo answers 404 for this account. Re-asking
		// costs a request per round forever and can never succeed on its own; a
		// forced refresh is how the user says to try again.
		report.Skipped++
		return nil, nil
	}
	etag := state.ETag
	if opts.Force {
		etag = "" // an unconditional poll, so a forced refresh really re-reads
	}

	poll, err := pollRepo(group.path, group.ref, etag)
	w.SetBudget(poll.Budget)
	if errors.Is(err, ErrRepoNotFound) {
		// Not a failure of the round — a fact about this repo. Record it and
		// carry on with the others.
		w.SetRepoState(repo, RepoState{Unavailable: true, PolledAt: now})
		report.Skipped++
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	// A page that reaches back past our last poll carries every change since;
	// one that stops short might have missed some, so we cannot treat a branch's
	// absence from it as meaningful. A 304 carries no page at all, so what we
	// know about coverage is what the last real listing told us.
	complete := poll.NotModified || poll.Complete(state.PolledAt)
	truncated := poll.Truncated
	if poll.NotModified {
		truncated = state.Truncated
		report.NotModified++
	} else {
		report.Polled++
	}

	seen := make(map[string]*PRInfo, len(poll.PRs))
	for _, pr := range poll.PRs {
		seen[pr.HeadRef] = pr.Info
	}

	var unresolved []unresolvedTarget
	for _, t := range group.targets {
		if info, ok := seen[t.Branch]; ok {
			w.Set(t.Branch, info)
			report.Updated++
			continue
		}
		// The branch was not in the listing. If the listing is trustworthy, that
		// is itself an answer: a PR that had been created, updated, merged or
		// closed would have appeared at the top of it.
		if complete {
			if w.Get(t.Branch) != nil {
				w.Touch(t.Branch, now)
				report.Confirmed++
				continue
			}
			if !truncated {
				// The listing covered the repo's entire history, so there is no
				// PR for this branch — an authoritative absence, recorded so we
				// never ask about it again.
				w.Set(t.Branch, &PRInfo{Status: PRNone, FetchedAt: now})
				report.Updated++
				continue
			}
		}
		unresolved = append(unresolved, unresolvedTarget{target: t, ref: group.ref})
	}

	// poll.ETag carries the previous one forward on a 304, so this is correct in
	// both cases.
	w.SetRepoState(repo, RepoState{ETag: poll.ETag, PolledAt: now, Truncated: truncated})
	return unresolved, nil
}

// armCooldown pauses every process when the error says a quota is gone, using
// the reset the response reported rather than a guess, and against the bucket
// that actually ran out. fallback names the bucket the failing call spends,
// for errors that do not say. It reports whether a cooldown was armed.
func armCooldown(w *Writable, err error, now time.Time, fallback string) bool {
	if !IsRateLimited(err) {
		return false
	}
	resource := fallback
	deadline := now.Add(FallbackCooldown)

	var limited *RateLimitedError
	if errors.As(err, &limited) {
		switch limited.Resource {
		case "":
			// Keep the caller's bucket.
		case resourceSecondary:
			// The burst limit is not a bucket — it applies to everything.
			resource = ResourceAll
		default:
			resource = limited.Resource
		}
		if !limited.ResetAt.IsZero() {
			deadline = limited.ResetAt
		}
	}
	w.SetRetryAfter(resource, deadline)
	return true
}

// FallbackCooldown is how long fetches pause after a rate-limit response that
// carried no reset time. Responses normally carry one, and an exact deadline is
// always preferred — this is only the floor for the ones that don't.
const FallbackCooldown = 15 * time.Minute

// unresolvedFor marks every target in a group as needing a per-branch lookup.
func unresolvedFor(group repoGroup) []unresolvedTarget {
	out := make([]unresolvedTarget, 0, len(group.targets))
	for _, t := range group.targets {
		out = append(out, unresolvedTarget{target: t, ref: group.ref})
	}
	return out
}

type repoGroup struct {
	// path is any checkout of the repo — gh only needs one to resolve the
	// remote from.
	path    string
	ref     RepoRef
	targets []Target
}

// groupByRepo collects targets per *repo*, not per checkout. That distinction
// is the difference between one request and one per worktree: a supatree gives
// every tree its own checkout of the same repo, so grouping by path polled
// admin-frontend six times a round (five of them 304s, but five round trips all
// the same). Repos with no usable GitHub remote group under their own path so
// the per-branch fallback can still handle them.
func groupByRepo(targets []Target) []repoGroup {
	index := make(map[string]int, len(targets))
	groups := make([]repoGroup, 0, len(targets))
	refs := make(map[string]RepoRef, len(targets))

	for _, t := range targets {
		ref, ok := refs[t.RepoPath]
		if !ok {
			ref, _ = RepoRefFromRemote(originURL(t.RepoPath))
			refs[t.RepoPath] = ref
		}
		// A repo we can address by owner/name is one group however many
		// checkouts of it we hold; anything else stays per-path.
		key := t.RepoPath
		if ref.Valid() {
			key = ref.String()
		}
		if i, seen := index[key]; seen {
			groups[i].targets = append(groups[i].targets, t)
			continue
		}
		index[key] = len(groups)
		groups = append(groups, repoGroup{path: t.RepoPath, ref: ref, targets: []Target{t}})
	}
	return groups
}
