package supatree

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"

	"github.com/panamafrancis/workbench/pkg/mcp"
)

// ReviewComment is one inline comment in a batched review.
//
// Line is a line number **on the pull request head**, which is what this tree
// has checked out. Numbers taken from the base branch land on unrelated code —
// the single most common way a machine-written review is confidently wrong.
type ReviewComment struct {
	Path      string `json:"path"`
	Line      int    `json:"line"`
	StartLine int    `json:"start_line,omitempty"`
	Body      string `json:"body"`
}

// reviewPayload is the GitHub reviews API request body.
type reviewPayload struct {
	CommitID string          `json:"commit_id"`
	Body     string          `json:"body,omitempty"`
	Event    string          `json:"event"`
	Comments []ReviewComment `json:"comments,omitempty"`
}

// validReviewEvents are the verdicts the API accepts.
var validReviewEvents = map[string]bool{"COMMENT": true, "REQUEST_CHANGES": true, "APPROVE": true}

// PostReview submits one batched review to a member's pull request.
//
// Batched on purpose: a review is one verdict with its evidence attached, and a
// stream of separate comments is both noisier to read and impossible to answer
// as a whole. It is anchored to the commit this worktree is checked out at, so
// the line numbers mean what they say.
func PostReview(inst *Instance, alias, body, event string, comments []ReviewComment) (string, error) {
	m := inst.FindMember(alias)
	if m == nil || m.Review == nil {
		return "", fmt.Errorf("%q is not a reviewed member of %s", alias, inst.Name)
	}
	if event == "" {
		event = "COMMENT"
	}
	event = strings.ToUpper(event)
	if !validReviewEvents[event] {
		return "", fmt.Errorf("event %q is not one of COMMENT, REQUEST_CHANGES, APPROVE", event)
	}
	if strings.TrimSpace(body) == "" && len(comments) == 0 {
		return "", fmt.Errorf("refusing to post an empty review")
	}
	for i, c := range comments {
		if c.Path == "" || c.Line <= 0 {
			return "", fmt.Errorf("comment %d needs a path and a line number from the PR head", i+1)
		}
	}

	payload, err := json.Marshal(reviewPayload{
		CommitID: m.Review.Head,
		Body:     body,
		Event:    event,
		Comments: comments,
	})
	if err != nil {
		return "", fmt.Errorf("encode review: %w", err)
	}

	ctx, cancel := mcp.ToolContext()
	defer cancel()
	cmd := exec.CommandContext(ctx, "gh", "api",
		fmt.Sprintf("repos/%s/pulls/%d/reviews", m.Review.Repo, m.Review.Number),
		"--method", "POST", "--input", "-")
	cmd.Stdin = bytes.NewReader(payload)
	// Stderr kept separate: gh writes progress and deprecation notices there,
	// and folding them into stdout corrupts the JSON body — which would cost
	// the review URL on every successful post that happened to warn.
	var errBuf bytes.Buffer
	cmd.Stderr = &errBuf
	out, err := cmd.Output()
	if err != nil {
		detail := strings.TrimSpace(errBuf.String())
		if detail == "" {
			detail = err.Error()
		}
		return "", fmt.Errorf("gh api: %s", detail)
	}
	var res struct {
		HTMLURL string `json:"html_url"`
		State   string `json:"state"`
	}
	if err := json.Unmarshal(out, &res); err != nil || res.HTMLURL == "" {
		return fmt.Sprintf("posted %s on %s#%d", event, m.Review.Repo, m.Review.Number), nil
	}
	return fmt.Sprintf("posted %s on %s#%d with %d inline comment(s): %s",
		event, m.Review.Repo, m.Review.Number, len(comments), res.HTMLURL), nil
}

// ParseReviewComments decodes the inline comments argument.
func ParseReviewComments(raw string) ([]ReviewComment, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	var out []ReviewComment
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil, fmt.Errorf(`comments must be a JSON array like [{"path":"a.go","line":12,"body":"..."}]: %w`, err)
	}
	return out, nil
}
