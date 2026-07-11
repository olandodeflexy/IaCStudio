package agentrouting

import (
	"errors"
	"fmt"
	"strings"

	"github.com/iac-studio/iac-studio/internal/agentruns"
	"github.com/iac-studio/iac-studio/internal/mcpairlock"
)

var (
	ErrRunStoreRequired = errors.New("agent run store is required")
	ErrRunIDRequired    = errors.New("agent run id is required")
	ErrRunScopeMismatch = errors.New("agent run scope does not match tool route")
)

// RunRecorder persists authorization outcomes without invoking an external
// tool. The referenced run must already exist.
type RunRecorder struct {
	store *agentruns.Store
}

func NewRunRecorder(store *agentruns.Store) (*RunRecorder, error) {
	if store == nil {
		return nil, ErrRunStoreRequired
	}
	return &RunRecorder{store: store}, nil
}

// Record verifies the run and route scopes before applying exactly one state
// mutation for the authorization outcome.
func (r *RunRecorder) Record(runID string, request Request, decision Decision) (agentruns.Run, error) {
	if r == nil || r.store == nil {
		return agentruns.Run{}, ErrRunStoreRequired
	}
	trimmedRunID := strings.TrimSpace(runID)
	if trimmedRunID == "" || trimmedRunID != runID {
		return agentruns.Run{}, ErrRunIDRequired
	}
	if err := request.Validate(); err != nil {
		return agentruns.Run{}, err
	}
	if err := decision.Validate(); err != nil {
		return agentruns.Run{}, err
	}
	if decision.Status != DecisionDenied && !modeAllowsRisk(request.Mode, request.Risk) {
		return agentruns.Run{}, fmt.Errorf("%w: mode %q cannot authorize risk %q", ErrInvalidRequest, request.Mode, request.Risk)
	}
	if decision.Status == DecisionAllowed && request.Risk != mcpairlock.RiskReadOnly {
		return agentruns.Run{}, fmt.Errorf("%w: non-read-only risks require approval", ErrInvalidDecision)
	}

	run, ok := r.store.Get(runID)
	if !ok {
		return agentruns.Run{}, agentruns.ErrNotFound
	}
	if run.Project != request.Project || run.ProviderID != request.ProviderID || run.Mode != request.Mode {
		return agentruns.Run{}, ErrRunScopeMismatch
	}

	switch decision.Status {
	case DecisionDenied:
		return r.store.Fail(runID, fmt.Sprintf(
			"MCP tool %q on server %q for connection %q authorization denied (%s risk, %s mode): %s.",
			request.ToolName, request.ServerID, request.ConnectionID,
			request.Risk, request.Mode, decision.Reason,
		))
	case DecisionApprovalRequired:
		kind, ok := approvalKind(request.Risk)
		if !ok {
			return agentruns.Run{}, fmt.Errorf("%w: no approval mapping for risk %q", ErrInvalidDecision, request.Risk)
		}
		return r.store.AddApproval(runID, agentruns.ApprovalGate{
			Kind:    kind,
			Summary: routeAuditMessage("Authorize", request),
		})
	case DecisionAllowed:
		return r.store.AddLog(runID, agentruns.LogAudit, routeAuditMessage("Allowed", request))
	default:
		return agentruns.Run{}, ErrInvalidDecision
	}
}

func approvalKind(risk mcpairlock.ToolRisk) (agentruns.ApprovalKind, bool) {
	switch risk {
	case mcpairlock.RiskReadOnly, mcpairlock.RiskGenerateCode:
		return agentruns.ApprovalMCPNetwork, true
	case mcpairlock.RiskModifyWorkspace:
		return agentruns.ApprovalFileWrite, true
	case mcpairlock.RiskCloudMutation:
		return agentruns.ApprovalCloudWrite, true
	case mcpairlock.RiskSecretSensitive:
		return agentruns.ApprovalSecretRead, true
	case mcpairlock.RiskDestructive:
		return agentruns.ApprovalIaCAction, true
	default:
		return "", false
	}
}

func routeAuditMessage(action string, request Request) string {
	return fmt.Sprintf(
		"%s MCP tool %q on server %q for connection %q (%s risk, %s mode).",
		action,
		request.ToolName,
		request.ServerID,
		request.ConnectionID,
		request.Risk,
		request.Mode,
	)
}
