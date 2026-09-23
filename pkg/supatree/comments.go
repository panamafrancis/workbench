package supatree

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/panamafrancis/workbench/pkg/config"
	"github.com/panamafrancis/workbench/pkg/github"
)

// CommentsCachePath holds fetched review feedback, beside the PR status cache
// and serialized by the same lock.
func CommentsCachePath() string {
	return filepath.Join(CacheDir(), "pr-comments.json")
}

// commentsEntry is one PR's feedback plus the PR timestamp it was valid for.
type commentsEntry struct {
	// PRUpdatedAt is the pull request's updatedAt at fetch time. GitHub moves it
	// on any activity, comments included, so it is an exact validity token: if
	// it has not moved, nothing has been said since, and a repeated ask is free.
	PRUpdatedAt time.Time         `json:"pr_updated_at"`
	Feedback    github.PRFeedback `json:"feedback"`
}

type commentsCache struct {
	Entries map[string]commentsEntry `json:"entries"`
}

func loadCommentsCache() commentsCache {
	c := commentsCache{Entries: map[string]commentsEntry{}}
	data, err := os.ReadFile(CommentsCachePath())
	if err != nil {
		return c
	}
	var on commentsCache
	if err := json.Unmarshal(data, &on); err != nil || on.Entries == nil {
		return c
	}
	return on
}

func (c commentsCache) save() error {
	return writeJSONAtomic(CommentsCachePath(), c)
}

// commentsKey identifies a PR's feedback. It carries the branch as well as the
// number because the PR cache is keyed on branch name alone and two member
// repos can share one — the same reasoning that makes ResolvePR check the URL.
func commentsKey(branch string, number int) string {
	return fmt.Sprintf("%s#%d", branch, number)
}

// ErrNoPR means the member has no pull request to have feedback on.
var ErrNoPR = fmt.Errorf("no pull request for this repo yet")

// Comments returns the review feedback on one member repo's pull request.
//
// It is the only path in supatree that spends a *second* GraphQL query shape
// per PR, so it is on-demand only — a human or an agent asked — and never runs
// on the watcher's tick. Repeated asks about an unchanged PR are served from
// the cache and cost nothing.
func Comments(inst *Instance, alias string, cache *github.Cache, force bool) (*github.PRFeedback, error) {
	var member *Member
	for i := range inst.Members {
		if inst.Members[i].Alias == alias {
			member = &inst.Members[i]
			break
		}
	}
	if member == nil {
		return nil, fmt.Errorf("%q is not a member of supatree %q", alias, inst.Name)
	}
	pr := cache.Get(member.CacheKey())
	if pr == nil || pr.Number == 0 {
		return nil, ErrNoPR
	}

	key := commentsKey(member.CacheKey(), pr.Number)
	cc := loadCommentsCache()
	if e, ok := cc.Entries[key]; ok && !force && e.PRUpdatedAt.Equal(pr.UpdatedAt) {
		fb := e.Feedback
		return &fb, nil
	}
	if cache.InBackoff(time.Now()) {
		return nil, fmt.Errorf("gh fetches are paused (rate limited)")
	}

	var fb *github.PRFeedback
	// The blocking lock, not the try-lock the pollers use: somebody explicitly
	// asked for this, so waiting out another process's round is right where
	// returning nothing would not be.
	err := config.WithFileLock(PRCacheLockPath(), func() error {
		var err error
		fb, err = github.PRComments(member.Path, pr.Number)
		if err != nil {
			if github.IsRateLimited(err) {
				// Arm the same persisted cooldown every other fetch path
				// observes, so one rate-limited comment fetch does not leave the
				// sidebars hammering away.
				cache.SetRetryAfter(time.Now().Add(RateLimitCooldown))
				_ = cache.Save()
			}
			return err
		}
		cc := loadCommentsCache()
		cc.Entries[key] = commentsEntry{PRUpdatedAt: pr.UpdatedAt, Feedback: *fb}
		return cc.save()
	})
	if err != nil {
		return nil, err
	}
	return fb, nil
}
