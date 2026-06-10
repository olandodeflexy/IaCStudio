package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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
