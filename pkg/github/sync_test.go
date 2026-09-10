package github

import (
	"errors"
	"path/filepath"
	"testing"
	"time"
)

// fakeGH swaps the package's gh entry points for the duration of a test.
type fakeGH struct {
	polls          []RepoPoll
	pollErr        error
	pollN          int
	lookupN        int
	graphqlLookupN int
	lookup         *PRInfo
	lookupErr      error
}

func (f *fakeGH) install(t *testing.T, remote string, remoteBranch bool) {
	t.Helper()
	f.installRemotes(t, func(string) string { return remote }, remoteBranch)
}

// installRemotes is install for tests that need different checkouts to resolve
// to different repos.
func (f *fakeGH) installRemotes(t *testing.T, remoteFor func(string) string, remoteBranch bool) {
	t.Helper()
	origPoll, origLookup, origREST := pollRepo, lookupBranch, lookupREST
	origURL, origHas := originURL, hasRemote
	t.Cleanup(func() {
		pollRepo, lookupBranch, lookupREST = origPoll, origLookup, origREST
		originURL, hasRemote = origURL, origHas
	})

	pollRepo = func(string, RepoRef, string) (RepoPoll, error) {
		f.pollN++
		if f.pollErr != nil {
			return RepoPoll{}, f.pollErr
		}
		return f.polls[min(f.pollN-1, len(f.polls)-1)], nil
	}
	answer := func() (*PRInfo, error) {
		f.lookupN++
		if f.lookupErr != nil {
			return nil, f.lookupErr
		}
		if f.lookup != nil {
			return f.lookup, nil
		}
		return &PRInfo{Status: PRNone, FetchedAt: time.Now()}, nil
	}
	lookupBranch = func(string, string) (*PRInfo, error) {
		f.graphqlLookupN++
		return answer()
	}
	lookupREST = func(string, RepoRef, string) (*PRInfo, error) { return answer() }
	originURL = remoteFor
	hasRemote = func(string, string) bool { return remoteBranch }
}

const (
	testRemote = "git@github.com:acme/widgets.git"
	testRepo   = "/repo"
	testBranch = "wt/a/one"
	testETag   = `W/"one"`
)

func syncCache(t *testing.T) *Cache {
	t.Helper()
	return NewCache(filepath.Join(t.TempDir(), "pr-status.json"))
}

func runSync(t *testing.T, c *Cache, targets []Target, opts SyncOptions) SyncReport {
	t.Helper()
	var report SyncReport
	if err := c.Mutate(func(w *Writable) error {
		report = Sync(w, targets, opts)
		return nil
	}); err != nil {
		t.Fatalf("mutate: %v", err)
	}
	return report
}

func TestSyncAppliesPollResults(t *testing.T) {
	f := &fakeGH{polls: []RepoPoll{{
		ETag: testETag,
		PRs: []PollPR{
			{HeadRef: testBranch, Info: &PRInfo{Number: 1, Status: PROpen, FetchedAt: time.Now()}},
			{HeadRef: "wt/a/two", Info: &PRInfo{Number: 2, Status: PRMerged, FetchedAt: time.Now()}},
		},
	}}}
	f.install(t, testRemote, true)

	c := syncCache(t)
	report := runSync(t, c, []Target{
		{RepoPath: testRepo, Branch: testBranch},
		{RepoPath: testRepo, Branch: "wt/a/two"},
	}, SyncOptions{MaxAge: time.Minute})

	if f.pollN != 1 {
		t.Errorf("polled %d times, want one poll for the repo", f.pollN)
	}
	if f.lookupN != 0 {
		t.Errorf("spent %d per-branch lookups, want none", f.lookupN)
	}
	if report.Updated != 2 {
		t.Errorf("Updated = %d, want 2", report.Updated)
	}
	if info := c.Get(testBranch); info == nil || info.Status != PROpen {
		t.Errorf("wt/a/one = %+v, want the polled open PR", info)
	}
	if got := c.RepoState("acme/widgets").ETag; got != testETag {
		t.Errorf("stored ETag = %q, want the poll's", got)
	}
}

// A 304 costs nothing and still proves every tracked branch is current — the
// entries must be marked fresh, or they would age out and be re-fetched one
// branch at a time, which is the whole cost this design removes.
func TestSyncNotModifiedConfirmsEntries(t *testing.T) {
	c := syncCache(t)
	stale := time.Now().Add(-time.Hour)
	if err := c.Mutate(func(w *Writable) error {
		w.Set(testBranch, &PRInfo{Number: 1, Status: PROpen, FetchedAt: stale})
		w.SetRepoState("acme/widgets", RepoState{ETag: testETag, PolledAt: stale})
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	f := &fakeGH{polls: []RepoPoll{{NotModified: true, ETag: testETag}}}
	f.install(t, testRemote, true)

	report := runSync(t, c, []Target{{RepoPath: testRepo, Branch: testBranch}}, SyncOptions{MaxAge: time.Minute})

	if report.NotModified != 1 || report.Confirmed != 1 {
		t.Errorf("report = %+v, want one not-modified repo and one confirmed entry", report)
	}
	if f.lookupN != 0 {
		t.Errorf("spent %d lookups on an unchanged repo, want none", f.lookupN)
	}
	if c.IsStale(testBranch, time.Minute) {
		t.Error("a confirmed entry should count as fresh")
	}
	if info := c.Get(testBranch); info.Number != 1 || info.Status != PROpen {
		t.Errorf("confirming must not change what the entry says: %+v", info)
	}
}

func TestSyncCompleteListingProvesAbsence(t *testing.T) {
	f := &fakeGH{polls: []RepoPoll{{ETag: testPollETag, Truncated: false, PRs: nil}}}
	f.install(t, testRemote, true)

	c := syncCache(t)
	report := runSync(t, c, []Target{{RepoPath: testRepo, Branch: "wt/a/no-pr"}}, SyncOptions{MaxAge: time.Minute})

	if f.lookupN != 0 {
		t.Errorf("spent %d lookups, want none — the listing already proved absence", f.lookupN)
	}
	if report.Updated != 1 {
		t.Errorf("Updated = %d, want the absence recorded", report.Updated)
	}
	info := c.Get("wt/a/no-pr")
	if info == nil || info.Status != PRNone {
		t.Fatalf("entry = %+v, want a cached PRNone", info)
	}
	if c.IsStale("wt/a/no-pr", time.Minute) {
		t.Error("a freshly established absence should not be stale")
	}
}

func TestSyncTruncatedListingFallsBackToLookup(t *testing.T) {
	f := &fakeGH{polls: []RepoPoll{{
		ETag:      testPollETag,
		Truncated: true,
		Oldest:    time.Now().Add(-24 * time.Hour),
	}}}
	f.install(t, testRemote, true)

	c := syncCache(t)
	runSync(t, c, []Target{{RepoPath: testRepo, Branch: "wt/a/unknown"}}, SyncOptions{MaxAge: time.Minute})

	if f.lookupN != 1 {
		t.Errorf("lookups = %d, want one — a truncated listing cannot prove absence", f.lookupN)
	}
}

// The regression that a 304 tempts you into: "not modified" says nothing about
// whether the last listing covered the repo's whole history, so a repo known to
// be truncated must not have absence inferred for it.
func TestSyncNotModifiedDoesNotFabricateAbsence(t *testing.T) {
	c := syncCache(t)
	if err := c.Mutate(func(w *Writable) error {
		w.SetRepoState("acme/widgets", RepoState{
			ETag: testETag, PolledAt: time.Now().Add(-time.Hour), Truncated: true,
		})
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	// The lookup answers with a real PR, so a fabricated absence and a genuine
	// resolution are distinguishable in the cache.
	f := &fakeGH{
		polls:  []RepoPoll{{NotModified: true, ETag: testETag}},
		lookup: &PRInfo{Number: 7, Status: PROpen, FetchedAt: time.Now()},
	}
	f.install(t, testRemote, true)

	runSync(t, c, []Target{{RepoPath: testRepo, Branch: "wt/a/unknown"}}, SyncOptions{MaxAge: time.Minute})

	if f.lookupN != 1 {
		t.Errorf("lookups = %d, want the unknown branch resolved by a lookup", f.lookupN)
	}
	info := c.Get("wt/a/unknown")
	if info == nil || info.Number != 7 {
		t.Errorf("entry = %+v, want the PR the lookup found — a 304 on a truncated "+
			"listing must not be turned into 'no PR'", info)
	}
	if !c.RepoState("acme/widgets").Truncated {
		t.Error("truncation must survive a not-modified round")
	}
}

func TestSyncRateLimitArmsExactCooldown(t *testing.T) {
	reset := time.Now().Add(9 * time.Minute).Truncate(time.Second)
	f := &fakeGH{pollErr: &RateLimitedError{Resource: ResourceCore, ResetAt: reset}}
	f.install(t, testRemote, true)

	c := syncCache(t)
	report := runSync(t, c, []Target{
		{RepoPath: "/repo-a", Branch: testBranch},
		{RepoPath: "/repo-b", Branch: "wt/b/one"},
	}, SyncOptions{MaxAge: time.Minute})

	if !errors.Is(report.Err, ErrGHRateLimited) {
		t.Errorf("report.Err = %v, want a rate limit", report.Err)
	}
	if !c.InBackoff(ResourceCore, reset.Add(-time.Second)) {
		t.Error("cooldown should be armed until the reported reset")
	}
	if c.InBackoff(ResourceCore, reset.Add(time.Second)) {
		t.Error("cooldown should end exactly at the reported reset, not later")
	}
	if f.lookupN != 0 {
		t.Error("a rate-limited round must not keep spending on lookups")
	}
}

func TestSyncPausedWhileInBackoff(t *testing.T) {
	c := syncCache(t)
	if err := c.Mutate(func(w *Writable) error {
		w.SetRetryAfter(ResourceCore, time.Now().Add(10*time.Minute))
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	f := &fakeGH{polls: []RepoPoll{{ETag: testPollETag}}}
	f.install(t, testRemote, true)

	report := runSync(t, c, []Target{{RepoPath: testRepo, Branch: testBranch}}, SyncOptions{MaxAge: time.Minute})

	if !report.Paused {
		t.Error("a round inside the cooldown should report itself paused")
	}
	if f.pollN != 0 || f.lookupN != 0 {
		t.Errorf("paused round still called gh: polls=%d lookups=%d", f.pollN, f.lookupN)
	}
}

func TestSyncCapsFallbackLookups(t *testing.T) {
	f := &fakeGH{polls: []RepoPoll{{ETag: testPollETag, Truncated: true, Oldest: time.Now()}}}
	f.install(t, testRemote, true)

	targets := make([]Target, 0, 8)
	for _, b := range []string{"b1", "b2", "b3", "b4", "b5", "b6", "b7", "b8"} {
		targets = append(targets, Target{RepoPath: testRepo, Branch: "wt/a/" + b})
	}

	c := syncCache(t)
	report := runSync(t, c, targets, SyncOptions{MaxAge: time.Minute, MaxLookups: 3})

	if f.lookupN != 3 {
		t.Errorf("lookups = %d, want the cap of 3", f.lookupN)
	}
	if report.Lookups != 3 {
		t.Errorf("report.Lookups = %d, want 3", report.Lookups)
	}
}

func TestSyncSkipsUnpushedBranches(t *testing.T) {
	f := &fakeGH{polls: []RepoPoll{{ETag: testPollETag, Truncated: true, Oldest: time.Now()}}}
	f.install(t, testRemote, false) // no origin/<branch> ref

	c := syncCache(t)
	runSync(t, c, []Target{{RepoPath: testRepo, Branch: "wt/a/local-only"}}, SyncOptions{MaxAge: time.Minute})

	if f.lookupN != 0 {
		t.Error("an unpushed branch cannot have a PR; asking is a wasted call")
	}
}

func TestSyncNonGitHubRemoteUsesLookup(t *testing.T) {
	f := &fakeGH{}
	f.install(t, "git@gitlab.com:team/thing.git", true)

	c := syncCache(t)
	runSync(t, c, []Target{{RepoPath: testRepo, Branch: testBranch}}, SyncOptions{MaxAge: time.Minute})

	if f.pollN != 0 {
		t.Error("a non-GitHub remote cannot be polled by owner/name")
	}
	if f.lookupN != 1 {
		t.Errorf("lookups = %d, want the per-branch path to handle it", f.lookupN)
	}
	if f.graphqlLookupN != 1 {
		t.Error("a non-GitHub remote can only be resolved by gh's own remote resolution")
	}
}

// Several checkouts of one repo are one poll, not one each. Supatree gives
// every tree its own checkout, so grouping by path meant polling the same repo
// once per tree — the extra requests came back 304 and cost no quota, but they
// were still round trips, and they made a round take a minute.
func TestSyncGroupsCheckoutsOfOneRepo(t *testing.T) {
	f := &fakeGH{polls: []RepoPoll{{ETag: testPollETag}}}
	f.install(t, testRemote, true)

	c := syncCache(t)
	runSync(t, c, []Target{
		{RepoPath: "/trees/one/repos/widgets", Branch: "st/one/widgets"},
		{RepoPath: "/trees/two/repos/widgets", Branch: "st/two/widgets"},
		{RepoPath: "/trees/three/repos/widgets", Branch: "st/three/widgets"},
	}, SyncOptions{MaxAge: time.Minute})

	if f.pollN != 1 {
		t.Errorf("polls = %d, want one for the repo all three checkouts share", f.pollN)
	}
}

func TestSyncPollsEachRepoOnce(t *testing.T) {
	remotes := map[string]string{
		"/repo-a": "git@github.com:acme/widgets.git",
		"/repo-b": "git@github.com:acme/gadgets.git",
	}
	f := &fakeGH{polls: []RepoPoll{{ETag: testPollETag}}}
	f.installRemotes(t, func(path string) string { return remotes[path] }, true)

	c := syncCache(t)
	runSync(t, c, []Target{
		{RepoPath: "/repo-a", Branch: "wt/a/one"},
		{RepoPath: "/repo-a", Branch: "wt/a/two"},
		{RepoPath: "/repo-a", Branch: "wt/a/three"},
		{RepoPath: "/repo-b", Branch: "wt/b/one"},
	}, SyncOptions{MaxAge: time.Minute})

	if f.pollN != 2 {
		t.Errorf("polls = %d, want one per repo (not one per branch)", f.pollN)
	}
}

// The fallback for a GitHub repo must stay on REST. The GraphQL bucket is the
// one agents drain with gh pr view/checks, and it is routinely exhausted while
// the core bucket is untouched — a fallback that reaches for GraphQL fails
// exactly when the sidebar most needs to resolve a new branch.
func TestSyncFallbackStaysOffGraphQL(t *testing.T) {
	f := &fakeGH{polls: []RepoPoll{{ETag: testPollETag, Truncated: true, Oldest: time.Now()}}}
	f.install(t, testRemote, true)

	c := syncCache(t)
	runSync(t, c, []Target{{RepoPath: testRepo, Branch: "wt/a/unknown"}}, SyncOptions{MaxAge: time.Minute})

	if f.lookupN != 1 {
		t.Fatalf("lookups = %d, want one", f.lookupN)
	}
	if f.graphqlLookupN != 0 {
		t.Error("a GitHub repo's fallback must use the REST lookup, not gh pr list")
	}
}

// A repo the account cannot see (private to another org, renamed, deleted)
// answers 404 forever. Retrying it every round spends a request each time and
// paints a permanent error in the sidebar, so the fact is remembered — and a
// forced refresh is the way to re-test it.
func TestSyncRemembersUnavailableRepo(t *testing.T) {
	f := &fakeGH{pollErr: ErrRepoNotFound}
	f.install(t, testRemote, true)

	c := syncCache(t)
	first := runSync(t, c, []Target{{RepoPath: testRepo, Branch: testBranch}}, SyncOptions{MaxAge: time.Minute})
	if first.Skipped != 1 {
		t.Errorf("Skipped = %d, want the repo recorded as unavailable", first.Skipped)
	}
	if first.Err != nil {
		t.Errorf("Err = %v, want an unreachable repo not to fail the whole round", first.Err)
	}
	if !c.RepoState("acme/widgets").Unavailable {
		t.Fatal("the repo should be remembered as unavailable")
	}

	second := runSync(t, c, []Target{{RepoPath: testRepo, Branch: testBranch}}, SyncOptions{MaxAge: time.Minute})
	if f.pollN != 1 {
		t.Errorf("polls = %d, want the second round to skip the unavailable repo", f.pollN)
	}
	if second.Skipped != 1 {
		t.Errorf("Skipped = %d, want the skip reported", second.Skipped)
	}

	// A forced refresh is the user asking us to try again.
	runSync(t, c, []Target{{RepoPath: testRepo, Branch: testBranch}}, SyncOptions{MaxAge: time.Minute, Force: true})
	if f.pollN != 2 {
		t.Errorf("polls = %d, want a forced refresh to retry the repo", f.pollN)
	}
}

// One bad repo must not stop the others.
func TestSyncUnavailableRepoDoesNotBlockOthers(t *testing.T) {
	remotes := map[string]string{
		"/bad":  "git@github.com:acme/missing.git",
		"/good": testRemote,
	}
	calls := 0
	f := &fakeGH{}
	f.installRemotes(t, func(p string) string { return remotes[p] }, true)
	origPoll := pollRepo
	t.Cleanup(func() { pollRepo = origPoll })
	pollRepo = func(_ string, ref RepoRef, _ string) (RepoPoll, error) {
		calls++
		if ref.Name == "missing" {
			return RepoPoll{}, ErrRepoNotFound
		}
		return RepoPoll{ETag: testPollETag, PRs: []PollPR{
			{HeadRef: testBranch, Info: &PRInfo{Number: 3, Status: PROpen, FetchedAt: time.Now()}},
		}}, nil
	}

	c := syncCache(t)
	report := runSync(t, c, []Target{
		{RepoPath: "/bad", Branch: "wt/x/one"},
		{RepoPath: "/good", Branch: testBranch},
	}, SyncOptions{MaxAge: time.Minute})

	if calls != 2 {
		t.Errorf("polls = %d, want both repos attempted", calls)
	}
	if report.Updated != 1 {
		t.Errorf("Updated = %d, want the reachable repo still applied", report.Updated)
	}
	if c.Get(testBranch) == nil {
		t.Error("the reachable repo's PR should be cached")
	}
}

// A rate limit met during the fallback stage must pause everything too, and
// stop the round rather than spending the rest of its budget proving it.
func TestSyncRateLimitDuringFallbackStops(t *testing.T) {
	reset := time.Now().Add(4 * time.Minute).Truncate(time.Second)
	f := &fakeGH{
		polls:     []RepoPoll{{ETag: testPollETag, Truncated: true, Oldest: time.Now()}},
		lookupErr: &RateLimitedError{Resource: ResourceCore, ResetAt: reset},
	}
	f.install(t, testRemote, true)

	c := syncCache(t)
	report := runSync(t, c, []Target{
		{RepoPath: testRepo, Branch: "wt/a/one"},
		{RepoPath: testRepo, Branch: "wt/a/two"},
		{RepoPath: testRepo, Branch: "wt/a/three"},
	}, SyncOptions{MaxAge: time.Minute})

	if f.lookupN != 1 {
		t.Errorf("lookups = %d, want the round to stop after the first refusal", f.lookupN)
	}
	if !errors.Is(report.Err, ErrGHRateLimited) {
		t.Errorf("Err = %v, want the rate limit reported", report.Err)
	}
	if !c.InBackoff(ResourceCore, reset.Add(-time.Second)) {
		t.Error("cooldown should be armed from the fallback path too")
	}
}

func TestBudgetAllows(t *testing.T) {
	now := time.Now()
	tests := []struct {
		name   string
		budget Budget
		want   bool
	}{
		{"unknown observation allows", Budget{}, true},
		{"plenty left", Budget{Remaining: 4000, ObservedAt: now}, true},
		{"below the reserve", Budget{Remaining: 100, ObservedAt: now}, false},
		{"exactly the reserve", Budget{Remaining: 500, ObservedAt: now}, false},
		{
			name:   "expired observation allows — the window has refilled",
			budget: Budget{Remaining: 0, ResetAt: now.Add(-time.Minute), ObservedAt: now.Add(-time.Hour)},
			want:   true,
		},
	}
	for _, tc := range tests {
		if got := tc.budget.Allows(DefaultReserve, now); got != tc.want {
			t.Errorf("%s: Allows = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestSyncRecordsBudgetFromPoll(t *testing.T) {
	observed := Budget{Resource: ResourceCore, Limit: 5000, Remaining: 4321,
		ResetAt: time.Now().Add(time.Hour), ObservedAt: time.Now()}
	f := &fakeGH{polls: []RepoPoll{{ETag: testPollETag, Budget: observed}}}
	f.install(t, testRemote, true)

	c := syncCache(t)
	runSync(t, c, []Target{{RepoPath: testRepo, Branch: testBranch}}, SyncOptions{MaxAge: time.Minute})

	got := c.Budget()
	if got.Remaining != 4321 || got.Resource != ResourceCore {
		t.Errorf("budget = %+v, want the observation the poll carried", got)
	}
}

// Polls are conditional and usually free, but a fallback lookup is always
// charged — so a round stops spending before it eats the headroom the user's
// own gh calls need.
func TestSyncDefersLookupsBelowReserve(t *testing.T) {
	low := Budget{Resource: ResourceCore, Limit: 5000, Remaining: 10,
		ResetAt: time.Now().Add(time.Hour), ObservedAt: time.Now()}
	f := &fakeGH{polls: []RepoPoll{{ETag: testPollETag, Truncated: true, Oldest: time.Now(), Budget: low}}}
	f.install(t, testRemote, true)

	c := syncCache(t)
	report := runSync(t, c, []Target{{RepoPath: testRepo, Branch: "wt/a/unknown"}}, SyncOptions{MaxAge: time.Minute})

	if f.lookupN != 0 {
		t.Errorf("lookups = %d, want none below the reserve", f.lookupN)
	}
	if report.Deferred != 1 {
		t.Errorf("Deferred = %d, want the branch left for a later round", report.Deferred)
	}
	if f.pollN != 1 {
		t.Error("the poll itself is conditional and should still run")
	}
}

// The person waiting on a manual refresh outranks the background reserve.
func TestSyncForcedRefreshSpendsIntoTheReserve(t *testing.T) {
	low := Budget{Resource: ResourceCore, Limit: 5000, Remaining: 200,
		ResetAt: time.Now().Add(time.Hour), ObservedAt: time.Now()}
	f := &fakeGH{polls: []RepoPoll{{ETag: testPollETag, Truncated: true, Oldest: time.Now(), Budget: low}}}
	f.install(t, testRemote, true)

	c := syncCache(t)
	runSync(t, c, []Target{{RepoPath: testRepo, Branch: "wt/a/unknown"}},
		SyncOptions{MaxAge: time.Minute, Force: true})

	if f.lookupN != 1 {
		t.Errorf("lookups = %d, want a forced refresh to proceed above the hard floor", f.lookupN)
	}
}

// The buckets run out independently. An exhausted graphql bucket used to arm
// one global cooldown, which paused the conditional core-bucket polls too —
// pausing work that costs nothing because unrelated work had run out.
func TestSyncGraphQLCooldownDoesNotPausePolls(t *testing.T) {
	c := syncCache(t)
	if err := c.Mutate(func(w *Writable) error {
		w.SetRetryAfter(ResourceGraphQL, time.Now().Add(30*time.Minute))
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	f := &fakeGH{polls: []RepoPoll{{ETag: testPollETag, PRs: []PollPR{
		{HeadRef: testBranch, Info: &PRInfo{Number: 5, Status: PROpen, FetchedAt: time.Now()}},
	}}}}
	f.install(t, testRemote, true)

	report := runSync(t, c, []Target{{RepoPath: testRepo, Branch: testBranch}}, SyncOptions{MaxAge: time.Minute})

	if report.Paused {
		t.Error("a graphql cooldown must not pause the core-bucket polls")
	}
	if f.pollN != 1 {
		t.Errorf("polls = %d, want the round to proceed", f.pollN)
	}
	if c.Get(testBranch) == nil {
		t.Error("the poll's result should have been applied")
	}
}

// ...but it does stop the lookups that need that bucket.
func TestSyncGraphQLCooldownStopsGraphQLLookups(t *testing.T) {
	c := syncCache(t)
	if err := c.Mutate(func(w *Writable) error {
		w.SetRetryAfter(ResourceGraphQL, time.Now().Add(30*time.Minute))
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	f := &fakeGH{}
	f.install(t, "git@gitlab.com:team/thing.git", true) // non-GitHub → gh pr list

	report := runSync(t, c, []Target{{RepoPath: testRepo, Branch: testBranch}}, SyncOptions{MaxAge: time.Minute})

	if f.graphqlLookupN != 0 {
		t.Error("a graphql lookup must not run while that bucket is paused")
	}
	if report.Deferred != 1 {
		t.Errorf("Deferred = %d, want the branch left for later", report.Deferred)
	}
}

func TestArmCooldownTargetsTheFailingBucket(t *testing.T) {
	now := time.Now()
	reset := now.Add(10 * time.Minute)

	tests := []struct {
		name        string
		err         error
		fallback    string
		coreBlocked bool
		gqlBlocked  bool
	}{
		{
			name:       "graphql exhaustion pauses graphql only",
			err:        &RateLimitedError{Resource: ResourceGraphQL, ResetAt: reset},
			fallback:   ResourceCore,
			gqlBlocked: true,
		},
		{
			name:        "core exhaustion pauses core only",
			err:         &RateLimitedError{Resource: ResourceCore, ResetAt: reset},
			fallback:    ResourceCore,
			coreBlocked: true,
		},
		{
			name:        "the burst limit applies to everything",
			err:         &RateLimitedError{Resource: resourceSecondary, ResetAt: reset},
			fallback:    ResourceCore,
			coreBlocked: true,
			gqlBlocked:  true,
		},
		{
			name:       "an unlabelled limit pauses the bucket the call spends",
			err:        ErrGHRateLimited,
			fallback:   ResourceGraphQL,
			gqlBlocked: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := syncCache(t)
			if err := c.Mutate(func(w *Writable) error {
				if !armCooldown(w, tc.err, now, tc.fallback) {
					t.Error("expected a cooldown to be armed")
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			if got := c.InBackoff(ResourceCore, now); got != tc.coreBlocked {
				t.Errorf("core blocked = %v, want %v", got, tc.coreBlocked)
			}
			if got := c.InBackoff(ResourceGraphQL, now); got != tc.gqlBlocked {
				t.Errorf("graphql blocked = %v, want %v", got, tc.gqlBlocked)
			}
		})
	}
}
