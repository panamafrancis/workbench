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
				InputSchema: emptyObject(),
				Handler:     handleInfo,
			},
			{
				Name:        "sync",
				Description: "Reconcile member worktrees with supatree.yml after editing it (creates missing members). Set prune to also remove members no longer listed.",
				InputSchema: objectSchema(map[string]any{
					"prune": boolProp("Also remove member worktrees no longer listed in supatree.yml"),
				}, nil),
				Handler: handleSync,
			},
			{
				Name:        "rename_branches",
				Description: "Rename every member branch from st/<slug>/<alias> to st/<new_slug>/<alias>. Do this before creating PRs so branches have a meaningful name.",
				InputSchema: objectSchema(map[string]any{
					"new_slug": stringProp("New branch slug (lowercase alphanumeric and hyphens, max 40 chars)"),
					"push":     boolProp("Push the new branches and delete the old remote branches"),
				}, []string{"new_slug"}),
				Handler: handleRenameBranches,
			},
			{
				Name:        "create_pr",
				Description: "Push one member repo's branch and open a PR via gh. Refuses if the slug is still an auto-generated name (call rename_branches first) or if the repo's dependencies have no PRs yet (override with force).",
				InputSchema: objectSchema(map[string]any{
					"repo":  stringProp("Member repo alias"),
					"title": stringProp("PR title (omit to auto-fill from commits)"),
					"body":  stringProp("PR body"),
					"draft": boolProp("Create as draft"),
					"force": boolProp("Create even if dependencies have no PRs yet"),
				}, []string{"repo"}),
				Handler: handleCreatePR,
			},
			{
				Name:        "create_prs",
				Description: "Open PRs for every member with commits, in dependency order, cross-linking the sibling PRs. Refuses if the slug is still auto-generated.",
				InputSchema: objectSchema(map[string]any{
					"title": stringProp("PR title applied to every repo (omit to auto-fill)"),
					"body":  stringProp("PR body prepended to every repo's cross-link section"),
					"draft": boolProp("Create all as drafts"),
				}, nil),
				Handler: handleCreatePRs,
			},
			{
				Name:        "pr_status",
				Description: "Look up the PR status of every member branch and return an aggregate.",
				InputSchema: emptyObject(),
				Handler:     handlePRStatus,
			},
			{
				Name:        "docs",
				Description: "Supatree usage documentation.",
				InputSchema: emptyObject(),
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
		if missing := depsWithoutPRs(inst, m); len(missing) > 0 {
			return fmt.Sprintf("dependencies without PRs yet: %s (pass force=true to override)", strings.Join(missing, ", ")), true
		}
	}
	out, err := createOnePR(m.Path, args)
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
		out, err := createOnePR(m.Path, args)
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

func handlePRStatus(map[string]any) (string, bool) {
	_, _, inst, err := currentInstance()
	if err != nil {
		return err.Error(), true
	}
	cache := github.NewCache(PRCachePath())
	_ = cache.Load()
	insts := []*Instance{inst}

	// An agent asking for status wants a current answer, so force past the
	// staleness gate — but still go through the shared locked fetch, which skips
	// unpushed branches and honors a rate-limit backoff.
	note := ""
	if cache.InBackoff(time.Now()) {
		note = "\n(GitHub fetches are paused after a rate limit — this is cached status.)"
	} else if out := FetchPRs(FetchTargets(insts, cache, true, PRStaleAge), cache, true, PRStaleAge); out.Err != nil {
		note = fmt.Sprintf("\n(PR fetch incomplete: %v — some entries may be cached.)", out.Err)
	}

	sum := Status(insts, cache, StatusOptions{})
	if len(sum.Trees) == 0 {
		return "no members", false
	}
	t := sum.Trees[0]

	var b strings.Builder
	fmt.Fprintf(&b, "Supatree %s (slug st/%s) — %s\n", t.Name, t.Slug, t.State)
	for _, m := range t.Members {
		fmt.Fprintf(&b, "  %-20s %-10s", m.Alias, m.State)
		if m.PR != nil && m.PR.Number > 0 {
			fmt.Fprintf(&b, " #%d", m.PR.Number)
			if m.PR.Checks != github.CheckNone {
				fmt.Fprintf(&b, " checks:%s", m.PR.Checks)
			}
		}
		fmt.Fprintf(&b, " %s\n", m.Branch)
	}
	fmt.Fprintf(&b, "\nopen=%d approved=%d merged=%d total_prs=%d", t.OpenPRs, t.ApprovedPRs, t.MergedPRs, t.TotalPRs)
	if t.Blocked {
		b.WriteString("\nblocked: a PR has changes requested or failing checks")
	}
	b.WriteString(note)
	return b.String(), false
}

// depsWithoutPRs returns dependency aliases of m that have no open/merged PR.
func depsWithoutPRs(inst *Instance, m *Member) []string {
	var missing []string
	for _, dep := range m.DependsOn {
		dm := inst.FindMember(dep)
		if dm == nil {
			continue
		}
		info, err := github.LookupPR(dm.Path, dm.Branch)
		if err != nil || info == nil || info.Status == github.PRNone {
			missing = append(missing, dep)
		}
	}
	sort.Strings(missing)
	return missing
}

// createOnePR pushes HEAD and runs gh pr create in worktreePath.
func createOnePR(worktreePath string, args map[string]any) (string, error) {
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
	return strings.TrimSpace(string(out)), nil
}

// --- small JSON-schema helpers ---

func emptyObject() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{}}
}

func objectSchema(props map[string]any, required []string) map[string]any {
	s := map[string]any{"type": "object", "properties": props}
	if len(required) > 0 {
		s["required"] = required
	}
	return s
}

func stringProp(desc string) map[string]any {
	return map[string]any{"type": "string", "description": desc}
}
func boolProp(desc string) map[string]any {
	return map[string]any{"type": "boolean", "description": desc}
}

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
