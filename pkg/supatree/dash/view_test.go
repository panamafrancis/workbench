package dash

import (
	"strings"
	"testing"
	"time"

	"github.com/panamafrancis/workbench/pkg/github"
	"github.com/panamafrancis/workbench/pkg/supatree"
)

func openPR(review github.ReviewState) *github.PRInfo {
	return &github.PRInfo{Number: 1, Status: github.PROpen, Review: review}
}

func TestReviewCell(t *testing.T) {
	tree := supatree.TreeStatus{Members: []supatree.MemberStatus{
		{PR: openPR(github.ReviewApproved)},
		{PR: openPR(github.ReviewApproved)},
		{PR: openPR(github.ReviewChanges)},
		{PR: openPR(github.ReviewNone)},
		{PR: &github.PRInfo{Number: 9, Status: github.PRMerged}}, // merged PRs are not under review
		{},
	}}
	if got, want := reviewCell(tree), "2✓ 1✗ 1·"; got != want {
		t.Errorf("reviewCell = %q, want %q", got, want)
	}
	if got := reviewCell(supatree.TreeStatus{}); got != "-" {
		t.Errorf("reviewCell(empty) = %q, want %q", got, "-")
	}
}

func TestNotesOrder(t *testing.T) {
	tree := supatree.TreeStatus{State: supatree.TreeSetup, Blocked: true, Stale: true, Dirty: true}
	if got, want := strings.Join(notes(tree), ","), "blocked,run sync,stale,uncommitted"; got != want {
		t.Errorf("notes = %q, want %q", got, want)
	}
	if got := notes(supatree.TreeStatus{State: supatree.TreeDone}); len(got) != 1 || got[0] != "rm me" {
		t.Errorf("notes(done) = %v, want [rm me]", got)
	}
}

// truncate slices runes, so it must only ever see unstyled text — and it must
// keep multi-byte names intact rather than cutting mid-rune.
func TestTruncate(t *testing.T) {
	if got := truncate("kind-security-compliance", 10); got != "kind-secu…" {
		t.Errorf("truncate = %q", got)
	}
	if got := truncate("short", 0); got != "short" {
		t.Errorf("truncate with unknown width = %q, want %q", got, "short")
	}
	if got := truncate("héllo wörld", 6); got != "héllo…" {
		t.Errorf("truncate multibyte = %q, want %q", got, "héllo…")
	}
}

func TestPadKeepsColumnsApart(t *testing.T) {
	if got := pad("wip", 6); got != "wip   " {
		t.Errorf("pad = %q", got)
	}
	// An over-long cell is cut, never allowed to push the next column right.
	if got := pad("changes-requested", 8); len([]rune(got)) != 8 || !strings.HasSuffix(got, " ") {
		t.Errorf("pad(overlong) = %q, want 8 cells ending in a space", got)
	}
}

func TestAgo(t *testing.T) {
	tests := []struct {
		d    time.Duration
		want string
	}{
		{30 * time.Second, "now"},
		{5 * time.Minute, "5m"},
		{3 * time.Hour, "3h"},
		{50 * time.Hour, "2d"},
	}
	for _, tc := range tests {
		if got := ago(tc.d); got != tc.want {
			t.Errorf("ago(%s) = %q, want %q", tc.d, got, tc.want)
		}
	}
}
