package api

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/iac-studio/iac-studio/internal/cloudconnections"
)

func writeTerraformDriftFixture(t *testing.T, root, name string) {
	t.Helper()

	projectDir := filepath.Join(root, name)
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
}

func TestProjectDriftEndpointReturnsClassifiedFindings(t *testing.T) {
	root := t.TempDir()
	writeTerraformDriftFixture(t, root, "demo")

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

func TestProjectDriftEndpointIncludesRedactedConnectionContext(t *testing.T) {
	root := t.TempDir()
	writeTerraformDriftFixture(t, root, "demo")

	manager := cloudconnections.NewManager(root)
	connection, err := manager.Save(cloudconnections.Connection{
		Name:       "prod-aws",
		Provider:   cloudconnections.ProviderAWS,
		AuthMethod: "aws_static",
		Region:     "us-east-1",
		Metadata: map[string]string{
			"access_key_id": "AKIAIOSFODNN7EXAMPLE",
		},
		Secrets: map[string]string{
			"secret_access_key": "SECRET_SHOULD_NOT_LEAK",
		},
	})
	if err != nil {
		t.Fatalf("save cloud connection: %v", err)
	}

	srv := httptest.NewServer(fullRouterForTest(t, root))
	defer srv.Close()

	resp, err := http.Post(srv.URL+"/api/projects/demo/drift", "application/json", strings.NewReader(`{"tool":"terraform","connection_id":"`+connection.ID+`"}`))
	if err != nil {
		t.Fatalf("POST drift: %v", err)
	}
	defer closeBody(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("drift status = %d", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read drift response: %v", err)
	}
	if strings.Contains(string(body), "SECRET_SHOULD_NOT_LEAK") ||
		strings.Contains(string(body), "AKIAIOSFODNN7EXAMPLE") {
		t.Fatalf("drift response leaked connection credential material: %s", string(body))
	}

	var payload struct {
		ConnectionID       string `json:"connection_id"`
		ConnectionName     string `json:"connection_name"`
		ConnectionProvider string `json:"connection_provider"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("decode drift response: %v", err)
	}
	if payload.ConnectionID != connection.ID ||
		payload.ConnectionName != "prod-aws" ||
		payload.ConnectionProvider != cloudconnections.ProviderAWS {
		t.Fatalf("unexpected connection context: %#v", payload)
	}
}

func TestProjectDriftEndpointRejectsUnknownConnection(t *testing.T) {
	root := t.TempDir()
	writeTerraformDriftFixture(t, root, "demo")

	srv := httptest.NewServer(fullRouterForTest(t, root))
	defer srv.Close()

	resp, err := http.Post(srv.URL+"/api/projects/demo/drift", "application/json", strings.NewReader(`{"tool":"terraform","connection_id":"missing"}`))
	if err != nil {
		t.Fatalf("POST drift: %v", err)
	}
	defer closeBody(resp.Body)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("drift status = %d, want 404", resp.StatusCode)
	}
	assertResponseBodyContains(t, resp, "cloud connection not found")
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

func TestProjectDriftRemediationPREndpointCreatesReviewBranch(t *testing.T) {
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
	initProjectGitRepo(t, projectDir)

	srv := httptest.NewServer(fullRouterForTest(t, root))
	defer srv.Close()

	resp, err := http.Post(srv.URL+"/api/projects/demo/drift/remediation/pr", "application/json", strings.NewReader(`{"tool":"terraform","mode":"revert"}`))
	if err != nil {
		t.Fatalf("POST drift remediation pr: %v", err)
	}
	defer closeBody(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("drift remediation pr status = %d", resp.StatusCode)
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
			Files      []struct {
				Path string `json:"path"`
			} `json:"-"`
			Commands []struct {
				Label   string   `json:"label"`
				Args    []string `json:"args"`
				Display string   `json:"display"`
			} `json:"commands"`
		} `json:"pull_request"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("decode drift remediation pr response: %v", err)
	}
	if payload.Artifacts.ID != "iac-studio-drift-revert-demo" ||
		payload.PullRequest.Branch != "iac-studio-drift-revert-demo" ||
		payload.PullRequest.BaseBranch != "main" ||
		payload.PullRequest.Commit == "" ||
		payload.PullRequest.BodyPath != ".iac-studio/remediations/iac-studio-drift-revert-demo/pr-body.md" {
		t.Fatalf("unexpected PR payload: %#v", payload)
	}
	if len(payload.PullRequest.Commands) != 2 ||
		payload.PullRequest.Commands[0].Args[0] != "git" ||
		payload.PullRequest.Commands[1].Args[0] != "gh" {
		t.Fatalf("unexpected PR commands: %#v", payload.PullRequest.Commands)
	}
	if branch := gitOutputForTest(t, projectDir, "rev-parse", "--abbrev-ref", "HEAD"); branch != "iac-studio-drift-revert-demo" {
		t.Fatalf("current branch = %q", branch)
	}
	if status := gitOutputForTest(t, projectDir, "status", "--short"); status != "" {
		t.Fatalf("worktree should be clean after PR handoff, got:\n%s", status)
	}
	committed := gitOutputForTest(t, projectDir, "show", "--name-only", "--format=", "HEAD")
	if !strings.Contains(committed, ".iac-studio/remediations/iac-studio-drift-revert-demo/README.md") ||
		strings.Contains(committed, "main.tf") {
		t.Fatalf("review commit should contain only generated artifacts, got:\n%s", committed)
	}
}

func TestProjectDriftRemediationPREndpointRejectsDirtySourceFiles(t *testing.T) {
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
	initProjectGitRepo(t, projectDir)
	if err := os.WriteFile(filepath.Join(projectDir, "main.tf"), []byte(`
resource "aws_security_group" "web" {
  name    = "web"
  ingress = []
}

# dirty source edit
`), 0o644); err != nil {
		t.Fatalf("dirty source file: %v", err)
	}

	srv := httptest.NewServer(fullRouterForTest(t, root))
	defer srv.Close()

	resp, err := http.Post(srv.URL+"/api/projects/demo/drift/remediation/pr", "application/json", strings.NewReader(`{"tool":"terraform","mode":"revert"}`))
	if err != nil {
		t.Fatalf("POST drift remediation pr: %v", err)
	}
	defer closeBody(resp.Body)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("drift remediation pr status = %d, want 409", resp.StatusCode)
	}
	if branch := gitOutputForTest(t, projectDir, "rev-parse", "--abbrev-ref", "HEAD"); branch != "main" {
		t.Fatalf("dirty rejection should leave branch on main, got %q", branch)
	}
	if _, err := os.Stat(filepath.Join(projectDir, ".iac-studio", "remediations", "iac-studio-drift-revert-demo", "README.md")); !os.IsNotExist(err) {
		t.Fatalf("dirty rejection should not write review artifacts, stat err = %v", err)
	}
}

func TestProjectDriftRemediationArtifactsEndpointRebuildsServerProposal(t *testing.T) {
	root := t.TempDir()
	projectDir := filepath.Join(root, "demo")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatalf("mkdir project: %v", err)
	}

	srv := httptest.NewServer(fullRouterForTest(t, root))
	defer srv.Close()

	body := `{
		"mode": "codify",
		"proposal": {
			"mode": "codify",
			"title": "Codify reviewed drift for demo",
			"branch": "iac-studio-drift-codify-demo",
			"commit_message": "Codify drift for demo",
			"body": "## Summary\n- Reviewed finding: aws_s3_bucket.logs\n",
			"findings": [{
				"address": "aws_s3_bucket.logs",
				"type": "aws_s3_bucket",
				"name": "logs",
				"status": "drifted",
				"path": "tags",
				"classification": "legitimate_config_change",
				"recommended_action": "codify_or_accept",
				"reason": "Only metadata fields drifted."
			}],
			"file_changes": [{
				"path": "main.tf",
				"action": "codify",
				"address": "aws_s3_bucket.logs",
				"field": "tags",
				"summary": "Update aws_s3_bucket.logs tags to the current state value."
			}]
		}
	}`
	resp, err := http.Post(srv.URL+"/api/projects/demo/drift/remediation/artifacts", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("POST drift remediation artifacts with proposal: %v", err)
	}
	defer closeBody(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("drift remediation artifacts status = %d", resp.StatusCode)
	}

	var payload struct {
		ID       string `json:"id"`
		Root     string `json:"root"`
		Proposal struct {
			Title string `json:"title"`
		} `json:"proposal"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("decode drift remediation artifacts response: %v", err)
	}
	if payload.ID != "iac-studio-drift-codify-demo" ||
		payload.Proposal.Title != "Codify drift for demo" {
		t.Fatalf("unexpected artifact payload: %#v", payload)
	}
	prBody, err := os.ReadFile(filepath.Join(projectDir, ".iac-studio", "remediations", "iac-studio-drift-codify-demo", "pr-body.md"))
	if err != nil {
		t.Fatalf("read pr-body artifact: %v", err)
	}
	if strings.Contains(string(prBody), "Reviewed finding: aws_s3_bucket.logs") ||
		!strings.Contains(string(prBody), "No active drift findings matched this remediation mode.") {
		t.Fatalf("artifact body should be rebuilt server-side, got:\n%s", string(prBody))
	}
}

func TestProjectDriftRemediationArtifactsEndpointRejectsProposalModeMismatch(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "demo"), 0o755); err != nil {
		t.Fatalf("mkdir project: %v", err)
	}
	srv := httptest.NewServer(fullRouterForTest(t, root))
	defer srv.Close()

	resp, err := http.Post(srv.URL+"/api/projects/demo/drift/remediation/artifacts", "application/json", strings.NewReader(`{
		"mode": "revert",
		"proposal": {
			"mode": "codify",
			"title": "Codify drift for demo",
			"branch": "iac-studio-drift-codify-demo",
			"commit_message": "Codify drift for demo",
			"body": "## Summary",
			"findings": [],
			"file_changes": []
		}
	}`))
	if err != nil {
		t.Fatalf("POST drift remediation artifacts mismatch: %v", err)
	}
	defer closeBody(resp.Body)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("drift remediation artifacts status = %d, want 400", resp.StatusCode)
	}
	assertResponseBodyContains(t, resp, "mode must match proposal mode")
}
