package github

import (
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"
)

const testPollETag = `W/"x"`

func rawResponse(status string, headers map[string]string, body string) []byte {
	var b strings.Builder
	b.WriteString("HTTP/2.0 " + status + "\r\n")
	for k, v := range headers {
		b.WriteString(k + ": " + v + "\r\n")
	}
	b.WriteString("\r\n")
	b.WriteString(body)
	return []byte(b.String())
}

func TestParseRemoteURL(t *testing.T) {
	tests := []struct {
		raw  string
		want string // "owner/name", or "" when the remote isn't GitHub
	}{
		{"git@github.com:fraud-zero/admin-frontend.git", "fraud-zero/admin-frontend"},
		{"git@github.com:fraud-zero/admin-frontend", "fraud-zero/admin-frontend"},
		{"https://github.com/panamafrancis/workbench.git", "panamafrancis/workbench"},
		{"https://github.com/panamafrancis/workbench/", "panamafrancis/workbench"},
		{"ssh://git@github.com/fraud-zero/keystone-api.git", "fraud-zero/keystone-api"},
		{"  git@github.com:fraud-zero/docs.git\n", "fraud-zero/docs"},
		{"git@gitlab.com:team/thing.git", ""},
		{"git@github.enterprise.io:team/thing.git", ""},
		{"/local/path/to/repo", ""},
		{"", ""},
		{"https://github.com/onlyowner", ""},
	}
	for _, tc := range tests {
		ref, ok := ParseRemoteURL(tc.raw)
		if tc.want == "" {
			if ok {
				t.Errorf("ParseRemoteURL(%q) = %v, want not-GitHub", tc.raw, ref)
			}
			continue
		}
		if !ok || ref.String() != tc.want {
			t.Errorf("ParseRemoteURL(%q) = %v/%v, want %q", tc.raw, ref, ok, tc.want)
		}
	}
}

const pollBody = `[
  {"number": 974, "state": "open", "draft": false, "title": "open one",
   "html_url": "https://github.com/o/n/pull/974", "updated_at": "2026-09-09T16:24:48Z",
   "merged_at": null, "head": {"ref": "wt/n/open-branch"}},
  {"number": 973, "state": "open", "draft": true, "title": "draft one",
   "html_url": "https://github.com/o/n/pull/973", "updated_at": "2026-09-09T15:00:00Z",
   "merged_at": null, "head": {"ref": "wt/n/draft-branch"}},
  {"number": 972, "state": "closed", "draft": false, "title": "merged one",
   "html_url": "https://github.com/o/n/pull/972", "updated_at": "2026-09-09T14:00:00Z",
   "merged_at": "2026-09-09T14:00:00Z", "head": {"ref": "wt/n/merged-branch"}},
  {"number": 971, "state": "closed", "draft": false, "title": "closed one",
   "html_url": "https://github.com/o/n/pull/971", "updated_at": "2026-09-09T13:00:00Z",
   "merged_at": null, "head": {"ref": "wt/n/closed-branch"}}
]`

func TestParsePollResponseOK(t *testing.T) {
	raw := rawResponse("200 OK", map[string]string{
		"Etag":                  `W/"abc123"`,
		"X-Ratelimit-Resource":  ResourceCore,
		"X-Ratelimit-Remaining": "4999",
		"Content-Type":          "application/json",
	}, pollBody)

	poll, err := parsePollResponse(raw, "")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if poll.NotModified {
		t.Error("200 should not report NotModified")
	}
	if poll.ETag != `W/"abc123"` {
		t.Errorf("ETag = %q, want the response's", poll.ETag)
	}
	if poll.Truncated {
		t.Error("no Link header means no further pages")
	}
	want := map[string]PRStatus{
		"wt/n/open-branch":   PROpen,
		"wt/n/draft-branch":  PRDraft,
		"wt/n/merged-branch": PRMerged, // closed + merged_at
		"wt/n/closed-branch": PRClosed, // closed, never merged
	}
	if len(poll.PRs) != len(want) {
		t.Fatalf("got %d PRs, want %d", len(poll.PRs), len(want))
	}
	for _, pr := range poll.PRs {
		if got := pr.Info.Status; got != want[pr.HeadRef] {
			t.Errorf("%s: status = %q, want %q", pr.HeadRef, got, want[pr.HeadRef])
		}
		if pr.Info.FetchedAt.IsZero() || pr.Info.Number == 0 || pr.Info.URL == "" {
			t.Errorf("%s: incomplete entry %+v", pr.HeadRef, pr.Info)
		}
	}
	if oldest := poll.Oldest.UTC(); !oldest.Equal(time.Date(2026, 9, 9, 13, 0, 0, 0, time.UTC)) {
		t.Errorf("Oldest = %v, want the last item's updated_at", oldest)
	}
}

// TestParsePollResponseNotModified guards the trap that makes this whole design
// work: `gh api -i` exits non-zero on a 304, so a caller keying off the exit
// status would treat every free response as a failure and re-fetch — turning
// the cheapest possible round into the most expensive one.
func TestParsePollResponseNotModified(t *testing.T) {
	raw := rawResponse("304 Not Modified", map[string]string{
		"X-Ratelimit-Resource":  ResourceCore,
		"X-Ratelimit-Remaining": "4999",
	}, "")

	poll, err := parsePollResponse(raw, `W/"previous"`)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if !poll.NotModified {
		t.Error("304 should report NotModified")
	}
	if poll.ETag != `W/"previous"` {
		t.Errorf("ETag = %q, want the previous one carried forward", poll.ETag)
	}
	if len(poll.PRs) != 0 {
		t.Errorf("304 carries no PRs, got %d", len(poll.PRs))
	}
}

func TestParsePollResponseTruncated(t *testing.T) {
	link := `<https://api.github.com/repositories/1/pulls?page=2>; rel="next", ` +
		`<https://api.github.com/repositories/1/pulls?page=9>; rel="last"`
	raw := rawResponse("200 OK", map[string]string{"Etag": testPollETag, "Link": link}, pollBody)

	poll, err := parsePollResponse(raw, "")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if !poll.Truncated {
		t.Fatal("a Link header with rel=next means more pages exist")
	}

	// Truncation only matters relative to when we last looked: a poll that
	// reaches back past our last round still carries every change we missed.
	if !poll.Complete(poll.Oldest.Add(time.Minute)) {
		t.Error("a page reaching past the last poll is still a complete delta")
	}
	if poll.Complete(poll.Oldest.Add(-time.Minute)) {
		t.Error("a page that stops short of the last poll may have missed changes")
	}
}

func TestParsePollResponseRateLimited(t *testing.T) {
	reset := time.Now().Add(12 * time.Minute).Truncate(time.Second)
	raw := rawResponse("403 Forbidden", map[string]string{
		"X-Ratelimit-Resource":  ResourceCore,
		"X-Ratelimit-Remaining": "0",
		"X-Ratelimit-Reset":     strconv.FormatInt(reset.Unix(), 10),
	}, `{"message":"API rate limit exceeded"}`)

	_, err := parsePollResponse(raw, "")
	if !errors.Is(err, ErrGHRateLimited) {
		t.Fatalf("err = %v, want it to read as ErrGHRateLimited", err)
	}
	var limited *RateLimitedError
	if !errors.As(err, &limited) {
		t.Fatal("want a RateLimitedError carrying the reset time")
	}
	if !limited.ResetAt.Equal(reset) {
		t.Errorf("ResetAt = %v, want %v (from the header, not a guess)", limited.ResetAt, reset)
	}
	if limited.Resource != ResourceCore {
		t.Errorf("Resource = %q, want the core bucket", limited.Resource)
	}
}

// A 403 that is not a rate limit (no quota headers) must not read as one, or a
// permissions problem would silently pause every sidebar for the cooldown.
func TestParsePollResponseForbiddenIsNotRateLimit(t *testing.T) {
	raw := rawResponse("403 Forbidden", map[string]string{
		"X-Ratelimit-Remaining": "4321",
	}, `{"message":"Resource not accessible"}`)

	_, err := parsePollResponse(raw, "")
	if err == nil {
		t.Fatal("expected an error")
	}
	if errors.Is(err, ErrGHRateLimited) {
		t.Error("a plain 403 must not be treated as a rate limit")
	}
}

func TestParsePollResponseUnauthorized(t *testing.T) {
	raw := rawResponse("401 Unauthorized", map[string]string{}, `{"message":"Bad credentials"}`)
	_, err := parsePollResponse(raw, "")
	if !errors.Is(err, ErrGHAuth) {
		t.Errorf("err = %v, want ErrGHAuth", err)
	}
	if !IsPermanentError(err) {
		t.Error("auth failure should be permanent so the tick loop stops retrying")
	}
}

// A burst can trip GitHub's *secondary* limit with thousands of primary quota
// still available. It reports itself only through Retry-After and a message, so
// a parser that keys off X-RateLimit-Remaining alone reads it as a permissions
// error and keeps hammering.
func TestParsePollResponseSecondaryRateLimit(t *testing.T) {
	raw := rawResponse("403 Forbidden", map[string]string{
		"Retry-After":           "60",
		"X-Ratelimit-Remaining": "4321",
	}, `{"message":"You have exceeded a secondary rate limit. Please wait a few minutes before you try again."}`)

	_, err := parsePollResponse(raw, "")
	if !errors.Is(err, ErrGHRateLimited) {
		t.Fatalf("err = %v, want a rate limit", err)
	}
	var limited *RateLimitedError
	if !errors.As(err, &limited) {
		t.Fatal("want a RateLimitedError")
	}
	if limited.Resource != "secondary" {
		t.Errorf("Resource = %q, want secondary", limited.Resource)
	}
	if d := time.Until(limited.ResetAt); d < 55*time.Second || d > 65*time.Second {
		t.Errorf("ResetAt is %v away, want ~60s from Retry-After", d)
	}
}

func TestParsePollResponseSecondaryWithoutRetryAfter(t *testing.T) {
	raw := rawResponse("403 Forbidden", map[string]string{},
		`{"message":"You have exceeded a secondary rate limit."}`)

	_, err := parsePollResponse(raw, "")
	var limited *RateLimitedError
	if !errors.As(err, &limited) {
		t.Fatalf("err = %v, want a rate limit even without Retry-After", err)
	}
	if time.Until(limited.ResetAt) <= 0 {
		t.Error("want a pause in the future")
	}
}

// A remote that goes through an ssh config alias is still GitHub, and must not
// be pushed onto the expensive per-branch GraphQL path. Four of the twenty
// repos in the setup this was written for use this form, including the repo
// itself.
func TestRepoRefFromRemoteResolvesSSHAliases(t *testing.T) {
	orig := sshResolve
	t.Cleanup(func() { sshResolve = orig })
	sshResolve = func(host string) string {
		switch host {
		case "github-panamafrancis", "gh-work":
			return "github.com"
		case "git.internal":
			return "git.internal"
		}
		return ""
	}

	tests := []struct {
		raw  string
		want string // "" means "not a GitHub repo we can address"
	}{
		{"git@github-panamafrancis:panamafrancis/workbench.git", "panamafrancis/workbench"},
		{"ssh://git@gh-work/acme/widgets.git", "acme/widgets"},
		{"git@github.com:fraud-zero/docs.git", "fraud-zero/docs"},
		{"https://github.com/acme/widgets", "acme/widgets"},
		{"git@git.internal:team/thing.git", ""},
		{"git@unknown-alias:team/thing.git", ""},
		{"/local/path", ""},
	}
	for _, tc := range tests {
		ref, ok := RepoRefFromRemote(tc.raw)
		if tc.want == "" {
			if ok {
				t.Errorf("RepoRefFromRemote(%q) = %v, want unresolvable", tc.raw, ref)
			}
			continue
		}
		if !ok || ref.String() != tc.want {
			t.Errorf("RepoRefFromRemote(%q) = %v/%v, want %q", tc.raw, ref, ok, tc.want)
		}
	}
}
