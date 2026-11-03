package github

import (
	"strconv"
	"testing"
	"time"
)

func watchedPR(n int, status PRStatus, updated time.Time, reviewers ...string) PollPR {
	return PollPR{HeadRef: "b", Info: &PRInfo{
		Number: n, Status: status, URL: "https://github.com/acme/widgets/pull/" + strconv.Itoa(n),
		UpdatedAt: updated, FetchedAt: time.Now(), RequestedReviewers: reviewers, Body: "desc",
	}}
}

var acme = RepoRef{Owner: "acme", Name: "widgets"}

func TestWatchKeepsRequestedPRsAndFollowsThem(t *testing.T) {
	now := time.Now()
	f := &fakeGH{polls: []RepoPoll{
		{ETag: testETag, PRs: []PollPR{
			watchedPR(1, PROpen, now, "bot"),
			watchedPR(2, PROpen, now, "alice"), // not asked: ignored
			watchedPR(3, PRMerged, now, "bot"), // already over: ignored
		}},
		// The bot reviewed #1, so GitHub dropped the request.
		{ETag: `W/"two"`, PRs: []PollPR{watchedPR(1, PROpen, now.Add(time.Minute))}},
	}}
	f.install(t, testRemote, true)
	c := syncCache(t)
	targets := []Target{{RepoPath: testRepo, Watch: "Bot"}}

	runSync(t, c, targets, SyncOptions{MaxAge: time.Minute})
	if err := c.Load(); err != nil {
		t.Fatal(err)
	}
	got := c.Watched("bot")
	if len(got) != 1 || got[WatchKey("bot", acme, 1)] == nil {
		t.Fatalf("watched = %v, want only #1", got)
	}
	if got[WatchKey("bot", acme, 1)].Body != "desc" {
		t.Error("a watched entry keeps its description")
	}
	if f.lookupN != 0 || f.numberN != 0 {
		t.Errorf("a watch target spent %d lookups, %d detail lookups; want none", f.lookupN, f.numberN)
	}

	runSync(t, c, targets, SyncOptions{MaxAge: time.Minute})
	_ = c.Load()
	if info := c.Watched("bot")[WatchKey("bot", acme, 1)]; info == nil || info.Requests("bot") {
		t.Errorf("a watched PR must follow the request being answered, got %+v", info)
	}
}

// A repo already polled for its branches has an ETag whose 304 says "nothing
// new since a listing that ignored the bot". Adding a watch must re-read once.
func TestWatchSeedsPastAnOldETag(t *testing.T) {
	f := &fakeGH{polls: []RepoPoll{{ETag: testETag, PRs: []PollPR{watchedPR(1, PROpen, time.Now(), "bot")}}}}
	f.install(t, testRemote, true)
	var etags []string
	inner := pollRepo
	pollRepo = func(p string, r RepoRef, etag string) (RepoPoll, error) {
		etags = append(etags, etag)
		return inner(p, r, etag)
	}
	c := syncCache(t)
	if err := c.Mutate(func(w *Writable) error {
		w.SetRepoState("acme/widgets", RepoState{ETag: "old", PolledAt: time.Now()})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	targets := []Target{{RepoPath: testRepo, Branch: testBranch}, {RepoPath: testRepo, Watch: "bot"}}
	runSync(t, c, targets, SyncOptions{MaxAge: time.Minute})
	runSync(t, c, targets, SyncOptions{MaxAge: time.Minute})
	if len(etags) != 2 || etags[0] != "" || etags[1] != testETag {
		t.Errorf("etags sent = %q, want an unconditional seed then the new ETag", etags)
	}
}

func TestWatchDropsLongFinishedEntries(t *testing.T) {
	old := time.Now().Add(-2 * watchRetention)
	f := &fakeGH{polls: []RepoPoll{{ETag: testETag, PRs: nil}}}
	f.install(t, testRemote, true)
	c := syncCache(t)
	_ = c.Mutate(func(w *Writable) error {
		w.Set(WatchKey("bot", acme, 1), &PRInfo{Number: 1, Status: PRMerged, UpdatedAt: old})
		w.Set(WatchKey("bot", acme, 2), &PRInfo{Number: 2, Status: PRMerged, UpdatedAt: time.Now()})
		w.Set(WatchKey("bot", acme, 3), &PRInfo{Number: 3, Status: PROpen, UpdatedAt: old})
		return nil
	})
	runSync(t, c, []Target{{RepoPath: testRepo, Watch: "bot"}}, SyncOptions{})
	_ = c.Load()
	got := c.Watched("bot")
	if got[WatchKey("bot", acme, 1)] != nil || got[WatchKey("bot", acme, 2)] == nil || got[WatchKey("bot", acme, 3)] == nil {
		t.Errorf("kept %v; want only the long-merged entry dropped", got)
	}
}

func TestBranchEntriesCarryNoBody(t *testing.T) {
	pr := watchedPR(1, PROpen, time.Now())
	pr.HeadRef = testBranch
	f := &fakeGH{polls: []RepoPoll{{ETag: testETag, PRs: []PollPR{pr}}}}
	f.install(t, testRemote, true)
	c := syncCache(t)
	runSync(t, c, []Target{{RepoPath: testRepo, Branch: testBranch}}, SyncOptions{})
	_ = c.Load()
	if info := c.Get(testBranch); info == nil || info.Body != "" {
		t.Errorf("branch entry = %+v, want one without a description", info)
	}
}
