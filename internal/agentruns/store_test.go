package agentruns

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

var testPromptHashKey = []byte("01234567890123456789012345678901")

func TestStoreCreateRedactsPromptAndDefaultsReadOnly(t *testing.T) {
	now := fixedClock()
	store := NewStore(WithClock(now.now), WithPromptHashKey(testPromptHashKey))
	prompt := "rotate password=supersecret for AKIA1234567890ABCDEF"

	run, err := store.Create(CreateRequest{
		Project:    " prod ",
		Prompt:     prompt,
		ProviderID: "codex",
		CreatedBy:  "alice",
	})
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}

	if run.ID != "run_000001" {
		t.Fatalf("run ID = %q, want run_000001", run.ID)
	}
	if run.Project != "prod" {
		t.Fatalf("project = %q, want prod", run.Project)
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
	if want := hashText(prompt, testPromptHashKey); run.PromptHash != want {
		t.Fatalf("prompt hash = %q, want %q", run.PromptHash, want)
	}
	if run.CreatedAt != now.current || run.UpdatedAt != now.current {
		t.Fatalf("timestamps = %s/%s, want %s", run.CreatedAt, run.UpdatedAt, now.current)
	}
	if run.Logs == nil || run.Patches == nil || run.Approvals == nil {
		t.Fatalf("list fields should be initialized: %+v", run)
	}
}

func TestStorePromptHashUsesStoreKey(t *testing.T) {
	prompt := "short secret"
	store := NewStore(WithClock(fixedClock().now), WithPromptHashKey(testPromptHashKey))

	first, err := store.Create(CreateRequest{Project: "prod", Prompt: prompt})
	if err != nil {
		t.Fatalf("Create first returned error: %v", err)
	}
	second, err := store.Create(CreateRequest{Project: "prod", Prompt: prompt})
	if err != nil {
		t.Fatalf("Create second returned error: %v", err)
	}
	if first.PromptHash != second.PromptHash {
		t.Fatalf("same prompt in one store produced different hashes: %q/%q", first.PromptHash, second.PromptHash)
	}

	otherStore := NewStore(WithClock(fixedClock().now), WithPromptHashKey([]byte("another-test-key-for-prompt-hmac")))
	other, err := otherStore.Create(CreateRequest{Project: "prod", Prompt: prompt})
	if err != nil {
		t.Fatalf("Create other returned error: %v", err)
	}
	if first.PromptHash == other.PromptHash {
		t.Fatalf("different store keys produced identical prompt hash: %q", first.PromptHash)
	}

	plain := sha256.Sum256([]byte(prompt))
	if first.PromptHash == hex.EncodeToString(plain[:]) {
		t.Fatal("prompt hash should not be a plain SHA-256 digest")
	}
}

func TestStoreRedactsQuotedSecretsWithSpaces(t *testing.T) {
	store := NewStore(WithClock(fixedClock().now))
	run, err := store.Create(CreateRequest{
		Project: "prod",
		Prompt:  `rotate password="two word secret" and token='another secret phrase' for ASIA1234567890ABCDEF`,
	})
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}
	if strings.Contains(run.PromptPreview, "two word secret") ||
		strings.Contains(run.PromptPreview, "another secret phrase") ||
		strings.Contains(run.PromptPreview, "ASIA1234567890ABCDEF") {
		t.Fatalf("prompt preview leaked quoted secret: %q", run.PromptPreview)
	}

	run, err = store.AddLog(run.ID, LogInfo, `using api_key="local model secret" and session_token='temporary session secret'`)
	if err != nil {
		t.Fatalf("AddLog returned error: %v", err)
	}
	if strings.Contains(run.Logs[0].Message, "local model secret") ||
		strings.Contains(run.Logs[0].Message, "temporary session secret") {
		t.Fatalf("log leaked quoted secret: %q", run.Logs[0].Message)
	}

	run, err = store.AddPatch(run.ID, ProposedPatch{
		Path: "main.tf",
		Diff: `+ access_key_id = "quoted cloud secret"
+ secret_access_key = "quoted aws secret"`,
	})
	if err != nil {
		t.Fatalf("AddPatch returned error: %v", err)
	}
	if strings.Contains(run.Patches[0].Diff, "quoted cloud secret") ||
		strings.Contains(run.Patches[0].Diff, "quoted aws secret") {
		t.Fatalf("patch leaked quoted secret: %q", run.Patches[0].Diff)
	}
}

func TestStoreRedactsShortAssignmentSecrets(t *testing.T) {
	store := NewStore(WithClock(fixedClock().now))
	run, err := store.Create(CreateRequest{
		Project: "prod",
		Prompt:  `run with token := "short assignment secret"`,
	})
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}
	if strings.Contains(run.PromptPreview, "short assignment secret") {
		t.Fatalf("prompt preview leaked short assignment secret: %q", run.PromptPreview)
	}
	if !strings.Contains(run.PromptPreview, "token := [REDACTED]") {
		t.Fatalf("prompt preview did not preserve short assignment delimiter: %q", run.PromptPreview)
	}

	run, err = store.AddLog(run.ID, LogInfo, `password := 'operator supplied secret'`)
	if err != nil {
		t.Fatalf("AddLog returned error: %v", err)
	}
	if strings.Contains(run.Logs[0].Message, "operator supplied secret") {
		t.Fatalf("log leaked short assignment secret: %q", run.Logs[0].Message)
	}
	if !strings.Contains(run.Logs[0].Message, "password := [REDACTED]") {
		t.Fatalf("log did not preserve short assignment delimiter: %q", run.Logs[0].Message)
	}
}

func TestStoreFormatsKeyValueRedactionsConsistently(t *testing.T) {
	cases := map[string]string{
		`secret="plain secret"`:      "secret= [REDACTED]",
		`token: "colon secret"`:      "token: [REDACTED]",
		`password := "short secret"`: "password := [REDACTED]",
	}
	for input, want := range cases {
		t.Run(input, func(t *testing.T) {
			got := redactText(input)
			if got != want {
				t.Fatalf("redactText(%q) = %q, want %q", input, got, want)
			}
		})
	}
}

func TestTruncatePreservesUTF8(t *testing.T) {
	got := truncate("世界🙂terraform", 8)
	if got != "世..." {
		t.Fatalf("truncate = %q, want %q", got, "世...")
	}
	if len(got) > 8 {
		t.Fatalf("truncate length = %d, want at most 8 bytes", len(got))
	}
	if !utf8.ValidString(got) {
		t.Fatalf("truncate returned invalid UTF-8: %q", got)
	}
}

func TestStoreSetStatusCanceledMarksCanceled(t *testing.T) {
	clock := fixedClock()
	store := NewStore(WithClock(clock.now))
	run, err := store.Create(CreateRequest{Project: "prod", Prompt: "x"})
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}

	clock.tick(time.Second)
	run, err = store.SetStatus(run.ID, StatusCanceled)
	if err != nil {
		t.Fatalf("SetStatus canceled returned error: %v", err)
	}
	if !run.Canceled || run.Status != StatusCanceled || run.CompletedAt == nil || *run.CompletedAt != clock.current {
		t.Fatalf("canceled status not recorded consistently: %+v", run)
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
		Path:    " main.tf ",
		Summary: "add bucket",
		Diff:    "+ secret = \"dont-store-me\"",
	})
	if err != nil {
		t.Fatalf("AddPatch returned error: %v", err)
	}
	if len(run.Patches) != 1 || run.Patches[0].ID != "patch_000001" {
		t.Fatalf("unexpected patches: %+v", run.Patches)
	}
	if run.Patches[0].Path != "main.tf" {
		t.Fatalf("patch path = %q, want main.tf", run.Patches[0].Path)
	}
	if strings.Contains(run.Patches[0].Diff, "dont-store-me") {
		t.Fatalf("patch diff leaked secret: %q", run.Patches[0].Diff)
	}

	clock.tick(time.Second)
	run, err = store.AddApproval(run.ID, ApprovalGate{
		Kind:      ApprovalCommand,
		Summary:   "run terraform plan",
		DecidedAt: timePtr(clock.current),
		DecidedBy: "caller-supplied",
	})
	if err != nil {
		t.Fatalf("AddApproval returned error: %v", err)
	}
	if run.Status != StatusWaitingApproval || len(run.Approvals) != 1 || run.Approvals[0].Status != ApprovalPending {
		t.Fatalf("approval state not recorded: %+v", run)
	}
	if run.Approvals[0].DecidedAt != nil || run.Approvals[0].DecidedBy != "" {
		t.Fatalf("pending approval should not retain decision metadata: %+v", run.Approvals[0])
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

func TestStoreRejectsUnsafePatchPaths(t *testing.T) {
	store := NewStore(WithClock(fixedClock().now))
	run, err := store.Create(CreateRequest{Project: "prod", Prompt: "x"})
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}

	unsafePaths := []string{
		"/etc/passwd",
		"../main.tf",
		"dir/../main.tf",
		"dir/sub/../../main.tf",
		`C:\temp\main.tf`,
		"C:/temp/main.tf",
		`\\server\share\main.tf`,
		`dir\main.tf`,
	}
	for _, unsafePath := range unsafePaths {
		t.Run(unsafePath, func(t *testing.T) {
			_, err := store.AddPatch(run.ID, ProposedPatch{Path: unsafePath, Diff: "x"})
			if !errors.Is(err, ErrUnsafePatchPath) {
				t.Fatalf("AddPatch error = %v, want ErrUnsafePatchPath", err)
			}
		})
	}

	got, err := store.AddPatch(run.ID, ProposedPatch{Path: " ./dir/main.tf ", Diff: "x"})
	if err != nil {
		t.Fatalf("AddPatch safe path returned error: %v", err)
	}
	if got.Patches[0].Path != "dir/main.tf" {
		t.Fatalf("safe patch path = %q, want dir/main.tf", got.Patches[0].Path)
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
	run, err = store.DecideApproval(run.ID, approvalID, ApprovalApproved, "alice token=abc123")
	if err != nil {
		t.Fatalf("DecideApproval returned error: %v", err)
	}
	a := run.Approvals[0]
	if a.Status != ApprovalApproved {
		t.Fatalf("approval status = %q, want %q", a.Status, ApprovalApproved)
	}
	if run.Status != StatusRunning {
		t.Fatalf("run status after final approval = %q, want %q", run.Status, StatusRunning)
	}
	if strings.Contains(a.DecidedBy, "abc123") {
		t.Fatalf("decided_by leaked token: %q", a.DecidedBy)
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

func TestStoreDecideApprovalWaitsForAllPendingGates(t *testing.T) {
	store := NewStore(WithClock(fixedClock().now))
	run, err := store.Create(CreateRequest{Project: "prod", Prompt: "x"})
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}
	run, err = store.AddApproval(run.ID, ApprovalGate{Kind: ApprovalCommand, Summary: "first"})
	if err != nil {
		t.Fatalf("AddApproval first returned error: %v", err)
	}
	firstID := run.Approvals[0].ID
	run, err = store.AddApproval(run.ID, ApprovalGate{Kind: ApprovalFileWrite, Summary: "second"})
	if err != nil {
		t.Fatalf("AddApproval second returned error: %v", err)
	}
	secondID := run.Approvals[1].ID

	run, err = store.DecideApproval(run.ID, firstID, ApprovalApproved, "alice")
	if err != nil {
		t.Fatalf("DecideApproval first returned error: %v", err)
	}
	if run.Status != StatusWaitingApproval {
		t.Fatalf("run status with remaining pending gate = %q, want %q", run.Status, StatusWaitingApproval)
	}

	run, err = store.DecideApproval(run.ID, secondID, ApprovalRejected, "bob")
	if err != nil {
		t.Fatalf("DecideApproval second returned error: %v", err)
	}
	if run.Status != StatusRunning {
		t.Fatalf("run status after all gates decided = %q, want %q", run.Status, StatusRunning)
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
