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
	ErrInvalidRunID     = errors.New("agent run id must not contain leading or trailing whitespace")
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
	return r.record(runID, request, decision, agentruns.ApprovalBinding{})
}

// RecordBoundApproval creates and stores an exact-operation binding for an
// approval-required route. The key and raw arguments are never retained.
func (r *RunRecorder) RecordBoundApproval(
	key []byte,
	runID string,
	request Request,
	arguments mcpairlock.ToolCallArguments,
	decision Decision,
) (agentruns.Run, error) {
	if r == nil || r.store == nil {
		return agentruns.Run{}, ErrRunStoreRequired
	}
	if err := validateRunID(runID); err != nil {
		return agentruns.Run{}, err
	}
	if err := request.Validate(); err != nil {
		return agentruns.Run{}, err
	}
	if err := decision.Validate(); err != nil {
		return agentruns.Run{}, err
	}
	if decision.Status != DecisionApprovalRequired {
		return agentruns.Run{}, fmt.Errorf("%w: bound operation requires an approval decision", ErrInvalidDecision)
	}
	binding, err := NewToolApprovalBinding(key, runID, request, arguments)
	if err != nil {
		return agentruns.Run{}, err
	}
	storedBinding, err := agentruns.NewApprovalBinding(string(binding))
	if err != nil {
		return agentruns.Run{}, fmt.Errorf("store tool approval binding: %w", err)
	}
	return r.record(runID, request, decision, storedBinding)
}

// RecordApprovedToolCall consumes and records an allowed route only when the
// run already contains an approved gate bound to this exact MCP operation.
func (r *RunRecorder) RecordApprovedToolCall(
	key []byte,
	runID string,
	request Request,
	arguments mcpairlock.ToolCallArguments,
) (agentruns.Run, bool, error) {
	if r == nil || r.store == nil {
		return agentruns.Run{}, false, ErrRunStoreRequired
	}
	if err := validateRunID(runID); err != nil {
		return agentruns.Run{}, false, err
	}
	if err := request.Validate(); err != nil {
		return agentruns.Run{}, false, err
	}
	if _, err := NewToolApprovalBinding(key, runID, request, arguments); err != nil {
		return agentruns.Run{}, false, err
	}

	run, ok := r.store.Get(runID)
	if !ok {
		return agentruns.Run{}, false, agentruns.ErrNotFound
	}
	if run.Project != request.Project || run.ProviderID != request.ProviderID || run.Mode != request.Mode {
		return agentruns.Run{}, false, ErrRunScopeMismatch
	}
	for _, approval := range run.Approvals {
		if approval.Status != agentruns.ApprovalApproved {
			continue
		}
		if !ToolApprovalBinding(approval.OperationBinding.Value()).Matches(key, runID, request, arguments) {
			continue
		}
		recorded, err := r.store.ConsumeApprovalBinding(runID, approval.ID, routeAuditMessage("Allowed", request))
		if errors.Is(err, agentruns.ErrApprovalPending) {
			return agentruns.Run{}, false, fmt.Errorf("%w: cannot record authorization while approval gates are pending", ErrInvalidDecision)
		}
		return recorded, true, err
	}
	return agentruns.Run{}, false, nil
}

func (r *RunRecorder) record(
	runID string,
	request Request,
	decision Decision,
	operationBinding agentruns.ApprovalBinding,
) (agentruns.Run, error) {
	if r == nil || r.store == nil {
		return agentruns.Run{}, ErrRunStoreRequired
	}
	if err := validateRunID(runID); err != nil {
		return agentruns.Run{}, err
	}
	if err := request.Validate(); err != nil {
		return agentruns.Run{}, err
	}
	if err := decision.Validate(); err != nil {
		return agentruns.Run{}, err
	}
	if decision.Status == DecisionDenied && decision.Reason == ReasonInvalidRequest {
		return agentruns.Run{}, fmt.Errorf("%w: reason %q requires an invalid request", ErrInvalidDecision, ReasonInvalidRequest)
	}
	modeRiskAllowed := modeAllowsRisk(request.Mode, request.Risk)
	if !modeRiskAllowed {
		if decision.Status != DecisionDenied {
			return agentruns.Run{}, fmt.Errorf("%w: mode %q cannot authorize risk %q", ErrInvalidRequest, request.Mode, request.Risk)
		}
		if decision.Reason != ReasonModeRiskMismatch {
			return agentruns.Run{}, fmt.Errorf("%w: unsafe mode and risk require reason %q", ErrInvalidDecision, ReasonModeRiskMismatch)
		}
	} else if decision.Status == DecisionDenied && decision.Reason == ReasonModeRiskMismatch {
		return agentruns.Run{}, fmt.Errorf("%w: reason %q requires an unsafe mode and risk pair", ErrInvalidDecision, ReasonModeRiskMismatch)
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
			Kind:             kind,
			Summary:          routeAuditMessage("Authorize", request),
			OperationBinding: operationBinding,
		})
	case DecisionAllowed:
		recorded, err := r.store.AddLogIfNoPendingApprovals(runID, agentruns.LogAudit, routeAuditMessage("Allowed", request))
		if errors.Is(err, agentruns.ErrApprovalPending) {
			return agentruns.Run{}, fmt.Errorf("%w: cannot record authorization while approval gates are pending", ErrInvalidDecision)
		}
		return recorded, err
	default:
		return agentruns.Run{}, ErrInvalidDecision
	}
}

func validateRunID(runID string) error {
	trimmedRunID := strings.TrimSpace(runID)
	if trimmedRunID == "" {
		return ErrRunIDRequired
	}
	if trimmedRunID != runID {
		return ErrInvalidRunID
	}
	return nil
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
