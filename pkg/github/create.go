package github

import (
	"regexp"
	"strconv"
	"time"
)

// prURLPattern matches the pull request URL `gh pr create` prints on success.
var prURLPattern = regexp.MustCompile(`https://github\.com/[^\s/]+/[^\s/]+/pull/(\d+)`)

// ParseCreatedPR reads the PR `gh pr create` just made out of its output, so
// the caller can write it straight into the cache.
//
// This is the one moment when a PR's existence is known for free, and also the
// moment the user is most likely to be staring at the sidebar waiting for the
// badge to appear. Without it the new PR shows up only on the next poll, which
// invites exactly the impatient manual refreshing this whole design is trying
// to make unnecessary.
func ParseCreatedPR(ghOutput string, draft bool) (*PRInfo, bool) {
	match := prURLPattern.FindStringSubmatch(ghOutput)
	if match == nil {
		return nil, false
	}
	number, err := strconv.Atoi(match[1])
	if err != nil || number == 0 {
		return nil, false
	}
	now := time.Now()
	status := PROpen
	if draft {
		status = PRDraft
	}
	return &PRInfo{
		Number:    number,
		Status:    status,
		URL:       match[0],
		UpdatedAt: now,
		FetchedAt: now,
	}, true
}

// RecordCreatedPR caches a PR parsed from `gh pr create` output. A failure to
// parse or write is not worth surfacing: the PR exists either way, and the next
// poll will find it.
func RecordCreatedPR(cachePath, branch, ghOutput string, draft bool) {
	info, ok := ParseCreatedPR(ghOutput, draft)
	if !ok {
		return
	}
	_ = NewCache(cachePath).Mutate(func(w *Writable) error {
		w.Set(branch, info)
		return nil
	})
}
