package github

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"time"
)

// PostedReview is one submitted review as the REST API reports it, carrying
// the commit it was made against — which the GraphQL feedback in comments.go
// does not, and which is what "has this head been reviewed" turns on.
type PostedReview struct {
	ID          int64     `json:"id"`
	Author      string    `json:"author"`
	State       string    `json:"state"` // APPROVED, CHANGES_REQUESTED, COMMENTED, DISMISSED
	CommitID    string    `json:"commit_id"`
	URL         string    `json:"url"`
	SubmittedAt time.Time `json:"submitted_at"`
}

type restReview struct {
	ID          int64     `json:"id"`
	User        restUser  `json:"user"`
	State       string    `json:"state"`
	CommitID    string    `json:"commit_id"`
	HTMLURL     string    `json:"html_url"`
	SubmittedAt time.Time `json:"submitted_at"`
}

// reviewsTimeout bounds the paginated listing, which is several requests.
const reviewsTimeout = 60 * time.Second

// ReviewsBy lists the reviews login has submitted on a pull request, oldest
// first. It is an on-demand call — made once after an automated review run, to
// confirm what was actually posted — and must never be made from a poll.
// Pending (unsubmitted) reviews are left out.
func ReviewsBy(repo RepoRef, number int, login string) ([]PostedReview, error) {
	if !repo.Valid() || number <= 0 {
		return nil, fmt.Errorf("reviews: invalid pull request %s#%d", repo, number)
	}
	ctx, cancel := context.WithTimeout(context.Background(), reviewsTimeout)
	defer cancel()
	path := fmt.Sprintf("repos/%s/%s/pulls/%d/reviews?per_page=100", repo.Owner, repo.Name, number)
	out, err := exec.CommandContext(ctx, "gh", "api", "--paginate", path).Output()
	if err != nil {
		return nil, classifyGHError(err)
	}
	return parseReviews(out, login)
}

// parseReviews decodes `gh api --paginate` output, which is one JSON array per
// page written back to back, and keeps login's submitted reviews.
func parseReviews(raw []byte, login string) ([]PostedReview, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	var out []PostedReview
	for {
		var page []restReview
		err := dec.Decode(&page)
		if errors.Is(err, io.EOF) {
			return out, nil
		}
		if err != nil {
			return nil, fmt.Errorf("parse reviews: %w", err)
		}
		for _, r := range page {
			if !strings.EqualFold(r.User.Login, login) || r.State == "PENDING" {
				continue
			}
			out = append(out, PostedReview{
				ID:          r.ID,
				Author:      r.User.Login,
				State:       r.State,
				CommitID:    r.CommitID,
				URL:         r.HTMLURL,
				SubmittedAt: r.SubmittedAt,
			})
		}
	}
}
