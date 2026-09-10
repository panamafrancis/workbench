// Package mcp implements a minimal JSON-RPC 2.0 MCP server over stdio plus the
// workbench tool set. The Server type is a reusable framework: sibling tools
// (supatree) build their own Server with their own Tools/Prompts/Gate.
package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"time"
)

const toolTimeout = 120 * time.Second

// jsonRPCVersion is the JSON-RPC version string every request and response
// carries; MCP pins it to 2.0.
const jsonRPCVersion = "2.0"

// ToolContext returns a context with the standard per-tool-call timeout. Tool
// handlers that shell out should derive their exec context from it.
func ToolContext() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), toolTimeout)
}

// Tool is a callable MCP tool. Handler receives the decoded arguments and
// returns the text result plus whether it represents an error.
type Tool struct {
	Name        string
	Description string
	InputSchema any
	Handler     func(args map[string]any) (text string, isError bool)
}

// Prompt is a named MCP prompt whose Text is rendered lazily on prompts/get.
type Prompt struct {
	Name        string
	Description string
	Text        func() string
}

// Server is a JSON-RPC MCP server driven by a tool + prompt set.
type Server struct {
	Name    string
	Version string
	Tools   []Tool
	Prompts []Prompt
	// Gate is consulted before a tool's Handler runs. Return a non-empty string
	// to refuse the call (reported as an error result); empty string allows it.
	// Nil Gate allows every tool.
	Gate func(toolName string) string
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

type toolDesc struct {
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

type promptDesc struct {
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

// Run reads line-delimited JSON-RPC requests from stdin and writes responses to
// stdout until stdin closes.
func (s *Server) Run() error {
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
				JSONRPC: jsonRPCVersion,
				ID:      nil,
				Error:   &rpcError{Code: -32700, Message: "parse error"},
			})
			continue
		}
		resp := s.handle(req)
		if resp != nil {
			_ = enc.Encode(resp)
		}
	}
	return scanner.Err()
}

func (s *Server) handle(req request) *response {
	if req.ID == nil {
		return nil
	}

	switch req.Method {
	case "initialize":
		return &response{
			JSONRPC: jsonRPCVersion,
			ID:      req.ID,
			Result: map[string]any{
				"protocolVersion": "2024-11-05",
				"capabilities": map[string]any{
					"tools":   map[string]any{},
					"prompts": map[string]any{},
				},
				"serverInfo": map[string]any{
					"name":    s.Name,
					"version": s.Version,
				},
			},
		}

	case "ping":
		return &response{JSONRPC: jsonRPCVersion, ID: req.ID, Result: map[string]any{}}

	case "tools/list":
		return &response{JSONRPC: jsonRPCVersion, ID: req.ID, Result: map[string]any{"tools": s.toolDescs()}}

	case "tools/call":
		return s.handleToolCall(req)

	case "prompts/list":
		return &response{JSONRPC: jsonRPCVersion, ID: req.ID, Result: map[string]any{"prompts": s.promptDescs()}}

	case "prompts/get":
		return s.handlePromptGet(req)

	default:
		return &response{
			JSONRPC: jsonRPCVersion,
			ID:      req.ID,
			Error:   &rpcError{Code: -32601, Message: "method not found: " + req.Method},
		}
	}
}

func (s *Server) toolDescs() []toolDesc {
	out := make([]toolDesc, 0, len(s.Tools))
	for _, t := range s.Tools {
		out = append(out, toolDesc{Name: t.Name, Description: t.Description, InputSchema: t.InputSchema})
	}
	return out
}

func (s *Server) handleToolCall(req request) *response {
	var params toolCallParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return &response{
			JSONRPC: jsonRPCVersion,
			ID:      req.ID,
			Error:   &rpcError{Code: -32602, Message: "invalid params"},
		}
	}

	var tool *Tool
	for i := range s.Tools {
		if s.Tools[i].Name == params.Name {
			tool = &s.Tools[i]
			break
		}
	}
	if tool == nil {
		return &response{
			JSONRPC: jsonRPCVersion,
			ID:      req.ID,
			Error:   &rpcError{Code: -32602, Message: "unknown tool: " + params.Name},
		}
	}

	if s.Gate != nil {
		if msg := s.Gate(params.Name); msg != "" {
			return textResult(req.ID, msg, true)
		}
	}

	text, isError := tool.Handler(params.Arguments)
	return textResult(req.ID, text, isError)
}

func (s *Server) promptDescs() []promptDesc {
	out := make([]promptDesc, 0, len(s.Prompts))
	for _, p := range s.Prompts {
		out = append(out, promptDesc{Name: p.Name, Description: p.Description})
	}
	return out
}

func (s *Server) handlePromptGet(req request) *response {
	var params promptGetParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return &response{
			JSONRPC: jsonRPCVersion,
			ID:      req.ID,
			Error:   &rpcError{Code: -32602, Message: "invalid params"},
		}
	}

	for _, p := range s.Prompts {
		if p.Name == params.Name {
			return &response{
				JSONRPC: jsonRPCVersion,
				ID:      req.ID,
				Result: map[string]any{
					"messages": []promptMessage{
						{Role: "user", Content: contentBlock{Type: "text", Text: p.Text()}},
					},
				},
			}
		}
	}
	return &response{
		JSONRPC: jsonRPCVersion,
		ID:      req.ID,
		Error:   &rpcError{Code: -32602, Message: "unknown prompt: " + params.Name},
	}
}

func textResult(id json.RawMessage, text string, isError bool) *response {
	return &response{
		JSONRPC: jsonRPCVersion,
		ID:      id,
		Result: toolResult{
			Content: []contentBlock{{Type: "text", Text: text}},
			IsError: isError,
		},
	}
}
