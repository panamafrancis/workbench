package github

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

type PRStatus string

const (
	PRNone   PRStatus = ""
	PRDraft  PRStatus = "draft"
	PROpen   PRStatus = "open"
	PRMerged PRStatus = "merged"
	PRClosed PRStatus = "closed"
)

// ReviewState is the aggregate review verdict on a PR (GitHub's reviewDecision).
type ReviewState string

const (
	ReviewNone     ReviewState = ""
	ReviewRequired ReviewState = "review_required"
	ReviewApproved ReviewState = "approved"
	ReviewChanges  ReviewState = "changes_requested"
)

// CheckState is the rolled-up verdict of a PR's status checks.
type CheckState string

const (
	CheckNone    CheckState = ""
	CheckPending CheckState = "pending"
	CheckPassing CheckState = "passing"
	CheckFailing CheckState = "failing"
)

type PRInfo struct {
	Number int         `json:"number"`
	Status PRStatus    `json:"status"`
	Title  string      `json:"title"`
	URL    string      `json:"url"`
	Review ReviewState `json:"review,omitempty"`
	Checks CheckState  `json:"checks,omitempty"`
	// HeadOID is the commit the PR's head points at. It is what makes "the
	// author has pushed since you looked" answerable without a second request.
	HeadOID   string    `json:"head_oid,omitempty"`
	UpdatedAt time.Time `json:"updated_at"`
	FetchedAt time.Time `json:"fetched_at"`
}

// PRRef is what a previous round recorded about a branch's PR: the number that
// identifies it, and the URL that says which repository that number belongs to.
type PRRef struct {
	Number int
	URL    string
}

var ErrGHNotFound = errors.New("gh CLI not found")
var ErrGHAuth = errors.New("gh auth required")
var ErrGHRateLimited = errors.New("gh rate limited")

// ErrRepoNotFound means GitHub cannot see the repository a worktree's origin
// names: it was moved or renamed, or the gh account in use has no access.
// Retrying it on the ordinary schedule changes nothing and spends quota every
// round, so fetchers back that one branch off (Cache.MarkUnreachable) rather
// than aborting the batch — every other repo still resolves.
var ErrRepoNotFound = errors.New("repository not visible to gh")

// ErrPRNotFound means GitHub has no PR with the number we asked about — it was
// deleted, or the number belongs to another repo. Distinct from "this branch
// has no PR", which LookupPR reports as a PRNone result.
var ErrPRNotFound = errors.New("pull request not found")

type ghPR struct {
	Number         int           `json:"number"`
	State          string        `json:"state"`
	Title          string        `json:"title"`
	URL            string        `json:"url"`
	IsDraft        bool          `json:"isDraft"`
	ReviewDecision string        `json:"reviewDecision"`
	Checks         []ghCheckNode `json:"statusCheckRollup"`
	HeadRefOid     string        `json:"headRefOid"`
	UpdatedAt      time.Time     `json:"updatedAt"`
}

// ghCheckNode covers both shapes GitHub returns in statusCheckRollup: a
// CheckRun (status + conclusion) and a legacy StatusContext (state).
type ghCheckNode struct {
	Status     string `json:"status"`
	Conclusion string `json:"conclusion"`
	State      string `json:"state"`
}

// prJSONFields is the field set every PR lookup asks gh for. Both entry points
// unmarshal into ghPR, so they must stay in step.
const prJSONFields = "number,state,title,url,isDraft,reviewDecision,statusCheckRollup,headRefOid,updatedAt"

// LookupPR finds the PR whose head is branch. GitHub keys this on the current
// head ref, so it returns nothing for a PR whose head has since moved or whose
// branch was renamed after it merged — see ResolvePR.
func LookupPR(repoPath, branch string) (*PRInfo, error) {
	cmd := exec.CommandContext(context.Background(), "gh", "pr", "list",
		"--head", branch,
		"--state", "all",
		"--json", prJSONFields,
		"--limit", "1",
	)
	cmd.Dir = repoPath

	out, err := cmd.Output()
	if err != nil {
		return nil, classifyGHError(err)
	}

	var prs []ghPR
	if err := json.Unmarshal(out, &prs); err != nil {
		return nil, fmt.Errorf("parse gh output: %w", err)
	}

	now := time.Now()
	if len(prs) == 0 {
		return &PRInfo{Status: PRNone, FetchedAt: now}, nil
	}
	return prs[0].toInfo(now), nil
}

// LookupPRByNumber fetches a PR by its number. The number is the only stable
// identity a PR has: it survives branch renames, merges, and deletion of the
// head branch, all of which make a --head lookup come back empty.
func LookupPRByNumber(repoPath string, number int) (*PRInfo, error) {
	cmd := exec.CommandContext(context.Background(), "gh", "pr", "view",
		strconv.Itoa(number),
		"--json", prJSONFields,
	)
	cmd.Dir = repoPath

	out, err := cmd.Output()
	if err != nil {
		return nil, classifyGHError(err)
	}

	var pr ghPR
	if err := json.Unmarshal(out, &pr); err != nil {
		return nil, fmt.Errorf("parse gh output: %w", err)
	}
	if pr.Number == 0 {
		return nil, ErrPRNotFound
	}
	return pr.toInfo(time.Now()), nil
}

// prLookup is the seam the resolution policy is tested against; the exported
// ResolvePR binds it to the real gh calls.
type prLookup struct {
	byHead   func(repoPath, branch string) (*PRInfo, error)
	byNumber func(repoPath string, number int) (*PRInfo, error)
}

// ResolvePR refreshes the PR status for branch. prev is what a previous round
// recorded for this branch, zero if nothing is known.
//
// The head ref is not the PR's identity. Renaming a local branch only retargets
// a PR that is still open when the new branch is pushed; a PR that merged or
// closed first keeps the old head forever, as does one whose push failed or was
// never made (--push=false). Asking by head alone then returns nothing and the
// PR silently disappears from the cache. So a head lookup that comes back empty
// while we hold a number falls back to that number, which resolves the PR
// wherever its head has ended up.
//
// Head first, not number first: a branch may have picked up a *new* PR since
// the cached number was recorded (a reused worktree name), and that new PR is
// the one worth reporting. prev carries the URL as well as the number so a
// number recorded against a different repo (the cache is keyed on branch name
// alone) can be rejected instead of resolving an unrelated PR.
func ResolvePR(repoPath, branch string, prev PRRef) (*PRInfo, error) {
	return resolvePR(prLookup{byHead: LookupPR, byNumber: LookupPRByNumber}, repoPath, branch, prev)
}

// ResolvePRByNumber looks a PR up by number alone, skipping the head lookup.
//
// It exists for callers that know the PR by construction rather than by having
// found it from a branch — a review tree, whose members sit on a tree-local
// branch that provably has no PR. Going through ResolvePR would spend a
// guaranteed-empty `gh pr list --head` on every member of every round, which is
// exactly the wasted quota the fetch discipline is built to avoid.
func ResolvePRByNumber(repoPath string, ref PRRef) (*PRInfo, error) {
	return resolveByNumber(LookupPRByNumber, repoPath, ref)
}

// resolveByNumber is the shared by-number path: not found means the PR is gone,
// and a result from another repository is rejected the same way resolvePR
// rejects it.
func resolveByNumber(byNumber func(string, int) (*PRInfo, error), repoPath string, ref PRRef) (*PRInfo, error) {
	if ref.Number == 0 {
		return &PRInfo{Status: PRNone}, nil
	}
	info, err := byNumber(repoPath, ref.Number)
	if errors.Is(err, ErrPRNotFound) {
		return &PRInfo{Status: PRNone}, nil
	}
	if err != nil {
		return nil, err
	}
	if !sameRepo(ref.URL, info.URL) {
		return nil, fmt.Errorf("%w: #%d resolved to %s, not %s", ErrPRNotFound, ref.Number, info.URL, ref.URL)
	}
	return info, nil
}

func resolvePR(l prLookup, repoPath, branch string, prev PRRef) (*PRInfo, error) {
	info, err := l.byHead(repoPath, branch)
	if err != nil {
		return nil, err
	}
	if prev.Number == 0 || info.Status != PRNone {
		return info, nil
	}
	byNum, err := l.byNumber(repoPath, prev.Number)
	if errors.Is(err, ErrPRNotFound) {
		// The PR really is gone; the empty head result is the truth.
		return info, nil
	}
	if err != nil {
		// Transient (rate limit, auth, network): report it rather than letting
		// the caller cache an empty result over a PR we know exists.
		return nil, err
	}
	if !sameRepo(prev.URL, byNum.URL) {
		// The number was recorded against another repo — the cache is keyed on
		// branch name alone, so two repos with the same branch name share an
		// entry. Looking that number up here resolves whatever unrelated PR
		// happens to hold it, so fall back to what the head lookup said.
		return info, nil
	}
	return byNum, nil
}

// sameRepo reports whether two PR URLs name the same repository. A ref with no
// URL to compare against is taken at face value.
func sameRepo(a, b string) bool {
	if a == "" || b == "" {
		return true
	}
	return prRepoURL(a) == prRepoURL(b)
}

// prRepoURL strips the /pull/<n> suffix off a PR URL, leaving the repository it
// belongs to (https://github.com/owner/repo/pull/12 → .../owner/repo).
func prRepoURL(prURL string) string {
	if i := strings.LastIndex(prURL, "/pull/"); i >= 0 {
		return prURL[:i]
	}
	return prURL
}

// classifyGHError maps a failed gh invocation onto the sentinels callers switch
// on. Anything unrecognized comes back as an opaque error carrying gh's stderr.
func classifyGHError(err error) error {
	if errors.Is(err, exec.ErrNotFound) {
		return ErrGHNotFound
	}
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		return fmt.Errorf("gh: %w", err)
	}
	stderr := string(exitErr.Stderr)
	lower := strings.ToLower(stderr)
	if exitErr.ExitCode() == 4 || strings.Contains(stderr, "not logged in") || strings.Contains(stderr, "authentication") {
		return ErrGHAuth
	}
	if strings.Contains(lower, "rate limit") {
		return ErrGHRateLimited
	}
	if strings.Contains(lower, "could not resolve to a pullrequest") || strings.Contains(lower, "no pull requests found") {
		return ErrPRNotFound
	}
	if strings.Contains(lower, "could not resolve to a repository") {
		return fmt.Errorf("%w: %s", ErrRepoNotFound, strings.TrimSpace(stderr))
	}
	return fmt.Errorf("gh: %s", stderr)
}

func (pr ghPR) toInfo(now time.Time) *PRInfo {
	return &PRInfo{
		Number:    pr.Number,
		Status:    mapStatus(pr.State, pr.IsDraft),
		Title:     pr.Title,
		URL:       pr.URL,
		Review:    mapReview(pr.ReviewDecision),
		Checks:    rollupChecks(pr.Checks),
		HeadOID:   pr.HeadRefOid,
		UpdatedAt: pr.UpdatedAt,
		FetchedAt: now,
	}
}

// mapReview normalizes GitHub's reviewDecision enum. An empty decision means
// the repo requires no review (or none has been requested), not "pending".
func mapReview(decision string) ReviewState {
	switch decision {
	case "APPROVED":
		return ReviewApproved
	case "CHANGES_REQUESTED":
		return ReviewChanges
	case "REVIEW_REQUIRED":
		return ReviewRequired
	default:
		return ReviewNone
	}
}

// rollupChecks reduces the per-check nodes to one verdict: any failure wins,
// then any still-running check, otherwise passing. Both node shapes are
// handled — CheckRun reports status/conclusion, StatusContext reports state.
func rollupChecks(nodes []ghCheckNode) CheckState {
	out := CheckNone
	for _, n := range nodes {
		var s CheckState
		switch {
		case n.State != "":
			s = mapContextState(n.State)
		case n.Status != "COMPLETED":
			s = CheckPending
		default:
			s = mapConclusion(n.Conclusion)
		}
		if s == CheckFailing {
			return CheckFailing
		}
		if s == CheckPending || (s == CheckPassing && out == CheckNone) {
			out = s
		}
	}
	return out
}

func mapContextState(state string) CheckState {
	switch state {
	case "SUCCESS":
		return CheckPassing
	case "FAILURE", "ERROR":
		return CheckFailing
	default:
		return CheckPending
	}
}

func mapConclusion(conclusion string) CheckState {
	switch conclusion {
	case "SUCCESS", "NEUTRAL", "SKIPPED":
		return CheckPassing
	case "":
		return CheckPending
	default:
		return CheckFailing
	}
}

func mapStatus(state string, isDraft bool) PRStatus {
	switch state {
	case "MERGED":
		return PRMerged
	case "CLOSED":
		return PRClosed
	default:
		if isDraft {
			return PRDraft
		}
		return PROpen
	}
}

func IsPermanentError(err error) bool {
	return errors.Is(err, ErrGHNotFound) || errors.Is(err, ErrGHAuth)
}

// IsRepoNotFound reports whether err means gh cannot see the repository.
func IsRepoNotFound(err error) bool {
	return errors.Is(err, ErrRepoNotFound)
}

func IsRateLimited(err error) bool {
	return errors.Is(err, ErrGHRateLimited)
}
