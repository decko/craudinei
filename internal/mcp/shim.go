package mcp

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"
)

const toolName = "approval"

var toolInputSchema = json.RawMessage(`{
	"type": "object",
	"properties": {
		"tool_name": {
			"type": "string",
			"description": "The name of the tool requesting permission"
		},
		"input": {
			"type": "object",
			"description": "The input for the tool",
			"additionalProperties": true
		},
		"tool_use_id": {
			"type": "string",
			"description": "The unique tool use request ID"
		}
	},
	"required": ["tool_name", "input"]
}`)

type jsonrpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      any             `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type jsonrpcResponse struct {
	JSONRPC string `json:"jsonrpc"`
	ID      any    `json:"id"`
	Result  any    `json:"result,omitempty"`
	Error   any    `json:"error,omitempty"`
}

type mcpTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"inputSchema"`
}

type toolCallParams struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

type permissionInput struct {
	ToolName  string         `json:"tool_name"`
	Input     map[string]any `json:"input"`
	ToolUseID string         `json:"tool_use_id,omitempty"`
}

type approvalHTTPRequest struct {
	ToolName  string         `json:"tool_name"`
	ToolInput map[string]any `json:"tool_input"`
}

type approvalHTTPResponse struct {
	Decision string `json:"decision"`
	Reason   string `json:"reason"`
}

type allowResponse struct {
	Behavior     string         `json:"behavior"`
	UpdatedInput map[string]any `json:"updatedInput"`
}

type denyResponse struct {
	Behavior string `json:"behavior"`
	Message  string `json:"message"`
}

type textContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// Run starts the MCP stdio server. It reads JSON-RPC requests from stdin
// and writes responses to stdout, proxying tool calls to the approval
// HTTP server at the given port.
func Run(port int) error {
	approvalURL := fmt.Sprintf("http://127.0.0.1:%d/approval", port)
	client := &http.Client{Timeout: 10 * time.Minute}

	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}

		var req jsonrpcRequest
		if err := json.Unmarshal(line, &req); err != nil {
			writeError(os.Stdout, nil, -32700, "parse error")
			continue
		}

		switch req.Method {
		case "initialize":
			writeResult(os.Stdout, req.ID, map[string]any{
				"protocolVersion": "2024-11-05",
				"capabilities":    map[string]any{"tools": map[string]any{}},
				"serverInfo":      map[string]any{"name": "craudinei", "version": "1.0.0"},
			})

		case "notifications/initialized":
			// No response needed for notifications

		case "tools/list":
			writeResult(os.Stdout, req.ID, map[string]any{
				"tools": []mcpTool{{
					Name:        toolName,
					Description: "Request user approval for a tool call via Telegram",
					InputSchema: toolInputSchema,
				}},
			})

		case "tools/call":
			resp, err := handleToolCall(req.Params, approvalURL, client)
			if err != nil {
				writeResult(os.Stdout, req.ID, map[string]any{
					"content": []textContent{{Type: "text", Text: err.Error()}},
					"isError": true,
				})
			} else {
				writeResult(os.Stdout, req.ID, map[string]any{
					"content": []textContent{{Type: "text", Text: resp}},
				})
			}

		default:
			writeError(os.Stdout, req.ID, -32601, "method not found: "+req.Method)
		}
	}

	return scanner.Err()
}

func handleToolCall(params json.RawMessage, approvalURL string, client *http.Client) (string, error) {
	var call toolCallParams
	if err := json.Unmarshal(params, &call); err != nil {
		return "", fmt.Errorf("invalid tool call params: %w", err)
	}

	if call.Name != toolName {
		return "", fmt.Errorf("unknown tool: %s", call.Name)
	}

	var input permissionInput
	if err := json.Unmarshal(call.Arguments, &input); err != nil {
		return "", fmt.Errorf("invalid tool input: %w", err)
	}

	// Forward to HTTP approval server
	httpReq := approvalHTTPRequest{
		ToolName:  input.ToolName,
		ToolInput: input.Input,
	}
	body, err := json.Marshal(httpReq)
	if err != nil {
		return "", fmt.Errorf("marshalling request: %w", err)
	}

	resp, err := client.Post(approvalURL, "application/json", bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("calling approval server: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("reading response: %w", err)
	}

	var httpResp approvalHTTPResponse
	if err := json.Unmarshal(respBody, &httpResp); err != nil {
		return "", fmt.Errorf("decoding response: %w", err)
	}

	// Translate approval server response to Claude Code's expected format
	var result []byte
	switch httpResp.Decision {
	case "approve":
		result, _ = json.Marshal(allowResponse{
			Behavior:     "allow",
			UpdatedInput: input.Input,
		})
	default:
		msg := httpResp.Reason
		if msg == "" {
			msg = "User denied this action"
		}
		result, _ = json.Marshal(denyResponse{
			Behavior: "deny",
			Message:  msg,
		})
	}

	return string(result), nil
}

func writeResult(w io.Writer, id any, result any) {
	resp := jsonrpcResponse{JSONRPC: "2.0", ID: id, Result: result}
	data, _ := json.Marshal(resp)
	fmt.Fprintf(w, "%s\n", data)
}

func writeError(w io.Writer, id any, code int, message string) {
	resp := jsonrpcResponse{
		JSONRPC: "2.0",
		ID:      id,
		Error:   map[string]any{"code": code, "message": message},
	}
	data, _ := json.Marshal(resp)
	fmt.Fprintf(w, "%s\n", data)
}
