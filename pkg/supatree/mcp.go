package supatree

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
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
			// Two gates, because there are two kinds of caller. A tree-scoped
			// tool resolves "the current supatree" from the environment and is
			// meaningless without one; a PM tool spans every tree and is
			// meaningless inside one.
			if recallTools[name] {
				return ""
			}
			// Dual-context tools work for both: an agent inside a supatree acts
			// on its own, and the PM names one. Without this the PM could see
			// every tree and talk to none of them, which is most of the point of
			// having a PM.
			if dualTools[name] {
				if os.Getenv("SUPATREE") != "1" && os.Getenv("SUPATREE_PM") != "1" {
					return "Not inside a supatree session, and not the PM."
				}
				return ""
			}
			if pmTools[name] {
				if os.Getenv("SUPATREE_PM") != "1" {
					return "This tool is for the supatree PM agent (SUPATREE_PM env var not set). Run: supatree pm"
				}
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
				}, nil),
				Handler: handleSync,
			},
			{
				Name:        "rename_branches",
				Description: "Rename every member branch from st/<slug>/<alias> to st/<new_slug>/<alias>. Do this before creating PRs so branches have a meaningful name.",
				InputSchema: mcp.ObjectSchema(map[string]any{
					"new_slug": mcp.StringProp("New branch slug (lowercase alphanumeric and hyphens, max 40 chars)"),
					"push":     mcp.BoolProp("Push the new branches and delete the old remote branches"),
				}, []string{"new_slug"}),
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
				}, []string{"repo"}),
				Handler: handleCreatePR,
			},
			{
				Name:        "create_prs",
				Description: "Open PRs for every member with commits, in dependency order, cross-linking the sibling PRs. Refuses if the slug is still auto-generated.",
				InputSchema: mcp.ObjectSchema(map[string]any{
					"title": mcp.StringProp("PR title applied to every repo (omit to auto-fill)"),
					"body":  mcp.StringProp("PR body prepended to every repo's cross-link section"),
					"draft": mcp.BoolProp("Create all as drafts"),
				}, nil),
				Handler: handleCreatePRs,
			},
			{
				Name:        "pr_status",
				Description: "Look up the PR status of every member branch and return an aggregate.",
				InputSchema: mcp.EmptyObject(),
				Handler:     handlePRStatus,
			},
			{
				Name:        "agents",
				Description: "List the agents attached to a supatree: their names, models, bus addresses and whether each is reachable now. Use an address with your own message-bus tool to reach one directly.",
				InputSchema: mcp.ObjectSchema(map[string]any{
					argTree: mcp.StringProp("Supatree name (omit inside a supatree to mean your own)"),
				}, nil),
				Handler: handleAgents,
			},
			{
				Name:        "message_agent",
				Description: "Send a message to another agent in this supatree. Always delivered to the agent's mailbox, which it reads on its next turn; pair it with your own message-bus tool (see the address from `agents`) if you need it to arrive sooner.",
				InputSchema: mcp.ObjectSchema(map[string]any{
					"agent": mcp.StringProp("Agent name within the supatree (not its bus address)"),
					"text":  mcp.StringProp("What to tell it"),
					argTree: mcp.StringProp("Supatree name (omit inside a supatree to mean your own)"),
				}, []string{"agent", "text"}),
				Handler: handleMessageAgent,
			},
			{
				Name:        "inbox",
				Description: "Read and clear your own mailbox — messages other agents have left for you. Each message is returned once.",
				InputSchema: mcp.ObjectSchema(map[string]any{
					"peek": mcp.BoolProp("Read without clearing"),
				}, nil),
				Handler: handleInbox,
			},
			{
				Name:        "pr_comments",
				Description: "Read the review feedback on one member repo's PR: top-level comments, review verdicts, and line threads with their resolved state. Unresolved threads are what still needs an answer. Fetched on demand and cached until the PR changes.",
				InputSchema: mcp.ObjectSchema(map[string]any{
					"repo":  mcp.StringProp("Member repo alias"),
					"force": mcp.BoolProp("Re-fetch even if the cached copy is still valid"),
					argTree: mcp.StringProp("Supatree name (omit inside a supatree to mean your own)"),
				}, []string{"repo"}),
				Handler: handlePRComments,
			},
			{
				Name:        "requests",
				Description: "PM: read the queue of things asking for your attention, and mark them read. Call this first every turn — nothing interrupts you when a request arrives.",
				InputSchema: mcp.ObjectSchema(map[string]any{
					"peek": mcp.BoolProp("Read without marking them read"),
				}, nil),
				Handler: handleRequests,
			},
			{
				Name:        "list_trees",
				Description: "PM: every supatree and where it sits in the ship lifecycle. Reads a cache, so it costs no API quota and may be called freely.",
				InputSchema: mcp.ObjectSchema(map[string]any{
					"all": mcp.BoolProp("Include finished trees' members"),
				}, nil),
				Handler: handleListTrees,
			},
			{
				Name:        "events",
				Description: "PM: what has changed across every supatree recently — PRs opened, changes requested, checks failing, trees finished.",
				InputSchema: mcp.ObjectSchema(map[string]any{
					"hours": mcp.StringProp("How far back to look (default 24)"),
				}, nil),
				Handler: handleEvents,
			},
			{
				Name:        "notify",
				Description: "PM: tell the human something. You cannot reach the desktop from inside the sandbox; this queues it for the watcher, which applies the same tiering and deduping as its own notifications.",
				InputSchema: mcp.ObjectSchema(map[string]any{
					"text":   mcp.StringProp("What to say — one line"),
					argTree:  mcp.StringProp("Supatree it concerns, if any"),
					"urgent": mcp.BoolProp("Interrupt now rather than recording it quietly"),
				}, []string{"text"}),
				Handler: handleNotify,
			},
			{
				Name:        "board",
				Description: "PM: read or rewrite a supatree's status board (.supatree/board.md) — what each agent is on, what is blocked, what is waiting on the human. This, not your chat log, is where the human checks status.",
				InputSchema: mcp.ObjectSchema(map[string]any{
					argTree:    mcp.StringProp("Supatree name"),
					"markdown": mcp.StringProp("New board contents (omit to read the current one)"),
				}, []string{argTree}),
				Handler: handleBoard,
			},
			{
				Name:        "autonomy",
				Description: "PM: what you are permitted to do, for one supatree or overall. Check before acting unasked.",
				InputSchema: mcp.ObjectSchema(map[string]any{
					argTree: mcp.StringProp("Supatree name (omit for the workspace default)"),
				}, nil),
				Handler: handleAutonomy,
			},
			{
				Name:        "new_tree",
				Description: "PM: create a supatree from a stack. Needs autonomy 'auto' to do unasked, or `asked` when the human has asked you to. Returns the name; it does NOT open a tab — opening focuses it and takes the terminal away from whoever is using it.",
				InputSchema: mcp.ObjectSchema(map[string]any{
					"stack":  mcp.StringProp("Stack alias (omit if only one is registered)"),
					"name":   mcp.StringProp("Supatree name (omit to auto-generate)"),
					"intent": mcp.StringProp("What this supatree is for — the issue or task. Recorded, and worth filling in: the branch rename discards the generated name."),
					"asked":  mcp.BoolProp("The human asked for this in this turn. Set it only then — it is what distinguishes a request from your own initiative, and below autonomy 'auto' it is the difference between doing this and reporting that you could"),
				}, nil),
				Handler: handleNewTree,
			},
			{
				Name:        "remove_tree",
				Description: "PM: remove a finished supatree and its member worktrees. Needs autonomy 'auto' to do unasked, or `asked` when the human has asked you to; refuses a tree that is not done unless forced.",
				InputSchema: mcp.ObjectSchema(map[string]any{
					"tree":  mcp.StringProp("Supatree name"),
					"force": mcp.BoolProp("Remove even though it is not finished"),
					"asked": mcp.BoolProp("The human asked for this in this turn. Set it only then — it is what distinguishes a request from your own initiative, and below autonomy 'auto' it is the difference between doing this and reporting that you could"),
				}, []string{argTree}),
				Handler: handleRemoveTree,
			},
			{
				Name:        "history",
				Description: "PM: what has shipped — which supatrees touched which repos, how many PRs merged or closed, and when. Exact, derived from the ledger; it is not a search over notes.",
				InputSchema: mcp.ObjectSchema(map[string]any{
					"days": mcp.StringProp("How far back to look (default 30)"),
					"repo": mcp.StringProp("Only work that touched this member repo"),
				}, nil),
				Handler: handleHistory,
			},
			{
				Name:        "recall",
				Description: "Search this stack's durable notes. Use it for judgement and hard-won context — never for status: git, list_trees and history are authoritative for facts, and a memory store must not be asked what is true now.",
				InputSchema: mcp.ObjectSchema(map[string]any{
					"query": mcp.StringProp("What you are trying to remember"),
					"stack": mcp.StringProp("Stack alias (omit to use the current supatree's)"),
				}, []string{"query"}),
				Handler: handleRecall,
			},
			{
				Name:        "remember",
				Description: "Write a durable note into this stack's notes/ directory, in git. Record facts about the past — what a change cost, what review caught, what was tried and abandoned — never facts about where code lives, which go stale silently.",
				InputSchema: mcp.ObjectSchema(map[string]any{
					"slug":     mcp.StringProp("Short kebab-case file name, e.g. \"clicktracking-backfill\""),
					"markdown": mcp.StringProp("The note"),
					"stack":    mcp.StringProp("Stack alias (omit to use the current supatree's)"),
				}, []string{"slug", "markdown"}),
				Handler: handleRemember,
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
		missing, err := depsWithoutPRs(inst, m)
		if err != nil {
			return fmt.Sprintf("could not check whether dependencies have PRs: %v (pass force=true to skip the check)", err), true
		}
		if len(missing) > 0 {
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

// depsWithoutPRs returns dependency aliases of m that have no open/merged PR. A
// lookup that fails is reported as an error rather than counted as "no PR":
// that distinction is what the caller refuses on, and a rate-limited or
// unauthenticated gh would otherwise read as every dependency missing its PR.
func depsWithoutPRs(inst *Instance, m *Member) ([]string, error) {
	// The cache supplies known PR refs so a dependency whose PR merged under a
	// previous branch slug still resolves (see github.ResolvePR) instead of
	// reading as "no PR yet" and blocking the create.
	cache := github.NewCache(PRCachePath())
	_ = cache.Load()
	var missing []string
	for _, dep := range m.DependsOn {
		dm := inst.FindMember(dep)
		if dm == nil {
			continue
		}
		info, err := github.ResolvePR(dm.Path, dm.Branch, cache.Ref(dm.Branch))
		if err != nil {
			return nil, fmt.Errorf("%s: %w", dep, err)
		}
		if info == nil || info.Status == github.PRNone {
			missing = append(missing, dep)
		}
	}
	sort.Strings(missing)
	return missing, nil
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

const supatreeDocs = `Supatree — multi-repo worktrees for one issue.

You are running at the root of a supatree. Each ./repos/<alias>/ is an
independent git worktree; commit in each separately.

Workflow:
1. Read supatree_info for members, branches, and merge order.
2. Do the work, committing in each repo.
3. rename_branches with a meaningful slug (branches start as st/<city>/...).
4. create_prs to open all PRs in dependency order (or create_pr per repo).
5. pr_status to check PR states.
6. pr_comments to read what reviewers said — unresolved threads are what is
   still waiting on an answer.

Edit supatree.yml and call sync to add/remove member repos.`

func handlePRComments(args map[string]any) (string, bool) {
	alias, _ := args["repo"].(string)
	if strings.TrimSpace(alias) == "" {
		return "repo is required", true
	}
	force, _ := args["force"].(bool)

	inst, err := resolveTree(args)
	if err != nil {
		return err.Error(), true
	}
	cache := github.NewCache(PRCachePath())
	_ = cache.Load()

	fb, err := Comments(inst, alias, cache, force)
	if err != nil {
		return err.Error(), true
	}
	return FormatFeedback(alias, fb), false
}

// FormatFeedback renders review feedback for an agent or a human to act on.
// Unresolved threads lead, because they are the only part that is still a
// question; the rest is context.
func FormatFeedback(alias string, fb *github.PRFeedback) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s — PR #%d\n", alias, fb.Number)

	unresolved := fb.Unresolved()
	fmt.Fprintf(&b, "\nUnresolved threads (%d):\n", len(unresolved))
	if len(unresolved) == 0 {
		b.WriteString("  none\n")
	}
	for _, t := range unresolved {
		loc := t.Path
		if t.Line > 0 {
			loc = fmt.Sprintf("%s:%d", t.Path, t.Line)
		}
		fmt.Fprintf(&b, "  %s\n", loc)
		for _, c := range t.Comments {
			fmt.Fprintf(&b, "    @%s: %s\n", c.Author, indentBody(c.Body, "      "))
		}
	}

	if len(fb.Reviews) > 0 {
		b.WriteString("\nReviews:\n")
		for _, r := range fb.Reviews {
			fmt.Fprintf(&b, "  @%s %s", r.Author, r.State)
			if strings.TrimSpace(r.Body) != "" {
				fmt.Fprintf(&b, ": %s", indentBody(r.Body, "    "))
			}
			b.WriteString("\n")
		}
	}

	if len(fb.Comments) > 0 {
		b.WriteString("\nComments:\n")
		for _, c := range fb.Comments {
			fmt.Fprintf(&b, "  @%s: %s\n", c.Author, indentBody(c.Body, "    "))
		}
	}
	return b.String()
}

// indentBody re-indents a multi-line comment body so a long review does not
// break the outline the rest of the report is rendered as.
func indentBody(body, indent string) string {
	lines := strings.Split(strings.TrimSpace(body), "\n")
	for i := 1; i < len(lines); i++ {
		lines[i] = indent + lines[i]
	}
	return strings.Join(lines, "\n")
}

func handleAgents(args map[string]any) (string, bool) {
	inst, err := resolveTree(args)
	if err != nil {
		return err.Error(), true
	}
	agents, err := LoadAgents(inst.Root)
	if err != nil {
		return err.Error(), true
	}
	if len(agents) == 0 {
		return "no agents have been launched in this supatree yet", false
	}
	me := os.Getenv("SUPATREE_AGENT")

	var b strings.Builder
	fmt.Fprintf(&b, "Agents in %s:\n", inst.Name)
	for _, a := range agents {
		marker := " "
		if a.Name == me {
			marker = "*"
		}
		// Report reachability honestly rather than implying parity. An address
		// only exists for a model whose CLI has a message bus; everything else
		// is reachable by mailbox alone, which lands on the agent's next turn.
		reach := "mailbox (next turn)"
		if a.Address != "" {
			reach = "bus " + a.Address
		}
		pending := ""
		if msgs, err := Mail(inst.Root, a.Name); err == nil && len(msgs) > 0 {
			pending = fmt.Sprintf(" · %d unread", len(msgs))
		}
		fmt.Fprintf(&b, "%s %-16s %-10s %s%s\n", marker, a.Name, a.Model, reach, pending)
	}
	return b.String(), false
}

func handleMessageAgent(args map[string]any) (string, bool) {
	to, _ := args["agent"].(string)
	text, _ := args["text"].(string)
	if strings.TrimSpace(to) == "" || strings.TrimSpace(text) == "" {
		return "agent and text are both required", true
	}
	inst, err := resolveTree(args)
	if err != nil {
		return err.Error(), true
	}
	if deny := messagingDenied(inst); deny != "" {
		return deny, true
	}
	agents, err := LoadAgents(inst.Root)
	if err != nil {
		return err.Error(), true
	}
	target := FindAgent(agents, to)
	if target == nil {
		return fmt.Sprintf("no agent named %q in supatree %q — call `agents` to see them", to, inst.Name), true
	}
	from := os.Getenv("SUPATREE_AGENT")
	if from == "" {
		from = "unknown"
	}
	if err := Deliver(inst.Root, to, from, text); err != nil {
		return err.Error(), true
	}
	if target.Address != "" {
		return fmt.Sprintf("delivered to %s's mailbox. It is idle until its next turn — to reach it now, message %s on your session bus.", to, target.Address), false
	}
	return fmt.Sprintf("delivered to %s's mailbox; it will see this on its next turn.", to), false
}

func handleInbox(args map[string]any) (string, bool) {
	me := os.Getenv("SUPATREE_AGENT")
	if me == "" {
		return "cannot tell which agent you are (SUPATREE_AGENT unset)", true
	}
	root, err := mailboxRoot(args)
	if err != nil {
		return err.Error(), true
	}
	peek, _ := args["peek"].(bool)
	read := Drain
	if peek {
		read = Mail
	}
	msgs, err := read(root, me)
	if err != nil {
		return err.Error(), true
	}
	return FormatMail(msgs), false
}

// argTree is the tool-argument name for a supatree; goconst objects to the
// literal appearing in every PM tool's schema.
const argTree = "tree"

// pmTools are the cross-tree tools, gated on SUPATREE_PM rather than SUPATREE.
// A PM is rooted in no supatree, so the tree-scoped gate would lock it out of
// everything.
var pmTools = map[string]bool{
	"requests":    true,
	"list_trees":  true,
	"events":      true,
	"notify":      true,
	"board":       true,
	"autonomy":    true,
	"new_tree":    true,
	"remove_tree": true,
	"history":     true,
}

// recallTools work in both contexts: an agent inside a supatree recalls its own
// stack's notes, and the PM recalls any stack's. They are gated by neither
// env var, because memory that only one kind of caller can read is memory half
// the system cannot use.
var recallTools = map[string]bool{
	"recall":   true,
	"remember": true,
}

// dualTools act on one supatree but can be told which, so they serve an agent
// inside a tree and the PM outside every tree alike.
var dualTools = map[string]bool{
	"agents":        true,
	"message_agent": true,
	"inbox":         true,
	"pr_comments":   true,
}

func handleRequests(args map[string]any) (string, bool) {
	peek, _ := args["peek"].(bool)
	reqs, offset, err := PendingRequests()
	if err != nil {
		return err.Error(), true
	}
	if !peek && len(reqs) > 0 {
		// Commit only after reading them out: a PM that dies mid-turn should
		// re-read the backlog rather than lose it.
		if err := CommitRequests(offset); err != nil {
			return err.Error(), true
		}
	}
	return FormatRequests(reqs), false
}

func handleListTrees(args map[string]any) (string, bool) {
	all, _ := args["all"].(bool)
	cfg, err := Load()
	if err != nil {
		return err.Error(), true
	}
	insts, err := List(cfg)
	if err != nil {
		return err.Error(), true
	}
	cache := github.NewCache(PRCachePath())
	_ = cache.Load()
	sum := Status(insts, cache, StatusOptions{})
	if len(sum.Trees) == 0 {
		return "no supatrees", false
	}

	var b strings.Builder
	fmt.Fprintf(&b, "%d supatrees · %d open PRs · %d approved · %d stale · %d done\n",
		len(sum.Trees), sum.OpenPRs, sum.ApprovedPRs, sum.Stale, sum.Done)
	for _, t := range sum.Trees {
		fmt.Fprintf(&b, "\n%s (%s) — %s", t.Name, t.Stack, t.State)
		for _, flag := range []struct {
			on   bool
			name string
		}{{t.Blocked, "blocked"}, {t.Dirty, "dirty"}, {t.Stale, "stale"}} {
			if flag.on {
				fmt.Fprintf(&b, " [%s]", flag.name)
			}
		}
		b.WriteString("\n")
		if t.State == TreeDone && !all {
			continue
		}
		for _, m := range t.Members {
			fmt.Fprintf(&b, "  %-20s %-10s", m.Alias, m.State)
			if m.PR != nil && m.PR.Number > 0 {
				fmt.Fprintf(&b, " #%d", m.PR.Number)
			}
			b.WriteString("\n")
		}
	}
	return b.String(), false
}

func handleEvents(args map[string]any) (string, bool) {
	hours := 24.0
	if s, ok := args["hours"].(string); ok && s != "" {
		if v, err := strconv.ParseFloat(s, 64); err == nil && v > 0 {
			hours = v
		}
	}
	evs, err := ReadEvents(time.Now().Add(-time.Duration(hours * float64(time.Hour))))
	if err != nil {
		return err.Error(), true
	}
	if len(evs) == 0 {
		return fmt.Sprintf("nothing has changed in the last %gh", hours), false
	}
	var b strings.Builder
	for _, ev := range evs {
		fmt.Fprintf(&b, "%s  %-18s %s\n", ev.At.Format("2006-01-02 15:04"), ev.Kind, ev.Text)
	}
	return b.String(), false
}

func handleNotify(args map[string]any) (string, bool) {
	text, _ := args["text"].(string)
	if strings.TrimSpace(text) == "" {
		return "text is required", true
	}
	tree, _ := args[argTree].(string)
	urgent, _ := args["urgent"].(bool)

	// The outbox carries Events because the watcher's delivery path is tiered on
	// event kind. Borrowing two existing kinds keeps one tier table rather than
	// inventing a second vocabulary the notifier would have to learn.
	kind := EventApproved // board tier: recorded, does not interrupt
	if urgent {
		kind = EventChangesRequested
	}
	ev := Event{At: time.Now().UTC(), Kind: kind, Tree: tree, Text: text}
	if err := AppendNotification(ev); err != nil {
		return err.Error(), true
	}
	if urgent {
		return "queued — the watcher will deliver it, subject to its cooldown and to whether you are looking at that tree already", false
	}
	return "recorded quietly (not an interruption)", false
}

// pmTree resolves a named supatree for a PM tool, which — unlike every other
// tool here — is not running inside one.
func pmTree(name string) (*Config, *Instance, error) {
	cfg, err := Load()
	if err != nil {
		return nil, nil, err
	}
	inst, err := Get(cfg, name)
	if err != nil {
		return nil, nil, err
	}
	return cfg, inst, nil
}

func handleBoard(args map[string]any) (string, bool) {
	name, _ := args[argTree].(string)
	_, inst, err := pmTree(name)
	if err != nil {
		return err.Error(), true
	}
	markdown, writing := args["markdown"].(string)
	if !writing || strings.TrimSpace(markdown) == "" {
		// ReadBoard, not a raw read: it strips the generated-file header, which
		// is for whoever opens the file and is noise in a tool result.
		body := ReadBoard(inst.Root)
		if strings.TrimSpace(body) == "" {
			return "no board yet for " + inst.Name, false
		}
		return body, false
	}
	if err := WriteBoard(inst, markdown); err != nil {
		return err.Error(), true
	}
	return "board updated for " + inst.Name, false
}

// argAsked reads the `asked` flag the mutating tools carry: the PM's assertion
// that this action is one the human asked for in this turn, not its own idea.
func argAsked(args map[string]any) bool {
	asked, _ := args["asked"].(bool)
	return asked
}

func handleAutonomy(args map[string]any) (string, bool) {
	name, _ := args[argTree].(string)
	cfg, err := Load()
	if err != nil {
		return err.Error(), true
	}
	var meta *Meta
	scope := "workspace default"
	if name != "" {
		inst, err := Get(cfg, name)
		if err != nil {
			return err.Error(), true
		}
		if meta, err = LoadMeta(inst.Root); err != nil {
			return err.Error(), true
		}
		scope = inst.Name
	}
	p := cfg.Resolve(meta)

	var b strings.Builder
	fmt.Fprintf(&b, "%s: autonomy %s\n", scope, p.Level)
	fmt.Fprintf(&b, "  message agents:        %v\n", p.AllowsMessaging())
	fmt.Fprintf(&b, "  create / remove / push unasked: %v\n", p.AllowsMutation())
	fmt.Fprintf(&b, "  ... when the human asks:        %v\n", p.AsAsked(true).AllowsMutation())
	fmt.Fprintf(&b, "  outward-facing (anything a third party sees): %v\n", p.Outward)
	return b.String(), false
}

func handleNewTree(args map[string]any) (string, bool) {
	cfg, err := Load()
	if err != nil {
		return err.Error(), true
	}
	wb, err := config.Load()
	if err != nil {
		return err.Error(), true
	}
	// No tree exists yet to carry a level, which is exactly why the workspace
	// default has to exist: otherwise the most dangerous verb here would be the
	// one verb the permission model did not cover.
	if p := cfg.Resolve(nil).AsAsked(argAsked(args)); !p.AllowsMutation() {
		return p.Deny("creating a supatree"), true
	}
	stack, _ := args["stack"].(string)
	name, _ := args["name"].(string)
	intent, _ := args["intent"].(string)

	inst, _, err := New(cfg, wb, CreateOptions{Stack: stack, Name: name})
	if err != nil {
		return err.Error(), true
	}
	if intent != "" {
		if meta, err := LoadMeta(inst.Root); err == nil {
			meta.Intent = intent
			if err := meta.Save(inst.Root); err == nil {
				// New() generated info.md before the intent existed, so without
				// this the one thing the agent will actually read three weeks
				// later is the one place the intent is missing.
				_ = WriteInfo(inst)
			}
		}
	}
	return fmt.Sprintf("created supatree %q (%d members). It has no tab: tell the human to press enter on it in the sidebar.",
		inst.Name, len(inst.Members)), false
}

func handleRemoveTree(args map[string]any) (string, bool) {
	name, _ := args[argTree].(string)
	cfg, inst, err := pmTree(name)
	if err != nil {
		return err.Error(), true
	}
	meta, err := LoadMeta(inst.Root)
	if err != nil {
		return err.Error(), true
	}
	if p := cfg.Resolve(meta).AsAsked(argAsked(args)); !p.AllowsMutation() {
		return p.Deny("removing a supatree"), true
	}
	force, _ := args["force"].(bool)
	if !force {
		cache := github.NewCache(PRCachePath())
		_ = cache.Load()
		sum := Status([]*Instance{inst}, cache, StatusOptions{})
		if len(sum.Trees) > 0 && sum.Trees[0].State != TreeDone {
			return fmt.Sprintf("%s is %s, not done — removing it would throw away unmerged work. Pass force only if the human asked.",
				inst.Name, sum.Trees[0].State), true
		}
	}
	wb, err := config.Load()
	if err != nil {
		return err.Error(), true
	}
	res, err := Remove(cfg, wb, inst.Name, RemoveOptions{Force: force})
	if err != nil {
		return err.Error(), true
	}
	out := "removed supatree " + inst.Name
	if len(res.Warnings) > 0 {
		out += "\nwarnings: " + strings.Join(res.Warnings, "; ")
	}
	return out, false
}

func handleHistory(args map[string]any) (string, bool) {
	days := 30.0
	if s, ok := args["days"].(string); ok && s != "" {
		if v, err := strconv.ParseFloat(s, 64); err == nil && v > 0 {
			days = v
		}
	}
	repo, _ := args["repo"].(string)
	entries, err := History(time.Now().AddDate(0, 0, -int(days)), repo)
	if err != nil {
		return err.Error(), true
	}
	if len(entries) == 0 {
		return fmt.Sprintf("nothing recorded in the last %gd", days), false
	}
	var b strings.Builder
	for _, e := range entries {
		fmt.Fprintf(&b, "%-20s %s → %s", e.Tree, e.First.Format("2006-01-02"), e.Last.Format("2006-01-02"))
		if len(e.Repos) > 0 {
			fmt.Fprintf(&b, "  repos: %s", strings.Join(e.Repos, ","))
		}
		if e.Merged > 0 || e.Closed > 0 {
			fmt.Fprintf(&b, "  %d merged, %d closed", e.Merged, e.Closed)
		}
		b.WriteString("\n")
	}
	return b.String(), false
}

// stackPathFor resolves which stack's notes a memory tool acts on: an explicit
// alias, else the stack of the supatree the caller is running in.
func stackPathFor(alias string) (string, error) {
	cfg, err := Load()
	if err != nil {
		return "", err
	}
	if alias == "" {
		_, _, inst, err := currentInstance()
		if err != nil {
			return "", fmt.Errorf("name a stack: %w", err)
		}
		alias = inst.Stack
	}
	stack := cfg.FindStack(alias)
	if stack == nil {
		return "", fmt.Errorf("no stack %q is registered", alias)
	}
	return stack.Path, nil
}

func handleRecall(args map[string]any) (string, bool) {
	query, _ := args["query"].(string)
	alias, _ := args["stack"].(string)
	path, err := stackPathFor(alias)
	if err != nil {
		return err.Error(), true
	}
	out, err := Recall(path, query)
	if err != nil {
		return err.Error(), true
	}
	return out, false
}

func handleRemember(args map[string]any) (string, bool) {
	slug, _ := args["slug"].(string)
	markdown, _ := args["markdown"].(string)
	alias, _ := args["stack"].(string)
	if strings.TrimSpace(slug) == "" || strings.TrimSpace(markdown) == "" {
		return "slug and markdown are both required", true
	}
	if err := git.ValidateName(slug, nil); err != nil {
		return fmt.Sprintf("invalid slug %q: %v", slug, err), true
	}
	path, err := stackPathFor(alias)
	if err != nil {
		return err.Error(), true
	}
	if err := ScaffoldNotes(path); err != nil {
		return err.Error(), true
	}
	note := filepath.Join(NotesDir(path), slug+".md")
	if err := os.WriteFile(note, []byte(strings.TrimSpace(markdown)+"\n"), 0644); err != nil {
		return err.Error(), true
	}
	return fmt.Sprintf("wrote %s/%s.md in the %s stack. It is untracked until someone commits it — which is the point: memory arrives as a diff.",
		NotesDirName, slug, alias), false
}

// resolveTree resolves the supatree a dual-context tool acts on: the one named
// in the arguments, else the one the caller is running inside.
//
// The PM is rooted in no supatree, so it always names one; an agent inside a
// tree may omit it and mean its own.
func resolveTree(args map[string]any) (*Instance, error) {
	if name, _ := args[argTree].(string); strings.TrimSpace(name) != "" {
		_, inst, err := pmTree(name)
		return inst, err
	}
	_, _, inst, err := currentInstance()
	if err != nil {
		if os.Getenv("SUPATREE_PM") == "1" {
			return nil, fmt.Errorf("name a supatree with `tree` — you are the PM and belong to none")
		}
		return nil, err
	}
	return inst, nil
}

// mailboxRoot is where the caller's own mail lives. The PM has no supatree, so
// its mailbox hangs off its home instead of a tree's state directory.
func mailboxRoot(args map[string]any) (string, error) {
	if os.Getenv("SUPATREE_PM") == "1" {
		if name, _ := args[argTree].(string); strings.TrimSpace(name) == "" {
			return PMDir(), nil
		}
	}
	inst, err := resolveTree(args)
	if err != nil {
		return "", err
	}
	return inst.Root, nil
}

// messagingDenied reports why the PM may not nudge agents in this supatree, or
// "" when it may. It is what makes the `off` autonomy level mean something:
// before this, the level was printed by `autonomy` and enforced nowhere.
//
// It applies only to the PM. An agent already inside a supatree messaging its
// own siblings is ordinary collaboration, not autonomous action.
func messagingDenied(inst *Instance) string {
	if os.Getenv("SUPATREE_PM") != "1" {
		return ""
	}
	cfg, err := Load()
	if err != nil {
		return ""
	}
	meta, err := LoadMeta(inst.Root)
	if err != nil {
		meta = nil
	}
	if p := cfg.Resolve(meta); !p.AllowsMessaging() {
		return p.Deny("messaging an agent")
	}
	return ""
}
