package mcpairlock

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

const toolCallInitializeResponse = `{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":"2025-06-18","capabilities":{},"serverInfo":{"name":"test","version":"1"}}}`

func TestRunToolCallSessionWritesHandshakeAndSanitizesText(t *testing.T) {
	request := testToolCallRequest(t)
	responses := strings.Join([]string{
		toolCallInitializeResponse,
		`{"jsonrpc":"2.0","method":"notifications/progress","params":{}}`,
		`{"jsonrpc":"2.0","id":2,"result":{"content":[{"type":"text","text":"prefix token=do-not-leak"},{"type":"text","text":"safe"}],"isError":false}}`,
	}, "\n")
	var requests bytes.Buffer
	result, err := runToolCallSession(context.Background(), strings.NewReader(responses), &requests, request)
	if err != nil {
		t.Fatalf("runToolCallSession: %v", err)
	}
	if !result.UntrustedOutput || !result.Redacted || result.IsError || strings.Contains(result.Output, "do-not-leak") || !strings.Contains(result.Output, "safe") {
		t.Fatalf("unexpected result: %+v", result)
	}

	lines := strings.Split(strings.TrimSpace(requests.String()), "\n")
	if len(lines) != 3 {
		t.Fatalf("request count = %d, want 3: %s", len(lines), requests.String())
	}
	wantMethods := []string{"initialize", "notifications/initialized", "tools/call"}
	for i, line := range lines {
		var message struct {
			Method string `json:"method"`
			Params struct {
				Name      string         `json:"name"`
				Arguments map[string]any `json:"arguments"`
			} `json:"params"`
		}
		if err := json.Unmarshal([]byte(line), &message); err != nil {
			t.Fatalf("decode request %d: %v", i, err)
		}
		if message.Method != wantMethods[i] {
			t.Fatalf("request %d method = %q, want %q", i, message.Method, wantMethods[i])
		}
		if i == 2 && (message.Params.Name != request.ToolName || message.Params.Arguments["workspace"] != "demo") {
			t.Fatalf("tools/call params = %+v", message.Params)
		}
	}
}

func TestRunToolCallSessionSanitizesRPCError(t *testing.T) {
	responses := toolCallInitializeResponse + "\n" + `{"jsonrpc":"2.0","id":2,"error":{"code":-32603,"message":"client_secret=do-not-leak"}}`
	result, err := runToolCallSession(context.Background(), strings.NewReader(responses), &bytes.Buffer{}, testToolCallRequest(t))
	if err != nil {
		t.Fatalf("runToolCallSession: %v", err)
	}
	if !result.IsError || !result.Redacted || !result.UntrustedOutput || strings.Contains(result.Output, "do-not-leak") {
		t.Fatalf("unexpected RPC error result: %+v", result)
	}
}

func TestRunToolCallSessionRejectsInvalidRequestBeforeWriting(t *testing.T) {
	var requests bytes.Buffer
	_, err := runToolCallSession(context.Background(), strings.NewReader(""), &requests, ToolCallRequest{})
	if !errors.Is(err, ErrInvalidToolCallRequest) {
		t.Fatalf("error = %v, want ErrInvalidToolCallRequest", err)
	}
	if requests.Len() != 0 {
		t.Fatalf("invalid request wrote %d bytes", requests.Len())
	}
}

func TestRunToolCallSessionRejectsInvalidResponses(t *testing.T) {
	oversized := toolCallInitializeResponse + "\n" + strings.Repeat("x", maxToolCallWireMessageBytes+1)
	notifications := strings.Repeat(`{"jsonrpc":"2.0","method":"notifications/progress"}`+"\n", maxToolCallWireMessages)
	tests := []struct {
		name      string
		responses string
	}{
		{name: "malformed JSON", responses: "not-json"},
		{name: "wrong response id", responses: `{"jsonrpc":"2.0","id":9,"result":{}}`},
		{name: "unsupported version", responses: `{"jsonrpc":"1.0","id":1,"result":{}}`},
		{name: "oversized response", responses: oversized},
		{name: "message limit", responses: notifications},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := runToolCallSession(context.Background(), strings.NewReader(test.responses), &bytes.Buffer{}, testToolCallRequest(t))
			if !errors.Is(err, ErrInvalidToolCallResponse) {
				t.Fatalf("error = %v, want ErrInvalidToolCallResponse", err)
			}
		})
	}
}

func testToolCallRequest(t *testing.T) ToolCallRequest {
	t.Helper()
	arguments, err := ParseToolCallArguments([]byte(`{"workspace":"demo"}`))
	if err != nil {
		t.Fatalf("ParseToolCallArguments: %v", err)
	}
	request, err := NewToolCallRequest("terraform-official", "provider.plan/read_only-v2", arguments)
	if err != nil {
		t.Fatalf("NewToolCallRequest: %v", err)
	}
	return request
}
