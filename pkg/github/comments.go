package github

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// Comment is one piece of review feedback: a top-level PR comment, a review
// body, or a line note inside a review thread.
type Comment struct {
	Author    string    `json:"author"`
	Body      string    `json:"body"`
	URL       string    `json:"url"`
	CreatedAt time.Time `json:"created_at"`
}

// ReviewThread is a line-anchored conversation. Its resolution state is the
// point of this whole file: it is the difference between "a reviewer said
// something once" and "a reviewer is still waiting", and it exists only in
// GraphQL.
type ReviewThread struct {
	Path     string    `json:"path"`
	Line     int       `json:"line,omitempty"`
	Resolved bool      `json:"resolved"`
	Outdated bool      `json:"outdated"`
	Comments []Comment `json:"comments"`
}

// Review is a submitted review: its verdict plus the body, if any.
type Review struct {
	Author      string    `json:"author"`
	State       string    `json:"state"` // APPROVED, CHANGES_REQUESTED, COMMENTED
	Body        string    `json:"body,omitempty"`
	URL         string    `json:"url"`
	SubmittedAt time.Time `json:"submitted_at"`
}

// PRFeedback is everything a reviewer has said about one pull request.
type PRFeedback struct {
	Number    int            `json:"number"`
	Comments  []Comment      `json:"comments"`
	Reviews   []Review       `json:"reviews"`
	Threads   []ReviewThread `json:"threads"`
	FetchedAt time.Time      `json:"fetched_at"`
}

// Unresolved returns the review threads still waiting on someone. Outdated
// threads are excluded: the lines they hang off no longer exist, so acting on
// them means re-litigating a diff that has already changed.
func (f PRFeedback) Unresolved() []ReviewThread {
	var out []ReviewThread
	for _, t := range f.Threads {
		if !t.Resolved && !t.Outdated {
			out = append(out, t)
		}
	}
	return out
}

// commentsQuery asks for the three shapes of feedback in one round trip.
// Resolution state forces GraphQL — `gh pr view --json comments` cannot report
// it — and since we are paying for a GraphQL query anyway, the comments and
// reviews ride along rather than costing two more calls.
//
// The `last:` windows are bounded deliberately. A PR with 400 comments is one
// nobody is going to read in a notification, and an unbounded query is how a
// single pathological PR eats the hourly budget.
const commentsQuery = `
query($owner:String!, $name:String!, $number:Int!) {
  repository(owner:$owner, name:$name) {
    pullRequest(number:$number) {
      number
      comments(last:30) { nodes { author{login} body url createdAt } }
      reviews(last:20) { nodes { author{login} body state url submittedAt } }
      reviewThreads(last:50) {
        nodes {
          isResolved isOutdated path line
          comments(first:10) { nodes { author{login} body url createdAt } }
        }
      }
    }
  }
}`

type ghAuthor struct {
	Login string `json:"login"`
}

type ghComment struct {
	Author    ghAuthor  `json:"author"`
	Body      string    `json:"body"`
	URL       string    `json:"url"`
	CreatedAt time.Time `json:"createdAt"`
}

type ghCommentsResponse struct {
	Data struct {
		Repository struct {
			PullRequest struct {
				Number   int `json:"number"`
				Comments struct {
					Nodes []ghComment `json:"nodes"`
				} `json:"comments"`
				Reviews struct {
					Nodes []struct {
						Author      ghAuthor  `json:"author"`
						Body        string    `json:"body"`
						State       string    `json:"state"`
						URL         string    `json:"url"`
						SubmittedAt time.Time `json:"submittedAt"`
					} `json:"nodes"`
				} `json:"reviews"`
				ReviewThreads struct {
					Nodes []struct {
						IsResolved bool   `json:"isResolved"`
						IsOutdated bool   `json:"isOutdated"`
						Path       string `json:"path"`
						Line       int    `json:"line"`
						Comments   struct {
							Nodes []ghComment `json:"nodes"`
						} `json:"comments"`
					} `json:"nodes"`
				} `json:"reviewThreads"`
			} `json:"pullRequest"`
		} `json:"repository"`
	} `json:"data"`
}

// PRComments fetches every reviewer utterance on one pull request.
//
// It is deliberately on-demand only — never on a poll — because it is a second
// GraphQL query shape per PR on top of the one `gh pr list` already spends, and
// running it across every open PR on a timer is precisely what the shared
// 5,000/hour budget cannot absorb.
func PRComments(repoPath string, number int) (*PRFeedback, error) {
	owner, name, err := RepoSlug(repoPath)
	if err != nil {
		return nil, err
	}
	cmd := exec.CommandContext(context.Background(), "gh", "api", "graphql",
		"-f", "query="+commentsQuery,
		"-F", "owner="+owner,
		"-F", "name="+name,
		"-F", "number="+strconv.Itoa(number),
	)
	cmd.Dir = repoPath

	out, err := cmd.Output()
	if err != nil {
		return nil, classifyGHError(err)
	}
	var resp ghCommentsResponse
	if err := json.Unmarshal(out, &resp); err != nil {
		return nil, fmt.Errorf("parse gh output: %w", err)
	}
	pr := resp.Data.Repository.PullRequest
	if pr.Number == 0 {
		return nil, ErrPRNotFound
	}

	fb := &PRFeedback{Number: pr.Number, FetchedAt: time.Now()}
	for _, c := range pr.Comments.Nodes {
		fb.Comments = append(fb.Comments, toComment(c))
	}
	for _, r := range pr.Reviews.Nodes {
		// A review with no body and no verdict is the empty envelope GitHub
		// creates around line comments; the thread carries the content.
		if strings.TrimSpace(r.Body) == "" && r.State == "COMMENTED" {
			continue
		}
		fb.Reviews = append(fb.Reviews, Review{
			Author: r.Author.Login, State: r.State, Body: r.Body,
			URL: r.URL, SubmittedAt: r.SubmittedAt,
		})
	}
	for _, t := range pr.ReviewThreads.Nodes {
		th := ReviewThread{Path: t.Path, Line: t.Line, Resolved: t.IsResolved, Outdated: t.IsOutdated}
		for _, c := range t.Comments.Nodes {
			th.Comments = append(th.Comments, toComment(c))
		}
		fb.Threads = append(fb.Threads, th)
	}
	return fb, nil
}

func toComment(c ghComment) Comment {
	return Comment{Author: c.Author.Login, Body: c.Body, URL: c.URL, CreatedAt: c.CreatedAt}
}

// RepoSlug returns the owner and repository name of repoPath's origin remote.
//
// It reads the local git remote rather than asking GitHub, because every API
// route to the same answer (`gh repo view`) spends quota from the bucket this
// package spends most of its effort conserving — and the remote URL is on disk.
func RepoSlug(repoPath string) (owner, name string, err error) {
	cmd := exec.CommandContext(context.Background(), "git", "-C", repoPath, "remote", "get-url", "origin")
	out, err := cmd.Output()
	if err != nil {
		return "", "", fmt.Errorf("no origin remote for %s: %w", repoPath, err)
	}
	return parseRepoSlug(strings.TrimSpace(string(out)))
}

// parseRepoSlug pulls owner/name out of the URL forms git remotes come in:
// git@host:owner/name.git, https://host/owner/name(.git), ssh://host/owner/name.
func parseRepoSlug(remote string) (owner, name string, err error) {
	s := strings.TrimSuffix(remote, ".git")
	if i := strings.LastIndex(s, ":"); i >= 0 && !strings.Contains(s[i:], "/") {
		// scp-style with no path separator after the colon: not a repo URL.
		return "", "", fmt.Errorf("unrecognized remote %q", remote)
	}
	// Strip the scheme and host: everything up to and including the last host
	// separator, which is ":" for scp-style and "/" for URL-style.
	if i := strings.Index(s, "://"); i >= 0 {
		s = s[i+3:]
		if j := strings.Index(s, "/"); j >= 0 {
			s = s[j+1:]
		}
	} else if i := strings.LastIndex(s, ":"); i >= 0 {
		s = s[i+1:]
	}
	parts := strings.Split(strings.Trim(s, "/"), "/")
	if len(parts) < 2 || parts[len(parts)-1] == "" || parts[len(parts)-2] == "" {
		return "", "", fmt.Errorf("unrecognized remote %q", remote)
	}
	return parts[len(parts)-2], parts[len(parts)-1], nil
}
