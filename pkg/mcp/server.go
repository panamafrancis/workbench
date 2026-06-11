package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/panamafrancis/workbench/pkg/docs"
)

const toolTimeout = 120 * time.Second

func toolCtx() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), toolTimeout)
}

type request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type tool struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	InputSchema any    `json:"inputSchema"`
}

type toolCallParams struct {
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments,omitempty"`
}

type toolResult struct {
	Content []contentBlock `json:"content"`
	IsError bool           `json:"isError,omitempty"`
}

type contentBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type prompt struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

type promptGetParams struct {
	Name string `json:"name"`
}

type promptMessage struct {
	Role    string       `json:"role"`
	Content contentBlock `json:"content"`
}

func Run(version string) error {
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 0, 1024*1024), 1024*1024)
	enc := json.NewEncoder(os.Stdout)

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var req request
		if err := json.Unmarshal(line, &req); err != nil {
			_ = enc.Encode(&response{
				JSONRPC: "2.0",
				ID:      nil,
				Error:   &rpcError{Code: -32700, Message: "parse error"},
			})
			continue
		}
		resp := handle(req, version)
		if resp != nil {
			_ = enc.Encode(resp)
		}
	}
	return scanner.Err()
}

func handle(req request, version string) *response {
	if req.ID == nil {
		return nil
	}

	switch req.Method {
	case "initialize":
		return &response{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result: map[string]any{
				"protocolVersion": "2024-11-05",
				"capabilities": map[string]any{
					"tools":   map[string]any{},
					"prompts": map[string]any{},
				},
				"serverInfo": map[string]any{
					"name":    "workbench",
					"version": version,
				},
			},
		}

	case "ping":
		return &response{JSONRPC: "2.0", ID: req.ID, Result: map[string]any{}}

	case "tools/list":
		return &response{JSONRPC: "2.0", ID: req.ID, Result: map[string]any{"tools": tools()}}

	case "tools/call":
		return handleToolCall(req)

	case "prompts/list":
		return &response{JSONRPC: "2.0", ID: req.ID, Result: map[string]any{"prompts": prompts()}}

	case "prompts/get":
		return handlePromptGet(req)

	default:
		return &response{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error:   &rpcError{Code: -32601, Message: "method not found: " + req.Method},
		}
	}
}

func tools() []tool {
	return []tool{
		{
			Name:        "rename_branch",
			Description: "Rename the current worktree's branch and update workbench config + PR cache. Use this instead of bare git branch -m. Only works inside a workbench session.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"new_name": map[string]any{
						"type":        "string",
						"description": "New branch name — keep the wt/<alias>/ prefix (e.g. wt/wb/session-launcher)",
					},
					"push": map[string]any{
						"type":        "boolean",
						"description": "Push new branch and delete old remote branch",
					},
				},
				"required": []string{"new_name"},
			},
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
		},
	}
}

func handleToolCall(req request) *response {
	var params toolCallParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return &response{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error:   &rpcError{Code: -32602, Message: "invalid params"},
		}
	}

	if params.Name == "docs" {
		return handleDocs(req.ID, params.Arguments)
	}

	if os.Getenv("WORKBENCH") != "1" {
		return textResult(req.ID, "Not inside a workbench session (WORKBENCH env var not set).", true)
	}

	switch params.Name {
	case "rename_branch":
		return handleRenameBranch(req.ID, params.Arguments)
	case "create_pr":
		return handleCreatePR(req.ID, params.Arguments)
	default:
		return &response{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error:   &rpcError{Code: -32602, Message: "unknown tool: " + params.Name},
		}
	}
}

func handleRenameBranch(id json.RawMessage, args map[string]any) *response {
	newName, _ := args["new_name"].(string)
	if newName == "" {
		return textResult(id, "new_name is required", true)
	}

	cmdArgs := []string{"rename-branch", newName}
	if push, ok := args["push"].(bool); ok && push {
		cmdArgs = append(cmdArgs, "--push")
	}

	ctx, cancel := toolCtx()
	defer cancel()
	out, err := exec.CommandContext(ctx, "workbench", cmdArgs...).CombinedOutput()
	if err != nil {
		return textResult(id, fmt.Sprintf("error: %s\n%s", err, string(out)), true)
	}
	return textResult(id, string(out), false)
}

func currentBranch() string {
	ctx, cancel := toolCtx()
	defer cancel()
	out, err := exec.CommandContext(ctx,
		"git", "rev-parse", "--abbrev-ref", "HEAD").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func handleCreatePR(id json.RawMessage, args map[string]any) *response {
	branch := currentBranch()
	wtName := os.Getenv("WORKBENCH_WORKTREE_NAME")

	if branch != "" && wtName != "" {
		parts := strings.Split(branch, "/")
		if len(parts) > 0 && parts[len(parts)-1] == wtName {
			return textResult(id,
				"Branch still has the auto-generated name ("+branch+"). "+
					"Call rename_branch first to give it a meaningful name.",
				true)
		}
	}

	pushCtx, pushCancel := toolCtx()
	defer pushCancel()
	pushOut, err := exec.CommandContext(pushCtx,
		"git", "push", "-u", "origin", "HEAD").CombinedOutput()
	if err != nil {
		return textResult(id, fmt.Sprintf("git push failed: %s\n%s", err, string(pushOut)), true)
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

	ghCtx, ghCancel := toolCtx()
	defer ghCancel()
	ghOut, err := exec.CommandContext(ghCtx, "gh", ghArgs...).CombinedOutput()
	if err != nil {
		return textResult(id, fmt.Sprintf("gh pr create failed: %s\n%s", err, string(ghOut)), true)
	}

	return textResult(id, strings.TrimSpace(string(pushOut))+"\n"+strings.TrimSpace(string(ghOut)), false)
}

func handleDocs(id json.RawMessage, args map[string]any) *response {
	topic, _ := args["topic"].(string)
	if topic == "" {
		return textResult(id, docs.ListTopics(), false)
	}
	if topic == "all" {
		return textResult(id, docs.All(), false)
	}
	content, ok := docs.Get(topic)
	if !ok {
		return textResult(id, "Unknown topic. "+docs.ListTopics(), true)
	}
	return textResult(id, content, false)
}

func prompts() []prompt {
	return []prompt{
		{
			Name:        "workbench_conventions",
			Description: "Branch naming, scope discipline, and PR conventions for workbench sessions",
		},
	}
}

func handlePromptGet(req request) *response {
	var params promptGetParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return &response{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error:   &rpcError{Code: -32602, Message: "invalid params"},
		}
	}

	if params.Name != "workbench_conventions" {
		return &response{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error:   &rpcError{Code: -32602, Message: "unknown prompt: " + params.Name},
		}
	}

	text := conventionsText()
	return &response{
		JSONRPC: "2.0",
		ID:      req.ID,
		Result: map[string]any{
			"messages": []promptMessage{
				{
					Role:    "user",
					Content: contentBlock{Type: "text", Text: text},
				},
			},
		},
	}
}

func conventionsText() string {
	worktrees, _ := docs.Get("worktrees")
	mcp, _ := docs.Get("mcp")
	return "Workbench conventions — this applies because you are inside a workbench session.\n\n" +
		worktrees + "\n---\n\n" + mcp
}

func textResult(id json.RawMessage, text string, isError bool) *response {
	return &response{
		JSONRPC: "2.0",
		ID:      id,
		Result: toolResult{
			Content: []contentBlock{{Type: "text", Text: text}},
			IsError: isError,
		},
	}
}
