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

func TestProjectDriftEndpointAppliesConfiguredSuppressions(t *testing.T) {
	root := t.TempDir()
	projectDir := filepath.Join(root, "demo")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatalf("mkdir project: %v", err)
	}
	if err := os.WriteFile(filepath.Join(projectDir, ".iac-studio.json"), []byte(`{
  "tool": "terraform",
  "drift": {
    "suppressions": [
      {
        "address": "aws_s3_bucket.logs",
        "path": "tags",
        "classification": "legitimate_config_change",
        "reason": "provider-managed owner tag"
      }
    ]
  }
}`), 0o644); err != nil {
		t.Fatalf("write project metadata: %v", err)
	}
	if err := os.WriteFile(filepath.Join(projectDir, "main.tf"), []byte(`
resource "aws_s3_bucket" "logs" {
  bucket = "logs"
  tags = {
    Owner = "platform"
  }
}
`), 0o644); err != nil {
		t.Fatalf("write main.tf: %v", err)
	}
	if err := os.WriteFile(filepath.Join(projectDir, "terraform.tfstate"), []byte(`{
		"version": 4,
		"resources": [{
			"mode": "managed",
			"type": "aws_s3_bucket",
			"name": "logs",
			"instances": [{"attributes": {"bucket": "logs", "tags": {"Owner": "legacy"}}}]
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
		Findings           []json.RawMessage `json:"findings"`
		SuppressedFindings []struct {
			Address           string `json:"address"`
			Path              string `json:"path"`
			Suppressed        bool   `json:"suppressed"`
			SuppressionReason string `json:"suppression_reason"`
		} `json:"suppressed_findings"`
		Suppressed      int            `json:"suppressed"`
		Classifications map[string]int `json:"classifications"`
		Summary         string         `json:"summary"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("decode drift response: %v", err)
	}
	if len(payload.Findings) != 0 {
		t.Fatalf("active findings = %d, want 0", len(payload.Findings))
	}
	if payload.Suppressed != 1 || len(payload.SuppressedFindings) != 1 {
		t.Fatalf("suppressed = %d/%d, want 1/1", payload.Suppressed, len(payload.SuppressedFindings))
	}
	suppressed := payload.SuppressedFindings[0]
	if suppressed.Address != "aws_s3_bucket.logs" ||
		suppressed.Path != "tags" ||
		!suppressed.Suppressed ||
		suppressed.SuppressionReason != "provider-managed owner tag" {
		t.Fatalf("unexpected suppressed finding: %#v", suppressed)
	}
	if payload.Classifications["legitimate_config_change"] != 0 {
		t.Fatalf("suppressed findings should not increment active classification counts: %#v", payload.Classifications)
	}
	if !strings.Contains(payload.Summary, "1 suppressed") {
		t.Fatalf("summary should include suppressed count: %s", payload.Summary)
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

func TestProjectDriftRemediationEndpointReturnsDraftProposal(t *testing.T) {
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

	resp, err := http.Post(srv.URL+"/api/projects/demo/drift/remediation", "application/json", strings.NewReader(`{"tool":"terraform","mode":"revert"}`))
	if err != nil {
		t.Fatalf("POST drift remediation: %v", err)
	}
	defer closeBody(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("drift remediation status = %d", resp.StatusCode)
	}

	var payload struct {
		Mode          string `json:"mode"`
		Title         string `json:"title"`
		Branch        string `json:"branch"`
		CommitMessage string `json:"commit_message"`
		Body          string `json:"body"`
		FileChanges   []struct {
			Path    string `json:"path"`
			Line    int    `json:"line"`
			Action  string `json:"action"`
			Address string `json:"address"`
			Field   string `json:"field"`
		} `json:"file_changes"`
		Warnings []string `json:"warnings"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("decode drift remediation response: %v", err)
	}
	if payload.Mode != "revert" ||
		payload.Title != "Revert unauthorized drift for demo" ||
		payload.Branch != "iac-studio-drift-revert-demo" ||
		payload.CommitMessage != "Document drift revert for demo" {
		t.Fatalf("unexpected proposal metadata: %#v", payload)
	}
	if len(payload.FileChanges) != 1 {
		t.Fatalf("file changes = %d, want 1", len(payload.FileChanges))
	}
	change := payload.FileChanges[0]
	if change.Path != "main.tf" ||
		change.Line != 2 ||
		change.Action != "revert" ||
		change.Address != "aws_security_group.web" ||
		change.Field != "ingress" {
		t.Fatalf("unexpected remediation change: %#v", change)
	}
	if !strings.Contains(payload.Body, "Run drift again") {
		t.Fatalf("proposal body should include validation guidance: %s", payload.Body)
	}
	if !strings.Contains(strings.Join(payload.Warnings, "\n"), "provider-side change") {
		t.Fatalf("proposal should warn about provider-side revert: %#v", payload.Warnings)
	}
}

func TestProjectDriftRemediationArtifactsEndpointWritesReviewFiles(t *testing.T) {
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

	resp, err := http.Post(srv.URL+"/api/projects/demo/drift/remediation/artifacts", "application/json", strings.NewReader(`{"tool":"terraform","mode":"revert"}`))
	if err != nil {
		t.Fatalf("POST drift remediation artifacts: %v", err)
	}
	defer closeBody(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("drift remediation artifacts status = %d", resp.StatusCode)
	}

	var payload struct {
		ID    string `json:"id"`
		Root  string `json:"root"`
		Files []struct {
			Path string `json:"path"`
			Kind string `json:"kind"`
			Size int    `json:"size"`
		} `json:"files"`
		Proposal struct {
			Title string `json:"title"`
		} `json:"proposal"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("decode drift remediation artifacts response: %v", err)
	}
	if payload.ID != "iac-studio-drift-revert-demo" ||
		payload.Root != ".iac-studio/remediations/iac-studio-drift-revert-demo" ||
		payload.Proposal.Title != "Revert unauthorized drift for demo" {
		t.Fatalf("unexpected artifact payload: %#v", payload)
	}
	if len(payload.Files) != 3 {
		t.Fatalf("artifact files = %d, want 3", len(payload.Files))
	}
	for _, file := range payload.Files {
		if file.Size == 0 {
			t.Fatalf("artifact %s has zero size", file.Path)
		}
		if _, err := os.Stat(filepath.Join(projectDir, filepath.FromSlash(file.Path))); err != nil {
			t.Fatalf("artifact %s was not written: %v", file.Path, err)
		}
	}
	runbook, err := os.ReadFile(filepath.Join(projectDir, ".iac-studio", "remediations", "iac-studio-drift-revert-demo", "README.md"))
	if err != nil {
		t.Fatalf("read runbook: %v", err)
	}
	if !strings.Contains(string(runbook), "Generated by IaC Studio") ||
		!strings.Contains(string(runbook), "Run drift again after remediation") {
		t.Fatalf("runbook missing review guidance:\n%s", string(runbook))
	}
}
