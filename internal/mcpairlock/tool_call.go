package mcpairlock

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"unicode/utf8"
)

const (
	maxToolCallArgumentsBytes       = 64 << 10
	maxToolCallOutputBytes          = 256 << 10
	toolCallRedactionLookaheadBytes = 512
	maxToolCallIdentifierBytes      = 128
)

var (
	ErrInvalidToolCallArguments = errors.New("invalid MCP tool call arguments")
	ErrInvalidToolCallRequest   = errors.New("invalid MCP tool call request")
	ErrInvalidToolCallResult    = errors.New("invalid MCP tool call result")
)

// ToolCallArguments is an immutable, normalized JSON object passed to one MCP
// tool. Object-only arguments avoid ambiguous null, scalar, and array payloads.
type ToolCallArguments struct {
	encoded []byte
}

// ParseToolCallArguments validates and snapshots one bounded JSON object.
func ParseToolCallArguments(input []byte) (ToolCallArguments, error) {
	if len(input) > maxToolCallArgumentsBytes {
		return ToolCallArguments{}, fmt.Errorf("%w: payload exceeds %d bytes", ErrInvalidToolCallArguments, maxToolCallArgumentsBytes)
	}
	if len(bytes.TrimSpace(input)) == 0 {
		return ToolCallArguments{}, fmt.Errorf("%w: JSON object is required", ErrInvalidToolCallArguments)
	}

	decoder := json.NewDecoder(bytes.NewReader(input))
	decoder.UseNumber()
	var decoded any
	if err := decoder.Decode(&decoded); err != nil {
		return ToolCallArguments{}, fmt.Errorf("%w: %v", ErrInvalidToolCallArguments, err)
	}
	object, ok := decoded.(map[string]any)
	if !ok {
		return ToolCallArguments{}, fmt.Errorf("%w: top-level value must be an object", ErrInvalidToolCallArguments)
	}
	if err := requireJSONEOF(decoder); err != nil {
		return ToolCallArguments{}, err
	}

	encoded, err := json.Marshal(object)
	if err != nil {
		return ToolCallArguments{}, fmt.Errorf("%w: normalize object: %v", ErrInvalidToolCallArguments, err)
	}
	if len(encoded) > maxToolCallArgumentsBytes {
		return ToolCallArguments{}, fmt.Errorf("%w: normalized payload exceeds %d bytes", ErrInvalidToolCallArguments, maxToolCallArgumentsBytes)
	}
	return ToolCallArguments{encoded: encoded}, nil
}

func requireJSONEOF(decoder *json.Decoder) error {
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return fmt.Errorf("%w: multiple JSON values are not allowed", ErrInvalidToolCallArguments)
		}
		return fmt.Errorf("%w: trailing data: %v", ErrInvalidToolCallArguments, err)
	}
	return nil
}

// Bytes returns a copy of the normalized JSON object.
func (a ToolCallArguments) Bytes() []byte {
	return append([]byte(nil), a.encoded...)
}

func (a ToolCallArguments) MarshalJSON() ([]byte, error) {
	if len(a.encoded) == 0 {
		return nil, fmt.Errorf("%w: JSON object is required", ErrInvalidToolCallArguments)
	}
	return a.Bytes(), nil
}

func (a *ToolCallArguments) UnmarshalJSON(input []byte) error {
	if a == nil {
		return fmt.Errorf("%w: nil destination", ErrInvalidToolCallArguments)
	}
	parsed, err := ParseToolCallArguments(input)
	if err != nil {
		return err
	}
	a.encoded = parsed.Bytes()
	return nil
}

// ToolCallRequest is the transport-only input for a previously authorized MCP
// tool route. The routing layer remains responsible for project, provider,
// connection, mode, approval, and audit enforcement.
type ToolCallRequest struct {
	ServerID  string            `json:"server_id"`
	ToolName  string            `json:"tool_name"`
	Arguments ToolCallArguments `json:"arguments"`
}

func NewToolCallRequest(serverID, toolName string, arguments ToolCallArguments) (ToolCallRequest, error) {
	request := ToolCallRequest{
		ServerID:  serverID,
		ToolName:  toolName,
		Arguments: ToolCallArguments{encoded: arguments.Bytes()},
	}
	if err := request.Validate(); err != nil {
		return ToolCallRequest{}, err
	}
	return request, nil
}

func (r ToolCallRequest) Validate() error {
	if err := validateToolCallIdentifier("server_id", r.ServerID, false); err != nil {
		return err
	}
	if err := validateToolCallIdentifier("tool_name", r.ToolName, true); err != nil {
		return err
	}
	if _, err := r.Arguments.MarshalJSON(); err != nil {
		return fmt.Errorf("%w: arguments: %w", ErrInvalidToolCallRequest, err)
	}
	return nil
}

func validateToolCallIdentifier(name, value string, allowSlash bool) error {
	if strings.TrimSpace(value) == "" {
		return fmt.Errorf("%w: %s is required", ErrInvalidToolCallRequest, name)
	}
	if strings.TrimSpace(value) != value {
		return fmt.Errorf("%w: %s must not contain leading or trailing whitespace", ErrInvalidToolCallRequest, name)
	}
	if len(value) > maxToolCallIdentifierBytes {
		return fmt.Errorf("%w: %s exceeds %d bytes", ErrInvalidToolCallRequest, name, maxToolCallIdentifierBytes)
	}
	for i := 0; i < len(value); i++ {
		char := value[i]
		allowed := isASCIIAlpha(char) || (char >= '0' && char <= '9') || strings.ContainsRune("._-", rune(char))
		if allowSlash && char == '/' {
			allowed = true
		}
		if !allowed {
			return fmt.Errorf("%w: %s contains a character outside the ASCII identifier allowlist", ErrInvalidToolCallRequest, name)
		}
	}
	return nil
}

// ToolCallResult contains sanitized external output. Output remains untrusted
// even after redaction and must not be interpreted as instructions.
type ToolCallResult struct {
	Output          string `json:"output"`
	IsError         bool   `json:"is_error"`
	UntrustedOutput bool   `json:"untrusted_output"`
	Redacted        bool   `json:"redacted"`
	Truncated       bool   `json:"truncated"`
}

// NewToolCallResult scans only a bounded prefix plus a small lookahead. Known
// credential patterns are redacted before the final output bound is applied.
func NewToolCallResult(output []byte, isError bool) ToolCallResult {
	truncated := len(output) > maxToolCallOutputBytes
	processingLimit := maxToolCallOutputBytes + toolCallRedactionLookaheadBytes
	if len(output) > processingLimit {
		output = output[:processingLimit]
	}

	sanitized := strings.ToValidUTF8(string(output), "\uFFFD")
	redacted := false
	for _, pattern := range secretPatterns {
		replacement := pattern.ReplaceAllString(sanitized, "[REDACTED]")
		redacted = redacted || replacement != sanitized
		sanitized = replacement
	}
	var encodingTruncated bool
	sanitized, encodingTruncated = truncateUTF8(sanitized, maxToolCallOutputBytes)
	sanitized = strings.TrimSpace(sanitized)
	return ToolCallResult{
		Output:          sanitized,
		IsError:         isError,
		UntrustedOutput: true,
		Redacted:        redacted,
		Truncated:       truncated || encodingTruncated,
	}
}

func (r ToolCallResult) Validate() error {
	if !r.UntrustedOutput {
		return fmt.Errorf("%w: external output must remain untrusted", ErrInvalidToolCallResult)
	}
	if !utf8.ValidString(r.Output) {
		return fmt.Errorf("%w: output must be valid UTF-8", ErrInvalidToolCallResult)
	}
	if len(r.Output) > maxToolCallOutputBytes {
		return fmt.Errorf("%w: output exceeds %d bytes", ErrInvalidToolCallResult, maxToolCallOutputBytes)
	}
	for _, pattern := range secretPatterns {
		if pattern.MatchString(r.Output) {
			return fmt.Errorf("%w: output contains a recognized credential pattern", ErrInvalidToolCallResult)
		}
	}
	return nil
}

func truncateUTF8(value string, limit int) (string, bool) {
	if len(value) <= limit {
		return value, false
	}
	end := limit
	for end > 0 && !utf8.ValidString(value[:end]) {
		end--
	}
	return value[:end], true
}

// ToolInvoker is the transport boundary implemented by a later stdio MCP
// slice. Callers must authorize and audit the route before invoking it.
type ToolInvoker interface {
	InvokeTool(context.Context, ToolCallRequest) (ToolCallResult, error)
}
