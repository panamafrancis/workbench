package supatree

import (
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"
	"time"

	"github.com/panamafrancis/workbench/pkg/config"
	"github.com/panamafrancis/workbench/pkg/git"
	"github.com/panamafrancis/workbench/pkg/github"
	"github.com/panamafrancis/workbench/pkg/mcp"
)

// MCPServer builds the supatree MCP server. All tools except docs and
// supatree_info require SUPATREE=1 (set only inside a supatree agent pane).
func MCPServer(version string) *mcp.Server {
	return &mcp.Server{
		Name:    "supatree",
		Version: version,
		Gate: func(name string) string {
			if name == "docs" || name == "supatree_info" {
				return ""
			}
			if os.Getenv("SUPATREE") != "1" {
				return "Not inside a supatree session (SUPATREE env var not set)."
			}
			return ""
		},
		Tools: []mcp.Tool{
			{
				Name:        "supatree_info",
				Description: "Show the current supatree: member repos, branches, dependency edges, and merge order.",
				InputSchema: mcp.EmptyObject(),
				Handler:     handleInfo,
			},
			{
				Name:        "sync",
				Description: "Reconcile member worktrees with supatree.yml after editing it (creates missing members). Set prune to also remove members no longer listed.",
				InputSchema: mcp.ObjectSchema(map[string]any{
					"prune": mcp.BoolProp("Also remove member worktrees no longer listed in supatree.yml"),
				}),
				Handler: handleSync,
			},
			{
				Name:        "rename_branches",
				Description: "Rename every member branch from st/<slug>/<alias> to st/<new_slug>/<alias>. Do this before creating PRs so branches have a meaningful name.",
				InputSchema: mcp.ObjectSchema(map[string]any{
					"new_slug": mcp.StringProp("New branch slug (lowercase alphanumeric and hyphens, max 40 chars)"),
					"push":     mcp.BoolProp("Push the new branches and delete the old remote branches"),
				}, "new_slug"),
				Handler: handleRenameBranches,
			},
			{
				Name:        "create_pr",
				Description: "Push one member repo's branch and open a PR via gh. Refuses if the slug is still an auto-generated name (call rename_branches first) or if the repo's dependencies have no PRs yet (override with force).",
				InputSchema: mcp.ObjectSchema(map[string]any{
					"repo":  mcp.StringProp("Member repo alias"),
					"title": mcp.StringProp("PR title (omit to auto-fill from commits)"),
					"body":  mcp.StringProp("PR body"),
					"draft": mcp.BoolProp("Create as draft"),
					"force": mcp.BoolProp("Create even if dependencies have no PRs yet"),
				}, "repo"),
				Handler: handleCreatePR,
			},
			{
				Name:        "create_prs",
				Description: "Open PRs for every member with commits, in dependency order, cross-linking the sibling PRs. Refuses if the slug is still auto-generated.",
				InputSchema: mcp.ObjectSchema(map[string]any{
					"title": mcp.StringProp("PR title applied to every repo (omit to auto-fill)"),
					"body":  mcp.StringProp("PR body prepended to every repo's cross-link section"),
					"draft": mcp.BoolProp("Create all as drafts"),
				}),
				Handler: handleCreatePRs,
			},
			{
				Name:        "pr_status",
				Description: "Look up the PR status of every member branch and return an aggregate.",
				InputSchema: mcp.EmptyObject(),
				Handler:     handlePRStatus,
			},
			{
				Name:        "docs",
				Description: "Supatree usage documentation.",
				InputSchema: mcp.EmptyObject(),
				Handler:     func(map[string]any) (string, bool) { return supatreeDocs, false },
			},
		},
	}
}

// currentInstance resolves the supatree the MCP server is running inside, from
// SUPATREE_ROOT (preferred) or SUPATREE_NAME via the registry.
func currentInstance() (*Config, *config.Config, *Instance, error) {
	c, err := Load()
	if err != nil {
		return nil, nil, nil, err
	}
	wb, err := config.Load()
	if err != nil {
		return nil, nil, nil, err
	}
	if root := os.Getenv("SUPATREE_ROOT"); root != "" {
		inst, err := LoadInstance(root)
		return c, wb, inst, err
	}
	if name := os.Getenv("SUPATREE_NAME"); name != "" {
		inst, err := Get(c, name)
		return c, wb, inst, err
	}
	return nil, nil, nil, fmt.Errorf("cannot determine current supatree (SUPATREE_ROOT/SUPATREE_NAME unset)")
}

func handleInfo(map[string]any) (string, bool) {
	_, _, inst, err := currentInstance()
	if err != nil {
		return err.Error(), true
	}
	data, err := os.ReadFile(InfoPath(inst.Root))
	if err == nil {
		return string(data), false
	}
	// Fall back to a live summary if info.md is missing.
	var b strings.Builder
	fmt.Fprintf(&b, "Supatree %s (slug st/%s)\n", inst.Name, inst.Slug)
	for _, m := range inst.Members {
		fmt.Fprintf(&b, "  %s\t%s\tdeps: %s\n", m.Alias, m.Branch, strings.Join(m.DependsOn, ","))
	}
	return b.String(), false
}

func handleSync(args map[string]any) (string, bool) {
	c, wb, inst, err := currentInstance()
	if err != nil {
		return err.Error(), true
	}
	_ = c
	prune, _ := args["prune"].(bool)
	report, err := Sync(inst.Root, wb, prune)
	if err != nil {
		return err.Error(), true
	}
	var b strings.Builder
	for _, a := range report.Created {
		fmt.Fprintf(&b, "+ %s\n", a)
	}
	for _, a := range report.Pruned {
		fmt.Fprintf(&b, "- %s\n", a)
	}
	for _, w := range report.Warnings {
		fmt.Fprintf(&b, "warning: %s\n", w)
	}
	if b.Len() == 0 {
		return "already in sync", false
	}
	return b.String(), false
}

func handleRenameBranches(args map[string]any) (string, bool) {
	c, wb, inst, err := currentInstance()
	if err != nil {
		return err.Error(), true
	}
	newSlug, _ := args["new_slug"].(string)
	if newSlug == "" {
		return "new_slug is required", true
	}
	push, _ := args["push"].(bool)
	if err := RenameBranchSlug(c, wb, inst.Name, newSlug, push); err != nil {
		return err.Error(), true
	}
	return fmt.Sprintf("renamed member branches to st/%s/<alias>", newSlug), false
}

func handleCreatePR(args map[string]any) (string, bool) {
	_, _, inst, err := currentInstance()
	if err != nil {
		return err.Error(), true
	}
	if git.IsCityName(inst.Slug) {
		return fmt.Sprintf("Branch slug is still auto-generated (st/%s). Call rename_branches first.", inst.Slug), true
	}
	alias, _ := args["repo"].(string)
	m := inst.FindMember(alias)
	if m == nil || !m.Exists {
		return fmt.Sprintf("repo %q is not a created member of this supatree", alias), true
	}
	force, _ := args["force"].(bool)
	if !force {
		missing, depErr := depsWithoutPRs(inst, m)
		if depErr != nil {
			return fmt.Sprintf("could not verify dependency PRs: %v (pass force=true to skip the check)", depErr), true
		}
		if len(missing) > 0 {
			return fmt.Sprintf("dependencies without PRs yet: %s (pass force=true to override)", strings.Join(missing, ", ")), true
		}
	}
	out, err := createOnePR(m.Path, m.Branch, args)
	if err != nil {
		return out, true
	}
	return out, false
}

func handleCreatePRs(args map[string]any) (string, bool) {
	_, _, inst, err := currentInstance()
	if err != nil {
		return err.Error(), true
	}
	if git.IsCityName(inst.Slug) {
		return fmt.Sprintf("Branch slug is still auto-generated (st/%s). Call rename_branches first.", inst.Slug), true
	}
	var b strings.Builder
	created := 0
	for _, m := range inst.Members {
		if !m.Exists {
			continue
		}
		ahead, _ := git.CommitsAhead(m.Path)
		if ahead == 0 {
			fmt.Fprintf(&b, "%s: skipped (no commits ahead)\n", m.Alias)
			continue
		}
		out, err := createOnePR(m.Path, m.Branch, args)
		if err != nil {
			fmt.Fprintf(&b, "%s: ERROR %s\n", m.Alias, strings.TrimSpace(out))
			continue
		}
		created++
		fmt.Fprintf(&b, "%s: %s\n", m.Alias, strings.TrimSpace(out))
	}
	fmt.Fprintf(&b, "\ncreated %d PR(s) in order: %s", created, strings.Join(inst.MemberAliases(), " → "))
	return b.String(), false
}

const (
	// prStatusMaxAge bounds how stale a cached status may be before an explicit
	// pr_status call re-fetches it. Short, because the tool is interactive — but
	// non-zero, so an agent calling it repeatedly in one turn (or right after
	// create_prs) reads the cache the sidebars already filled instead of
	// re-asking GitHub for every member.
	prStatusMaxAge = 2 * time.Minute
)

// syncMembers brings the cache up to date for the given member branches using
// the same path the sidebars use: one conditional poll per repo, free when
// nothing changed, with a per-branch REST lookup only for what a poll cannot
// settle. Going through github.Sync is what keeps an agent's tool call from
// costing a GraphQL request per member — and what makes it observe (and arm)
// the same shared cooldown every sidebar observes.
func syncMembers(cache *github.Cache, members []*Member) (github.SyncReport, error) {
	targets := make([]github.Target, 0, len(members))
	for _, m := range members {
		if m.Exists {
			targets = append(targets, github.Target{RepoPath: m.Path, Branch: m.Branch})
		}
	}
	var report github.SyncReport
	if len(targets) == 0 {
		return report, nil
	}
	err := cache.Mutate(func(w *github.Writable) error {
		report = github.Sync(w, targets, github.SyncOptions{
			MaxAge:     prStatusMaxAge,
			MaxLookups: len(targets),
		})
		return nil
	})
	return report, err
}

func handlePRStatus(map[string]any) (string, bool) {
	_, _, inst, err := currentInstance()
	if err != nil {
		return err.Error(), true
	}
	cache := github.NewCache(PRCachePath())
	_ = cache.Load()

	members := make([]*Member, 0, len(inst.Members))
	for i := range inst.Members {
		members = append(members, &inst.Members[i])
	}
	report, syncErr := syncMembers(cache, members)
	if syncErr != nil {
		return syncErr.Error(), true
	}

	var b strings.Builder
	counts := map[github.PRStatus]int{}
	for _, m := range inst.Members {
		info := cache.Get(m.Branch)
		switch {
		case info == nil:
			// Absence of an entry is not absence of a PR — say so rather than
			// reporting a state we never established.
			fmt.Fprintf(&b, "%s: unknown (not fetched yet)\n", m.Alias)
		case info.Status == github.PRNone:
			counts[info.Status]++
			fmt.Fprintf(&b, "%s: no PR\n", m.Alias)
		default:
			counts[info.Status]++
			fmt.Fprintf(&b, "%s: %s #%d\n", m.Alias, info.Status, info.Number)
		}
	}
	fmt.Fprintf(&b, "\nopen=%d draft=%d merged=%d", counts[github.PROpen], counts[github.PRDraft], counts[github.PRMerged])
	switch {
	case report.Paused:
		fmt.Fprintf(&b, "\n(cached values: gh fetches paused until %s)", cache.RetryAfter().Format(time.Kitchen))
	case report.Err != nil:
		fmt.Fprintf(&b, "\n(some values may be stale: %v)", report.Err)
	}
	return b.String(), false
}

// depsWithoutPRs returns dependency aliases of m that have no open/merged PR.
// A failed lookup is reported as an error rather than folded into the missing
// list: treating a rate-limited or unauthenticated gh as "this dependency has
// no PR" blocks stacking on a fact that was never established.
func depsWithoutPRs(inst *Instance, m *Member) ([]string, error) {
	deps := make([]*Member, 0, len(m.DependsOn))
	for _, alias := range m.DependsOn {
		if dm := inst.FindMember(alias); dm != nil {
			deps = append(deps, dm)
		}
	}
	if len(deps) == 0 {
		return nil, nil
	}

	cache := github.NewCache(PRCachePath())
	_ = cache.Load()
	report, err := syncMembers(cache, deps)
	if err != nil {
		return nil, err
	}
	if report.Paused {
		return nil, fmt.Errorf("gh fetches are paused until %s after a rate limit",
			cache.RetryAfter().Format(time.Kitchen))
	}

	var missing []string
	for _, dm := range deps {
		info := cache.Get(dm.Branch)
		if info == nil {
			// No answer at all — say so rather than calling it "no PR". Sync's
			// own error, if it had one, explains why.
			if report.Err != nil {
				return nil, fmt.Errorf("%s: %w", dm.Alias, report.Err)
			}
			return nil, fmt.Errorf("%s: could not determine PR status", dm.Alias)
		}
		if info.Status == github.PRNone {
			missing = append(missing, dm.Alias)
		}
	}
	sort.Strings(missing)
	return missing, nil
}

// createOnePR pushes HEAD and runs gh pr create in worktreePath, caching the
// PR it just created for branch.
func createOnePR(worktreePath, branch string, args map[string]any) (string, error) {
	pushCtx, pushCancel := mcp.ToolContext()
	defer pushCancel()
	if out, err := exec.CommandContext(pushCtx, "git", "-C", worktreePath, "push", "-u", "origin", "HEAD").CombinedOutput(); err != nil {
		return fmt.Sprintf("git push failed: %s", strings.TrimSpace(string(out))), err
	}

	ghArgs := []string{"pr", "create"}
	if title, ok := args["title"].(string); ok && title != "" {
		ghArgs = append(ghArgs, "--title", title)
	} else {
		ghArgs = append(ghArgs, "--fill")
	}
	if body, ok := args["body"].(string); ok && body != "" {
		ghArgs = append(ghArgs, "--body", body)
	}
	if draft, ok := args["draft"].(bool); ok && draft {
		ghArgs = append(ghArgs, "--draft")
	}
	ghCtx, ghCancel := mcp.ToolContext()
	defer ghCancel()
	cmd := exec.CommandContext(ghCtx, "gh", ghArgs...)
	cmd.Dir = worktreePath
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Sprintf("gh pr create failed: %s", strings.TrimSpace(string(out))), err
	}
	text := strings.TrimSpace(string(out))
	// We know this PR exists without asking anyone: cache it now so the sidebar
	// shows it immediately instead of on the next poll.
	draft, _ := args["draft"].(bool)
	github.RecordCreatedPR(PRCachePath(), branch, text, draft)
	return text, nil
}

// --- small JSON-schema helpers ---

const supatreeDocs = `Supatree — multi-repo worktrees for one issue.

You are running at the root of a supatree. Each ./repos/<alias>/ is an
independent git worktree; commit in each separately.

Workflow:
1. Read supatree_info for members, branches, and merge order.
2. Do the work, committing in each repo.
3. rename_branches with a meaningful slug (branches start as st/<city>/...).
4. create_prs to open all PRs in dependency order (or create_pr per repo).
5. pr_status to check PR states.

Edit supatree.yml and call sync to add/remove member repos.`
