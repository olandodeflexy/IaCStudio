package mcpairlock

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// ErrExecutableApprovalUnavailable reports a fail-closed approval attempt.
var ErrExecutableApprovalUnavailable = errors.New("MCP executable approval unavailable")

// ApproveExecutable records the executable currently resolved for one
// available server. It fingerprints the binary without starting the process.
func (m *Manager) ApproveExecutable(ctx context.Context, id string) (ExecutableAttestation, error) {
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
	status := m.passiveStatus(definition)
	if status.State != "available" {
		return ExecutableAttestation{}, fmt.Errorf("%w: server is %s", ErrExecutableApprovalUnavailable, status.State)
	}
	store, err := NewExecutableAttestationStore(m.projectsDir)
	if err != nil {
		return ExecutableAttestation{}, fmt.Errorf("%w: approval storage is unavailable", ErrExecutableApprovalUnavailable)
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
	attestation := ExecutableAttestation{
		ServerID:     definition.ID,
		LaunchSource: definition.LaunchSource,
		Fingerprint:  fingerprint,
		ApprovedAt:   time.Now().UTC(),
	}
	if err := store.Save(attestation); err != nil {
		return ExecutableAttestation{}, fmt.Errorf("%w: approval could not be persisted", ErrExecutableApprovalUnavailable)
	}
	return attestation, nil
}
