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

	iacplan "github.com/iac-studio/iac-studio/internal/plan"
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
	if got := resp.Header.Get("Content-Type"); !strings.Contains(got, "application/json") {
		t.Fatalf("rollback content-type = %q, want application/json", got)
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
	if got := resp.Header.Get("Content-Type"); !strings.Contains(got, "application/json") {
		t.Fatalf("rollback artifacts content-type = %q, want application/json", got)
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

func TestCreateRollbackPREndpointCreatesReviewBranch(t *testing.T) {
	root := t.TempDir()
	projectDir := filepath.Join(root, "demo")
	devDir := filepath.Join(projectDir, "environments", "dev")
	if err := os.MkdirAll(devDir, 0o755); err != nil {
		t.Fatalf("mkdir dev env: %v", err)
	}
	if err := os.WriteFile(filepath.Join(devDir, "main.tf"), []byte("resource \"aws_s3_bucket\" \"logs\" {}\n"), 0o644); err != nil {
		t.Fatalf("write main.tf: %v", err)
	}
	initProjectGitRepo(t, projectDir)
	target, err := recovery.RecordSnapshot(projectDir, devDir, recovery.SnapshotInput{
		Project: "demo",
		Tool:    "terraform",
		Env:     "dev",
		Command: "apply",
	}, time.Date(2026, 6, 10, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("record target snapshot: %v", err)
	}

	srv := httptest.NewServer(fullRouterForTest(t, root))
	defer srv.Close()

	resp, err := http.Post(
		srv.URL+"/api/projects/demo/snapshots/"+target.ID+"/rollback/pr",
		"application/json",
		strings.NewReader(`{"env":"dev"}`),
	)
	if err != nil {
		t.Fatalf("POST rollback pr: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("rollback pr status = %d", resp.StatusCode)
	}

	var payload struct {
		Artifacts struct {
			ID string `json:"id"`
		} `json:"artifacts"`
		PullRequest struct {
			Title      string `json:"title"`
			Branch     string `json:"branch"`
			BaseBranch string `json:"base_branch"`
			Commit     string `json:"commit"`
			BodyPath   string `json:"body_path"`
			Commands   []struct {
				Args []string `json:"args"`
			} `json:"commands"`
		} `json:"pull_request"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("decode rollback pr response: %v", err)
	}
	if payload.Artifacts.ID == "" ||
		!strings.HasPrefix(payload.PullRequest.Branch, "iac-studio-rollback-demo-") ||
		payload.PullRequest.BaseBranch != "main" ||
		payload.PullRequest.Commit == "" ||
		!strings.HasSuffix(payload.PullRequest.BodyPath, "/proposal.md") {
		t.Fatalf("unexpected rollback PR payload: %#v", payload)
	}
	if len(payload.PullRequest.Commands) != 2 ||
		payload.PullRequest.Commands[0].Args[0] != "git" ||
		payload.PullRequest.Commands[1].Args[0] != "gh" {
		t.Fatalf("unexpected rollback PR commands: %#v", payload.PullRequest.Commands)
	}
	if branch := gitOutputForTest(t, projectDir, "rev-parse", "--abbrev-ref", "HEAD"); branch != payload.PullRequest.Branch {
		t.Fatalf("current branch = %q, want %q", branch, payload.PullRequest.Branch)
	}
	if status := gitOutputForTest(t, projectDir, "status", "--short"); status != "" {
		for _, line := range strings.Split(status, "\n") {
			if line != "" && !strings.Contains(line, ".iac-studio/snapshots/") {
				t.Fatalf("worktree should only contain uncommitted snapshot metadata, got:\n%s", status)
			}
		}
	}
	committed := gitOutputForTest(t, projectDir, "show", "--name-only", "--format=", "HEAD")
	if !strings.Contains(committed, ".iac-studio/rollbacks/") ||
		!strings.Contains(committed, "proposal.md") ||
		strings.Contains(committed, "environments/dev/main.tf") {
		t.Fatalf("rollback review commit should contain only generated artifacts, got:\n%s", committed)
	}
}

func TestCreateRollbackArtifactsRebuildsServerProposal(t *testing.T) {
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
	proposal.Branch = "client-controlled-branch"
	proposal.Body = "safe to apply"
	proposal.Warnings = nil
	proposal.Classification = &iacplan.ClassificationResult{
		Summary: iacplan.ClassificationSummary{
			Safe:                   1,
			Total:                  1,
			RequiresAcknowledgment: false,
			Text:                   "Semantic plan: 1 safe change",
		},
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
	if set.Proposal.Branch == "client-controlled-branch" || set.Proposal.Body == "safe to apply" {
		t.Fatalf("artifact set should use server-built proposal, got %#v", set.Proposal)
	}
	if set.Proposal.Classification == nil ||
		!set.Proposal.Classification.Summary.RequiresAcknowledgment ||
		set.Proposal.Classification.Summary.Unknown != 1 {
		t.Fatalf("artifact set should preserve fail-closed classification: %#v", set.Proposal.Classification)
	}
	if !strings.Contains(strings.Join(set.Proposal.Warnings, "\n"), "Generate and review a fresh plan") {
		t.Fatalf("artifact set should preserve server warnings: %#v", set.Proposal.Warnings)
	}

	metadataPath := filepath.Join(projectDir, ".iac-studio", "rollbacks", set.ID, "proposal.json")
	metadataData, err := os.ReadFile(metadataPath)
	if err != nil {
		t.Fatalf("read rollback metadata: %v", err)
	}
	var metadata recovery.RollbackArtifactSet
	if err := json.Unmarshal(metadataData, &metadata); err != nil {
		t.Fatalf("decode rollback metadata: %v", err)
	}
	if metadata.Proposal.Classification == nil ||
		!metadata.Proposal.Classification.Summary.RequiresAcknowledgment ||
		metadata.Proposal.Classification.Summary.Unknown != 1 {
		t.Fatalf("metadata should preserve fail-closed classification: %#v", metadata.Proposal.Classification)
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
