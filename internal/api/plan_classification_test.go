package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPlanClassificationEndpointClassifiesPostedPlanJSON(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "demo"), 0o755); err != nil {
		t.Fatalf("mkdir project: %v", err)
	}
	srv := httptest.NewServer(fullRouterForTest(t, root))
	defer srv.Close()

	body := `{
		"tool": "terraform",
		"plan_json": {
			"resource_changes": [{
				"address": "aws_security_group.web",
				"type": "aws_security_group",
				"name": "web",
				"change": {
					"actions": ["update"],
					"before": {"ingress": []},
					"after": {"ingress": [{"cidr_blocks": ["0.0.0.0/0"]}]}
				}
			}]
		}
	}`

	resp, err := http.Post(srv.URL+"/api/projects/demo/plan/classify", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("POST classify: %v", err)
	}
	defer closeBody(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("classify status = %d", resp.StatusCode)
	}

	var payload struct {
		Summary struct {
			Risky                  int  `json:"risky"`
			RequiresAcknowledgment bool `json:"requires_acknowledgment"`
		} `json:"summary"`
		Changes []struct {
			Address string `json:"address"`
			Risk    string `json:"risk"`
		} `json:"changes"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if payload.Summary.Risky != 1 || !payload.Summary.RequiresAcknowledgment {
		t.Fatalf("unexpected summary: %#v", payload.Summary)
	}
	if len(payload.Changes) != 1 || payload.Changes[0].Risk != "risky" {
		t.Fatalf("unexpected changes: %#v", payload.Changes)
	}
}

func TestApplyGateBlocksUnacknowledgedPlanRisk(t *testing.T) {
	root := t.TempDir()
	projectDir := filepath.Join(root, "demo")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatalf("mkdir project: %v", err)
	}
	t.Cleanup(func() { invalidatePlan(projectDir) })
	recordPlan(projectDir)
	if err := os.WriteFile(filepath.Join(projectDir, savedPlanJSONFile), []byte(`{
		"resource_changes": [{
			"address": "aws_db_instance.primary",
			"type": "aws_db_instance",
			"name": "primary",
			"change": {
				"actions": ["delete", "create"],
				"before": {"identifier": "prod-db"},
				"after": {"identifier": "prod-db"}
			}
		}]
	}`), 0o600); err != nil {
		t.Fatalf("write plan json: %v", err)
	}

	srv := httptest.NewServer(fullRouterForTest(t, root))
	defer srv.Close()

	resp, err := http.Post(
		srv.URL+"/api/projects/demo/run",
		"application/json",
		strings.NewReader(`{"tool":"terraform","command":"apply","approved":true}`),
	)
	if err != nil {
		t.Fatalf("POST run apply: %v", err)
	}
	defer closeBody(resp.Body)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("apply status = %d, want 409", resp.StatusCode)
	}
	assertResponseBodyContains(t, resp, "plan_risk_blocked", "destructive", "aws_db_instance.primary")
}
