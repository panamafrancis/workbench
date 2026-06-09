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

type PRInfo struct {
	Number    int       `json:"number"`
	Status    PRStatus  `json:"status"`
	Title     string    `json:"title"`
	URL       string    `json:"url"`
	UpdatedAt time.Time `json:"updated_at"`
	FetchedAt time.Time `json:"fetched_at"`
}

var ErrGHNotFound = errors.New("gh CLI not found")
var ErrGHAuth = errors.New("gh auth required")

type ghPR struct {
	Number    int       `json:"number"`
	State     string    `json:"state"`
	Title     string    `json:"title"`
	URL       string    `json:"url"`
	IsDraft   bool      `json:"isDraft"`
	UpdatedAt time.Time `json:"updatedAt"`
}

func LookupPR(repoPath, branch string) (*PRInfo, error) {
	cmd := exec.CommandContext(context.Background(), "gh", "pr", "list",
		"--head", branch,
		"--state", "all",
		"--json", "number,state,title,url,isDraft,updatedAt",
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
		UpdatedAt: pr.UpdatedAt,
		FetchedAt: now,
	}, nil
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
