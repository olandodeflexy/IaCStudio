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

func TestProjectDriftEndpointReturnsClassifiedFindings(t *testing.T) {
	root := t.TempDir()
	projectDir := filepath.Join(root, "demo")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatalf("mkdir project: %v", err)
	}
	if err := os.WriteFile(filepath.Join(projectDir, "main.tf"), []byte(`
resource "aws_security_group" "web" {
  name    = "web"
  ingress = []
}
`), 0o644); err != nil {
		t.Fatalf("write main.tf: %v", err)
	}
	if err := os.WriteFile(filepath.Join(projectDir, "terraform.tfstate"), []byte(`{
		"version": 4,
		"resources": [{
			"mode": "managed",
			"type": "aws_security_group",
			"name": "web",
			"instances": [{"attributes": {"name": "web", "ingress": [{"cidr_blocks": ["0.0.0.0/0"]}]}}]
		}]
	}`), 0o600); err != nil {
		t.Fatalf("write state: %v", err)
	}

	srv := httptest.NewServer(fullRouterForTest(t, root))
	defer srv.Close()

	resp, err := http.Post(srv.URL+"/api/projects/demo/drift", "application/json", strings.NewReader(`{"tool":"terraform"}`))
	if err != nil {
		t.Fatalf("POST drift: %v", err)
	}
	defer closeBody(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("drift status = %d", resp.StatusCode)
	}

	var payload struct {
		Findings []struct {
			Address           string `json:"address"`
			Path              string `json:"path"`
			Classification    string `json:"classification"`
			RecommendedAction string `json:"recommended_action"`
		} `json:"findings"`
		Classifications map[string]int `json:"classifications"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("decode drift response: %v", err)
	}
	if len(payload.Findings) == 0 {
		t.Fatal("expected drift findings")
	}
	got := payload.Findings[0]
	if got.Address != "aws_security_group.web" ||
		got.Path != "ingress" ||
		got.Classification != "unauthorized_change" ||
		got.RecommendedAction != "revert_or_codify_after_review" {
		t.Fatalf("unexpected finding: %#v", got)
	}
	if payload.Classifications["unauthorized_change"] != 1 {
		t.Fatalf("unexpected classification counts: %#v", payload.Classifications)
	}
}

func TestProjectDriftEndpointRejectsUnsupportedTool(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "demo"), 0o755); err != nil {
		t.Fatalf("mkdir project: %v", err)
	}
	srv := httptest.NewServer(fullRouterForTest(t, root))
	defer srv.Close()

	resp, err := http.Post(srv.URL+"/api/projects/demo/drift", "application/json", strings.NewReader(`{"tool":"ansible"}`))
	if err != nil {
		t.Fatalf("POST drift: %v", err)
	}
	defer closeBody(resp.Body)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("drift status = %d, want 400", resp.StatusCode)
	}
	assertResponseBodyContains(t, resp, "Terraform and OpenTofu")
}
