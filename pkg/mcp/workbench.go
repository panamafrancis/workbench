package mcp

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/panamafrancis/workbench/pkg/docs"
	"github.com/panamafrancis/workbench/pkg/git"
)

// Run starts the workbench MCP server on stdio.
func Run(version string) error {
	return WorkbenchServer(version).Run()
}

// WorkbenchServer builds the workbench tool set. rename_branch and create_pr
// require WORKBENCH=1 (set only inside a workbench-opened session); docs is
// always available.
func WorkbenchServer(version string) *Server {
	return &Server{
		Name:    "workbench",
		Version: version,
		Gate: func(name string) string {
			if name == "docs" {
				return ""
			}
			if os.Getenv("SUPATREE") == "1" {
				return "You're inside a supatree, not a plain workbench worktree. " +
					"Use the supatree MCP tools instead: create_prs (all members, dependency-ordered) " +
					"or create_pr (one member), and rename_branches for the st/<slug>/<alias> branches. " +
					"If those tools aren't listed, run `supatree init` to register the supatree MCP server."
			}
			if os.Getenv("WORKBENCH") != "1" {
				return "Not inside a workbench session (WORKBENCH env var not set)."
			}
			return ""
		},
		Tools: []Tool{
			{
				Name:        "rename_branch",
				Description: "Rename the current worktree's branch and update workbench config + PR cache. Use this instead of bare git branch -m. Only works inside a workbench session.",
				InputSchema: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"new_name": map[string]any{
							"type":        "string",
							"description": "New branch name — keep the wt/<alias>/ prefix (e.g. wt/wb/session-launcher). The final segment is lowercase alphanumeric and hyphens, max 40 chars.",
						},
						"push": map[string]any{
							"type":        "boolean",
							"description": "Push new branch and delete old remote branch",
						},
					},
					"required": []string{"new_name"},
				},
				Handler: handleRenameBranch,
			},
			{
				Name:        "create_pr",
				Description: "Push the current branch and create a pull request via gh. Refuses if the branch still has an auto-generated name — call rename_branch first. Only works inside a workbench session.",
				InputSchema: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"title": map[string]any{
							"type":        "string",
							"description": "PR title (omit to auto-fill from commits)",
						},
						"body": map[string]any{
							"type":        "string",
							"description": "PR body/description",
						},
						"draft": map[string]any{
							"type":        "boolean",
							"description": "Create as draft PR",
						},
					},
				},
				Handler: handleCreatePR,
			},
			{
				Name:        "docs",
				Description: "Look up workbench documentation. Returns reference docs on commands, config, TUI, worktree lifecycle, MCP tools, sandbox, and development.",
				InputSchema: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"topic": map[string]any{
							"type":        "string",
							"description": "Topic to look up: overview, commands, config, tui, worktrees, mcp, sandbox, development. Omit for a list of topics.",
							"enum":        []string{"overview", "commands", "config", "tui", "worktrees", "mcp", "sandbox", "development", "all"},
						},
					},
				},
				Handler: handleDocs,
			},
		},
		Prompts: []Prompt{
			{
				Name:        "workbench_conventions",
				Description: "Branch naming, scope discipline, and PR conventions for workbench sessions",
				Text:        conventionsText,
			},
		},
	}
}

func handleRenameBranch(args map[string]any) (string, bool) {
	newName, _ := args["new_name"].(string)
	if newName == "" {
		return "new_name is required", true
	}

	cmdArgs := []string{"rename-branch", newName}
	if push, ok := args["push"].(bool); ok && push {
		cmdArgs = append(cmdArgs, "--push")
	}

	ctx, cancel := ToolContext()
	defer cancel()
	out, err := exec.CommandContext(ctx, "workbench", cmdArgs...).CombinedOutput()
	if err != nil {
		return fmt.Sprintf("error: %s\n%s", err, string(out)), true
	}
	return string(out), false
}

func currentBranch() string {
	ctx, cancel := ToolContext()
	defer cancel()
	out, err := exec.CommandContext(ctx,
		"git", "rev-parse", "--abbrev-ref", "HEAD").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func handleCreatePR(args map[string]any) (string, bool) {
	branch := currentBranch()
	if branch != "" {
		parts := strings.Split(branch, "/")
		if git.IsCityName(parts[len(parts)-1]) {
			return "Branch still has the auto-generated name (" + branch + "). " +
				"Call rename_branch first to give it a meaningful name.", true
		}
	}

	pushCtx, pushCancel := ToolContext()
	defer pushCancel()
	pushOut, err := exec.CommandContext(pushCtx,
		"git", "push", "-u", "origin", "HEAD").CombinedOutput()
	if err != nil {
		return fmt.Sprintf("git push failed: %s\n%s", err, string(pushOut)), true
	}

	ghArgs := []string{"pr", "create"}
	if title, ok := args["title"].(string); ok && title != "" {
		ghArgs = append(ghArgs, "--title", title)
	}
	if body, ok := args["body"].(string); ok && body != "" {
		ghArgs = append(ghArgs, "--body", body)
	}
	if draft, ok := args["draft"].(bool); ok && draft {
		ghArgs = append(ghArgs, "--draft")
	}
	if _, hasTitle := args["title"]; !hasTitle {
		ghArgs = append(ghArgs, "--fill")
	}

	ghCtx, ghCancel := ToolContext()
	defer ghCancel()
	ghOut, err := exec.CommandContext(ghCtx, "gh", ghArgs...).CombinedOutput()
	if err != nil {
		return fmt.Sprintf("gh pr create failed: %s\n%s", err, string(ghOut)), true
	}

	return strings.TrimSpace(string(pushOut)) + "\n" + strings.TrimSpace(string(ghOut)), false
}

func handleDocs(args map[string]any) (string, bool) {
	topic, _ := args["topic"].(string)
	if topic == "" {
		return docs.ListTopics(), false
	}
	if topic == "all" {
		return docs.All(), false
	}
	content, ok := docs.Get(topic)
	if !ok {
		return "Unknown topic. " + docs.ListTopics(), true
	}
	return content, false
}

func conventionsText() string {
	worktrees, _ := docs.Get("worktrees")
	mcp, _ := docs.Get("mcp")
	return "Workbench conventions — this applies because you are inside a workbench session.\n\n" +
		worktrees + "\n---\n\n" + mcp
}
