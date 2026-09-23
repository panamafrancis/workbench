package supatree

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/panamafrancis/workbench/pkg/config"
	"github.com/panamafrancis/workbench/pkg/git"
	"github.com/panamafrancis/workbench/pkg/github"
)

// ErrReviewTree is the reason every authoring command gives for refusing.
//
// One sentence, shared, and it names the alternative: the agent that hits this
// is mid-task and needs to know what to do instead, not merely that it may not.
var ErrReviewTree = errors.New(
	"the branches here belong to the pull requests' authors, so renaming, pushing or opening PRs from them is not ours to do. " +
		"To propose changes, branch from the head yourself")

// prURL matches a GitHub pull request URL, which is the form `gh` prints and
// people paste. Owner and repo are deliberately loose — GitHub's own rules have
// changed over time and a wrong reject here is worse than a wrong accept, which
// the lookup catches anyway.
//
// Anything after the number is ignored for the same reason: a URL copied from
// the browser carries the tab (`/files`), the query (`?w=1`) or the anchor
// (`#discussion_r…`) far more often than it is bare, and refusing those makes
// the commonest paste in the world an error the human has to hand-edit.
var prURL = regexp.MustCompile(`^https?://[^/]+/([^/]+/[^/]+)/pull/(\d+)(?:[/?#].*)?$`)

// ParsePRRef reads a pull request reference in either of the two forms people
// have: the URL, and owner/repo#number.
func ParsePRRef(s string) (repo string, number int, err error) {
	s = strings.TrimSpace(s)
	if m := prURL.FindStringSubmatch(s); m != nil {
		n, convErr := strconv.Atoi(m[2])
		if convErr != nil {
			return "", 0, fmt.Errorf("parse %q: %w", s, convErr)
		}
		return m[1], n, nil
	}
	if owner, num, ok := strings.Cut(s, "#"); ok && strings.Count(owner, "/") == 1 {
		n, convErr := strconv.Atoi(num)
		if convErr == nil && n > 0 {
			return owner, n, nil
		}
	}
	return "", 0, fmt.Errorf("%q is not a pull request: expected a URL like https://github.com/owner/repo/pull/123, or owner/repo#123", s)
}

// ghPRView is the subset of `gh pr view` we read.
type ghPRView struct {
	Number         int    `json:"number"`
	URL            string `json:"url"`
	HeadRefName    string `json:"headRefName"`
	HeadRefOid     string `json:"headRefOid"`
	BaseRefName    string `json:"baseRefName"`
	Title          string `json:"title"`
	HeadRepository struct {
		Name string `json:"name"`
	} `json:"headRepository"`
}

// ResolvePRRefs turns pull request references into the facts a review tree
// records. It is split from tree creation so everything downstream of `gh` can
// be tested without a network.
func ResolvePRRefs(refs []string) ([]ReviewRef, error) {
	out := make([]ReviewRef, 0, len(refs))
	seen := make(map[string]bool, len(refs))
	for _, raw := range refs {
		repo, number, err := ParsePRRef(raw)
		if err != nil {
			return nil, err
		}
		key := fmt.Sprintf("%s#%d", repo, number)
		if seen[key] {
			continue
		}
		seen[key] = true
		v, err := lookupPR(repo, number)
		if err != nil {
			return nil, err
		}
		out = append(out, ReviewRef{
			Repo:    repo,
			Number:  v.Number,
			URL:     v.URL,
			Head:    v.HeadRefOid,
			HeadRef: v.HeadRefName,
			Base:    v.BaseRefName,
		})
	}
	if len(out) == 0 {
		return nil, errors.New("no pull requests given")
	}
	return out, nil
}

func lookupPR(repo string, number int) (*ghPRView, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "gh", "pr", "view", strconv.Itoa(number),
		"--repo", repo,
		"--json", "number,url,headRefName,headRefOid,baseRefName,title,headRepository")
	out, err := cmd.Output()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) && len(ee.Stderr) > 0 {
			return nil, fmt.Errorf("gh pr view %s#%d: %s", repo, number, strings.TrimSpace(string(ee.Stderr)))
		}
		return nil, fmt.Errorf("gh pr view %s#%d: %w", repo, number, err)
	}
	var v ghPRView
	if err := json.Unmarshal(out, &v); err != nil {
		return nil, fmt.Errorf("parse gh output for %s#%d: %w", repo, number, err)
	}
	return &v, nil
}

// ReviewOptions parameterizes NewReview.
type ReviewOptions struct {
	Stack  string
	Name   string
	Model  string
	Intent string
	PRs    []ReviewRef
	Now    time.Time
}

// NewReview creates a review tree: a supatree whose members are checked out at
// foreign pull request heads and whose authoring commands are refused.
//
// Members are mapped to the stack by workbench alias. A pull request whose
// repository is not a member of the stack is an error naming both, rather than
// a tree silently missing the repo the reviewer asked for.
func NewReview(c *Config, wb *config.Config, opts ReviewOptions) (*Instance, *SyncReport, error) {
	if len(opts.PRs) == 0 {
		return nil, nil, errors.New("no pull requests given")
	}
	stack, err := resolveStack(c, opts.Stack)
	if err != nil {
		return nil, nil, err
	}

	byAlias, err := mapPRsToMembers(stack.Path, opts.PRs, wb)
	if err != nil {
		return nil, nil, err
	}

	inst, report, err := New(c, wb, CreateOptions{
		Stack:  opts.Stack,
		Name:   opts.Name,
		Model:  opts.Model,
		Now:    opts.Now,
		Review: byAlias,
		Intent: opts.Intent,
	})
	if err != nil {
		return nil, report, err
	}
	// Seed the PR cache from what we already know, so the tree shows its PRs
	// by number before the first round has run.
	seedPRCache(byAlias)
	return inst, report, nil
}

// mapPRsToMembers keys the review set by workbench alias, matching each pull
// request's repository against the stack's members.
func mapPRsToMembers(stackPath string, prs []ReviewRef, wb *config.Config) (map[string]ReviewRef, error) {
	spec, err := LoadSpec(stackPath)
	if err != nil {
		return nil, err
	}
	byAlias := make(map[string]ReviewRef, len(prs))
	for _, pr := range prs {
		alias, err := aliasForRepo(pr.Repo, spec.Members, wb)
		if err != nil {
			return nil, err
		}
		if prev, dup := byAlias[alias]; dup {
			return nil, fmt.Errorf("%s and %s are both in repo %q — a review tree holds one pull request per repo",
				prev.URL, pr.URL, alias)
		}
		byAlias[alias] = pr
	}
	return byAlias, nil
}

// aliasForRepo finds the stack member whose git remote is repo.
func aliasForRepo(repo string, members []string, wb *config.Config) (string, error) {
	for _, alias := range members {
		r, _ := wb.FindRepo(alias)
		if r == nil {
			continue
		}
		if remoteRepo(r.LocalPath) == strings.ToLower(repo) {
			return alias, nil
		}
	}
	return "", fmt.Errorf("no member of this stack is %s — add its repo to supatree.yml, or review it in a stack that has it (members: %s)",
		repo, strings.Join(members, ", "))
}

// remoteRepo returns the owner/name of a clone's origin, lowercased, or "".
func remoteRepo(localPath string) string {
	out, err := exec.CommandContext(context.Background(), "git", "-C", localPath,
		"remote", "get-url", "origin").Output()
	if err != nil {
		return ""
	}
	url := strings.TrimSpace(string(out))
	url = strings.TrimSuffix(url, ".git")
	if _, after, ok := strings.Cut(url, ":"); ok && !strings.HasPrefix(url, "http") {
		return strings.ToLower(after) // git@github.com:owner/name
	}
	parts := strings.Split(url, "/")
	if len(parts) < 2 {
		return ""
	}
	return strings.ToLower(strings.Join(parts[len(parts)-2:], "/"))
}

// seedPRCache writes what the review set already knows into the shared cache,
// so the first status round resolves by number instead of finding nothing.
func seedPRCache(byAlias map[string]ReviewRef) {
	_ = github.NewCache(PRCachePath()).Mutate(func(w *github.Writable) error {
		for _, ref := range byAlias {
			key := fmt.Sprintf("pr:%s#%d", ref.Repo, ref.Number)
			if w.Get(key) != nil {
				continue
			}
			// The number and URL, and deliberately no status: what the seed is
			// for is making the lookup resolvable, not reporting a state nobody
			// has fetched. Writable.Rename takes the same line when a branch is
			// renamed — it carries the number across and zeroes FetchedAt — and
			// an entry that was never verified is looked up for real by the
			// first round rather than confirmed by a poll.
			w.Set(key, &github.PRInfo{Number: ref.Number, URL: ref.URL, Status: github.PRNone})
		}
		return nil
	})
}

// RefreshResult reports what a refresh did to one member.
type RefreshResult struct {
	Alias string
	PR    string // owner/repo#number
	Was   string // the head recorded before
	Now   string // the head the PR has now
	Moved bool
	// Skipped says the worktree was left alone, and why. A member with
	// uncommitted work is never reset: review notes are not disposable, and the
	// reviewer can commit or discard them and run refresh again.
	Skipped string
}

// RefreshReview re-fetches every tracked pull request head and moves the member
// worktrees onto it.
//
// This exists because authors push during review. Without it you read a tree
// that quietly stops matching the pull request you are commenting on, and
// GitHub marks the inline comments outdated the moment they land.
func RefreshReview(c *Config, wb *config.Config, name string) ([]RefreshResult, error) {
	inst, err := Get(c, name)
	if err != nil {
		return nil, err
	}
	meta, err := LoadMeta(inst.Root)
	if err != nil {
		return nil, err
	}
	if !meta.Reviewing() {
		return nil, fmt.Errorf("%s is not a review tree", name)
	}

	var out []RefreshResult
	changed := false
	for _, m := range inst.Members {
		if m.Review == nil {
			continue
		}
		res := RefreshResult{Alias: m.Alias, PR: fmt.Sprintf("%s#%d", m.Review.Repo, m.Review.Number), Was: m.Review.Head}
		repo, _ := wb.FindRepo(m.Alias)
		if repo == nil || !m.Exists {
			res.Skipped = "worktree is not checked out — run sync"
			out = append(out, res)
			continue
		}
		sha, err := git.FetchRef(repo.LocalPath, fmt.Sprintf("refs/pull/%d/head", m.Review.Number))
		if err != nil {
			res.Skipped = err.Error()
			out = append(out, res)
			continue
		}
		res.Now = sha
		res.Moved = sha != m.Review.Head
		if !res.Moved {
			out = append(out, res)
			continue
		}
		if git.IsDirty(m.Path) {
			res.Skipped = "uncommitted changes here — commit or discard them, then refresh again"
			out = append(out, res)
			continue
		}
		if err := resetTo(m.Path, sha); err != nil {
			res.Skipped = err.Error()
			out = append(out, res)
			continue
		}
		ref := meta.Review[m.Alias]
		ref.Head = sha
		meta.Review[m.Alias] = ref
		changed = true
		out = append(out, res)
	}

	if changed {
		if err := meta.Save(inst.Root); err != nil {
			return out, fmt.Errorf("save meta: %w", err)
		}
		// The recorded heads are what "the author pushed" is measured against,
		// so a stale info.md would describe commits nobody is looking at.
		if updated, err := LoadInstance(inst.Root); err == nil {
			_ = WriteInfo(updated)
		}
	}
	return out, nil
}

// resetTo moves a clean worktree onto sha, keeping it on its own branch.
func resetTo(path, sha string) error {
	return runGit(path, "reset", "--hard", sha)
}

// ForkResult reports one member's conversion from review to authoring.
type ForkResult struct {
	Alias string
	From  string // the review branch it was on
	To    string // the authoring branch it is on now
	Base  string // the branch its pull request will target
}

// ForkReview converts a review tree into an authoring one.
//
// Reviews turn into patches often enough to be worth a command rather than a
// procedure. Each member keeps the commits it is sitting on and moves from
// review/<slug>/<alias> to st/<slug>/<alias>, and each pull request's head ref
// is recorded as the new base — so the eventual PR is opened *against the
// author's branch*, arriving as a proposal on their work rather than as a rival
// pull request against main.
//
// Like RenameBranchSlug this is phased: every branch is renamed first, and a
// failure part-way rolls the earlier ones back, because the mode in meta is what
// derives every member's branch name and a half-converted tree would strand
// members outside their own tree.
func ForkReview(c *Config, wb *config.Config, name string) ([]ForkResult, error) {
	inst, err := Get(c, name)
	if err != nil {
		return nil, err
	}
	meta, err := LoadMeta(inst.Root)
	if err != nil {
		return nil, err
	}
	if !meta.Reviewing() {
		return nil, fmt.Errorf("%s is not a review tree", name)
	}

	// The names the members will carry once the mode flips.
	authoring := &Meta{Slug: meta.Slug}

	var done []renamedMember
	var out []ForkResult
	for _, m := range inst.Members {
		if !m.Exists {
			continue
		}
		to := authoring.MemberBranch(m.Alias)
		if err := git.RenameBranch(m.Path, to); err != nil {
			return nil, undoRenames(fmt.Errorf("fork %s: %w", m.Alias, err), done)
		}
		done = append(done, renamedMember{alias: m.Alias, path: m.Path, oldBranch: m.Branch, newBranch: to})
		res := ForkResult{Alias: m.Alias, From: m.Branch, To: to}
		if m.Review != nil {
			res.Base = m.Review.HeadRef
		}
		out = append(out, res)
	}

	base := make(map[string]string, len(out))
	for _, r := range out {
		if r.Base != "" {
			base[r.Alias] = r.Base
		}
	}
	meta.Mode = ModeAuthoring
	meta.Base = base
	// Review is kept: it is where this work came from, and the PR numbers are
	// what a description should cross-link back to.
	if err := meta.Save(inst.Root); err != nil {
		return nil, undoRenames(fmt.Errorf("save meta: %w", err), done)
	}

	// The PR cache entries were keyed on the pull requests being reviewed. The
	// members now key on their own branches, which have no PR yet, so nothing
	// needs migrating — but info.md describes a tree that no longer exists.
	if updated, err := LoadInstance(inst.Root); err == nil {
		_ = WriteInfo(updated)
	}
	return out, nil
}
