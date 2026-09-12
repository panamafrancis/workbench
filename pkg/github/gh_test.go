package github

import "testing"

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
