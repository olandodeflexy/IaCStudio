package agentrouting

import (
	"errors"
	"strings"
	"testing"

	"github.com/iac-studio/iac-studio/internal/agentruns"
	"github.com/iac-studio/iac-studio/internal/mcpairlock"
)

func recorderFixture(t *testing.T, request Request) (*RunRecorder, *agentruns.Store, agentruns.Run) {
	t.Helper()
	store := agentruns.NewStore(agentruns.WithPromptHashKey([]byte("recorder-test-key")))
	run, err := store.Create(agentruns.CreateRequest{
		Project:    request.Project,
		ProviderID: request.ProviderID,
		Mode:       request.Mode,
		Prompt:     "authorize a tool",
	})
	if err != nil {
		t.Fatalf("Create(): %v", err)
	}
	recorder, err := NewRunRecorder(store)
	if err != nil {
		t.Fatalf("NewRunRecorder(): %v", err)
	}
	return recorder, store, run
}

func TestRunRecorderRecordsAllowedAudit(t *testing.T) {
	_, request, _ := readOnlyEvaluation()
	recorder, _, run := recorderFixture(t, request)

	got, err := recorder.Record(run.ID, request, allowed())
	if err != nil {
		t.Fatalf("Record(): %v", err)
	}
	if got.Status != agentruns.StatusQueued || len(got.Logs) != 1 || got.Logs[0].Level != agentruns.LogAudit || len(got.Approvals) != 0 {
		t.Fatalf("recorded run = %+v, want one allowed audit entry", got)
	}
	if !strings.Contains(got.Logs[0].Message, "Allowed MCP tool") {
		t.Fatalf("audit message = %q, want allowed tool event", got.Logs[0].Message)
	}
}

func TestRunRecorderFailsDeniedRun(t *testing.T) {
	_, request, _ := readOnlyEvaluation()
	recorder, _, run := recorderFixture(t, request)

	got, err := recorder.Record(run.ID, request, denied(ReasonPolicyDenied))
	if err != nil {
		t.Fatalf("Record(): %v", err)
	}
	wantError := `MCP tool "plan_workspace" on server "terraform-official" for connection "aws-prod" authorization denied (read_only risk, read_only mode): policy_denied.`
	if got.Status != agentruns.StatusFailed || got.Error != wantError || len(got.Logs) != 1 || got.Logs[0].Level != agentruns.LogAudit {
		t.Fatalf("recorded run = %+v, want failed run with denial audit", got)
	}
}

func TestRunRecorderRecordsUnsafeModeDenial(t *testing.T) {
	request := validRequest()
	request.Mode = agentruns.ModeReadOnly
	request.Risk = mcpairlock.RiskCloudMutation
	recorder, _, run := recorderFixture(t, request)

	got, err := recorder.Record(run.ID, request, denied(ReasonModeRiskMismatch))
	if err != nil {
		t.Fatalf("Record(): %v", err)
	}
	wantError := `MCP tool "plan_workspace" on server "terraform-official" for connection "aws-prod" authorization denied (cloud_mutation risk, read_only mode): mode_risk_mismatch.`
	if got.Status != agentruns.StatusFailed || got.Error != wantError || len(got.Logs) != 1 {
		t.Fatalf("recorded run = %+v, want unsafe mode denial audit", got)
	}
}

func TestRunRecorderRejectsMisleadingUnsafeDenialWithoutMutation(t *testing.T) {
	request := validRequest()
	request.Mode = agentruns.ModeReadOnly
	request.Risk = mcpairlock.RiskCloudMutation
	recorder, store, run := recorderFixture(t, request)

	if _, err := recorder.Record(run.ID, request, denied(ReasonPolicyDenied)); !errors.Is(err, ErrInvalidDecision) {
		t.Fatalf("Record(misleading denial) error = %v, want ErrInvalidDecision", err)
	}
	unchanged, ok := store.Get(run.ID)
	if !ok || unchanged.Status != agentruns.StatusQueued || len(unchanged.Logs) != 0 || len(unchanged.Approvals) != 0 {
		t.Fatalf("run mutated after misleading denial: %+v", unchanged)
	}
}

func TestRunRecorderRejectsContradictoryModeMismatchReasonWithoutMutation(t *testing.T) {
	_, request, _ := readOnlyEvaluation()
	recorder, store, run := recorderFixture(t, request)

	if _, err := recorder.Record(run.ID, request, denied(ReasonModeRiskMismatch)); !errors.Is(err, ErrInvalidDecision) {
		t.Fatalf("Record(contradictory mode mismatch) error = %v, want ErrInvalidDecision", err)
	}
	unchanged, ok := store.Get(run.ID)
	if !ok || unchanged.Status != agentruns.StatusQueued || len(unchanged.Logs) != 0 || len(unchanged.Approvals) != 0 {
		t.Fatalf("run mutated after contradictory mode mismatch denial: %+v", unchanged)
	}
}

func TestRunRecorderRejectsContradictoryInvalidRequestReasonWithoutMutation(t *testing.T) {
	_, request, _ := readOnlyEvaluation()
	recorder, store, run := recorderFixture(t, request)

	if _, err := recorder.Record(run.ID, request, denied(ReasonInvalidRequest)); !errors.Is(err, ErrInvalidDecision) {
		t.Fatalf("Record(contradictory invalid_request) error = %v, want ErrInvalidDecision", err)
	}
	unchanged, ok := store.Get(run.ID)
	if !ok || unchanged.Status != agentruns.StatusQueued || len(unchanged.Logs) != 0 || len(unchanged.Approvals) != 0 {
		t.Fatalf("run mutated after contradictory invalid_request denial: %+v", unchanged)
	}
}

func TestRunRecorderMapsApprovalKinds(t *testing.T) {
	tests := []struct {
		name string
		mode agentruns.Mode
		risk mcpairlock.ToolRisk
		kind agentruns.ApprovalKind
	}{
		{name: "read only", mode: agentruns.ModeReadOnly, risk: mcpairlock.RiskReadOnly, kind: agentruns.ApprovalMCPNetwork},
		{name: "generate code", mode: agentruns.ModeProposeOnly, risk: mcpairlock.RiskGenerateCode, kind: agentruns.ApprovalMCPNetwork},
		{name: "workspace", mode: agentruns.ModeApprovedExecute, risk: mcpairlock.RiskModifyWorkspace, kind: agentruns.ApprovalFileWrite},
		{name: "cloud", mode: agentruns.ModeApprovedExecute, risk: mcpairlock.RiskCloudMutation, kind: agentruns.ApprovalCloudWrite},
		{name: "secret", mode: agentruns.ModeApprovedExecute, risk: mcpairlock.RiskSecretSensitive, kind: agentruns.ApprovalSecretRead},
		{name: "destructive", mode: agentruns.ModeApprovedExecute, risk: mcpairlock.RiskDestructive, kind: agentruns.ApprovalIaCAction},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := validRequest()
			request.Mode = test.mode
			request.Risk = test.risk
			recorder, _, run := recorderFixture(t, request)

			got, err := recorder.Record(run.ID, request, approvalRequired())
			if err != nil {
				t.Fatalf("Record(): %v", err)
			}
			if got.Status != agentruns.StatusWaitingApproval || len(got.Approvals) != 1 || got.Approvals[0].Kind != test.kind || got.Approvals[0].Status != agentruns.ApprovalPending {
				t.Fatalf("recorded run = %+v, want pending %q gate", got, test.kind)
			}
		})
	}
}

func TestRunRecorderRejectsScopeMismatchWithoutMutation(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Request)
	}{
		{name: "project", mutate: func(request *Request) { request.Project = "other-project" }},
		{name: "provider", mutate: func(request *Request) { request.ProviderID = "other-provider" }},
		{name: "mode", mutate: func(request *Request) { request.Mode = agentruns.ModeApprovedExecute }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, request, _ := readOnlyEvaluation()
			recorder, store, run := recorderFixture(t, request)
			test.mutate(&request)

			if _, err := recorder.Record(run.ID, request, allowed()); !errors.Is(err, ErrRunScopeMismatch) {
				t.Fatalf("Record() error = %v, want ErrRunScopeMismatch", err)
			}
			unchanged, ok := store.Get(run.ID)
			if !ok || unchanged.Status != agentruns.StatusQueued || len(unchanged.Logs) != 0 || len(unchanged.Approvals) != 0 {
				t.Fatalf("run mutated after scope mismatch: %+v", unchanged)
			}
		})
	}
}

func TestRunRecorderRejectsMalformedRequestWithoutMutation(t *testing.T) {
	_, request, _ := readOnlyEvaluation()
	recorder, store, run := recorderFixture(t, request)
	request.ToolName = ""

	if _, err := recorder.Record(run.ID, request, allowed()); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("Record(malformed request) error = %v, want ErrInvalidRequest", err)
	}
	unchanged, ok := store.Get(run.ID)
	if !ok || unchanged.Status != agentruns.StatusQueued || len(unchanged.Logs) != 0 || len(unchanged.Approvals) != 0 {
		t.Fatalf("run mutated after malformed request: %+v", unchanged)
	}
}

func TestRunRecorderRejectsMalformedDecisionWithoutMutation(t *testing.T) {
	_, request, _ := readOnlyEvaluation()
	tests := []Decision{
		{Status: DecisionAllowed, Reason: ReasonAllowed, UntrustedOutput: true},
		{Status: DecisionApprovalRequired, Reason: ReasonApprovalRequired, Allowed: true, ApprovalRequired: true, UntrustedOutput: true},
		{Status: DecisionDenied, Reason: ReasonAllowed, UntrustedOutput: true},
		{Status: DecisionDenied, Reason: ReasonPolicyDenied},
	}
	for _, decision := range tests {
		recorder, store, run := recorderFixture(t, request)
		if _, err := recorder.Record(run.ID, request, decision); !errors.Is(err, ErrInvalidDecision) {
			t.Fatalf("Record(%+v) error = %v, want ErrInvalidDecision", decision, err)
		}
		unchanged, ok := store.Get(run.ID)
		if !ok || unchanged.Status != agentruns.StatusQueued || len(unchanged.Logs) != 0 || len(unchanged.Approvals) != 0 {
			t.Fatalf("run mutated after malformed decision: %+v", unchanged)
		}
	}
}

func TestRunRecorderRejectsAllowedWriteWithoutMutation(t *testing.T) {
	request := validRequest()
	recorder, store, run := recorderFixture(t, request)

	if _, err := recorder.Record(run.ID, request, allowed()); !errors.Is(err, ErrInvalidDecision) {
		t.Fatalf("Record(allowed write) error = %v, want ErrInvalidDecision", err)
	}
	unchanged, ok := store.Get(run.ID)
	if !ok || unchanged.Status != agentruns.StatusQueued || len(unchanged.Logs) != 0 || len(unchanged.Approvals) != 0 {
		t.Fatalf("run mutated after allowed write: %+v", unchanged)
	}
}

func TestRunRecorderRejectsUnsafeApprovalWithoutMutation(t *testing.T) {
	request := validRequest()
	request.Mode = agentruns.ModeReadOnly
	request.Risk = mcpairlock.RiskCloudMutation
	recorder, store, run := recorderFixture(t, request)

	if _, err := recorder.Record(run.ID, request, approvalRequired()); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("Record(unsafe approval) error = %v, want ErrInvalidRequest", err)
	}
	unchanged, ok := store.Get(run.ID)
	if !ok || unchanged.Status != agentruns.StatusQueued || len(unchanged.Logs) != 0 || len(unchanged.Approvals) != 0 {
		t.Fatalf("run mutated after unsafe approval: %+v", unchanged)
	}
}

func TestRunRecorderRejectsMissingDependenciesAndRuns(t *testing.T) {
	_, request, _ := readOnlyEvaluation()
	if _, err := NewRunRecorder(nil); !errors.Is(err, ErrRunStoreRequired) {
		t.Fatalf("NewRunRecorder(nil) error = %v, want ErrRunStoreRequired", err)
	}
	var nilRecorder *RunRecorder
	if _, err := nilRecorder.Record("run_000001", request, allowed()); !errors.Is(err, ErrRunStoreRequired) {
		t.Fatalf("nil Record() error = %v, want ErrRunStoreRequired", err)
	}

	recorder, _, _ := recorderFixture(t, request)
	if _, err := recorder.Record("", request, allowed()); !errors.Is(err, ErrRunIDRequired) {
		t.Fatalf("Record(empty id) error = %v, want ErrRunIDRequired", err)
	}
	if _, err := recorder.Record(" run_000001", request, allowed()); !errors.Is(err, ErrInvalidRunID) {
		t.Fatalf("Record(padded id) error = %v, want ErrInvalidRunID", err)
	}
	if _, err := recorder.Record("run_missing", request, allowed()); !errors.Is(err, agentruns.ErrNotFound) {
		t.Fatalf("Record(missing run) error = %v, want ErrNotFound", err)
	}
}

func TestRunRecorderRejectsAllowedOnWaitingApprovalRunWithoutMutation(t *testing.T) {
	_, request, _ := readOnlyEvaluation()
	recorder, store, run := recorderFixture(t, request)

	if _, err := recorder.Record(run.ID, request, approvalRequired()); err != nil {
		t.Fatalf("Record(approval_required): %v", err)
	}
	waiting, ok := store.Get(run.ID)
	if !ok || waiting.Status != agentruns.StatusWaitingApproval {
		t.Fatalf("run not in waiting_approval after approval gate: %+v", waiting)
	}

	if _, err := recorder.Record(run.ID, request, allowed()); !errors.Is(err, ErrInvalidDecision) {
		t.Fatalf("Record(allowed on waiting_approval run) error = %v, want ErrInvalidDecision", err)
	}
	unchanged, ok := store.Get(run.ID)
	if !ok || unchanged.Status != agentruns.StatusWaitingApproval || len(unchanged.Logs) != 0 || len(unchanged.Approvals) != 1 {
		t.Fatalf("run mutated after allowed on waiting_approval run: %+v", unchanged)
	}
}

func TestRunRecorderRejectsTerminalRunWithoutDuplicateMutation(t *testing.T) {
	_, request, _ := readOnlyEvaluation()
	outcomes := []Decision{
		allowed(),
		denied(ReasonPolicyDenied),
		approvalRequired(),
	}
	for _, outcome := range outcomes {
		t.Run(string(outcome.Status), func(t *testing.T) {
			recorder, store, run := recorderFixture(t, request)
			if _, err := store.Fail(run.ID, "pre-terminal"); err != nil {
				t.Fatalf("Fail(): %v", err)
			}
			if _, err := recorder.Record(run.ID, request, outcome); !errors.Is(err, agentruns.ErrTerminated) {
				t.Fatalf("Record(terminal run, %s) error = %v, want ErrTerminated", outcome.Status, err)
			}
			terminal, ok := store.Get(run.ID)
			if !ok {
				t.Fatal("run evicted unexpectedly")
			}
			if terminal.Status != agentruns.StatusFailed || len(terminal.Logs) != 1 || len(terminal.Approvals) != 0 {
				t.Fatalf("terminal run mutated after Record(): %+v", terminal)
			}
		})
	}
}
