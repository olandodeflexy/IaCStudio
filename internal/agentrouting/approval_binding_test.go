package agentrouting

import (
	"errors"
	"strings"
	"testing"

	"github.com/iac-studio/iac-studio/internal/agentruns"
	"github.com/iac-studio/iac-studio/internal/mcpairlock"
)

var testToolApprovalKey = []byte("01234567890123456789012345678901")

func mustToolArguments(t *testing.T, input string) mcpairlock.ToolCallArguments {
	t.Helper()
	arguments, err := mcpairlock.ParseToolCallArguments([]byte(input))
	if err != nil {
		t.Fatalf("ParseToolCallArguments(): %v", err)
	}
	return arguments
}

func TestToolApprovalBindingMatchesOnlyExactOperation(t *testing.T) {
	request := validRequest()
	arguments := mustToolArguments(t, `{"region":"us-east-1","count":2}`)
	binding, err := NewToolApprovalBinding(testToolApprovalKey, "run_000001", request, arguments)
	if err != nil {
		t.Fatalf("NewToolApprovalBinding(): %v", err)
	}
	if err := binding.Validate(); err != nil {
		t.Fatalf("Validate(): %v", err)
	}
	if !binding.Matches(testToolApprovalKey, "run_000001", request, arguments) {
		t.Fatal("binding should match its exact operation")
	}

	tests := []struct {
		name      string
		runID     string
		request   Request
		arguments mcpairlock.ToolCallArguments
		key       []byte
	}{
		{name: "run", runID: "run_000002", request: request, arguments: arguments, key: testToolApprovalKey},
		{name: "key", runID: "run_000001", request: request, arguments: arguments, key: []byte("abcdefghijklmnopqrstuvwxyzABCDEF")},
		{name: "arguments", runID: "run_000001", request: request, arguments: mustToolArguments(t, `{"region":"us-east-1","count":3}`), key: testToolApprovalKey},
	}
	mutations := []struct {
		name   string
		mutate func(*Request)
	}{
		{name: "project", mutate: func(value *Request) { value.Project = "platform-dev" }},
		{name: "provider", mutate: func(value *Request) { value.ProviderID = "claude-code" }},
		{name: "connection", mutate: func(value *Request) { value.ConnectionID = "aws-dev" }},
		{name: "server", mutate: func(value *Request) { value.ServerID = "aws-official" }},
		{name: "tool", mutate: func(value *Request) { value.ToolName = "apply_workspace" }},
		{name: "mode", mutate: func(value *Request) { value.Mode = agentruns.ModeApprovedExecute }},
		{name: "risk", mutate: func(value *Request) { value.Risk = mcpairlock.RiskCloudMutation }},
	}
	for _, mutation := range mutations {
		candidate := request
		mutation.mutate(&candidate)
		tests = append(tests, struct {
			name      string
			runID     string
			request   Request
			arguments mcpairlock.ToolCallArguments
			key       []byte
		}{name: mutation.name, runID: "run_000001", request: candidate, arguments: arguments, key: testToolApprovalKey})
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if binding.Matches(test.key, test.runID, test.request, test.arguments) {
				t.Fatal("binding matched an altered operation")
			}
		})
	}
}

func TestToolApprovalBindingCanonicalizesArgumentsAndHidesInput(t *testing.T) {
	request := validRequest()
	first := mustToolArguments(t, `{"secret":"do-not-store","count":2}`)
	second := mustToolArguments(t, `{"count":2,"secret":"do-not-store"}`)

	firstBinding, err := NewToolApprovalBinding(testToolApprovalKey, "run_000001", request, first)
	if err != nil {
		t.Fatalf("NewToolApprovalBinding(first): %v", err)
	}
	secondBinding, err := NewToolApprovalBinding(testToolApprovalKey, "run_000001", request, second)
	if err != nil {
		t.Fatalf("NewToolApprovalBinding(second): %v", err)
	}
	if firstBinding != secondBinding {
		t.Fatalf("equivalent arguments produced different bindings: %q != %q", firstBinding, secondBinding)
	}
	for _, sensitive := range []string{"do-not-store", request.Project, request.ToolName} {
		if strings.Contains(string(firstBinding), sensitive) {
			t.Fatalf("binding exposed operation input %q", sensitive)
		}
	}
}

func TestToolApprovalBindingRejectsInvalidInputsAndRepresentations(t *testing.T) {
	request := validRequest()
	arguments := mustToolArguments(t, `{}`)
	tests := []struct {
		name      string
		key       []byte
		runID     string
		request   Request
		arguments mcpairlock.ToolCallArguments
	}{
		{name: "short key", key: []byte("short"), runID: "run_000001", request: request, arguments: arguments},
		{name: "empty run", key: testToolApprovalKey, runID: "", request: request, arguments: arguments},
		{name: "padded run", key: testToolApprovalKey, runID: " run_000001", request: request, arguments: arguments},
		{name: "invalid request", key: testToolApprovalKey, runID: "run_000001", request: Request{}, arguments: arguments},
		{name: "invalid arguments", key: testToolApprovalKey, runID: "run_000001", request: request},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if binding, err := NewToolApprovalBinding(test.key, test.runID, test.request, test.arguments); err == nil || !errors.Is(err, ErrInvalidToolApprovalBinding) || binding != "" {
				t.Fatalf("NewToolApprovalBinding() = %q, %v; want empty ErrInvalidToolApprovalBinding", binding, err)
			}
		})
	}

	invalidBindings := []ToolApprovalBinding{
		"",
		ToolApprovalBinding("mcp-tool-approval-v2:" + strings.Repeat("0", 64)),
		ToolApprovalBinding(toolApprovalBindingPrefix + strings.Repeat("0", 63)),
		ToolApprovalBinding(toolApprovalBindingPrefix + strings.Repeat("g", 64)),
		ToolApprovalBinding(toolApprovalBindingPrefix + strings.Repeat("A", 64)),
	}
	for _, binding := range invalidBindings {
		if err := binding.Validate(); err == nil || !errors.Is(err, ErrInvalidToolApprovalBinding) {
			t.Fatalf("Validate(%q) error = %v, want ErrInvalidToolApprovalBinding", binding, err)
		}
		if binding.Matches(testToolApprovalKey, "run_000001", request, arguments) {
			t.Fatalf("invalid binding %q matched", binding)
		}
	}
}
