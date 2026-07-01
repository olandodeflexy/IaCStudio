package agentruns

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestStoreCreateRedactsPromptAndDefaultsReadOnly(t *testing.T) {
	now := fixedClock()
	store := NewStore(WithClock(now.now))

	run, err := store.Create(CreateRequest{
		Project:    "prod",
		Prompt:     "rotate password=supersecret for AKIA1234567890ABCDEF",
		ProviderID: "codex",
		CreatedBy:  "alice",
	})
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}

	if run.ID != "run_000001" {
		t.Fatalf("run ID = %q, want run_000001", run.ID)
	}
	if run.Mode != ModeReadOnly {
		t.Fatalf("mode = %q, want %q", run.Mode, ModeReadOnly)
	}
	if run.Status != StatusQueued {
		t.Fatalf("status = %q, want %q", run.Status, StatusQueued)
	}
	if strings.Contains(run.PromptPreview, "supersecret") || strings.Contains(run.PromptPreview, "AKIA1234567890ABCDEF") {
		t.Fatalf("prompt preview leaked secret: %q", run.PromptPreview)
	}
	if run.PromptHash == "" || strings.Contains(run.PromptHash, "supersecret") {
		t.Fatalf("prompt hash was not set safely: %q", run.PromptHash)
	}
	if run.CreatedAt != now.current || run.UpdatedAt != now.current {
		t.Fatalf("timestamps = %s/%s, want %s", run.CreatedAt, run.UpdatedAt, now.current)
	}
	if run.Logs == nil || run.Patches == nil || run.Approvals == nil {
		t.Fatalf("list fields should be initialized: %+v", run)
	}
}

func TestStoreRedactsQuotedSecretsWithSpaces(t *testing.T) {
	store := NewStore(WithClock(fixedClock().now))
	run, err := store.Create(CreateRequest{
		Project: "prod",
		Prompt:  `rotate password="two word secret" and token='another secret phrase'`,
	})
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}
	if strings.Contains(run.PromptPreview, "two word secret") || strings.Contains(run.PromptPreview, "another secret phrase") {
		t.Fatalf("prompt preview leaked quoted secret: %q", run.PromptPreview)
	}

	run, err = store.AddLog(run.ID, LogInfo, `using api_key="local model secret"`)
	if err != nil {
		t.Fatalf("AddLog returned error: %v", err)
	}
	if strings.Contains(run.Logs[0].Message, "local model secret") {
		t.Fatalf("log leaked quoted secret: %q", run.Logs[0].Message)
	}

	run, err = store.AddPatch(run.ID, ProposedPatch{
		Path: "main.tf",
		Diff: `+ access_key = "quoted cloud secret"`,
	})
	if err != nil {
		t.Fatalf("AddPatch returned error: %v", err)
	}
	if strings.Contains(run.Patches[0].Diff, "quoted cloud secret") {
		t.Fatalf("patch leaked quoted secret: %q", run.Patches[0].Diff)
	}
}

func TestStoreLifecycleUpdates(t *testing.T) {
	clock := fixedClock()
	store := NewStore(WithClock(clock.now))
	run, err := store.Create(CreateRequest{Project: "prod", Prompt: "make a plan", Mode: ModeProposeOnly})
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}

	clock.tick(time.Second)
	run, err = store.SetStatus(run.ID, StatusRunning)
	if err != nil {
		t.Fatalf("SetStatus running returned error: %v", err)
	}
	if run.Status != StatusRunning || run.StartedAt == nil || *run.StartedAt != clock.current {
		t.Fatalf("running state not recorded: %+v", run)
	}

	clock.tick(time.Second)
	run, err = store.AddLog(run.ID, LogInfo, "using token=abc123")
	if err != nil {
		t.Fatalf("AddLog returned error: %v", err)
	}
	if len(run.Logs) != 1 || run.Logs[0].ID != "log_000001" {
		t.Fatalf("unexpected logs: %+v", run.Logs)
	}
	if strings.Contains(run.Logs[0].Message, "abc123") {
		t.Fatalf("log leaked token: %q", run.Logs[0].Message)
	}

	clock.tick(time.Second)
	run, err = store.AddPatch(run.ID, ProposedPatch{
		Path:    "main.tf",
		Summary: "add bucket",
		Diff:    "+ secret = \"dont-store-me\"",
	})
	if err != nil {
		t.Fatalf("AddPatch returned error: %v", err)
	}
	if len(run.Patches) != 1 || run.Patches[0].ID != "patch_000001" {
		t.Fatalf("unexpected patches: %+v", run.Patches)
	}
	if strings.Contains(run.Patches[0].Diff, "dont-store-me") {
		t.Fatalf("patch diff leaked secret: %q", run.Patches[0].Diff)
	}

	clock.tick(time.Second)
	run, err = store.AddApproval(run.ID, ApprovalGate{
		Kind:    ApprovalCommand,
		Summary: "run terraform plan",
	})
	if err != nil {
		t.Fatalf("AddApproval returned error: %v", err)
	}
	if run.Status != StatusWaitingApproval || len(run.Approvals) != 1 || run.Approvals[0].Status != ApprovalPending {
		t.Fatalf("approval state not recorded: %+v", run)
	}

	clock.tick(time.Second)
	run, err = store.Cancel(run.ID)
	if err != nil {
		t.Fatalf("Cancel returned error: %v", err)
	}
	if !run.Canceled || run.Status != StatusCanceled || run.CompletedAt == nil || *run.CompletedAt != clock.current {
		t.Fatalf("cancel state not recorded: %+v", run)
	}
}

func TestStoreReturnsDefensiveCopies(t *testing.T) {
	store := NewStore(WithClock(fixedClock().now))
	run, err := store.Create(CreateRequest{Project: "prod", Prompt: "make a plan"})
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}
	run, err = store.AddLog(run.ID, LogInfo, "first")
	if err != nil {
		t.Fatalf("AddLog returned error: %v", err)
	}

	run.Logs[0].Message = "mutated"
	got, ok := store.Get(run.ID)
	if !ok {
		t.Fatal("expected run to exist")
	}
	if got.Logs[0].Message == "mutated" {
		t.Fatal("store returned mutable log slice")
	}

	list := store.List()
	list[0].Logs[0].Message = "mutated-list"
	got, ok = store.Get(run.ID)
	if !ok {
		t.Fatal("expected run to exist")
	}
	if got.Logs[0].Message == "mutated-list" {
		t.Fatal("store returned mutable list snapshot")
	}
}

func TestStoreValidatesInputsAndEvictsOldRuns(t *testing.T) {
	store := NewStore(WithClock(fixedClock().now), WithMaxRuns(2))
	if _, err := store.Create(CreateRequest{Project: "", Prompt: "x"}); err == nil {
		t.Fatal("expected missing project error")
	}
	if _, err := store.Create(CreateRequest{Project: "prod", Prompt: ""}); err == nil {
		t.Fatal("expected missing prompt error")
	}
	if _, err := store.Create(CreateRequest{Project: "prod", Prompt: "x", Mode: "unsafe"}); err == nil {
		t.Fatal("expected invalid mode error")
	}

	first, err := store.Create(CreateRequest{Project: "prod", Prompt: "first"})
	if err != nil {
		t.Fatalf("Create first returned error: %v", err)
	}
	if _, err := store.Create(CreateRequest{Project: "prod", Prompt: "second"}); err != nil {
		t.Fatalf("Create second returned error: %v", err)
	}
	third, err := store.Create(CreateRequest{Project: "prod", Prompt: "third"})
	if err != nil {
		t.Fatalf("Create third returned error: %v", err)
	}
	if _, ok := store.Get(first.ID); ok {
		t.Fatal("oldest run should have been evicted")
	}
	if runs := store.List(); len(runs) != 2 || runs[1].ID != third.ID {
		t.Fatalf("unexpected retained runs: %+v", runs)
	}

	if _, err := store.AddLog("missing", LogInfo, "x"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing run error = %v, want ErrNotFound", err)
	}
}

func TestStoreTerminalStateGuard(t *testing.T) {
	store := NewStore(WithClock(fixedClock().now))
	run, err := store.Create(CreateRequest{Project: "prod", Prompt: "x"})
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}
	if _, err := store.SetStatus(run.ID, StatusRunning); err != nil {
		t.Fatalf("SetStatus running: %v", err)
	}
	run, err = store.AddApproval(run.ID, ApprovalGate{Kind: ApprovalCommand, Summary: "x"})
	if err != nil {
		t.Fatalf("AddApproval: %v", err)
	}
	approvalID := run.Approvals[0].ID
	if _, err := store.SetStatus(run.ID, StatusCompleted); err != nil {
		t.Fatalf("SetStatus completed: %v", err)
	}

	if _, err := store.SetStatus(run.ID, StatusRunning); !errors.Is(err, ErrTerminated) {
		t.Fatalf("SetStatus on completed run: got %v, want ErrTerminated", err)
	}
	if _, err := store.Cancel(run.ID); !errors.Is(err, ErrTerminated) {
		t.Fatalf("Cancel on completed run: got %v, want ErrTerminated", err)
	}
	if _, err := store.Fail(run.ID, "late fail"); !errors.Is(err, ErrTerminated) {
		t.Fatalf("Fail on completed run: got %v, want ErrTerminated", err)
	}
	if _, err := store.AddLog(run.ID, LogInfo, "late log"); !errors.Is(err, ErrTerminated) {
		t.Fatalf("AddLog on completed run: got %v, want ErrTerminated", err)
	}
	if _, err := store.AddApproval(run.ID, ApprovalGate{Kind: ApprovalCommand, Summary: "x"}); !errors.Is(err, ErrTerminated) {
		t.Fatalf("AddApproval on completed run: got %v, want ErrTerminated", err)
	}
	if _, err := store.AddPatch(run.ID, ProposedPatch{Path: "x.tf", Summary: "s", Diff: "d"}); !errors.Is(err, ErrTerminated) {
		t.Fatalf("AddPatch on completed run: got %v, want ErrTerminated", err)
	}
	if _, err := store.DecideApproval(run.ID, approvalID, ApprovalApproved, "alice"); !errors.Is(err, ErrTerminated) {
		t.Fatalf("DecideApproval on completed run: got %v, want ErrTerminated", err)
	}
}

func TestStoreDecideApproval(t *testing.T) {
	clock := fixedClock()
	store := NewStore(WithClock(clock.now))
	run, err := store.Create(CreateRequest{Project: "prod", Prompt: "x", Mode: ModeProposeOnly})
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}
	if _, err := store.SetStatus(run.ID, StatusRunning); err != nil {
		t.Fatalf("SetStatus running: %v", err)
	}

	run, err = store.AddApproval(run.ID, ApprovalGate{Kind: ApprovalCommand, Summary: "run plan"})
	if err != nil {
		t.Fatalf("AddApproval returned error: %v", err)
	}
	if run.Status != StatusWaitingApproval || len(run.Approvals) != 1 {
		t.Fatalf("unexpected state after AddApproval: %+v", run)
	}
	approvalID := run.Approvals[0].ID

	clock.tick(time.Second)
	decideTime := clock.current
	run, err = store.DecideApproval(run.ID, approvalID, ApprovalApproved, "alice")
	if err != nil {
		t.Fatalf("DecideApproval returned error: %v", err)
	}
	a := run.Approvals[0]
	if a.Status != ApprovalApproved {
		t.Fatalf("approval status = %q, want %q", a.Status, ApprovalApproved)
	}
	if a.DecidedBy != "alice" {
		t.Fatalf("decided_by = %q, want alice", a.DecidedBy)
	}
	if a.DecidedAt == nil || *a.DecidedAt != decideTime {
		t.Fatalf("decided_at = %v, want %s", a.DecidedAt, decideTime)
	}

	// Re-deciding an already-decided gate should error.
	if _, err := store.DecideApproval(run.ID, approvalID, ApprovalApproved, "alice"); err == nil {
		t.Fatal("expected error re-deciding an already-decided gate")
	}

	// Non-existent approval gate should return ErrApprovalNotFound.
	if _, err := store.DecideApproval(run.ID, "approval_999999", ApprovalApproved, "alice"); !errors.Is(err, ErrApprovalNotFound) {
		t.Fatalf("missing gate error = %v, want ErrApprovalNotFound", err)
	}

	// Invalid decision value should error before hitting the store.
	if _, err := store.DecideApproval(run.ID, approvalID, "maybe", "alice"); err == nil {
		t.Fatal("expected error for invalid approval decision value")
	}
}

func TestStoreDefensiveTimePointers(t *testing.T) {
	clock := fixedClock()
	store := NewStore(WithClock(clock.now))
	run, err := store.Create(CreateRequest{Project: "prod", Prompt: "x"})
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}

	clock.tick(time.Second)
	startTime := clock.current
	run, err = store.SetStatus(run.ID, StatusRunning)
	if err != nil {
		t.Fatalf("SetStatus running: %v", err)
	}
	if run.StartedAt == nil || *run.StartedAt != startTime {
		t.Fatalf("StartedAt not recorded: %v", run.StartedAt)
	}

	// Mutate the pointer from the returned copy; the stored run must be unaffected.
	*run.StartedAt = time.Time{}

	got, ok := store.Get(run.ID)
	if !ok {
		t.Fatal("expected run to exist")
	}
	if got.StartedAt == nil || *got.StartedAt != startTime {
		t.Fatal("time pointer aliasing: stored StartedAt was mutated via returned copy")
	}

	// Same isolation must hold for ApprovalGate.DecidedAt.
	if _, err := store.AddApproval(run.ID, ApprovalGate{Kind: ApprovalCommand, Summary: "x"}); err != nil {
		t.Fatalf("AddApproval: %v", err)
	}
	clock.tick(time.Second)
	decideTime := clock.current
	run, err = store.DecideApproval(run.ID, "approval_000001", ApprovalApproved, "bob")
	if err != nil {
		t.Fatalf("DecideApproval: %v", err)
	}
	*run.Approvals[0].DecidedAt = time.Time{}

	got, _ = store.Get(run.ID)
	if got.Approvals[0].DecidedAt == nil || *got.Approvals[0].DecidedAt != decideTime {
		t.Fatal("time pointer aliasing: stored ApprovalGate.DecidedAt was mutated via returned copy")
	}
}

type testClock struct {
	current time.Time
}

func fixedClock() *testClock {
	return &testClock{current: time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)}
}

func (c *testClock) now() time.Time {
	return c.current
}

func (c *testClock) tick(d time.Duration) {
	c.current = c.current.Add(d)
}
