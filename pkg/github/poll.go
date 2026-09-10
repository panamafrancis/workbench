package github

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// Polling a repo's pull request list is how PR status stays current without
// spending quota. `gh pr list` costs one GraphQL point per *branch*; this costs
// one REST point per *repo*, and nothing at all when the repo has not changed:
// a conditional request (If-None-Match) that comes back 304 Not Modified is not
// charged against the rate limit at all. It also spends from the core bucket
// rather than the GraphQL one that agents drain with `gh pr view` and friends,
// so background polling stops competing with interactive work.
//
// What this cannot answer is whether a branch has *no* PR: one page only proves
// what changed recently. Callers establish absence with a targeted lookup
// instead, and re-poll on the ordinary schedule.

// resourceCore is what GitHub calls the REST bucket in its rate-limit headers.
// Background polling lives here deliberately, leaving the GraphQL bucket to the
// agents' own gh calls.
const resourceCore = "core"

// pollPageSize is the page size for a poll. One page is enough to carry every
// PR that changed between two rounds; Truncated reports when it might not be.
const pollPageSize = 100

// RepoRef is a GitHub owner/name pair.
type RepoRef struct {
	Owner string
	Name  string
}

func (r RepoRef) String() string { return r.Owner + "/" + r.Name }

// Valid reports whether the ref names a repo.
func (r RepoRef) Valid() bool { return r.Owner != "" && r.Name != "" }

// ParseRemoteURL extracts the GitHub owner/name from a git remote URL. It
// recognises the scp-style, https and ssh:// forms, and reports false for a
// remote that isn't GitHub (an enterprise host, a local path, no origin).
func ParseRemoteURL(raw string) (RepoRef, bool) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return RepoRef{}, false
	}
	switch {
	case strings.HasPrefix(s, "git@github.com:"):
		s = strings.TrimPrefix(s, "git@github.com:")
	case strings.HasPrefix(s, "ssh://git@github.com/"):
		s = strings.TrimPrefix(s, "ssh://git@github.com/")
	case strings.HasPrefix(s, "https://github.com/"):
		s = strings.TrimPrefix(s, "https://github.com/")
	case strings.HasPrefix(s, "http://github.com/"):
		s = strings.TrimPrefix(s, "http://github.com/")
	default:
		return RepoRef{}, false
	}
	s = strings.TrimSuffix(strings.TrimSuffix(s, "/"), ".git")
	owner, name, ok := strings.Cut(s, "/")
	if !ok || owner == "" || name == "" || strings.Contains(name, "/") {
		return RepoRef{}, false
	}
	return RepoRef{Owner: owner, Name: name}, true
}

// PollPR is one pull request as returned by a repo poll, carrying the head
// branch so callers can match it against the branches they track.
type PollPR struct {
	HeadRef string
	Info    *PRInfo
}

// RepoPoll is the outcome of one conditional poll of a repo's PR list.
type RepoPoll struct {
	// NotModified is set when the repo answered 304: nothing has changed since
	// the ETag was issued, and the request cost nothing.
	NotModified bool
	// ETag to send on the next poll of this repo.
	ETag string
	// PRs on the page, most recently updated first. Empty when NotModified.
	PRs []PollPR
	// Truncated reports that more pages exist. The page still holds every PR
	// updated since Oldest, so a caller whose last poll is newer than Oldest has
	// a complete delta regardless; an older one may have missed something.
	Truncated bool
	// Oldest is the update time of the last PR on the page.
	Oldest time.Time
}

// Complete reports whether this page carries every change since since.
func (p RepoPoll) Complete(since time.Time) bool {
	return !p.Truncated || since.After(p.Oldest) || since.Equal(p.Oldest)
}

// RateLimitedError carries the reset time the response reported, so callers can
// pause exactly as long as the quota is actually gone instead of guessing a
// fixed window.
type RateLimitedError struct {
	Resource string
	ResetAt  time.Time
}

func (e *RateLimitedError) Error() string {
	if e.ResetAt.IsZero() {
		return "gh rate limited"
	}
	return fmt.Sprintf("gh rate limited (%s resets %s)", e.Resource, e.ResetAt.Format(time.Kitchen))
}

// Is reports RateLimitedError as ErrGHRateLimited so existing checks
// (IsRateLimited) keep working on the richer error.
func (e *RateLimitedError) Is(target error) bool { return target == ErrGHRateLimited }

// PollRepo fetches the repo's recently updated pull requests, sending etag (if
// any) so an unchanged repo answers 304 for free.
func PollRepo(repoPath string, ref RepoRef, etag string) (RepoPoll, error) {
	path := fmt.Sprintf("repos/%s/%s/pulls?state=all&sort=updated&direction=desc&per_page=%d",
		ref.Owner, ref.Name, pollPageSize)
	args := []string{"api", "-i", path}
	if etag != "" {
		args = append(args, "-H", "If-None-Match: "+etag)
	}

	ctx, cancel := context.WithTimeout(context.Background(), lookupTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "gh", args...)
	cmd.Dir = repoPath
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	// The exit status is deliberately not the primary signal: `gh api -i` exits
	// non-zero for any non-2xx, and 304 — the response we most want — is one of
	// them. Parse what it wrote first and only fall back to the exit status when
	// there is no parsable response.
	runErr := cmd.Run()
	poll, parseErr := parsePollResponse(stdout.Bytes(), etag)
	if parseErr == nil {
		return poll, nil
	}
	if !errors.Is(parseErr, errNoResponse) {
		// gh exits non-zero for every non-2xx, including the 304 and 404 we
		// classify ourselves — the parsed status is the better answer.
		return RepoPoll{}, parseErr
	}
	if runErr != nil {
		if errors.Is(runErr, exec.ErrNotFound) {
			return RepoPoll{}, ErrGHNotFound
		}
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return RepoPoll{}, fmt.Errorf("gh: %s", msg)
		}
		return RepoPoll{}, fmt.Errorf("gh: %w", runErr)
	}
	return RepoPoll{}, parseErr
}

// LookupBranchPR resolves one branch's PR through the REST API. It is the
// fallback for a branch a repo poll could not settle, and it stays on the core
// bucket: the GraphQL equivalent (`gh pr list`) competes with every agent's
// `gh pr view`/`pr checks`, and in practice that bucket is the one that runs
// out. An empty result is authoritative — the branch has no PR.
func LookupBranchPR(repoPath string, ref RepoRef, branch string) (*PRInfo, error) {
	path := fmt.Sprintf("repos/%s/%s/pulls?state=all&per_page=1&head=%s:%s",
		ref.Owner, ref.Name, ref.Owner, url.QueryEscape(branch))

	ctx, cancel := context.WithTimeout(context.Background(), lookupTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "gh", "api", "-i", path)
	cmd.Dir = repoPath
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	runErr := cmd.Run()
	poll, parseErr := parsePollResponse(stdout.Bytes(), "")
	if parseErr != nil {
		if !errors.Is(parseErr, errNoResponse) {
			return nil, parseErr
		}
		if runErr != nil {
			if errors.Is(runErr, exec.ErrNotFound) {
				return nil, ErrGHNotFound
			}
			if msg := strings.TrimSpace(stderr.String()); msg != "" {
				return nil, fmt.Errorf("gh: %s", msg)
			}
		}
		return nil, parseErr
	}
	if len(poll.PRs) == 0 {
		return &PRInfo{Status: PRNone, FetchedAt: time.Now()}, nil
	}
	return poll.PRs[0].Info, nil
}

// restPR is the subset of GitHub's pull request representation we cache.
type restPR struct {
	Number    int        `json:"number"`
	State     string     `json:"state"`
	Draft     bool       `json:"draft"`
	Title     string     `json:"title"`
	HTMLURL   string     `json:"html_url"`
	UpdatedAt time.Time  `json:"updated_at"`
	MergedAt  *time.Time `json:"merged_at"`
	Head      struct {
		Ref string `json:"ref"`
	} `json:"head"`
}

// errNoResponse marks the case where gh produced nothing we could read as an
// HTTP response, as opposed to a response that reports a problem. The two need
// separate handling: the latter is already a precise answer, and wrapping it in
// gh's stderr line loses the status we just classified.
var errNoResponse = errors.New("no parsable HTTP response")

// ErrRepoNotFound is returned when the repo does not exist for the
// authenticated account — private to another org, renamed, or deleted. It is a
// property of the repo, not of the request, so callers stop polling it rather
// than retrying every round.
var ErrRepoNotFound = errors.New("repo not found")

func parsePollResponse(raw []byte, prevETag string) (RepoPoll, error) {
	resp, err := http.ReadResponse(bufio.NewReader(bytes.NewReader(raw)), nil)
	if err != nil {
		return RepoPoll{}, fmt.Errorf("%w: %w", errNoResponse, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusNotModified {
		// Nothing changed, and GitHub charged nothing for saying so.
		return RepoPoll{NotModified: true, ETag: prevETag}, nil
	}
	if resp.StatusCode == http.StatusUnauthorized {
		return RepoPoll{}, ErrGHAuth
	}

	body, err := readAllBody(resp)
	if err != nil {
		return RepoPoll{}, err
	}

	if resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusTooManyRequests {
		if limited := rateLimitFrom(resp.Header, body, time.Now()); limited != nil {
			return RepoPoll{}, limited
		}
		return RepoPoll{}, fmt.Errorf("gh: HTTP %d: %s", resp.StatusCode, apiMessage(body))
	}
	if resp.StatusCode == http.StatusNotFound {
		return RepoPoll{}, ErrRepoNotFound
	}
	if resp.StatusCode != http.StatusOK {
		return RepoPoll{}, fmt.Errorf("gh: HTTP %d: %s", resp.StatusCode, apiMessage(body))
	}
	var prs []restPR
	if err := json.Unmarshal(body, &prs); err != nil {
		return RepoPoll{}, fmt.Errorf("parse pr list: %w", err)
	}

	now := time.Now()
	poll := RepoPoll{
		ETag:      resp.Header.Get("ETag"),
		Truncated: hasNextPage(resp.Header.Get("Link")),
		PRs:       make([]PollPR, 0, len(prs)),
	}
	for _, pr := range prs {
		poll.PRs = append(poll.PRs, PollPR{
			HeadRef: pr.Head.Ref,
			Info: &PRInfo{
				Number:    pr.Number,
				Status:    restStatus(pr),
				Title:     pr.Title,
				URL:       pr.HTMLURL,
				UpdatedAt: pr.UpdatedAt,
				FetchedAt: now,
			},
		})
	}
	if len(prs) > 0 {
		poll.Oldest = prs[len(prs)-1].UpdatedAt
	}
	return poll, nil
}

// restStatus maps the REST representation to a PRStatus. The list endpoint
// reports merged PRs as state "closed" with merged_at set, so merged and closed
// are only distinguishable through that field.
func restStatus(pr restPR) PRStatus {
	if pr.MergedAt != nil && !pr.MergedAt.IsZero() {
		return PRMerged
	}
	if pr.State == "closed" {
		return PRClosed
	}
	if pr.Draft {
		return PRDraft
	}
	return PROpen
}

// rateLimitFrom classifies a 403/429. GitHub has two distinct limits and they
// are reported differently: the primary quota exhausts with
// X-RateLimit-Remaining: 0 and a reset timestamp, while the secondary limit —
// which a burst of requests can trip with thousands of quota left — comes back
// with Retry-After and a message, and no quota headers at all. Reading only the
// first would leave a burst looking like an ordinary permissions failure and
// keep the sidebars hammering. Anything else is a real 403 and must not pause
// fetches at all.
func rateLimitFrom(header http.Header, body []byte, now time.Time) *RateLimitedError {
	if header.Get("X-Ratelimit-Remaining") == "0" {
		return &RateLimitedError{
			Resource: header.Get("X-Ratelimit-Resource"),
			ResetAt:  headerReset(header.Get("X-Ratelimit-Reset")),
		}
	}
	secondary := mentionsSecondaryLimit(body)
	if after := retryAfter(header.Get("Retry-After")); after > 0 && secondary {
		return &RateLimitedError{Resource: "secondary", ResetAt: now.Add(after)}
	}
	if secondary {
		return &RateLimitedError{Resource: "secondary", ResetAt: now.Add(defaultSecondaryPause)}
	}
	return nil
}

// defaultSecondaryPause is how long to stand down after a secondary rate limit
// that came without a Retry-After. GitHub's guidance is to wait at least a
// minute before retrying.
const defaultSecondaryPause = time.Minute

func mentionsSecondaryLimit(body []byte) bool {
	msg := strings.ToLower(string(body))
	return strings.Contains(msg, "secondary rate limit") || strings.Contains(msg, "abuse detection")
}

func retryAfter(v string) time.Duration {
	secs, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil || secs <= 0 {
		return 0
	}
	return time.Duration(secs) * time.Second
}

// apiMessage pulls GitHub's "message" out of an error body for the log line.
func apiMessage(body []byte) string {
	var payload struct {
		Message string `json:"message"`
	}
	if err := json.Unmarshal(body, &payload); err == nil && payload.Message != "" {
		return payload.Message
	}
	return strings.TrimSpace(string(body))
}

func headerReset(v string) time.Time {
	secs, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
	if err != nil || secs <= 0 {
		return time.Time{}
	}
	return time.Unix(secs, 0)
}

// hasNextPage reports whether the Link header advertises a further page.
func hasNextPage(link string) bool {
	for _, part := range strings.Split(link, ",") {
		if strings.Contains(part, `rel="next"`) {
			return true
		}
	}
	return false
}

func readAllBody(resp *http.Response) ([]byte, error) {
	var buf bytes.Buffer
	if _, err := buf.ReadFrom(resp.Body); err != nil {
		return nil, fmt.Errorf("read gh response body: %w", err)
	}
	return buf.Bytes(), nil
}
