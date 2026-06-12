package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/iac-studio/iac-studio/internal/cloudconnections"
	"github.com/iac-studio/iac-studio/internal/runner"
)

const (
	ProtocolVersion = "2025-06-18"
	ServerName      = "iac-studio-mcp"
)

type Config struct {
	ProjectsDir   string
	ApprovalToken string
	Version       string
	Now           func() time.Time
}

type Server struct {
	projectsDir   string
	approvalToken string
	version       string
	now           func() time.Time

	cloudConnections *cloudconnections.Manager
	run              *runner.SafeRunner
	audit            *AuditLogger

	tools    []Tool
	handlers map[string]toolHandler
	writeMu  sync.Mutex
}

type Tool struct {
	Name         string         `json:"name"`
	Title        string         `json:"title,omitempty"`
	Description  string         `json:"description"`
	InputSchema  map[string]any `json:"inputSchema"`
	OutputSchema map[string]any `json:"outputSchema,omitempty"`
	Annotations  map[string]any `json:"annotations,omitempty"`
}

type ToolCallResult struct {
	Content           []ToolContent `json:"content"`
	StructuredContent any           `json:"structuredContent,omitempty"`
	IsError           bool          `json:"isError,omitempty"`
}

type ToolContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type toolResponse struct {
	Result ToolCallResult
	Audit  AuditDecision
}

type toolHandler func(context.Context, json.RawMessage) toolResponse

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

type toolCallParams struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments,omitempty"`
}

func NewServer(cfg Config) *Server {
	if cfg.Version == "" {
		cfg.Version = "0.1.0"
	}
	if cfg.Now == nil {
		cfg.Now = func() time.Time { return time.Now().UTC() }
	}
	run := runner.NewSafeRunner(runner.DefaultSafetyConfig())
	s := &Server{
		projectsDir:      cfg.ProjectsDir,
		approvalToken:    cfg.ApprovalToken,
		version:          cfg.Version,
		now:              cfg.Now,
		cloudConnections: cloudconnections.NewManager(cfg.ProjectsDir),
		run:              run,
		audit:            NewAuditLogger(cfg.ProjectsDir, cfg.Now),
	}
	s.tools, s.handlers = s.buildTools()
	return s
}

func (s *Server) Serve(ctx context.Context, in io.Reader, out io.Writer) error {
	scanner := bufio.NewScanner(in)
	scanner.Buffer(make([]byte, 0, 64*1024), 10*1024*1024)
	encoder := json.NewEncoder(out)
	encoder.SetEscapeHTML(false)

	for scanner.Scan() {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		response, respond := s.handleLine(ctx, line)
		if !respond {
			continue
		}
		s.writeMu.Lock()
		err := encoder.Encode(response)
		s.writeMu.Unlock()
		if err != nil {
			return err
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	return nil
}

func (s *Server) handleLine(ctx context.Context, line []byte) (rpcResponse, bool) {
	var req rpcRequest
	if err := json.Unmarshal(line, &req); err != nil {
		return rpcResponse{
			JSONRPC: "2.0",
			Error:   &rpcError{Code: -32700, Message: "parse error", Data: err.Error()},
		}, true
	}
	if len(req.ID) == 0 {
		s.handleNotification(req)
		return rpcResponse{}, false
	}
	result, rpcErr := s.handleRequest(ctx, req)
	response := rpcResponse{JSONRPC: "2.0", ID: req.ID}
	if rpcErr != nil {
		response.Error = rpcErr
	} else {
		response.Result = result
	}
	return response, true
}

func (s *Server) handleNotification(req rpcRequest) {
	// notifications/initialized is the normal MCP lifecycle notification.
	// Other notifications are ignored so older or richer clients can still
	// connect without this local server needing every optional capability.
	_ = req
}

func (s *Server) handleRequest(ctx context.Context, req rpcRequest) (any, *rpcError) {
	switch req.Method {
	case "initialize":
		return map[string]any{
			"protocolVersion": ProtocolVersion,
			"capabilities": map[string]any{
				"tools": map[string]any{"listChanged": false},
			},
			"serverInfo": map[string]any{
				"name":    ServerName,
				"title":   "IaC Studio MCP",
				"version": s.version,
			},
			"instructions": "Use read-only tools for inspection. Mutating or high-risk infrastructure actions require explicit IaC Studio approval and are audited.",
		}, nil
	case "ping":
		return map[string]any{}, nil
	case "tools/list":
		return map[string]any{"tools": s.tools}, nil
	case "tools/call":
		return s.callTool(ctx, req.Params)
	default:
		return nil, &rpcError{Code: -32601, Message: "method not found", Data: req.Method}
	}
}

func (s *Server) callTool(ctx context.Context, params json.RawMessage) (ToolCallResult, *rpcError) {
	var call toolCallParams
	if err := decode(params, &call); err != nil {
		return ToolCallResult{}, &rpcError{Code: -32602, Message: "invalid tool call params", Data: err.Error()}
	}
	handler := s.handlers[call.Name]
	if handler == nil {
		return ToolCallResult{}, &rpcError{Code: -32602, Message: "unknown tool", Data: call.Name}
	}
	args := call.Arguments
	if len(args) == 0 || string(args) == "null" {
		args = []byte("{}")
	}
	response := handler(ctx, args)
	if response.Audit.Tool == "" {
		response.Audit.Tool = call.Name
	}
	if response.Audit.Decision == "" {
		response.Audit.Decision = "allowed"
	}
	if err := s.audit.Append(response.Audit); err != nil {
		return errorResult("audit log write failed", map[string]any{"error": err.Error()}), nil
	}
	return response.Result, nil
}

func decode(raw json.RawMessage, target any) error {
	if len(raw) == 0 {
		raw = []byte("{}")
	}
	if string(raw) == "null" {
		raw = []byte("{}")
	}
	if err := json.Unmarshal(raw, target); err != nil {
		return err
	}
	return nil
}

func structuredResult(payload any) ToolCallResult {
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		data = []byte(fmt.Sprintf("%v", payload))
	}
	return ToolCallResult{
		Content: []ToolContent{{
			Type: "text",
			Text: string(data),
		}},
		StructuredContent: payload,
	}
}

func errorResult(message string, payload any) ToolCallResult {
	if payload == nil {
		payload = map[string]any{"error": message}
	}
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		data = []byte(message)
	}
	return ToolCallResult{
		Content: []ToolContent{{
			Type: "text",
			Text: string(data),
		}},
		StructuredContent: payload,
		IsError:           true,
	}
}

func approvalRequiredResult(tool, reason string) ToolCallResult {
	return structuredResult(map[string]any{
		"status":            "approval_required",
		"tool":              tool,
		"reason":            reason,
		"approval_required": true,
		"next_step":         "Approve the action through IaC Studio or retry with the configured local approval token.",
	})
}

func (s *Server) approved(token string) bool {
	if s.approvalToken == "" {
		return false
	}
	return token == s.approvalToken
}

func errResponse(tool string, err error, audit AuditDecision) toolResponse {
	if audit.Tool == "" {
		audit.Tool = tool
	}
	audit.Decision = "error"
	audit.Error = err.Error()
	return toolResponse{
		Result: errorResult(err.Error(), map[string]any{
			"error": err.Error(),
			"tool":  tool,
		}),
		Audit: audit,
	}
}

func requireNonEmpty(name, value string) error {
	if value == "" {
		return fmt.Errorf("%s is required", name)
	}
	return nil
}

func firstErr(errs ...error) error {
	for _, err := range errs {
		if err != nil {
			return err
		}
	}
	return nil
}

var errApprovalRequired = errors.New("approval required")
