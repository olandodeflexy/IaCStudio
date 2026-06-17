package mcpairlock

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"
)

func TestDiscoverToolsPersistsInventoryAndDetectsSchemaChanges(t *testing.T) {
	root := t.TempDir()
	calls := 0
	manager := NewManager(root,
		WithDefinitions([]ServerDefinition{{
			ID:              "terraform",
			Name:            "Terraform",
			Command:         testExecutable(t),
			Transport:       "stdio",
			Trusted:         true,
			ReadOnlyDefault: true,
			CredentialMode:  "none",
		}}),
		WithToolDiscoverer(func(context.Context, ServerDefinition, time.Duration) DiscoveryProbeResult {
			calls++
			schema := map[string]any{"type": "object", "properties": map[string]any{"query": map[string]any{"type": "string"}}}
			if calls == 2 {
				schema = map[string]any{"type": "object", "properties": map[string]any{"query": map[string]any{"type": "string"}, "limit": map[string]any{"type": "integer"}}}
			}
			return DiscoveryProbeResult{Tools: []DiscoveredTool{{
				Name:        "list_modules",
				Description: "List Terraform registry modules.",
				InputSchema: schema,
				Annotations: map[string]any{"readOnlyHint": true},
			}}}
		}),
	)

	first, err := manager.DiscoverTools(context.Background(), "terraform")
	if err != nil {
		t.Fatalf("DiscoverTools first: %v", err)
	}
	if len(first.Tools) != 1 || first.Tools[0].SchemaState != "new" || first.Tools[0].Risk != RiskReadOnly || !first.Tools[0].Decision.Allowed {
		t.Fatalf("unexpected first discovery: %+v", first)
	}

	second, err := manager.DiscoverTools(context.Background(), "terraform")
	if err != nil {
		t.Fatalf("DiscoverTools second: %v", err)
	}
	if len(second.Tools) != 1 || second.Tools[0].SchemaState != "changed" {
		t.Fatalf("expected changed schema state, got %+v", second)
	}

	stored, err := manager.Inventory("terraform")
	if err != nil {
		t.Fatalf("Inventory: %v", err)
	}
	if len(stored.Tools) != 1 || stored.Tools[0].SchemaState != "known" || stored.Tools[0].InputSchemaHash != second.Tools[0].InputSchemaHash {
		t.Fatalf("inventory was not persisted: %+v", stored)
	}
}

func TestDiscoveredToolDecodesMCPCamelCaseSchema(t *testing.T) {
	var tool DiscoveredTool
	if err := json.Unmarshal([]byte(`{"name":"list_modules","inputSchema":{"type":"object"}}`), &tool); err != nil {
		t.Fatalf("decode tool: %v", err)
	}
	if tool.InputSchema["type"] != "object" {
		t.Fatalf("expected inputSchema to decode, got %+v", tool.InputSchema)
	}
}

func TestToolFirewallBlocksUnknownAndApprovalGatesAllowlistedMutation(t *testing.T) {
	manager := NewManager(t.TempDir(), WithToolAllowlist(ToolAllowlist{
		ServerTools: map[string][]string{"aws": []string{"create_bucket"}},
		ProjectTools: map[string]map[string][]string{
			"demo": map[string][]string{"aws": []string{"mystery_tool"}},
		},
	}))

	unknown := manager.DecideTool("aws", "", "mystery_tool", RiskUnknown)
	if unknown.Status != "blocked" || unknown.Allowed || unknown.ApprovalRequired {
		t.Fatalf("unknown tools must fail closed, got %+v", unknown)
	}

	projectUnknown := manager.DecideTool("aws", "demo", "mystery_tool", RiskUnknown)
	if projectUnknown.Status != "approval_required" || !projectUnknown.ApprovalRequired || !projectUnknown.Allowlisted {
		t.Fatalf("project allowlisted unknown tool should require approval, got %+v", projectUnknown)
	}

	mutation := manager.DecideTool("aws", "", "create_bucket", RiskCloudMutation)
	if mutation.Status != "approval_required" || !mutation.ApprovalRequired || mutation.Allowed {
		t.Fatalf("allowlisted cloud mutation should require approval before execution, got %+v", mutation)
	}

	readOnly := manager.DecideTool("aws", "", "list_buckets", RiskReadOnly)
	if readOnly.Status != "allowed" || !readOnly.Allowed || readOnly.ApprovalRequired {
		t.Fatalf("read-only tools should be allowed by default, got %+v", readOnly)
	}
}

func TestClassifyToolConservatively(t *testing.T) {
	cases := []struct {
		name string
		tool DiscoveredTool
		want ToolRisk
	}{
		{
			name: "read-only annotation",
			tool: DiscoveredTool{Name: "search_docs", Description: "Search provider documentation", Annotations: map[string]any{"readOnlyHint": true}},
			want: RiskReadOnly,
		},
		{
			name: "destructive",
			tool: DiscoveredTool{Name: "delete_bucket", Description: "Delete an S3 bucket"},
			want: RiskDestructive,
		},
		{
			name: "secret sensitive",
			tool: DiscoveredTool{Name: "assume_role", Description: "Return a session token"},
			want: RiskSecretSensitive,
		},
		{
			name: "workspace write",
			tool: DiscoveredTool{Name: "edit_file", Description: "Modify file in workspace"},
			want: RiskModifyWorkspace,
		},
		{
			name: "unknown",
			tool: DiscoveredTool{Name: "do_magic", Description: "Perform custom action"},
			want: RiskUnknown,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ClassifyTool(tc.tool); got != tc.want {
				t.Fatalf("ClassifyTool() = %s, want %s", got, tc.want)
			}
		})
	}
}

func TestDiscoverToolsUnknownServerFailsClosed(t *testing.T) {
	manager := NewManager(t.TempDir())

	_, err := manager.DiscoverTools(context.Background(), "missing")

	if !errors.Is(err, ErrUnknownServer) {
		t.Fatalf("expected ErrUnknownServer, got %v", err)
	}
}
