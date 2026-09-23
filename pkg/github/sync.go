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
	// Key is the cache entry this target writes; empty means Branch. A review
	// member is keyed by its PR reference rather than its tree-local branch.
	Key string
	// Ref pins the target to a known PR. A pinned target is matched by number
	// only: its branch is tree-local and provably has no PR of its own, so a
	// head lookup would be a guaranteed-empty round trip.
	Ref PRRef
}

func (t Target) key() string {
	if t.Key != "" {
		return t.Key
	}
	return t.Branch
}

func (t Target) pinned() bool { return t.Ref.Number != 0 }

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
	Details     int // GraphQL lookups spent refreshing open PRs' review and checks
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
	lookupNumber = LookupPRByNumber
	originURL    = git.BaseRemoteURL
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

	groups := groupByRepo(targets)
	var unresolved []unresolvedTarget
	for _, group := range groups {
		done, err := syncRepo(w, group, opts, now, &report)
		if err != nil {
			report.Err = err
			// A rate limit or a broken gh applies to every repo, not just this
			// one: stop the round rather than proving it repo by repo.
			if ArmCooldown(w, err, now, ResourceCore) || IsPermanentError(err) {
				return report
			}
			continue
		}
		unresolved = append(unresolved, done...)
	}

	if !resolveFallbacks(w, unresolved, opts, now, &report) {
		return report
	}
	refreshDetails(w, groups, opts, now, &report)
	return report
}

// resolveFallbacks spends one lookup on each target the polls could not answer
// — a branch with no PR in a repo whose listing was truncated, a pinned PR not
// cached yet, or a repo with no usable GitHub remote — capped so a cold cache
// spreads over rounds. It reports false when the round must stop.
func resolveFallbacks(w *Writable, unresolved []unresolvedTarget, opts SyncOptions, now time.Time, report *SyncReport) bool {
	for _, u := range unresolved {
		t := u.target
		if report.Lookups >= opts.MaxLookups {
			break
		}
		if w.RepoState(u.repoKey).Unavailable(now) && !opts.Force {
			// An earlier lookup this round just found the repo invisible.
			continue
		}
		resource := u.resource()
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
		if !opts.Force && !w.IsStale(t.key(), opts.MaxAge) {
			continue
		}
		// An unpushed branch cannot have a PR, so asking is a guaranteed-empty
		// round trip. A pinned target's branch is tree-local by design, so the
		// rule does not apply to it.
		if !t.pinned() && !w.KnowsPR(t.key()) && !hasRemote(t.RepoPath, t.Branch) {
			continue
		}
		report.Lookups++
		info, err := lookupOne(u, w.Ref(t.key()))
		if err != nil {
			report.Err = err
			if IsRepoNotFound(err) {
				// A fact about the repo, not a failure of the round: back it off
				// and carry on with the others.
				markUnavailable(w, u.repoKey, now)
				report.Skipped++
				continue
			}
			if ArmCooldown(w, err, now, resource) {
				// The bucket this lookup needed is gone; the others may still be
				// usable, so keep going rather than abandoning the round.
				continue
			}
			if IsPermanentError(err) {
				return false
			}
			continue
		}
		if info != nil && info.DetailedAt.IsZero() {
			// A REST lookup carries no review verdict or check rollup; keep the
			// previous ones rather than blanking the badges until the details
			// refresh (which a GraphQL cooldown can hold off indefinitely).
			info = mergePolled(w.Get(t.key()), info)
		}
		w.Set(t.key(), info)
		report.Updated++
	}
	return true
}

// refreshDetails keeps review verdicts and check rollups current for open PRs.
// Neither is in the REST listing a poll reads, and a check run finishing does
// not even change the listing's ETag, so they need their own lookup: one
// GraphQL query per open PR, on the ordinary MaxAge schedule, or sooner when a
// poll saw the PR move. Merged, closed and PR-less branches — the great majority
// of what a sidebar tracks — never reach this, which is what keeps the GraphQL
// bucket free for the agents.
func refreshDetails(w *Writable, groups []repoGroup, opts SyncOptions, now time.Time, report *SyncReport) {
	seen := make(map[string]bool)
	for _, group := range groups {
		// A repo backed off as invisible this round (or earlier) would only
		// fail again.
		if w.RepoState(group.key).Unavailable(now) {
			continue
		}
		if !refreshGroupDetails(w, group, opts, now, report, seen) {
			return
		}
	}
}

// refreshGroupDetails is refreshDetails for one repo. It reports false when the
// round must stop.
func refreshGroupDetails(w *Writable, group repoGroup, opts SyncOptions, now time.Time, report *SyncReport, seen map[string]bool) bool {
	for _, t := range group.targets {
		key := t.key()
		if seen[key] {
			continue
		}
		seen[key] = true
		info := w.Get(key)
		if info == nil || info.Number == 0 || isTerminal(info.Status) {
			continue
		}
		if !opts.Force && now.Sub(info.DetailedAt) <= opts.MaxAge {
			continue
		}
		if report.Details >= opts.MaxLookups {
			report.Deferred++
			continue
		}
		if w.InBackoff(ResourceGraphQL, now) {
			report.Deferred++
			continue
		}
		report.Details++
		detailed, err := resolveByNumber(lookupNumber, t.RepoPath, PRRef{Number: info.Number, URL: info.URL})
		if err != nil {
			if errors.Is(err, ErrPRNotFound) {
				continue
			}
			report.Err = err
			if ArmCooldown(w, graphQLLimited(err), now, ResourceGraphQL) || IsPermanentError(err) {
				return false
			}
			continue
		}
		if detailed.Number == 0 {
			// Gone since the poll; the poll's answer stands until the next one.
			continue
		}
		w.Set(key, detailed)
	}
	return true
}

// unresolvedTarget is a target a poll could not settle, carrying the repo it
// belongs to so the fallback can use the REST lookup rather than the GraphQL
// one, and so a repo that turns out to be invisible can be backed off as a
// whole.
type unresolvedTarget struct {
	target  Target
	ref     RepoRef
	repoKey string
}

// resource is the bucket this target's lookup spends: REST for a branch in a
// repo we can address by owner/name, gh's own GraphQL resolution otherwise —
// and GraphQL always for a pinned PR, which is looked up by number.
func (u unresolvedTarget) resource() string {
	if u.ref.Valid() && !u.target.pinned() {
		return ResourceCore
	}
	return ResourceGraphQL
}

// lookupOne resolves a single target. A pinned one goes straight to its
// number. A branch is asked by head first, preferring REST — it spends from the
// core bucket, which background work has to itself, rather than the GraphQL
// bucket the agents live on — and falls back to the cached number when the head
// comes back empty, which is how a PR whose branch was renamed after it merged
// stays found (see resolvePR).
func lookupOne(u unresolvedTarget, prev PRRef) (*PRInfo, error) {
	t := u.target
	if t.pinned() {
		info, err := resolveByNumber(lookupNumber, t.RepoPath, t.Ref)
		return info, graphQLLimited(err)
	}
	byHead := func(repoPath, branch string) (*PRInfo, error) {
		if u.ref.Valid() {
			return lookupREST(repoPath, u.ref, branch)
		}
		return lookupBranch(repoPath, branch)
	}
	byNumber := func(repoPath string, number int) (*PRInfo, error) {
		info, err := lookupNumber(repoPath, number)
		return info, graphQLLimited(err)
	}
	return resolvePR(prLookup{byHead: byHead, byNumber: byNumber}, t.RepoPath, t.Branch, prev)
}

// graphQLLimited attributes a bare rate-limit error from a gh GraphQL command
// to the GraphQL bucket. gh's own commands report the limit only as text, and
// without this the cooldown would land on core — pausing the free polls over a
// bucket they do not spend.
func graphQLLimited(err error) error {
	var limited *RateLimitedError
	if IsRateLimited(err) && !errors.As(err, &limited) {
		return &RateLimitedError{Resource: ResourceGraphQL}
	}
	return err
}

func markUnavailable(w *Writable, repoKey string, now time.Time) {
	state := w.RepoState(repoKey)
	state.UnavailableUntil = now.Add(UnreachableBackoff)
	state.PolledAt = now
	w.SetRepoState(repoKey, state)
}

// syncRepo polls one repo and applies the result, returning the targets the
// poll could not settle.
func syncRepo(w *Writable, group repoGroup, opts SyncOptions, now time.Time, report *SyncReport) ([]unresolvedTarget, error) {
	state := w.RepoState(group.key)
	if state.Unavailable(now) && !opts.Force {
		// We already learned this repo is invisible to this account. Re-asking
		// costs a request per round and cannot succeed until someone fixes the
		// remote or the account; a forced refresh is how the user says to try.
		report.Skipped++
		return nil, nil
	}
	if !group.ref.Valid() {
		// Not a GitHub remote we can address by owner/name (an enterprise host,
		// or no origin at all). Let the per-branch path handle it — gh still
		// resolves those from the working directory.
		return unresolvedFor(group), nil
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
		markUnavailable(w, group.key, now)
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

	byHead := make(map[string]*PRInfo, len(poll.PRs))
	byNumber := make(map[int]*PRInfo, len(poll.PRs))
	for _, pr := range poll.PRs {
		// The page is newest first: keep the most recent PR for a reused head.
		if _, ok := byHead[pr.HeadRef]; !ok {
			byHead[pr.HeadRef] = pr.Info
		}
		byNumber[pr.Info.Number] = pr.Info
	}

	var unresolved []unresolvedTarget
	for _, t := range group.targets {
		key := t.key()
		if info := matchPolled(t, w.Ref(key), byHead, byNumber); info != nil {
			w.Set(key, mergePolled(w.Get(key), info))
			report.Updated++
			continue
		}
		// The target was not in the listing. If the listing is trustworthy, that
		// is itself an answer: a PR that had been created, updated, merged or
		// closed would have appeared at the top of it.
		if complete {
			prev := w.Get(key)
			// Only an entry verified at or after the repo's last poll can be
			// confirmed unchanged: the listing (or the 304) covers changes since
			// that poll, nothing older. An entry verified before it — tracked
			// by another process whose round consumed the delta without it as a
			// target, or a branch that has just come back into view — may have
			// missed a change that poll saw and dropped. One carried over a
			// rename or seeded from a review set has a zero FetchedAt and also
			// has to be looked up for real.
			if prev != nil && !prev.FetchedAt.IsZero() && !prev.FetchedAt.Before(state.PolledAt) {
				w.Touch(key, now)
				report.Confirmed++
				continue
			}
			if prev == nil && !truncated && !t.pinned() {
				// The listing covered the repo's entire history, so there is no
				// PR for this branch — an authoritative absence, recorded so we
				// never ask about it again.
				w.Set(key, &PRInfo{Status: PRNone, FetchedAt: now})
				report.Updated++
				continue
			}
		}
		unresolved = append(unresolved, unresolvedTarget{target: t, ref: group.ref, repoKey: group.key})
	}

	// poll.ETag carries the previous one forward on a 304, so this is correct in
	// both cases.
	w.SetRepoState(group.key, RepoState{ETag: poll.ETag, PolledAt: now, Truncated: truncated})
	return unresolved, nil
}

// matchPolled finds target's PR on a polled page. A pinned target is matched by
// its number. A branch is matched by head first — it may have picked up a new
// PR since a number was cached (a reused worktree name), and that is the one
// worth reporting — then by the number the cache holds for it, which is what
// still finds a PR whose head no longer matches the branch after a rename. A
// number recorded against another repo is not trusted.
func matchPolled(t Target, cached PRRef, byHead map[string]*PRInfo, byNumber map[int]*PRInfo) *PRInfo {
	if t.pinned() {
		if info := byNumber[t.Ref.Number]; info != nil && sameRepo(t.Ref.URL, info.URL) {
			return info
		}
		return nil
	}
	if info := byHead[t.Branch]; info != nil {
		return info
	}
	if cached.Number != 0 {
		if info := byNumber[cached.Number]; info != nil && sameRepo(cached.URL, info.URL) {
			return info
		}
	}
	return nil
}

// mergePolled folds a polled PR into what the cache held. The listing has no
// review verdict or check rollup, so those carry over from the previous entry
// for the same PR — blanking them every poll would flap the sidebar badges and
// fire spurious check events. If the PR moved (new head, or anything else that
// bumps updated_at), the carried verdicts are marked due for a refresh.
func mergePolled(prev, polled *PRInfo) *PRInfo {
	if prev == nil || prev.Number != polled.Number {
		return polled
	}
	merged := *polled
	merged.Review = prev.Review
	merged.Checks = prev.Checks
	merged.DetailedAt = prev.DetailedAt
	if !prev.UpdatedAt.Equal(polled.UpdatedAt) || prev.HeadOID != polled.HeadOID {
		merged.DetailedAt = time.Time{}
	}
	return &merged
}

// armCooldown pauses every process when the error says a quota is gone, using
// the reset the response reported rather than a guess, and against the bucket
// that actually ran out. fallback names the bucket the failing call spends,
// for errors that do not say. It reports whether a cooldown was armed.
func ArmCooldown(w *Writable, err error, now time.Time, fallback string) bool {
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
		out = append(out, unresolvedTarget{target: t, ref: group.ref, repoKey: group.key})
	}
	return out
}

type repoGroup struct {
	// key names the group in RepoState: "owner/name" for a GitHub repo, the
	// checkout path for anything else.
	key string
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
		groups = append(groups, repoGroup{key: key, path: t.RepoPath, ref: ref, targets: []Target{t}})
	}
	return groups
}
