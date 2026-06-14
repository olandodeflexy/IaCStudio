package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/iac-studio/iac-studio/internal/cloudconnections"
	"github.com/iac-studio/iac-studio/internal/runner"
)

func TestServerLifecycleAndToolList(t *testing.T) {
	server := newTestServer(t)
	input := strings.NewReader(strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"test","version":"1"}}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}`,
		"",
	}, "\n"))
	var output bytes.Buffer

	if err := server.Serve(context.Background(), input, &output); err != nil {
		t.Fatalf("Serve: %v", err)
	}

	lines := strings.Split(strings.TrimSpace(output.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected initialize and tools/list responses, got %d: %s", len(lines), output.String())
	}
	var initResp struct {
		Result struct {
			ProtocolVersion string `json:"protocolVersion"`
			Capabilities    struct {
				Tools map[string]any `json:"tools"`
			} `json:"capabilities"`
		} `json:"result"`
	}
	if err := json.Unmarshal([]byte(lines[0]), &initResp); err != nil {
		t.Fatalf("decode initialize response: %v", err)
	}
	if initResp.Result.ProtocolVersion != ProtocolVersion {
		t.Fatalf("protocol version = %q", initResp.Result.ProtocolVersion)
	}
	if initResp.Result.Capabilities.Tools == nil {
		t.Fatalf("initialize response did not advertise tools capability")
	}

	var listResp struct {
		Result struct {
			Tools []Tool `json:"tools"`
		} `json:"result"`
	}
	if err := json.Unmarshal([]byte(lines[1]), &listResp); err != nil {
		t.Fatalf("decode tools/list response: %v", err)
	}
	for _, name := range []string{"list_projects", "inspect_project", "list_mcp_airlock_servers", "check_mcp_airlock_server", "classify_plan", "scan_drift", "open_remediation_pr", "apply"} {
		if !containsTool(listResp.Result.Tools, name) {
			t.Fatalf("tools/list missing %s", name)
		}
	}
}

func TestInspectProjectReusesParserAndSnapshots(t *testing.T) {
	server := newTestServer(t)
	projectDir := writeTerraformProject(t, server.projectsDir, "demo")
	if err := os.MkdirAll(filepath.Join(projectDir, ".iac-studio", "snapshots"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(projectDir, ".iac-studio", "snapshots", "checkpoint.json"), []byte(`{
  "id": "checkpoint",
  "project": "demo",
  "tool": "terraform",
  "command": "apply",
  "work_dir": "",
  "created_at": "2026-06-12T10:00:00Z",
  "status": "recorded"
}`), 0o644); err != nil {
		t.Fatal(err)
	}

	result := callTool(t, server, "inspect_project", map[string]any{"project": "demo"})
	if result.IsError {
		t.Fatalf("inspect_project returned error: %s", result.Content[0].Text)
	}
	var payload struct {
		Tool          string `json:"tool"`
		ResourceCount int    `json:"resource_count"`
		Resources     []struct {
			ID   string `json:"id"`
			File string `json:"file"`
		} `json:"resources"`
		Snapshots []struct {
			ID string `json:"id"`
		} `json:"snapshots"`
	}
	mustRemarshal(t, result.StructuredContent, &payload)
	if payload.Tool != "terraform" || payload.ResourceCount != 1 {
		t.Fatalf("unexpected project inspection: %+v", payload)
	}
	if payload.Resources[0].ID != "aws_s3_bucket.logs" || payload.Resources[0].File != "main.tf" {
		t.Fatalf("unexpected resource metadata: %+v", payload.Resources[0])
	}
	if len(payload.Snapshots) != 1 || payload.Snapshots[0].ID != "checkpoint" {
		t.Fatalf("expected snapshot metadata, got %+v", payload.Snapshots)
	}
}

func TestClassifyPlanReturnsStructuredRisk(t *testing.T) {
	server := newTestServer(t)
	planJSON := `{
  "resource_changes": [
    {
      "address": "aws_security_group.web",
      "type": "aws_security_group",
      "name": "web",
      "provider_name": "registry.terraform.io/hashicorp/aws",
      "change": {
        "actions": ["create"],
        "before": null,
        "after": {
          "ingress": [{"cidr_blocks": ["0.0.0.0/0"], "from_port": 22, "to_port": 22}]
        }
      }
    }
  ]
}`
	result := callTool(t, server, "classify_plan", map[string]any{"plan_json": planJSON})
	if result.IsError {
		t.Fatalf("classify_plan returned error: %s", result.Content[0].Text)
	}
	var payload struct {
		Summary struct {
			Risky                  int  `json:"risky"`
			RequiresAcknowledgment bool `json:"requires_acknowledgment"`
		} `json:"summary"`
		Changes []struct {
			Risk string `json:"risk"`
		} `json:"changes"`
	}
	mustRemarshal(t, result.StructuredContent, &payload)
	if payload.Summary.Risky != 1 || !payload.Summary.RequiresAcknowledgment || payload.Changes[0].Risk != "risky" {
		t.Fatalf("unexpected classification: %+v", payload)
	}
}

func TestConnectionScopeRedactsSecrets(t *testing.T) {
	server := newTestServer(t)
	connection, err := server.cloudConnections.Save(cloudconnections.Connection{
		Name:       "prod",
		Provider:   cloudconnections.ProviderAWS,
		AuthMethod: "aws_static",
		Region:     "us-east-1",
		Metadata:   map[string]string{"access_key_id": "AKIATEST"},
		Secrets:    map[string]string{"secret_access_key": "super-secret", "session_token": "session-secret"},
	})
	if err != nil {
		t.Fatalf("save connection: %v", err)
	}

	result := callTool(t, server, "inspect_connection_scope", map[string]any{"connection_id": connection.ID})
	if result.IsError {
		t.Fatalf("inspect_connection_scope returned error: %s", result.Content[0].Text)
	}
	if strings.Contains(result.Content[0].Text, "super-secret") || strings.Contains(result.Content[0].Text, "session-secret") {
		t.Fatalf("connection scope leaked secret values: %s", result.Content[0].Text)
	}
	var payload struct {
		CommandEnvKeys []string `json:"command_env_keys"`
		Connection     struct {
			SecretFields []string `json:"secret_fields"`
		} `json:"connection"`
	}
	mustRemarshal(t, result.StructuredContent, &payload)
	if !contains(payload.CommandEnvKeys, "AWS_SECRET_ACCESS_KEY") || !contains(payload.Connection.SecretFields, "secret_access_key") {
		t.Fatalf("expected redacted secret field metadata, got %+v", payload)
	}
}

func TestMCPAirlockToolsExposeTrustedReadOnlyStatus(t *testing.T) {
	server := newTestServer(t)

	listResult := callTool(t, server, "list_mcp_airlock_servers", map[string]any{})
	if listResult.IsError {
		t.Fatalf("list_mcp_airlock_servers returned error: %s", listResult.Content[0].Text)
	}
	var listPayload struct {
		Servers []struct {
			Server struct {
				ID              string `json:"id"`
				Trusted         bool   `json:"trusted"`
				ReadOnlyDefault bool   `json:"read_only_default"`
				CredentialMode  string `json:"credential_mode"`
			} `json:"server"`
		} `json:"servers"`
	}
	mustRemarshal(t, listResult.StructuredContent, &listPayload)
	if len(listPayload.Servers) == 0 {
		t.Fatal("expected Airlock server definitions")
	}
	for _, status := range listPayload.Servers {
		if !status.Server.Trusted || !status.Server.ReadOnlyDefault || status.Server.CredentialMode != "none" {
			t.Fatalf("Airlock server is not constrained by default: %+v", status.Server)
		}
	}

	checkResult := callTool(t, server, "check_mcp_airlock_server", map[string]any{"server_id": "aws-official"})
	if checkResult.IsError {
		t.Fatalf("check_mcp_airlock_server returned error: %s", checkResult.Content[0].Text)
	}
	var checkPayload struct {
		Server struct {
			State      string `json:"state"`
			Configured bool   `json:"configured"`
		} `json:"server"`
	}
	mustRemarshal(t, checkResult.StructuredContent, &checkPayload)
	if checkPayload.Server.State != "not_configured" || checkPayload.Server.Configured {
		t.Fatalf("expected AWS built-in to require explicit local command config, got %+v", checkPayload.Server)
	}
}

func TestSanitizeExecutionResultRedactsCredentialValues(t *testing.T) {
	result := sanitizeExecutionResult(&runner.ExecutionResult{
		ID:       "plan-1",
		Output:   "aws=AKIATEST secret=super-secret token=session-secret client=tenant-client-secret creds={\"private_key\":\"abc\"} region=us-east-1",
		ExitCode: 1,
		Status:   "failed",
	}, map[string]string{
		"AWS_ACCESS_KEY_ID":     "AKIATEST",
		"AWS_SECRET_ACCESS_KEY": "super-secret",
		"AWS_SESSION_TOKEN":     "session-secret",
		"ARM_CLIENT_SECRET":     "tenant-client-secret",
		"GOOGLE_CREDENTIALS":    "{\"private_key\":\"abc\"}",
		"AWS_REGION":            "us-east-1",
	})
	if result == nil {
		t.Fatal("expected sanitized result")
	}
	if strings.Contains(result.Output, "AKIATEST") ||
		strings.Contains(result.Output, "super-secret") ||
		strings.Contains(result.Output, "session-secret") ||
		strings.Contains(result.Output, "tenant-client-secret") ||
		strings.Contains(result.Output, "{\"private_key\":\"abc\"}") {
		t.Fatalf("expected secrets to be redacted, got %q", result.Output)
	}
	if !strings.Contains(result.Output, "region=us-east-1") {
		t.Fatalf("expected non-secret env values to remain visible, got %q", result.Output)
	}
}

func TestHighRiskToolRequiresApprovalAndAudits(t *testing.T) {
	server := newTestServer(t)
	result := callTool(t, server, "apply", map[string]any{"project": "demo", "reason": "test"})
	if result.IsError {
		t.Fatalf("approval_required is a structured gate, not an MCP error: %s", result.Content[0].Text)
	}
	var payload struct {
		Status           string `json:"status"`
		ApprovalRequired bool   `json:"approval_required"`
	}
	mustRemarshal(t, result.StructuredContent, &payload)
	if payload.Status != "approval_required" || !payload.ApprovalRequired {
		t.Fatalf("unexpected approval gate payload: %+v", payload)
	}

	events := readAuditEvents(t, server.projectsDir)
	if len(events) != 1 {
		t.Fatalf("expected one audit event, got %d", len(events))
	}
	if events[0].Tool != "apply" || events[0].Project != "demo" || !events[0].ApprovalRequired || events[0].Decision != "approval_required" {
		t.Fatalf("unexpected audit event: %+v", events[0])
	}
}

func TestOpenRemediationPRRequiresApprovalBeforeMutation(t *testing.T) {
	server := newTestServer(t)
	writeTerraformProject(t, server.projectsDir, "demo")

	result := callTool(t, server, "open_remediation_pr", map[string]any{"project": "demo", "mode": "revert"})
	if result.IsError {
		t.Fatalf("approval gate should be a structured response: %s", result.Content[0].Text)
	}
	var payload struct {
		Status string `json:"status"`
	}
	mustRemarshal(t, result.StructuredContent, &payload)
	if payload.Status != "approval_required" {
		t.Fatalf("expected approval_required, got %+v", payload)
	}
	if _, err := os.Stat(filepath.Join(server.projectsDir, "demo", ".iac-studio", "remediations")); !os.IsNotExist(err) {
		t.Fatalf("remediation artifacts should not be written before approval")
	}
}

func TestApprovalTokenValidation(t *testing.T) {
	server := newTestServer(t)
	if !server.approved("approve-me") {
		t.Fatalf("expected configured approval token to validate")
	}
	for _, token := range []string{"", "approve-m", "approve-me ", strings.Repeat("x", 1024)} {
		if server.approved(token) {
			t.Fatalf("expected token %q to be rejected", token)
		}
	}

	withoutToken := NewServer(Config{ProjectsDir: t.TempDir()})
	if withoutToken.approved("approve-me") {
		t.Fatalf("expected approval to be disabled without a configured token")
	}
}

func newTestServer(t *testing.T) *Server {
	t.Helper()
	return NewServer(Config{
		ProjectsDir:   t.TempDir(),
		ApprovalToken: "approve-me",
		Version:       "test",
		Now: func() time.Time {
			return time.Date(2026, 6, 12, 10, 0, 0, 0, time.UTC)
		},
	})
}

func callTool(t *testing.T, server *Server, name string, args map[string]any) ToolCallResult {
	t.Helper()
	rawArgs, err := json.Marshal(args)
	if err != nil {
		t.Fatal(err)
	}
	params, err := json.Marshal(toolCallParams{Name: name, Arguments: rawArgs})
	if err != nil {
		t.Fatal(err)
	}
	result, rpcErr := server.callTool(context.Background(), params)
	if rpcErr != nil {
		t.Fatalf("tool RPC error: %+v", rpcErr)
	}
	return result
}

func writeTerraformProject(t *testing.T, projectsDir, name string) string {
	t.Helper()
	projectDir := filepath.Join(projectsDir, name)
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(projectDir, ".iac-studio.json"), []byte(`{
  "name": "demo",
  "tool": "terraform",
  "layout": "flat"
}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(projectDir, "main.tf"), []byte(`resource "aws_s3_bucket" "logs" {
  bucket = "demo-logs"
}
`), 0o644); err != nil {
		t.Fatal(err)
	}
	return projectDir
}

func readAuditEvents(t *testing.T, projectsDir string) []AuditDecision {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(projectsDir, ".iac-studio", "mcp-audit.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	events := make([]AuditDecision, 0, len(lines))
	for _, line := range lines {
		var event AuditDecision
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			t.Fatal(err)
		}
		events = append(events, event)
	}
	return events
}

func mustRemarshal(t *testing.T, in any, out any) {
	t.Helper()
	data, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, out); err != nil {
		t.Fatal(err)
	}
}

func containsTool(tools []Tool, name string) bool {
	for _, tool := range tools {
		if tool.Name == name {
			return true
		}
	}
	return false
}

func contains(values []string, needle string) bool {
	for _, value := range values {
		if value == needle {
			return true
		}
	}
	return false
}
