package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/iac-studio/iac-studio/internal/recovery"
)

func TestListSnapshotsFiltersByEnvironment(t *testing.T) {
	root := t.TempDir()
	projectDir := filepath.Join(root, "demo")
	devDir := filepath.Join(projectDir, "environments", "dev")
	prodDir := filepath.Join(projectDir, "environments", "prod")
	if err := os.MkdirAll(devDir, 0o755); err != nil {
		t.Fatalf("mkdir dev env: %v", err)
	}
	if err := os.MkdirAll(prodDir, 0o755); err != nil {
		t.Fatalf("mkdir prod env: %v", err)
	}
	if _, err := recovery.RecordSnapshot(projectDir, prodDir, recovery.SnapshotInput{
		Project: "demo",
		Tool:    "terraform",
		Env:     "prod",
		Command: "apply",
	}, time.Date(2026, 6, 10, 12, 0, 0, 0, time.UTC)); err != nil {
		t.Fatalf("record prod snapshot: %v", err)
	}
	if _, err := recovery.RecordSnapshot(projectDir, devDir, recovery.SnapshotInput{
		Project: "demo",
		Tool:    "terraform",
		Env:     "dev",
		Command: "apply",
	}, time.Date(2026, 6, 10, 13, 0, 0, 0, time.UTC)); err != nil {
		t.Fatalf("record dev snapshot: %v", err)
	}

	srv := httptest.NewServer(fullRouterForTest(t, root))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/projects/demo/snapshots?env=dev")
	if err != nil {
		t.Fatalf("GET snapshots: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("snapshots status = %d", resp.StatusCode)
	}

	var snapshots []recovery.StateSnapshot
	if err := json.NewDecoder(resp.Body).Decode(&snapshots); err != nil {
		t.Fatalf("decode snapshots: %v", err)
	}
	if len(snapshots) != 1 {
		t.Fatalf("got %d snapshots, want 1", len(snapshots))
	}
	if snapshots[0].Env != "dev" || snapshots[0].WorkDir != "environments/dev" {
		t.Fatalf("unexpected snapshot: %#v", snapshots[0])
	}
}

func TestListSnapshotsRejectsUnsafeEnvironmentFilter(t *testing.T) {
	root := t.TempDir()
	projectDir := filepath.Join(root, "demo")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatalf("mkdir project: %v", err)
	}

	srv := httptest.NewServer(fullRouterForTest(t, root))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/projects/demo/snapshots?env=../prod")
	if err != nil {
		t.Fatalf("GET snapshots: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("unsafe env status = %d, want 400", resp.StatusCode)
	}
}

func TestCreateRollbackProposalForSnapshot(t *testing.T) {
	root := t.TempDir()
	projectDir := filepath.Join(root, "demo")
	devDir := filepath.Join(projectDir, "environments", "dev")
	if err := os.MkdirAll(devDir, 0o755); err != nil {
		t.Fatalf("mkdir dev env: %v", err)
	}
	target, err := recovery.RecordSnapshot(projectDir, devDir, recovery.SnapshotInput{
		Project: "demo",
		Tool:    "terraform",
		Env:     "dev",
		Command: "apply",
	}, time.Date(2026, 6, 10, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("record target snapshot: %v", err)
	}
	current, err := recovery.RecordSnapshot(projectDir, devDir, recovery.SnapshotInput{
		Project: "demo",
		Tool:    "terraform",
		Env:     "dev",
		Command: "apply",
	}, time.Date(2026, 6, 10, 13, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("record current snapshot: %v", err)
	}

	srv := httptest.NewServer(fullRouterForTest(t, root))
	defer srv.Close()

	resp, err := http.Post(
		srv.URL+"/api/projects/demo/snapshots/"+target.ID+"/rollback",
		"application/json",
		strings.NewReader(`{"env":"dev"}`),
	)
	if err != nil {
		t.Fatalf("POST rollback: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("rollback status = %d", resp.StatusCode)
	}

	var proposal recovery.RollbackProposal
	if err := json.NewDecoder(resp.Body).Decode(&proposal); err != nil {
		t.Fatalf("decode rollback proposal: %v", err)
	}
	if proposal.TargetSnapshot.ID != target.ID || proposal.CurrentSnapshot == nil || proposal.CurrentSnapshot.ID != current.ID {
		t.Fatalf("unexpected snapshot linkage: %#v", proposal)
	}
	if proposal.Classification == nil || !proposal.Classification.Summary.RequiresAcknowledgment {
		t.Fatalf("rollback proposal should include fail-closed classification: %#v", proposal.Classification)
	}
	if !strings.Contains(proposal.Body, "not an unconditional undo button") {
		t.Fatalf("rollback body should explain review semantics:\n%s", proposal.Body)
	}
}

func TestCreateRollbackArtifactsWritesReviewFiles(t *testing.T) {
	root := t.TempDir()
	projectDir := filepath.Join(root, "demo")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatalf("mkdir project: %v", err)
	}
	target, err := recovery.RecordSnapshot(projectDir, projectDir, recovery.SnapshotInput{
		Project: "demo",
		Tool:    "terraform",
		Command: "apply",
	}, time.Date(2026, 6, 10, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("record target snapshot: %v", err)
	}
	proposal, err := recovery.BuildRollbackProposal(recovery.RollbackInput{
		ProjectName:    "demo",
		TargetSnapshot: target,
	})
	if err != nil {
		t.Fatalf("build proposal: %v", err)
	}

	body, err := json.Marshal(map[string]any{"proposal": proposal})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	srv := httptest.NewServer(fullRouterForTest(t, root))
	defer srv.Close()

	resp, err := http.Post(
		srv.URL+"/api/projects/demo/snapshots/"+target.ID+"/rollback/artifacts",
		"application/json",
		bytes.NewReader(body),
	)
	if err != nil {
		t.Fatalf("POST rollback artifacts: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("rollback artifacts status = %d", resp.StatusCode)
	}

	var set recovery.RollbackArtifactSet
	if err := json.NewDecoder(resp.Body).Decode(&set); err != nil {
		t.Fatalf("decode rollback artifacts: %v", err)
	}
	if set.Proposal.TargetSnapshot.ID != target.ID || set.Root == "" {
		t.Fatalf("unexpected artifact set: %#v", set)
	}
	runbook := filepath.Join(projectDir, ".iac-studio", "rollbacks", set.ID, "README.md")
	data, err := os.ReadFile(runbook)
	if err != nil {
		t.Fatalf("read rollback runbook: %v", err)
	}
	if !strings.Contains(string(data), "does not apply infrastructure changes automatically") {
		t.Fatalf("runbook missing safety warning:\n%s", string(data))
	}
}

func TestCreateRollbackArtifactsRejectsMismatchedProposal(t *testing.T) {
	root := t.TempDir()
	projectDir := filepath.Join(root, "demo")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatalf("mkdir project: %v", err)
	}
	target, err := recovery.RecordSnapshot(projectDir, projectDir, recovery.SnapshotInput{
		Project: "demo",
		Tool:    "terraform",
		Command: "apply",
	}, time.Date(2026, 6, 10, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("record target snapshot: %v", err)
	}
	proposal, err := recovery.BuildRollbackProposal(recovery.RollbackInput{
		ProjectName:    "demo",
		TargetSnapshot: target,
	})
	if err != nil {
		t.Fatalf("build proposal: %v", err)
	}
	proposal.TargetSnapshot.ID = "other-snapshot"
	body, err := json.Marshal(map[string]any{"proposal": proposal})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}

	srv := httptest.NewServer(fullRouterForTest(t, root))
	defer srv.Close()

	resp, err := http.Post(
		srv.URL+"/api/projects/demo/snapshots/"+target.ID+"/rollback/artifacts",
		"application/json",
		bytes.NewReader(body),
	)
	if err != nil {
		t.Fatalf("POST rollback artifacts mismatch: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("rollback artifacts mismatch status = %d, want 400", resp.StatusCode)
	}
}
