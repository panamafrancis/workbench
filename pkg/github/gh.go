package github

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
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
	Number    int         `json:"number"`
	Status    PRStatus    `json:"status"`
	Title     string      `json:"title"`
	URL       string      `json:"url"`
	Review    ReviewState `json:"review,omitempty"`
	Checks    CheckState  `json:"checks,omitempty"`
	UpdatedAt time.Time   `json:"updated_at"`
	FetchedAt time.Time   `json:"fetched_at"`
}

var ErrGHNotFound = errors.New("gh CLI not found")
var ErrGHAuth = errors.New("gh auth required")
var ErrGHRateLimited = errors.New("gh rate limited")

type ghPR struct {
	Number         int           `json:"number"`
	State          string        `json:"state"`
	Title          string        `json:"title"`
	URL            string        `json:"url"`
	IsDraft        bool          `json:"isDraft"`
	ReviewDecision string        `json:"reviewDecision"`
	Checks         []ghCheckNode `json:"statusCheckRollup"`
	UpdatedAt      time.Time     `json:"updatedAt"`
}

// ghCheckNode covers both shapes GitHub returns in statusCheckRollup: a
// CheckRun (status + conclusion) and a legacy StatusContext (state).
type ghCheckNode struct {
	Status     string `json:"status"`
	Conclusion string `json:"conclusion"`
	State      string `json:"state"`
}

func LookupPR(repoPath, branch string) (*PRInfo, error) {
	cmd := exec.CommandContext(context.Background(), "gh", "pr", "list",
		"--head", branch,
		"--state", "all",
		"--json", "number,state,title,url,isDraft,reviewDecision,statusCheckRollup,updatedAt",
		"--limit", "1",
	)
	cmd.Dir = repoPath

	out, err := cmd.Output()
	if err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			return nil, ErrGHNotFound
		}
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			stderr := string(exitErr.Stderr)
			if exitErr.ExitCode() == 4 || strings.Contains(stderr, "not logged in") || strings.Contains(stderr, "authentication") {
				return nil, ErrGHAuth
			}
			if strings.Contains(strings.ToLower(stderr), "rate limit") {
				return nil, ErrGHRateLimited
			}
			return nil, fmt.Errorf("gh: %s", stderr)
		}
		return nil, fmt.Errorf("gh: %w", err)
	}

	var prs []ghPR
	if err := json.Unmarshal(out, &prs); err != nil {
		return nil, fmt.Errorf("parse gh output: %w", err)
	}

	now := time.Now()
	if len(prs) == 0 {
		return &PRInfo{Status: PRNone, FetchedAt: now}, nil
	}

	pr := prs[0]
	return &PRInfo{
		Number:    pr.Number,
		Status:    mapStatus(pr.State, pr.IsDraft),
		Title:     pr.Title,
		URL:       pr.URL,
		Review:    mapReview(pr.ReviewDecision),
		Checks:    rollupChecks(pr.Checks),
		UpdatedAt: pr.UpdatedAt,
		FetchedAt: now,
	}, nil
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

func IsRateLimited(err error) bool {
	return errors.Is(err, ErrGHRateLimited)
}
