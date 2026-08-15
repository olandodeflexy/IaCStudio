package mcpairlock

import (
	"context"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
)

var (
	// ErrExecutableApprovalUnavailable reports a fail-closed approval attempt.
	ErrExecutableApprovalUnavailable = errors.New("MCP executable approval unavailable")
	// ErrInvalidExecutableFingerprint reports a malformed approval precondition.
	ErrInvalidExecutableFingerprint = errors.New("invalid MCP executable fingerprint")
	// ErrExecutableFingerprintMismatch reports that the executable changed after
	// the caller observed it but before approval.
	ErrExecutableFingerprintMismatch = errors.New("MCP executable fingerprint changed before approval")
)

// ApproveExecutable records the executable currently resolved for one
// available server only when it matches the caller's observed fingerprint. It
// fingerprints the binary without starting the process.
func (m *Manager) ApproveExecutable(ctx context.Context, id string, expected ExecutableFingerprint) (ExecutableAttestation, error) {
	if m == nil || ctx == nil {
		return ExecutableAttestation{}, ErrExecutableApprovalUnavailable
	}
	if err := ctx.Err(); err != nil {
		return ExecutableAttestation{}, err
	}
	definition, ok := m.lookup(id)
	if !ok {
		return ExecutableAttestation{}, ErrUnknownServer
	}
	if err := validateExecutableFingerprint(expected); err != nil {
		return ExecutableAttestation{}, err
	}
	status := m.passiveStatus(definition)
	if status.State != "available" {
		return ExecutableAttestation{}, fmt.Errorf("%w: server is %s", ErrExecutableApprovalUnavailable, status.State)
	}
	resolvedCommand, err := resolveExecutable(definition.Command)
	if err != nil {
		return ExecutableAttestation{}, fmt.Errorf("%w: executable identity is unavailable", ErrExecutableApprovalUnavailable)
	}
	fingerprint, err := fingerprintExecutable(resolvedCommand)
	if err != nil {
		return ExecutableAttestation{}, fmt.Errorf("%w: executable identity is unavailable", ErrExecutableApprovalUnavailable)
	}
	if err := ctx.Err(); err != nil {
		return ExecutableAttestation{}, err
	}
	if !sameExecutableFingerprint(expected, fingerprint) {
		return ExecutableAttestation{}, ErrExecutableFingerprintMismatch
	}
	store, err := NewExecutableAttestationStore(m.projectsDir)
	if err != nil {
		return ExecutableAttestation{}, fmt.Errorf("%w: approval storage is unavailable", ErrExecutableApprovalUnavailable)
	}
	attestation := ExecutableAttestation{
		ServerID:     definition.ID,
		LaunchSource: definition.LaunchSource,
		Fingerprint:  fingerprint,
		ApprovedAt:   time.Now().UTC(),
	}
	if err := store.Save(attestation); err != nil {
		return ExecutableAttestation{}, executableApprovalPersistenceError(err)
	}
	return attestation, nil
}

func validateExecutableFingerprint(fingerprint ExecutableFingerprint) error {
	if fingerprint.Algorithm != "sha256" {
		return fmt.Errorf("%w: unsupported algorithm", ErrInvalidExecutableFingerprint)
	}
	if len(fingerprint.Digest) != hex.EncodedLen(executableDigestByteLength) ||
		fingerprint.Digest != strings.ToLower(fingerprint.Digest) {
		return fmt.Errorf("%w: malformed digest", ErrInvalidExecutableFingerprint)
	}
	digest, err := hex.DecodeString(fingerprint.Digest)
	if err != nil || len(digest) != executableDigestByteLength {
		return fmt.Errorf("%w: malformed digest", ErrInvalidExecutableFingerprint)
	}
	return nil
}

func sameExecutableFingerprint(expected, observed ExecutableFingerprint) bool {
	return expected.Algorithm == observed.Algorithm &&
		subtle.ConstantTimeCompare([]byte(expected.Digest), []byte(observed.Digest)) == 1
}

func executableApprovalPersistenceError(err error) error {
	detail := "approval storage write failed"
	switch {
	case errors.Is(err, ErrAttestationStorePathRequired):
		detail = "approval storage is not configured"
	case errors.Is(err, ErrInvalidAttestationStore):
		detail = "approval store is invalid, changed, or full"
	}
	return fmt.Errorf("%w: %s", ErrExecutableApprovalUnavailable, detail)
}
