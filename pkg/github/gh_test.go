package github

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"testing"
)

func TestMapStatus(t *testing.T) {
	tests := []struct {
		state   string
		isDraft bool
		want    PRStatus
	}{
		{"OPEN", false, PROpen},
		{"OPEN", true, PRDraft},
		{"MERGED", false, PRMerged},
		{"MERGED", true, PRMerged},
		{"CLOSED", false, PRClosed},
		{"CLOSED", true, PRClosed},
	}
	for _, tt := range tests {
		got := mapStatus(tt.state, tt.isDraft)
		if got != tt.want {
			t.Errorf("mapStatus(%q, %v) = %q, want %q", tt.state, tt.isDraft, got, tt.want)
		}
	}
}

func TestIsPermanentError(t *testing.T) {
	if !IsPermanentError(ErrGHNotFound) {
		t.Error("ErrGHNotFound should be permanent")
	}
	if !IsPermanentError(ErrGHAuth) {
		t.Error("ErrGHAuth should be permanent")
	}
	if IsPermanentError(nil) {
		t.Error("nil should not be permanent")
	}
	if IsPermanentError(ErrGHRateLimited) {
		t.Error("rate-limit errors must not be permanent (they must recover)")
	}
}

func TestIsRateLimited(t *testing.T) {
	if !IsRateLimited(ErrGHRateLimited) {
		t.Error("ErrGHRateLimited should be rate limited")
	}
	if IsRateLimited(ErrGHAuth) {
		t.Error("auth error is not a rate limit")
	}
	if IsRateLimited(nil) {
		t.Error("nil is not a rate limit")
	}
}

func TestMapReview(t *testing.T) {
	tests := map[string]ReviewState{
		"APPROVED":          ReviewApproved,
		"CHANGES_REQUESTED": ReviewChanges,
		"REVIEW_REQUIRED":   ReviewRequired,
		"":                  ReviewNone,
		"SOMETHING_NEW":     ReviewNone,
	}
	for in, want := range tests {
		if got := mapReview(in); got != want {
			t.Errorf("mapReview(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestRollupChecks(t *testing.T) {
	completed := func(conclusion string) ghCheckNode {
		return ghCheckNode{Status: "COMPLETED", Conclusion: conclusion}
	}
	tests := []struct {
		name  string
		nodes []ghCheckNode
		want  CheckState
	}{
		{"no checks", nil, CheckNone},
		{"all green", []ghCheckNode{completed("SUCCESS"), completed("SKIPPED")}, CheckPassing},
		{"one red wins", []ghCheckNode{completed("SUCCESS"), completed("FAILURE")}, CheckFailing},
		{"still running", []ghCheckNode{completed("SUCCESS"), {Status: "IN_PROGRESS"}}, CheckPending},
		{"red beats running", []ghCheckNode{{Status: "QUEUED"}, completed("TIMED_OUT")}, CheckFailing},
		{"legacy status context", []ghCheckNode{{State: "SUCCESS"}}, CheckPassing},
		{"legacy error", []ghCheckNode{{State: "SUCCESS"}, {State: "ERROR"}}, CheckFailing},
		{"legacy pending", []ghCheckNode{{State: "PENDING"}}, CheckPending},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := rollupChecks(tc.nodes); got != tc.want {
				t.Errorf("rollupChecks = %q, want %q", got, tc.want)
			}
		})
	}
}

// --- ResolvePR policy ---

func fakeInfo(number int, status PRStatus) *PRInfo {
	return &PRInfo{Number: number, Status: status}
}

func TestResolvePRUsesHeadResult(t *testing.T) {
	l := prLookup{
		byHead: func(string, string) (*PRInfo, error) { return fakeInfo(7, PROpen), nil },
		byNumber: func(string, int) (*PRInfo, error) {
			t.Fatal("must not fall back when the head names a PR")
			return nil, nil
		},
	}
	got, err := resolvePR(l, "/repo", "branch", PRRef{Number: 5})
	if err != nil {
		t.Fatalf("resolvePR: %v", err)
	}
	if got.Number != 7 {
		t.Errorf("number = %d, want 7 (the PR currently on the branch wins over the cached number)", got.Number)
	}
}

func TestResolvePRFallsBackToNumber(t *testing.T) {
	// The case from the bug: the branch was renamed after PR 485 merged, so the
	// PR's head is frozen on the old name and a --head lookup finds nothing.
	l := prLookup{
		byHead:   func(string, string) (*PRInfo, error) { return &PRInfo{Status: PRNone}, nil },
		byNumber: func(_ string, n int) (*PRInfo, error) { return fakeInfo(n, PRMerged), nil },
	}
	got, err := resolvePR(l, "/repo", "st/new-slug/ads-service", PRRef{Number: 485})
	if err != nil {
		t.Fatalf("resolvePR: %v", err)
	}
	if got.Number != 485 || got.Status != PRMerged {
		t.Errorf("got #%d %q, want #485 merged", got.Number, got.Status)
	}
}

func TestResolvePRNoKnownNumber(t *testing.T) {
	l := prLookup{
		byHead:   func(string, string) (*PRInfo, error) { return &PRInfo{Status: PRNone}, nil },
		byNumber: func(string, int) (*PRInfo, error) { t.Fatal("no number to look up"); return nil, nil },
	}
	got, err := resolvePR(l, "/repo", "branch", PRRef{})
	if err != nil {
		t.Fatalf("resolvePR: %v", err)
	}
	if got.Status != PRNone {
		t.Errorf("status = %q, want none", got.Status)
	}
}

func TestResolvePRNumberGone(t *testing.T) {
	// A PR that no longer exists must clear the entry, not error.
	l := prLookup{
		byHead:   func(string, string) (*PRInfo, error) { return &PRInfo{Status: PRNone}, nil },
		byNumber: func(string, int) (*PRInfo, error) { return nil, ErrPRNotFound },
	}
	got, err := resolvePR(l, "/repo", "branch", PRRef{Number: 485})
	if err != nil {
		t.Fatalf("resolvePR: %v", err)
	}
	if got.Status != PRNone {
		t.Errorf("status = %q, want none", got.Status)
	}
}

func TestResolvePRPropagatesFallbackError(t *testing.T) {
	// A transient failure must not be cached as "this branch has no PR".
	l := prLookup{
		byHead:   func(string, string) (*PRInfo, error) { return &PRInfo{Status: PRNone}, nil },
		byNumber: func(string, int) (*PRInfo, error) { return nil, ErrGHRateLimited },
	}
	if _, err := resolvePR(l, "/repo", "branch", PRRef{Number: 485}); !errors.Is(err, ErrGHRateLimited) {
		t.Errorf("err = %v, want ErrGHRateLimited", err)
	}
}

func TestResolvePRPropagatesHeadError(t *testing.T) {
	l := prLookup{
		byHead: func(string, string) (*PRInfo, error) { return nil, ErrGHAuth },
		byNumber: func(string, int) (*PRInfo, error) {
			t.Fatal("must not fall back past a failed head lookup")
			return nil, nil
		},
	}
	if _, err := resolvePR(l, "/repo", "branch", PRRef{Number: 485}); !errors.Is(err, ErrGHAuth) {
		t.Errorf("err = %v, want ErrGHAuth", err)
	}
}

// --- error classification ---

// ghFailure runs a shell command that mimics a failed gh invocation so that
// classifyGHError sees a real *exec.ExitError with stderr attached.
func ghFailure(t *testing.T, code int, stderr string) error {
	t.Helper()
	cmd := exec.CommandContext(context.Background(), "sh", "-c",
		fmt.Sprintf("printf %%s %q >&2; exit %d", stderr, code))
	_, err := cmd.Output()
	if err == nil {
		t.Fatal("expected the fake gh invocation to fail")
	}
	return err
}

func TestClassifyGHError(t *testing.T) {
	tests := []struct {
		name   string
		code   int
		stderr string
		want   error
	}{
		{"auth exit code", 4, "gh: authentication failed", ErrGHAuth},
		{"not logged in", 1, "gh: not logged in to github.com", ErrGHAuth},
		{"rate limit", 1, "API rate limit exceeded", ErrGHRateLimited},
		{"unknown pr number", 1, "GraphQL: Could not resolve to a PullRequest with the number of 999999.", ErrPRNotFound},
		{"no pr for branch", 1, "no pull requests found for branch", ErrPRNotFound},
		{"repo not visible", 1, "GraphQL: Could not resolve to a Repository with the name 'PiwikPRO/fraud0_api_contract'. (repository)", ErrRepoNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := classifyGHError(ghFailure(t, tt.code, tt.stderr)); !errors.Is(got, tt.want) {
				t.Errorf("classifyGHError(%q) = %v, want %v", tt.stderr, got, tt.want)
			}
		})
	}
}

func TestClassifyGHErrorUnknown(t *testing.T) {
	err := classifyGHError(ghFailure(t, 1, "something else went wrong"))
	for _, sentinel := range []error{ErrGHAuth, ErrGHRateLimited, ErrPRNotFound, ErrGHNotFound} {
		if errors.Is(err, sentinel) {
			t.Fatalf("unclassified stderr matched %v", sentinel)
		}
	}
	if !strings.Contains(err.Error(), "something else went wrong") {
		t.Errorf("err = %v, want gh stderr preserved", err)
	}
}

func TestPRNotFoundIsNotPermanent(t *testing.T) {
	// A missing PR is about one branch; it must not shut the whole fetch round
	// down the way a missing gh binary or a failed login does.
	if IsPermanentError(ErrPRNotFound) {
		t.Error("ErrPRNotFound must not be a permanent error")
	}
}

func TestResolvePRRejectsOtherRepoNumber(t *testing.T) {
	// The cache is keyed on branch name alone, so two repos with the same branch
	// name share an entry. A number recorded against one must not be looked up
	// against the other, where it would resolve an unrelated PR that happens to
	// hold that number.
	l := prLookup{
		byHead: func(string, string) (*PRInfo, error) { return &PRInfo{Status: PRNone}, nil },
		byNumber: func(string, int) (*PRInfo, error) {
			return &PRInfo{Number: 12, Status: PROpen, URL: "https://github.com/org/other/pull/12"}, nil
		},
	}
	got, err := resolvePR(l, "/repo", "fix-login", PRRef{Number: 12, URL: "https://github.com/org/mine/pull/12"})
	if err != nil {
		t.Fatalf("resolvePR: %v", err)
	}
	if got.Status != PRNone {
		t.Errorf("got #%d %q, want none — the number belongs to another repo", got.Number, got.Status)
	}
}

func TestResolvePRAcceptsSameRepoNumber(t *testing.T) {
	l := prLookup{
		byHead: func(string, string) (*PRInfo, error) { return &PRInfo{Status: PRNone}, nil },
		byNumber: func(string, int) (*PRInfo, error) {
			return &PRInfo{Number: 485, Status: PRMerged, URL: "https://github.com/org/ads/pull/485"}, nil
		},
	}
	got, err := resolvePR(l, "/repo", "st/new/ads", PRRef{Number: 485, URL: "https://github.com/org/ads/pull/485"})
	if err != nil {
		t.Fatalf("resolvePR: %v", err)
	}
	if got.Status != PRMerged {
		t.Errorf("status = %q, want merged", got.Status)
	}
}

func TestSameRepo(t *testing.T) {
	tests := []struct {
		a, b string
		want bool
	}{
		{"https://github.com/org/repo/pull/1", "https://github.com/org/repo/pull/99", true},
		{"https://github.com/org/repo/pull/1", "https://github.com/org/other/pull/1", false},
		{"https://github.com/org/repo/pull/1", "https://ghe.corp/org/repo/pull/1", false},
		// Nothing to compare against: take the result at face value.
		{"", "https://github.com/org/repo/pull/1", true},
		{"https://github.com/org/repo/pull/1", "", true},
	}
	for _, tt := range tests {
		if got := sameRepo(tt.a, tt.b); got != tt.want {
			t.Errorf("sameRepo(%q, %q) = %v, want %v", tt.a, tt.b, got, tt.want)
		}
	}
}
