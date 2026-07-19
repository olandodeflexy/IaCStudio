package mcpairlock

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestParseToolCallArgumentsNormalizesAndSnapshotsObject(t *testing.T) {
	input := []byte(` { "z": 1, "nested": { "enabled": true }, "duplicate": "first", "duplicate": "second" } `)
	arguments, err := ParseToolCallArguments(input)
	if err != nil {
		t.Fatalf("ParseToolCallArguments: %v", err)
	}
	input[3] = 'x'

	want := `{"duplicate":"second","nested":{"enabled":true},"z":1}`
	if got := string(arguments.Bytes()); got != want {
		t.Fatalf("arguments = %s, want %s", got, want)
	}
	copyOfArguments := arguments.Bytes()
	copyOfArguments[0] = '['
	if got := string(arguments.Bytes()); got != want {
		t.Fatalf("mutating Bytes result changed arguments to %s", got)
	}

	marshaled, err := json.Marshal(arguments)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if string(marshaled) != want {
		t.Fatalf("marshaled arguments = %s, want %s", marshaled, want)
	}
}

func TestParseToolCallArgumentsRejectsUnsafeShapes(t *testing.T) {
	oversized := []byte(`{"value":"` + strings.Repeat("x", maxToolCallArgumentsBytes) + `"}`)
	tests := []struct {
		name  string
		input []byte
	}{
		{name: "empty", input: nil},
		{name: "whitespace", input: []byte(" \n\t ")},
		{name: "null", input: []byte(`null`)},
		{name: "array", input: []byte(`[]`)},
		{name: "scalar", input: []byte(`"value"`)},
		{name: "trailing value", input: []byte(`{} {}`)},
		{name: "invalid", input: []byte(`{"value":`)},
		{name: "oversized", input: oversized},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := ParseToolCallArguments(test.input)
			if !errors.Is(err, ErrInvalidToolCallArguments) {
				t.Fatalf("error = %v, want ErrInvalidToolCallArguments", err)
			}
		})
	}
}

func TestParseToolCallArgumentsExplainsWhitespaceOnlyInput(t *testing.T) {
	_, err := ParseToolCallArguments([]byte(" \n\t "))
	if !errors.Is(err, ErrInvalidToolCallArguments) || !strings.Contains(err.Error(), "JSON object is required") {
		t.Fatalf("error = %v, want required JSON object error", err)
	}
}

func TestToolCallArgumentsUnmarshalKeepsPriorValueOnFailure(t *testing.T) {
	arguments, err := ParseToolCallArguments([]byte(`{"safe":true}`))
	if err != nil {
		t.Fatalf("ParseToolCallArguments: %v", err)
	}
	if err := json.Unmarshal([]byte(`[]`), &arguments); !errors.Is(err, ErrInvalidToolCallArguments) {
		t.Fatalf("Unmarshal error = %v, want ErrInvalidToolCallArguments", err)
	}
	if got := string(arguments.Bytes()); got != `{"safe":true}` {
		t.Fatalf("arguments changed after failed unmarshal: %s", got)
	}
}

func TestToolCallRequestValidatesExactIdentifiersAndSnapshotsArguments(t *testing.T) {
	arguments, err := ParseToolCallArguments([]byte(`{"workspace":"demo"}`))
	if err != nil {
		t.Fatalf("ParseToolCallArguments: %v", err)
	}
	request, err := NewToolCallRequest("terraform", "plan_workspace", arguments)
	if err != nil {
		t.Fatalf("NewToolCallRequest: %v", err)
	}
	arguments.encoded[0] = '['
	if err := request.Validate(); err != nil {
		t.Fatalf("snapshotted request Validate: %v", err)
	}
	namespaced, err := NewToolCallRequest("terraform-official", "provider.plan/read_only-v2", request.Arguments)
	if err != nil {
		t.Fatalf("namespaced NewToolCallRequest: %v", err)
	}
	if err := namespaced.Validate(); err != nil {
		t.Fatalf("namespaced request Validate: %v", err)
	}

	tests := []ToolCallRequest{
		{ToolName: "plan_workspace", Arguments: request.Arguments},
		{ServerID: " terraform", ToolName: "plan_workspace", Arguments: request.Arguments},
		{ServerID: "terraform", ToolName: "plan\nworkspace", Arguments: request.Arguments},
		{ServerID: "terraform", ToolName: "plan\u200bworkspace", Arguments: request.Arguments},
		{ServerID: "terraform", ToolName: "pl\u0430n_workspace", Arguments: request.Arguments},
		{ServerID: "terraform/official", ToolName: "plan_workspace", Arguments: request.Arguments},
		{ServerID: "terraform", ToolName: strings.Repeat("x", maxToolCallIdentifierBytes+1), Arguments: request.Arguments},
		{ServerID: "terraform", ToolName: "plan_workspace"},
	}
	for i, invalid := range tests {
		if err := invalid.Validate(); !errors.Is(err, ErrInvalidToolCallRequest) {
			t.Fatalf("invalid request %d error = %v, want ErrInvalidToolCallRequest", i, err)
		}
	}
}

func TestNewToolCallResultRedactsBoundsAndMarksOutputUntrusted(t *testing.T) {
	secret := "aws_secret_access_key=not-for-output"
	result := NewToolCallResult([]byte("prefix "+secret+" "+strings.Repeat("x", maxToolCallOutputBytes)), false)
	if strings.Contains(result.Output, "not-for-output") || !strings.Contains(result.Output, "[REDACTED]") {
		t.Fatalf("result did not redact secret: %q", result.Output[:64])
	}
	if !result.UntrustedOutput || !result.Redacted || !result.Truncated {
		t.Fatalf("result flags = untrusted:%v redacted:%v truncated:%v", result.UntrustedOutput, result.Redacted, result.Truncated)
	}
	if len(result.Output) > maxToolCallOutputBytes || !utf8.ValidString(result.Output) {
		t.Fatalf("result output is not bounded valid UTF-8")
	}
	if err := result.Validate(); err != nil {
		t.Fatalf("result Validate: %v", err)
	}
}

func TestNewToolCallResultTruncatesAtUTF8Boundary(t *testing.T) {
	output := strings.Repeat("x", maxToolCallOutputBytes-1) + "\u20ac"
	result := NewToolCallResult([]byte(output), true)
	if !result.IsError || !result.Truncated {
		t.Fatalf("result flags = is_error:%v truncated:%v", result.IsError, result.Truncated)
	}
	if !utf8.ValidString(result.Output) || len(result.Output) != maxToolCallOutputBytes-1 {
		t.Fatalf("output is not safely truncated: bytes=%d valid=%v", len(result.Output), utf8.ValidString(result.Output))
	}
	if err := result.Validate(); err != nil {
		t.Fatalf("result Validate: %v", err)
	}
}

func TestNewToolCallResultMarksOversizedWhitespaceTruncated(t *testing.T) {
	result := NewToolCallResult([]byte(strings.Repeat(" ", maxToolCallOutputBytes+1)), false)
	if !result.Truncated || result.Output != "" {
		t.Fatalf("result = output:%q truncated:%v, want empty truncated output", result.Output, result.Truncated)
	}
	if err := result.Validate(); err != nil {
		t.Fatalf("result Validate: %v", err)
	}
}

func TestNewToolCallResultRedactsCredentialAcrossOutputBoundary(t *testing.T) {
	output := strings.Repeat("x", maxToolCallOutputBytes-8) + "AKIA123456789012 tail"
	result := NewToolCallResult([]byte(output), false)
	if !result.Redacted || !result.Truncated || strings.Contains(result.Output, "AKIA") {
		t.Fatalf("result flags = redacted:%v truncated:%v; output tail = %q", result.Redacted, result.Truncated, result.Output[len(result.Output)-32:])
	}
	if err := result.Validate(); err != nil {
		t.Fatalf("result Validate: %v", err)
	}
}

func TestToolCallResultValidateRejectsUnsafeOutput(t *testing.T) {
	tests := []ToolCallResult{
		{Output: "safe"},
		{Output: "token=exposed", UntrustedOutput: true},
		{Output: strings.Repeat("x", maxToolCallOutputBytes+1), UntrustedOutput: true},
		{Output: string([]byte{0xff}), UntrustedOutput: true},
	}
	for i, result := range tests {
		if err := result.Validate(); !errors.Is(err, ErrInvalidToolCallResult) {
			t.Fatalf("unsafe result %d error = %v, want ErrInvalidToolCallResult", i, err)
		}
	}
}
