package recovery

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRecordSnapshotCapturesStateAndPlanMetadata(t *testing.T) {
	root := t.TempDir()
	workDir := filepath.Join(root, "environments", "dev")
	if err := os.MkdirAll(workDir, 0o755); err != nil {
		t.Fatalf("mkdir work dir: %v", err)
	}
	state := []byte(`{"version":4}`)
	plan := []byte(`{"resource_changes":[]}`)
	if err := os.WriteFile(filepath.Join(workDir, "terraform.tfstate"), state, 0o644); err != nil {
		t.Fatalf("write state: %v", err)
	}
	if err := os.WriteFile(filepath.Join(workDir, "tfplan.json"), plan, 0o644); err != nil {
		t.Fatalf("write plan: %v", err)
	}
	now := time.Date(2026, 6, 10, 12, 30, 0, 0, time.UTC)

	snapshot, err := RecordSnapshot(root, workDir, SnapshotInput{
		Project: "demo",
		Tool:    "terraform",
		Env:     "dev",
		Command: "apply",
	}, now)
	if err != nil {
		t.Fatalf("record snapshot: %v", err)
	}

	if snapshot.Project != "demo" || snapshot.Tool != "terraform" || snapshot.Env != "dev" || snapshot.Command != "apply" {
		t.Fatalf("unexpected snapshot identity: %#v", snapshot)
	}
	if snapshot.WorkDir != "environments/dev" {
		t.Fatalf("work dir = %q", snapshot.WorkDir)
	}
	if snapshot.StatePath != "environments/dev/terraform.tfstate" || snapshot.StateSize != int64(len(state)) {
		t.Fatalf("state metadata = path %q size %d", snapshot.StatePath, snapshot.StateSize)
	}
	if snapshot.StateSHA != sha(state) {
		t.Fatalf("state sha = %q, want %q", snapshot.StateSHA, sha(state))
	}
	if snapshot.PlanPath != "environments/dev/tfplan.json" || snapshot.PlanSHA != sha(plan) {
		t.Fatalf("plan metadata = path %q sha %q", snapshot.PlanPath, snapshot.PlanSHA)
	}
	if snapshot.ID == "" {
		t.Fatal("snapshot id should be set")
	}
	if _, err := os.Stat(filepath.Join(root, ".iac-studio", "snapshots", snapshot.ID+".json")); err != nil {
		t.Fatalf("snapshot metadata file missing: %v", err)
	}
}

func TestListSnapshotsSortsNewestFirst(t *testing.T) {
	root := t.TempDir()
	if _, err := RecordSnapshot(root, root, SnapshotInput{Project: "demo", Tool: "terraform", Command: "apply"}, time.Date(2026, 6, 10, 12, 0, 0, 0, time.UTC)); err != nil {
		t.Fatalf("record first: %v", err)
	}
	if _, err := RecordSnapshot(root, root, SnapshotInput{Project: "demo", Tool: "pulumi", Command: "up"}, time.Date(2026, 6, 10, 13, 0, 0, 0, time.UTC)); err != nil {
		t.Fatalf("record second: %v", err)
	}

	snapshots, err := ListSnapshots(root)
	if err != nil {
		t.Fatalf("list snapshots: %v", err)
	}
	if len(snapshots) != 2 {
		t.Fatalf("got %d snapshots, want 2", len(snapshots))
	}
	if snapshots[0].Tool != "pulumi" || snapshots[1].Tool != "terraform" {
		t.Fatalf("snapshots not sorted newest first: %#v", snapshots)
	}
	if len(snapshots[0].Notes) == 0 {
		t.Fatal("pulumi snapshot without local state should include a note")
	}
}

func TestBuildSnapshotRejectsWorkDirOutsideProject(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()

	_, err := BuildSnapshot(root, outside, SnapshotInput{Project: "demo", Tool: "terraform", Command: "apply"}, time.Now())
	if err == nil {
		t.Fatal("expected work dir escape to fail")
	}
}

func sha(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
