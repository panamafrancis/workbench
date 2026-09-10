package github

import (
	"path/filepath"
	"testing"
	"time"
)

func TestParseCreatedPR(t *testing.T) {
	tests := []struct {
		name   string
		output string
		draft  bool
		want   int
		wantOK bool
	}{
		{
			name:   "bare url",
			output: "https://github.com/fraud-zero/admin-frontend/pull/975",
			want:   975,
			wantOK: true,
		},
		{
			name: "url after push output",
			output: "branch 'wt/a/thing' set up to track 'origin/wt/a/thing'.\n" +
				"Creating pull request for wt/a/thing into main in acme/widgets\n" +
				"https://github.com/acme/widgets/pull/12\n",
			want:   12,
			wantOK: true,
		},
		{name: "draft", output: "https://github.com/acme/widgets/pull/3", draft: true, want: 3, wantOK: true},
		{name: "no url", output: "gh pr create failed: a pull request already exists"},
		{name: "empty", output: ""},
		{name: "issue url is not a pr", output: "https://github.com/acme/widgets/issues/12"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			info, ok := ParseCreatedPR(tc.output, tc.draft)
			if ok != tc.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tc.wantOK)
			}
			if !ok {
				return
			}
			if info.Number != tc.want {
				t.Errorf("number = %d, want %d", info.Number, tc.want)
			}
			wantStatus := PROpen
			if tc.draft {
				wantStatus = PRDraft
			}
			if info.Status != wantStatus {
				t.Errorf("status = %q, want %q", info.Status, wantStatus)
			}
			if info.FetchedAt.IsZero() || info.URL == "" {
				t.Errorf("incomplete entry: %+v", info)
			}
		})
	}
}

func TestRecordCreatedPRWritesThrough(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pr-status.json")
	RecordCreatedPR(path, "wt/a/thing", "https://github.com/acme/widgets/pull/42", false)

	c := NewCache(path)
	if err := c.Load(); err != nil {
		t.Fatalf("load: %v", err)
	}
	info := c.Get("wt/a/thing")
	if info == nil || info.Number != 42 || info.Status != PROpen {
		t.Fatalf("entry = %+v, want the created PR cached", info)
	}
	// Fresh by definition — the sidebar must not re-ask for what it just made.
	// (maxAge 0 is the "force a refetch" sentinel elsewhere in this package, so
	// the check uses a real staleness window.)
	if c.IsStale("wt/a/thing", time.Minute) {
		t.Error("a just-created PR should not read as stale")
	}
}

func TestRecordCreatedPRIgnoresUnparsableOutput(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pr-status.json")
	RecordCreatedPR(path, "wt/a/thing", "something went wrong", false)

	c := NewCache(path)
	_ = c.Load()
	if c.Get("wt/a/thing") != nil {
		t.Error("nothing should be cached when no PR URL was printed")
	}
}
