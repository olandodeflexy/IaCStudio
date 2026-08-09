package agentrouting

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"strings"

	"github.com/iac-studio/iac-studio/internal/mcpairlock"
)

const (
	toolApprovalBindingPrefix      = "mcp-tool-approval-v1:"
	minToolApprovalBindingKeyBytes = 32
)

var ErrInvalidToolApprovalBinding = errors.New("invalid MCP tool approval binding")

// ToolApprovalBinding is an opaque, keyed fingerprint of one exact MCP tool
// operation. It is safe to persist with an approval gate because it contains
// neither route fields nor raw arguments.
type ToolApprovalBinding string

// NewToolApprovalBinding binds one run, scoped route, and normalized argument
// object. Callers must retain the key outside approval records and logs.
func NewToolApprovalBinding(
	key []byte,
	runID string,
	request Request,
	arguments mcpairlock.ToolCallArguments,
) (ToolApprovalBinding, error) {
	if len(key) < minToolApprovalBindingKeyBytes {
		return "", fmt.Errorf("%w: key must contain at least %d bytes", ErrInvalidToolApprovalBinding, minToolApprovalBindingKeyBytes)
	}
	if err := validateRequiredFields(ErrInvalidToolApprovalBinding, fieldValue{name: "run_id", value: runID}); err != nil {
		return "", err
	}
	if err := request.Validate(); err != nil {
		return "", fmt.Errorf("%w: request: %v", ErrInvalidToolApprovalBinding, err)
	}
	toolRequest, err := mcpairlock.NewToolCallRequest(request.ServerID, request.ToolName, arguments)
	if err != nil {
		return "", fmt.Errorf("%w: MCP tool request: %v", ErrInvalidToolApprovalBinding, err)
	}
	encodedArguments := toolRequest.Arguments.Bytes()

	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte(toolApprovalBindingPrefix))
	for _, field := range [][]byte{
		[]byte(runID),
		[]byte(request.Project),
		[]byte(request.ProviderID),
		[]byte(request.ConnectionID),
		[]byte(toolRequest.ServerID),
		[]byte(toolRequest.ToolName),
		[]byte(request.Mode),
		[]byte(request.Risk),
		encodedArguments,
	} {
		writeApprovalBindingField(mac, field)
	}
	return ToolApprovalBinding(toolApprovalBindingPrefix + hex.EncodeToString(mac.Sum(nil))), nil
}

// Validate checks the binding's versioned wire representation without
// asserting that it matches any operation.
func (b ToolApprovalBinding) Validate() error {
	value := string(b)
	wantLen := len(toolApprovalBindingPrefix) + sha256.Size*2
	if len(value) != wantLen || !strings.HasPrefix(value, toolApprovalBindingPrefix) {
		return fmt.Errorf("%w: malformed value", ErrInvalidToolApprovalBinding)
	}
	digest := strings.TrimPrefix(value, toolApprovalBindingPrefix)
	if digest != strings.ToLower(digest) {
		return fmt.Errorf("%w: digest must use lowercase hexadecimal", ErrInvalidToolApprovalBinding)
	}
	if _, err := hex.DecodeString(digest); err != nil {
		return fmt.Errorf("%w: malformed digest", ErrInvalidToolApprovalBinding)
	}
	return nil
}

// Matches reports whether this binding was created for the exact supplied
// operation. Invalid bindings, keys, requests, and arguments fail closed.
func (b ToolApprovalBinding) Matches(
	key []byte,
	runID string,
	request Request,
	arguments mcpairlock.ToolCallArguments,
) bool {
	if b.Validate() != nil {
		return false
	}
	expected, err := NewToolApprovalBinding(key, runID, request, arguments)
	if err != nil {
		return false
	}
	return hmac.Equal([]byte(b), []byte(expected))
}

func writeApprovalBindingField(mac hash.Hash, field []byte) {
	var size [8]byte
	binary.BigEndian.PutUint64(size[:], uint64(len(field)))
	_, _ = mac.Write(size[:])
	_, _ = mac.Write(field)
}
