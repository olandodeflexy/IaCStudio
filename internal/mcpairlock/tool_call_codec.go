package mcpairlock

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

const (
	toolCallProtocolVersion     = "2025-06-18"
	toolCallInitializeID        = 1
	toolCallInvocationID        = 2
	maxToolCallWireMessageBytes = 2 << 20
	maxToolCallWireSessionBytes = 4 << 20
	maxToolCallWireMessages     = 32
)

// ErrInvalidToolCallResponse classifies malformed, mismatched, or unsupported
// responses received from an external MCP server.
var ErrInvalidToolCallResponse = errors.New("invalid MCP tool call response")

type toolCallRPCMessage struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   json.RawMessage `json:"error,omitempty"`
}

type toolCallRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type toolCallInitializeResult struct {
	ProtocolVersion string `json:"protocolVersion"`
}

type toolCallWireResult struct {
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
	IsError bool `json:"isError,omitempty"`
}

// runToolCallSession exchanges one MCP tools/call request over an already
// connected newline-delimited stream. The process adapter owns cancellation
// and stream closure; this codec owns protocol and payload bounds.
func runToolCallSession(ctx context.Context, serverOutput io.Reader, serverInput io.Writer, request ToolCallRequest) (ToolCallResult, error) {
	if err := request.Validate(); err != nil {
		return ToolCallResult{}, err
	}
	if err := ctx.Err(); err != nil {
		return ToolCallResult{}, err
	}

	encoder := json.NewEncoder(serverInput)
	encoder.SetEscapeHTML(false)
	if err := writeToolCallInitialize(encoder); err != nil {
		return ToolCallResult{}, err
	}
	if err := flushToolCallWriter(serverInput, "initialize request"); err != nil {
		return ToolCallResult{}, err
	}

	scanner := bufio.NewScanner(serverOutput)
	scanner.Buffer(make([]byte, 0, 64*1024), maxToolCallWireMessageBytes)
	remainingMessages := maxToolCallWireMessages
	remainingBytes := maxToolCallWireSessionBytes
	initializeJSON, rpcErr, err := readToolCallRPCResponse(ctx, scanner, toolCallInitializeID, &remainingMessages, &remainingBytes)
	if err != nil {
		return ToolCallResult{}, err
	}
	if rpcErr != nil {
		result := newToolCallRPCErrorResult(rpcErr)
		return ToolCallResult{}, fmt.Errorf("%w: initialize failed: %s", ErrInvalidToolCallResponse, result.Output)
	}
	if err := validateToolCallInitializeResult(initializeJSON); err != nil {
		return ToolCallResult{}, err
	}

	if err := writeToolCallInvocation(encoder, request); err != nil {
		return ToolCallResult{}, err
	}
	if err := flushToolCallWriter(serverInput, "tools/call request"); err != nil {
		return ToolCallResult{}, err
	}
	resultJSON, rpcErr, err := readToolCallRPCResponse(ctx, scanner, toolCallInvocationID, &remainingMessages, &remainingBytes)
	if err != nil {
		return ToolCallResult{}, err
	}
	if rpcErr != nil {
		return newToolCallRPCErrorResult(rpcErr), nil
	}
	return decodeToolCallWireResult(resultJSON)
}

func writeToolCallInitialize(encoder *json.Encoder) error {
	if err := encoder.Encode(map[string]any{
		"jsonrpc": "2.0",
		"id":      toolCallInitializeID,
		"method":  "initialize",
		"params": map[string]any{
			"protocolVersion": toolCallProtocolVersion,
			"capabilities":    map[string]any{},
			"clientInfo":      map[string]any{"name": "iac-studio-airlock", "version": "0"},
		},
	}); err != nil {
		return fmt.Errorf("write initialize request: %w", err)
	}
	return nil
}

func writeToolCallInvocation(encoder *json.Encoder, request ToolCallRequest) error {
	if err := encoder.Encode(map[string]any{"jsonrpc": "2.0", "method": "notifications/initialized"}); err != nil {
		return fmt.Errorf("write initialized notification: %w", err)
	}
	if err := encoder.Encode(map[string]any{
		"jsonrpc": "2.0",
		"id":      toolCallInvocationID,
		"method":  "tools/call",
		"params": map[string]any{
			"name":      request.ToolName,
			"arguments": json.RawMessage(request.Arguments.Bytes()),
		},
	}); err != nil {
		return fmt.Errorf("write tools/call request: %w", err)
	}
	return nil
}

func flushToolCallWriter(writer io.Writer, operation string) error {
	flusher, ok := writer.(interface{ Flush() error })
	if !ok {
		return nil
	}
	if err := flusher.Flush(); err != nil {
		return fmt.Errorf("flush %s: %w", operation, err)
	}
	return nil
}

func validateToolCallInitializeResult(input json.RawMessage) error {
	if bytes.Equal(bytes.TrimSpace(input), []byte("null")) {
		return fmt.Errorf("%w: initialize result must be an object", ErrInvalidToolCallResponse)
	}
	var result toolCallInitializeResult
	if err := json.Unmarshal(input, &result); err != nil {
		return fmt.Errorf("%w: decode initialize result: %w", ErrInvalidToolCallResponse, err)
	}
	if result.ProtocolVersion != toolCallProtocolVersion {
		return fmt.Errorf("%w: initialize protocol version is unsupported", ErrInvalidToolCallResponse)
	}
	return nil
}

func readToolCallRPCResponse(ctx context.Context, scanner *bufio.Scanner, expectedID int, remainingMessages, remainingBytes *int) (json.RawMessage, *toolCallRPCError, error) {
	for *remainingMessages > 0 {
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		*remainingMessages = *remainingMessages - 1
		if !scanner.Scan() {
			if err := scanner.Err(); err != nil {
				return nil, nil, fmt.Errorf("%w: read response: %w", ErrInvalidToolCallResponse, err)
			}
			return nil, nil, fmt.Errorf("%w: response %d not received: %w", ErrInvalidToolCallResponse, expectedID, io.ErrUnexpectedEOF)
		}
		if len(scanner.Bytes()) > *remainingBytes {
			return nil, nil, fmt.Errorf("%w: response byte limit exceeded", ErrInvalidToolCallResponse)
		}
		*remainingBytes -= len(scanner.Bytes())
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}

		var message toolCallRPCMessage
		if err := json.Unmarshal(line, &message); err != nil {
			return nil, nil, fmt.Errorf("%w: decode response: %w", ErrInvalidToolCallResponse, err)
		}
		if message.JSONRPC != "2.0" {
			return nil, nil, fmt.Errorf("%w: unsupported JSON-RPC version", ErrInvalidToolCallResponse)
		}
		if len(message.ID) == 0 && message.Method != "" {
			continue
		}

		var responseID int
		if err := json.Unmarshal(message.ID, &responseID); err != nil {
			return nil, nil, fmt.Errorf("%w: decode response id: %w", ErrInvalidToolCallResponse, err)
		}
		if responseID != expectedID {
			return nil, nil, fmt.Errorf("%w: unexpected response id %d, want %d", ErrInvalidToolCallResponse, responseID, expectedID)
		}
		hasResult := len(bytes.TrimSpace(message.Result)) > 0
		hasError := len(bytes.TrimSpace(message.Error)) > 0
		if hasResult == hasError {
			return nil, nil, fmt.Errorf("%w: response must contain exactly one of result or error", ErrInvalidToolCallResponse)
		}
		if hasError {
			var rpcErr toolCallRPCError
			if bytes.Equal(bytes.TrimSpace(message.Error), []byte("null")) {
				return nil, nil, fmt.Errorf("%w: error must be an object", ErrInvalidToolCallResponse)
			}
			if err := json.Unmarshal(message.Error, &rpcErr); err != nil {
				return nil, nil, fmt.Errorf("%w: decode response error: %w", ErrInvalidToolCallResponse, err)
			}
			return nil, &rpcErr, nil
		}
		return message.Result, nil, nil
	}
	return nil, nil, fmt.Errorf("%w: response message limit exceeded", ErrInvalidToolCallResponse)
}

func decodeToolCallWireResult(input json.RawMessage) (ToolCallResult, error) {
	var wireResult toolCallWireResult
	if err := json.Unmarshal(input, &wireResult); err != nil {
		return ToolCallResult{}, fmt.Errorf("%w: decode tools/call result: %w", ErrInvalidToolCallResponse, err)
	}
	texts := make([]string, 0, len(wireResult.Content))
	for _, content := range wireResult.Content {
		if content.Type == "text" {
			texts = append(texts, content.Text)
		}
	}
	if len(texts) == 0 {
		return ToolCallResult{}, fmt.Errorf("%w: tools/call result has no text content", ErrInvalidToolCallResponse)
	}
	result := NewToolCallResult([]byte(strings.Join(texts, "\n")), wireResult.IsError)
	if err := result.Validate(); err != nil {
		return ToolCallResult{}, err
	}
	return result, nil
}

func newToolCallRPCErrorResult(rpcErr *toolCallRPCError) ToolCallResult {
	message := strings.TrimSpace(rpcErr.Message)
	if message == "" {
		message = fmt.Sprintf("MCP error %d", rpcErr.Code)
	}
	return NewToolCallResult([]byte(message), true)
}
