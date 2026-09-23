package supatree

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/panamafrancis/workbench/pkg/github"
)

// repoKeystone is the PR repo used throughout, distinct from the workbench
// alias for the same repository.
const repoKeystone = "fraud-zero/keystone-api"

func TestParsePRRef(t *testing.T) {
	tests := []struct {
		in       string
		wantRepo string
		wantNum  int
	}{
		{"https://github.com/fraud-zero/keystone-api/pull/600", repoKeystone, 600},
		{"https://github.com/fraud-zero/keystone-api/pull/600/", repoKeystone, 600},
		{"http://github.com/o/r/pull/1", "o/r", 1},
		{"fraud-zero/keystone-api#600", repoKeystone, 600},
		{"  fraud-zero/docs#108  ", "fraud-zero/docs", 108},
		// What a browser actually puts on the clipboard.
		{"https://github.com/fraud-zero/keystone-api/pull/600/files", repoKeystone, 600},
		{"https://github.com/fraud-zero/keystone-api/pull/600?w=1", repoKeystone, 600},
		{"https://github.com/fraud-zero/keystone-api/pull/600#discussion_r1", repoKeystone, 600},
	}
	for _, tc := range tests {
		t.Run(tc.in, func(t *testing.T) {
			repo, num, err := ParsePRRef(tc.in)
			if err != nil {
				t.Fatalf("ParsePRRef(%q) = %v", tc.in, err)
			}
			if repo != tc.wantRepo || num != tc.wantNum {
				t.Errorf("= %q #%d, want %q #%d", repo, num, tc.wantRepo, tc.wantNum)
			}
		})
	}
}

func TestParsePRRefRejects(t *testing.T) {
	// An issue URL is the one worth rejecting loudly: it parses as a number in a
	// repo, and silently reviewing issue 600 as if it were PR 600 would be a
	// confusing tree rather than an error.
	for _, in := range []string{
		"https://github.com/o/r/issues/600",
		"https://github.com/o/r/pull/notanumber",
		"o/r",
		"#600",
		"",
	} {
		t.Run(in, func(t *testing.T) {
			if _, _, err := ParsePRRef(in); err == nil {
				t.Errorf("ParsePRRef(%q) accepted a non-PR", in)
			}
		})
	}
}

// The branch prefix is what teardown matches on, so it must differ by mode and
// must not be derivable from a slug alone.
func TestMemberBranchByMode(t *testing.T) {
	authoring := &Meta{Slug: "refunds"}
	if got, want := authoring.MemberBranch("keystone"), "st/refunds/keystone"; got != want {
		t.Errorf("authoring MemberBranch = %q, want %q", got, want)
	}
	reviewing := &Meta{Slug: "refunds", Mode: ModeReviewing}
	if got, want := reviewing.MemberBranch("keystone"), "review/refunds/keystone"; got != want {
		t.Errorf("reviewing MemberBranch = %q, want %q", got, want)
	}
}

// The PR cache is one flat map across every repo and tree. Authoring branches
// are unique because st/<slug>/<alias> embeds the alias; review members carry
// the author's branch names, which for a cross-repo change are routinely
// identical across repos — so they must key on the PR instead.
func TestCacheKeyIsPRScopedWhenReviewing(t *testing.T) {
	authoring := Member{Alias: aliasKeystone, Branch: "st/refunds/keystone"}
	if got, want := authoring.CacheKey(), "st/refunds/keystone"; got != want {
		t.Errorf("authoring CacheKey = %q, want %q", got, want)
	}

	// Two members of one review tree whose authors used the same branch name.
	a := Member{Alias: aliasKeystone, Branch: "review/r/keystone",
		Review: &ReviewRef{Repo: repoKeystone, Number: 600, HeadRef: headRefRefunds}}
	b := Member{Alias: aliasAdmin, Branch: "review/r/admin",
		Review: &ReviewRef{Repo: "fraud-zero/admin-frontend", Number: 988, HeadRef: headRefRefunds}}
	if a.CacheKey() == b.CacheKey() {
		t.Fatalf("two repos sharing branch %q collided on cache key %q", a.Review.HeadRef, a.CacheKey())
	}
	if got, want := a.CacheKey(), "pr:fraud-zero/keystone-api#600"; got != want {
		t.Errorf("CacheKey = %q, want %q", got, want)
	}
}

// A review member's branch is tree-local and has no origin ref by design. The
// unpushed-branch skip must therefore not apply to it, or a review tree would
// never ask GitHub about its own pull requests — reporting total_prs=0 forever,
// which is the bug review mode exists to fix.
func TestFetchTargetsIncludesReviewMembers(t *testing.T) {
	inst := &Instance{
		Name: treeA,
		Mode: ModeReviewing,
		Members: []Member{{
			Alias:  aliasKeystone,
			Path:   t.TempDir(), // not a git repo: HasRemoteBranch is false
			Branch: "review/canberra/keystone",
			Exists: true,
			Review: &ReviewRef{Repo: repoKeystone, Number: 600, URL: "https://github.com/fraud-zero/keystone-api/pull/600"},
		}},
	}
	targets := FetchTargets([]*Instance{inst}, newEmptyCache(t), true, PRStaleAge)
	if len(targets) != 1 {
		t.Fatalf("got %d targets, want 1 — a review member was skipped as if it were an unpushed branch", len(targets))
	}
	if targets[0].Ref.Number != 600 {
		t.Errorf("target Ref.Number = %d, want 600 (the lookup must go by number, not by branch head)", targets[0].Ref.Number)
	}
	if targets[0].Key != "pr:fraud-zero/keystone-api#600" {
		t.Errorf("target Key = %q, want the PR-scoped key", targets[0].Key)
	}
}

// An authoring member with no origin ref and no known PR is still skipped: that
// is the quota discipline this must not weaken.
func TestFetchTargetsStillSkipsUnpushedAuthoringBranches(t *testing.T) {
	inst := &Instance{
		Name:    treeA,
		Members: []Member{{Alias: aliasKeystone, Path: t.TempDir(), Branch: "st/canberra/keystone", Exists: true}},
	}
	if targets := FetchTargets([]*Instance{inst}, newEmptyCache(t), true, PRStaleAge); len(targets) != 0 {
		t.Errorf("got %d targets, want 0", len(targets))
	}
}

// newEmptyCache is a cache backed by a path that does not exist, i.e. the state
// every fetch decision starts from on a fresh tree.
func newEmptyCache(t *testing.T) *github.Cache {
	t.Helper()
	return github.NewCache(filepath.Join(t.TempDir(), "pr-status.json"))
}

// The refusal has to redirect, not just refuse: the agent that reaches it was
// told by its own instructions to rename before opening a PR, so the message is
// read at the one moment it can change what happens next.
func TestRefuseAuthoringRedirects(t *testing.T) {
	authoring := &Instance{Name: treeA}
	if msg := refuseAuthoring(authoring, "Renaming the branches"); msg != "" {
		t.Errorf("authoring tree was refused: %q", msg)
	}

	reviewing := &Instance{Name: treeA, Mode: ModeReviewing, Members: []Member{{
		Alias:  aliasKeystone,
		Review: &ReviewRef{Repo: repoKeystone, Number: 600, HeadRef: headRefRefunds},
	}}}
	msg := refuseAuthoring(reviewing, "Renaming the branches")
	for _, want := range []string{"review tree", "fraud-zero/keystone-api#600", headRefRefunds, "docs"} {
		if !strings.Contains(msg, want) {
			t.Errorf("refusal does not mention %q:\n%s", want, msg)
		}
	}
}

// Every topic the tool advertises must resolve, or the discovery path dead-ends
// at the moment it is needed.
func TestDocsTopics(t *testing.T) {
	for _, topic := range []string{"", "overview", "review", "REVIEW"} {
		out, isErr := handleDocsTopic(map[string]any{"topic": topic})
		if isErr || strings.TrimSpace(out) == "" {
			t.Errorf("topic %q did not resolve: %s", topic, out)
		}
	}
	if _, isErr := handleDocsTopic(map[string]any{"topic": "nope"}); !isErr {
		t.Error("unknown topic was not reported as an error")
	}
}

// The seed exists to make the lookup resolvable, not to report a status nobody
// fetched: it carries the number and URL, and is stale from the moment it is
// written so the first real round replaces it.
func TestSeedPRCacheIsResolvableButNotAuthoritative(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	ref := ReviewRef{Repo: repoKeystone, Number: 600, URL: "https://github.com/fraud-zero/keystone-api/pull/600"}
	seedPRCache(map[string]ReviewRef{aliasKeystone: ref})

	cache := github.NewCache(PRCachePath())
	if err := cache.Load(); err != nil {
		t.Fatalf("load cache: %v", err)
	}
	key := "pr:" + repoKeystone + "#600"
	if !cache.KnowsPR(key) {
		t.Fatal("seeded entry does not identify the PR, so the by-number lookup cannot resolve it")
	}
	if got := cache.Ref(key); got.Number != 600 || got.URL != ref.URL {
		t.Errorf("Ref = %+v, want number 600 and the PR URL", got)
	}
	if !cache.IsStale(key, PRStaleAge) {
		t.Error("seeded entry is not stale: a status nobody fetched would be reported as fact")
	}
	if info := cache.Get(key); info == nil || info.Status != github.PRNone {
		t.Errorf("seed asserted a status: %+v", info)
	}
}

// Posting publishes in the user's name, so it is refused unless the outward
// permission is on — which it is not, at any autonomy level, by default.
func TestReviewPostRefusedWithoutOutward(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	inst := &Instance{Name: treeA, Root: filepath.Join(home, "tree"), Mode: ModeReviewing}
	msg := outwardDenied(inst, "posting a review")
	if msg == "" {
		t.Fatal("posting was permitted with outward off")
	}
	for _, want := range []string{"outward", "default_outward", "let the human post it"} {
		if !strings.Contains(msg, want) {
			t.Errorf("refusal does not mention %q:\n%s", want, msg)
		}
	}
}

// An empty review is a mistake, not a verdict.
func TestPostReviewRejectsEmptyAndBadAnchors(t *testing.T) {
	inst := &Instance{Name: treeA, Members: []Member{{
		Alias:  aliasKeystone,
		Review: &ReviewRef{Repo: repoKeystone, Number: 600, Head: "abc123"},
	}}}
	if _, err := PostReview(inst, aliasKeystone, "  ", "COMMENT", nil); err == nil {
		t.Error("an empty review was accepted")
	}
	if _, err := PostReview(inst, aliasKeystone, "x", "LGTM", nil); err == nil {
		t.Error("an invalid event was accepted")
	}
	// A comment with no line number cannot be anchored, and GitHub would either
	// reject it or attach it somewhere arbitrary.
	bad := []ReviewComment{{Path: "pkg/x.go", Body: "hm"}}
	if _, err := PostReview(inst, aliasKeystone, "", "COMMENT", bad); err == nil {
		t.Error("a comment with no line number was accepted")
	}
	if _, err := PostReview(inst, "nosuch", "x", "COMMENT", nil); err == nil {
		t.Error("posting to a non-member was accepted")
	}
}

func TestParseReviewComments(t *testing.T) {
	got, err := ParseReviewComments(`[{"path":"pkg/x.go","line":42,"body":"here"}]`)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(got) != 1 || got[0].Path != "pkg/x.go" || got[0].Line != 42 {
		t.Errorf("got %+v", got)
	}
	if out, err := ParseReviewComments("  "); err != nil || out != nil {
		t.Errorf("empty input should be no comments, got %+v %v", out, err)
	}
	if _, err := ParseReviewComments("not json"); err == nil {
		t.Error("malformed input was accepted")
	}
}

// A review tree holds every member of its stack, not only the repos the change
// touched. A bystander left on the review rung would pin the rollup below
// `reviewed` forever, so the tree could never be reaped.
func TestUntrackedReviewMemberIsIdle(t *testing.T) {
	const branch = "review/refunds/docs"
	local := MemberLocal{Exists: true, CheckedOut: branch}
	if got := memberState(local, nil, branch, ModeReviewing, false); got != MemberIdle {
		t.Errorf("untracked review member = %q, want %q", got, MemberIdle)
	}
	if got := memberState(local, nil, branch, ModeReviewing, true); got != MemberInReview {
		t.Errorf("tracked review member = %q, want %q", got, MemberInReview)
	}
}

// Every reviewed PR has landed and the repos the change did not touch are idle:
// the tree is finished, and finished under its own name rather than as `done`.
func TestReviewTreeReachesReviewedWithBystanders(t *testing.T) {
	merged := github.PRInfo{Number: 600, Status: github.PRMerged}
	tree := TreeStatus{Mode: ModeReviewing, Members: []MemberStatus{
		{Alias: aliasKeystone, State: MemberMerged, PR: &merged},
		{Alias: "docs", State: MemberIdle},
	}}
	rollup(&tree, time.Now(), DefaultStaleAfter)
	if tree.State != TreeReviewed {
		t.Errorf("state = %q, want %q", tree.State, TreeReviewed)
	}
	if !tree.Reap() {
		t.Error("a finished review tree should be reapable")
	}
}
