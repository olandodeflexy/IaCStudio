package mcpairlock

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
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
			if calls >= 2 {
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
	if len(first.Tools) != 1 || first.Tools[0].SchemaState != "new" || first.Tools[0].Risk != RiskReadOnly || first.Tools[0].Decision.Status != "blocked" {
		t.Fatalf("unexpected first discovery: %+v", first)
	}

	second, err := manager.DiscoverTools(context.Background(), "terraform")
	if err != nil {
		t.Fatalf("DiscoverTools second: %v", err)
	}
	if len(second.Tools) != 1 || second.Tools[0].SchemaState != "changed" || second.Tools[0].Decision.Status != "blocked" {
		t.Fatalf("expected changed schema state, got %+v", second)
	}

	stored, err := manager.Inventory("terraform")
	if err != nil {
		t.Fatalf("Inventory: %v", err)
	}
	if len(stored.Tools) != 1 || stored.Tools[0].SchemaState != "changed" || stored.Tools[0].Decision.Status != "blocked" || stored.Tools[0].InputSchemaHash != second.Tools[0].InputSchemaHash {
		t.Fatalf("inventory was not persisted: %+v", stored)
	}

	third, err := manager.DiscoverTools(context.Background(), "terraform")
	if err != nil {
		t.Fatalf("DiscoverTools third: %v", err)
	}
	if len(third.Tools) != 1 || third.Tools[0].SchemaState != "known" || !third.Tools[0].Decision.Allowed {
		t.Fatalf("expected stable schema to become known and allowed, got %+v", third)
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

func TestDiscoverToolsPreservesPersistedAllowlist(t *testing.T) {
	manager := NewManager(t.TempDir(),
		WithDefinitions([]ServerDefinition{{
			ID:              "aws",
			Name:            "AWS",
			Command:         testExecutable(t),
			Transport:       "stdio",
			Trusted:         true,
			ReadOnlyDefault: true,
			CredentialMode:  "none",
		}}),
		WithToolDiscoverer(func(context.Context, ServerDefinition, time.Duration) DiscoveryProbeResult {
			return DiscoveryProbeResult{Tools: []DiscoveredTool{{
				Name:        "create_bucket",
				Description: "Create an S3 bucket",
				InputSchema: map[string]any{"type": "object"},
			}}}
		}),
	)

	if _, err := manager.SetToolAllowlist("aws", "", "create_bucket", true); err != nil {
		t.Fatalf("SetToolAllowlist: %v", err)
	}

	inventory, err := manager.DiscoverTools(context.Background(), "aws")
	if err != nil {
		t.Fatalf("DiscoverTools: %v", err)
	}
	if len(inventory.Tools) != 1 {
		t.Fatalf("expected one discovered tool, got %+v", inventory.Tools)
	}
	decision := inventory.Tools[0].Decision
	if !decision.Allowlisted || decision.Status != "approval_required" {
		t.Fatalf("expected persisted allowlist to survive discovery, got %+v", decision)
	}

	entry, err := manager.EvaluateTool("aws", "", "create_bucket")
	if err != nil {
		t.Fatalf("EvaluateTool: %v", err)
	}
	if !entry.Decision.Allowlisted || entry.Decision.Status != "approval_required" {
		t.Fatalf("expected allowlist to remain persisted after discovery, got %+v", entry.Decision)
	}
}

func TestDiscoverToolsDoesNotDropConcurrentAllowlistUpdate(t *testing.T) {
	discoveryStarted := make(chan struct{})
	releaseDiscovery := make(chan struct{})
	manager := NewManager(t.TempDir(),
		WithDefinitions([]ServerDefinition{{
			ID:              "aws",
			Name:            "AWS",
			Command:         testExecutable(t),
			Transport:       "stdio",
			Trusted:         true,
			ReadOnlyDefault: true,
			CredentialMode:  "none",
		}}),
		WithToolDiscoverer(func(context.Context, ServerDefinition, time.Duration) DiscoveryProbeResult {
			close(discoveryStarted)
			<-releaseDiscovery
			return DiscoveryProbeResult{Tools: []DiscoveredTool{{
				Name:        "create_bucket",
				Description: "Create an S3 bucket",
				InputSchema: map[string]any{"type": "object"},
			}}}
		}),
	)

	errCh := make(chan error, 1)
	go func() {
		_, err := manager.DiscoverTools(context.Background(), "aws")
		errCh <- err
	}()

	<-discoveryStarted
	if _, err := manager.SetToolAllowlist("aws", "demo", "create_bucket", true); err != nil {
		t.Fatalf("SetToolAllowlist during discovery: %v", err)
	}
	close(releaseDiscovery)
	if err := <-errCh; err != nil {
		t.Fatalf("DiscoverTools: %v", err)
	}

	entry, err := manager.EvaluateTool("aws", "demo", "create_bucket")
	if err != nil {
		t.Fatalf("EvaluateTool: %v", err)
	}
	if !entry.Decision.Allowlisted || entry.Decision.Status != "approval_required" {
		t.Fatalf("expected concurrent allowlist update to survive discovery, got %+v", entry.Decision)
	}
}

func TestDiscoverToolsDoesNotDropConcurrentServerInventory(t *testing.T) {
	started := map[string]chan struct{}{
		"aws":       make(chan struct{}),
		"terraform": make(chan struct{}),
	}
	releaseDiscovery := make(chan struct{})
	manager := NewManager(t.TempDir(),
		WithDefinitions([]ServerDefinition{
			{
				ID:              "aws",
				Name:            "AWS",
				Command:         testExecutable(t),
				Transport:       "stdio",
				Trusted:         true,
				ReadOnlyDefault: true,
				CredentialMode:  "none",
			},
			{
				ID:              "terraform",
				Name:            "Terraform",
				Command:         testExecutable(t),
				Transport:       "stdio",
				Trusted:         true,
				ReadOnlyDefault: true,
				CredentialMode:  "none",
			},
		}),
		WithToolDiscoverer(func(_ context.Context, definition ServerDefinition, _ time.Duration) DiscoveryProbeResult {
			close(started[definition.ID])
			<-releaseDiscovery
			return DiscoveryProbeResult{Tools: []DiscoveredTool{{
				Name:        definition.ID + "_tool",
				Description: "Read-only test tool",
				InputSchema: map[string]any{"type": "object"},
				Annotations: map[string]any{"readOnlyHint": true},
			}}}
		}),
	)

	errCh := make(chan error, 2)
	for _, id := range []string{"aws", "terraform"} {
		id := id
		go func() {
			_, err := manager.DiscoverTools(context.Background(), id)
			errCh <- err
		}()
	}

	<-started["aws"]
	<-started["terraform"]
	close(releaseDiscovery)
	for range 2 {
		if err := <-errCh; err != nil {
			t.Fatalf("DiscoverTools: %v", err)
		}
	}

	for _, id := range []string{"aws", "terraform"} {
		inventory, err := manager.Inventory(id)
		if err != nil {
			t.Fatalf("Inventory %s: %v", id, err)
		}
		if len(inventory.Tools) != 1 || inventory.Tools[0].Name != id+"_tool" {
			t.Fatalf("expected %s inventory to survive concurrent discovery, got %+v", id, inventory.Tools)
		}
	}
}

func TestDiscoverToolsRecoversFromCorruptInventory(t *testing.T) {
	root := t.TempDir()
	manager := NewManager(root,
		WithDefinitions([]ServerDefinition{{
			ID:              "aws",
			Name:            "AWS",
			Command:         testExecutable(t),
			Transport:       "stdio",
			Trusted:         true,
			ReadOnlyDefault: true,
			CredentialMode:  "none",
		}}),
		WithToolDiscoverer(func(context.Context, ServerDefinition, time.Duration) DiscoveryProbeResult {
			return DiscoveryProbeResult{Tools: []DiscoveredTool{{
				Name:        "list_buckets",
				Description: "List S3 buckets.",
				InputSchema: map[string]any{"type": "object"},
				Annotations: map[string]any{"readOnlyHint": true},
			}}}
		}),
	)
	if err := os.MkdirAll(filepath.Dir(manager.inventoryPath), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(manager.inventoryPath, []byte("{"), 0o600); err != nil {
		t.Fatalf("WriteFile corrupt inventory: %v", err)
	}

	inventory, err := manager.DiscoverTools(context.Background(), "aws")
	if err != nil {
		t.Fatalf("DiscoverTools: %v", err)
	}
	if len(inventory.Tools) != 1 {
		t.Fatalf("expected discovered tool despite corrupt inventory, got %+v", inventory.Tools)
	}
	if inventory.Tools[0].Name != "list_buckets" || inventory.Tools[0].SchemaState != "new" || inventory.Tools[0].Decision.Status != "blocked" {
		t.Fatalf("expected fail-closed fresh discovery, got %+v", inventory.Tools[0])
	}
	if !hasCheck(inventory.Checks, "inventory", "warn") {
		t.Fatalf("expected inventory warning check, got %+v", inventory.Checks)
	}

	stored, err := manager.Inventory("aws")
	if err != nil {
		t.Fatalf("Inventory after recovery: %v", err)
	}
	if len(stored.Tools) != 1 || stored.Tools[0].Name != "list_buckets" {
		t.Fatalf("expected recovered inventory to be persisted, got %+v", stored.Tools)
	}
}

func TestDiscoverToolsInventoryWarningsDoNotExposeInventoryPath(t *testing.T) {
	root := t.TempDir()
	manager := NewManager(root,
		WithDefinitions([]ServerDefinition{{
			ID:              "aws",
			Name:            "AWS",
			Command:         testExecutable(t),
			Transport:       "stdio",
			Trusted:         true,
			ReadOnlyDefault: true,
			CredentialMode:  "none",
		}}),
		WithToolDiscoverer(func(context.Context, ServerDefinition, time.Duration) DiscoveryProbeResult {
			return DiscoveryProbeResult{Tools: []DiscoveredTool{{
				Name:        "list_buckets",
				Description: "List S3 buckets.",
				InputSchema: map[string]any{"type": "object"},
				Annotations: map[string]any{"readOnlyHint": true},
			}}}
		}),
	)
	inventoryDir := filepath.Join(root, ".iac-studio")
	if err := os.MkdirAll(inventoryDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	manager.inventoryPath = inventoryDir

	inventory, err := manager.DiscoverTools(context.Background(), "aws")
	if err != nil {
		t.Fatalf("DiscoverTools: %v", err)
	}
	if len(inventory.Tools) != 1 {
		t.Fatalf("expected discovery to continue, got %+v", inventory.Tools)
	}
	messages := checkMessages(inventory.Checks, "inventory")
	if len(messages) == 0 {
		t.Fatalf("expected inventory warning checks, got %+v", inventory.Checks)
	}
	for _, message := range messages {
		if strings.Contains(message, root) || strings.Contains(message, manager.inventoryPath) {
			t.Fatalf("inventory warning leaked path %q in %q", root, message)
		}
	}
}

func TestEvaluateToolErrorDoesNotExposeInventoryPath(t *testing.T) {
	root := t.TempDir()
	manager := NewManager(root,
		WithDefinitions([]ServerDefinition{{
			ID:              "aws",
			Name:            "AWS",
			Command:         testExecutable(t),
			Transport:       "stdio",
			Trusted:         true,
			ReadOnlyDefault: true,
			CredentialMode:  "none",
		}}),
	)
	inventoryDir := filepath.Join(root, ".iac-studio")
	if err := os.MkdirAll(inventoryDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	manager.inventoryPath = inventoryDir

	_, err := manager.EvaluateTool("aws", "", "list_buckets")
	if err == nil {
		t.Fatal("expected inventory load error")
	}
	assertInventoryErrorSanitized(t, err, root, manager.inventoryPath)
	if !strings.Contains(err.Error(), "Airlock tool inventory filesystem access failed") {
		t.Fatalf("expected public inventory filesystem error, got %q", err.Error())
	}
}

func TestSetToolAllowlistLoadErrorDoesNotExposeInventoryPath(t *testing.T) {
	root := t.TempDir()
	manager := NewManager(root,
		WithDefinitions([]ServerDefinition{{
			ID:              "aws",
			Name:            "AWS",
			Command:         testExecutable(t),
			Transport:       "stdio",
			Trusted:         true,
			ReadOnlyDefault: true,
			CredentialMode:  "none",
		}}),
	)
	inventoryDir := filepath.Join(root, ".iac-studio")
	if err := os.MkdirAll(inventoryDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	manager.inventoryPath = inventoryDir

	_, err := manager.SetToolAllowlist("aws", "", "list_buckets", true)
	if err == nil {
		t.Fatal("expected inventory load error")
	}
	assertInventoryErrorSanitized(t, err, root, manager.inventoryPath)
	if !strings.Contains(err.Error(), "Airlock tool inventory filesystem access failed") {
		t.Fatalf("expected public inventory filesystem error, got %q", err.Error())
	}
}

func TestPublicInventoryErrorDoesNotExposeSavePaths(t *testing.T) {
	root := t.TempDir()
	manager := NewManager(root)
	manager.inventoryPath = filepath.Join(root, ".iac-studio", toolInventoryName)
	rawErr := &os.LinkError{
		Op:  "rename",
		Old: manager.inventoryPath + ".tmp-123",
		New: manager.inventoryPath,
		Err: errors.New("permission denied"),
	}

	err := manager.publicInventoryError(rawErr)

	assertInventoryErrorSanitized(t, err, root, manager.inventoryPath)
	if !strings.Contains(err.Error(), "Airlock tool inventory replacement failed") {
		t.Fatalf("expected public inventory replacement error, got %q", err.Error())
	}
}

func TestSetToolAllowlistCanRemoveMissingEntries(t *testing.T) {
	manager := NewManager(t.TempDir(),
		WithDefinitions([]ServerDefinition{{
			ID:              "aws",
			Name:            "AWS",
			Command:         testExecutable(t),
			Transport:       "stdio",
			Trusted:         true,
			ReadOnlyDefault: true,
			CredentialMode:  "none",
		}}),
	)

	if _, err := manager.SetToolAllowlist("aws", "", "missing_tool", false); err != nil {
		t.Fatalf("remove missing server allowlist entry: %v", err)
	}
	if _, err := manager.SetToolAllowlist("aws", "demo", "missing_tool", false); err != nil {
		t.Fatalf("remove missing project allowlist entry: %v", err)
	}
}

func TestInventoryMissingSchemaStateFailsClosed(t *testing.T) {
	manager := NewManager(t.TempDir(),
		WithDefinitions([]ServerDefinition{{
			ID:              "aws",
			Name:            "AWS",
			Command:         testExecutable(t),
			Transport:       "stdio",
			Trusted:         true,
			ReadOnlyDefault: true,
			CredentialMode:  "none",
		}}),
	)
	if err := manager.saveInventoryUnlocked(persistedToolInventory{
		Servers: map[string]persistedServerTools{
			"aws": {
				DiscoveredAt: time.Now().UTC().Format(time.RFC3339),
				Tools: map[string]persistedToolRecord{
					"list_buckets": {
						Description:     "List S3 buckets.",
						InputSchemaHash: "sha256:legacy",
						LastSeenAt:      time.Now().UTC().Format(time.RFC3339),
						Risk:            RiskReadOnly,
					},
				},
			},
		},
	}); err != nil {
		t.Fatalf("saveInventoryUnlocked: %v", err)
	}

	inventory, err := manager.Inventory("aws")
	if err != nil {
		t.Fatalf("Inventory: %v", err)
	}
	if len(inventory.Tools) != 1 {
		t.Fatalf("expected one tool, got %+v", inventory.Tools)
	}
	tool := inventory.Tools[0]
	if tool.SchemaState != "unknown" {
		t.Fatalf("expected missing schema state to normalize to unknown, got %+v", tool)
	}
	if tool.Decision.Status != "blocked" || tool.Decision.Allowed {
		t.Fatalf("missing schema state should fail closed, got %+v", tool.Decision)
	}
}

func TestSaveInventoryAtomicWriteFailurePreservesExistingInventory(t *testing.T) {
	root := t.TempDir()
	manager := NewManager(root)
	inventoryDir := filepath.Join(root, ".iac-studio")
	if err := os.MkdirAll(inventoryDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	original := []byte("{\"servers\":{}}\n")
	if err := os.WriteFile(manager.inventoryPath, original, 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if err := os.Chmod(inventoryDir, 0o500); err != nil {
		t.Fatalf("Chmod read-only: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Chmod(inventoryDir, 0o700)
	})

	err := manager.saveInventoryUnlocked(persistedToolInventory{
		Servers: map[string]persistedServerTools{
			"aws": {
				Tools: map[string]persistedToolRecord{
					"create_bucket": {Description: "Create an S3 bucket"},
				},
			},
		},
	})
	if err == nil {
		t.Skip("directory permissions did not block inventory replacement on this platform")
	}

	got, readErr := os.ReadFile(manager.inventoryPath)
	if readErr != nil {
		t.Fatalf("ReadFile existing inventory: %v", readErr)
	}
	if !bytes.Equal(got, original) {
		t.Fatalf("existing inventory changed after failed write: got %q want %q", got, original)
	}
}

func TestWriteFileAtomicReplacesExistingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "inventory.json")
	if err := os.WriteFile(path, []byte("old\n"), 0o600); err != nil {
		t.Fatalf("WriteFile old: %v", err)
	}

	if err := writeFileAtomic(path, []byte("new\n")); err != nil {
		t.Fatalf("writeFileAtomic: %v", err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !bytes.Equal(got, []byte("new\n")) {
		t.Fatalf("unexpected replacement contents: got %q", got)
	}
}

func TestToolFirewallBlocksUnknownAndApprovalGatesAllowlistedMutation(t *testing.T) {
	manager := NewManager(t.TempDir(), WithToolAllowlist(ToolAllowlist{
		ServerTools: map[string][]string{"aws": []string{"create_bucket"}},
		ProjectTools: map[string]map[string][]string{
			"demo": map[string][]string{"aws": []string{"mystery_tool"}},
		},
	}))

	unknown := manager.DecideTool("aws", "", "mystery_tool", RiskUnknown, "unknown")
	if unknown.Status != "blocked" || unknown.Allowed || unknown.ApprovalRequired {
		t.Fatalf("unknown tools must fail closed, got %+v", unknown)
	}

	projectUnknown := manager.DecideTool("aws", "demo", "mystery_tool", RiskUnknown, "unknown")
	if projectUnknown.Status != "approval_required" || !projectUnknown.ApprovalRequired || !projectUnknown.Allowlisted {
		t.Fatalf("project allowlisted unknown tool should require approval, got %+v", projectUnknown)
	}

	mutation := manager.DecideTool("aws", "", "create_bucket", RiskCloudMutation, "known")
	if mutation.Status != "approval_required" || !mutation.ApprovalRequired || mutation.Allowed {
		t.Fatalf("allowlisted cloud mutation should require approval before execution, got %+v", mutation)
	}

	readOnly := manager.DecideTool("aws", "", "list_buckets", RiskReadOnly, "known")
	if readOnly.Status != "allowed" || !readOnly.Allowed || readOnly.ApprovalRequired {
		t.Fatalf("read-only tools should be allowed by default, got %+v", readOnly)
	}

	generateCode := manager.DecideTool("aws", "", "create_bucket", RiskGenerateCode, "known")
	if generateCode.Status != "approval_required" || !generateCode.ApprovalRequired || generateCode.Allowed {
		t.Fatalf("allowlisted generate-code tools should require approval, got %+v", generateCode)
	}

	changedReadOnly := manager.DecideTool("aws", "demo", "mystery_tool", RiskReadOnly, "changed")
	if changedReadOnly.Status != "approval_required" || !changedReadOnly.ApprovalRequired || !changedReadOnly.Allowlisted {
		t.Fatalf("allowlisted changed schemas should still require approval, got %+v", changedReadOnly)
	}

	missingStateReadOnly := manager.DecideTool("aws", "", "list_buckets", RiskReadOnly, "")
	if missingStateReadOnly.Status != "blocked" || missingStateReadOnly.Allowed {
		t.Fatalf("missing schema state should fail closed, got %+v", missingStateReadOnly)
	}

	invalidStateReadOnly := manager.DecideTool("aws", "", "list_buckets", RiskReadOnly, "unexpected")
	if invalidStateReadOnly.Status != "blocked" || invalidStateReadOnly.Allowed {
		t.Fatalf("invalid schema state should fail closed, got %+v", invalidStateReadOnly)
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

func TestDiscoverToolsReturnsEmptySlicesOnEarlyExit(t *testing.T) {
	root := t.TempDir()
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
			return DiscoveryProbeResult{TimedOut: true}
		}),
	)

	inventory, err := manager.DiscoverTools(context.Background(), "terraform")
	if err != nil {
		t.Fatalf("DiscoverTools: %v", err)
	}
	if inventory.Tools == nil {
		t.Fatal("expected Tools to be an empty slice")
	}
	if inventory.Checks == nil {
		t.Fatal("expected Checks to be an empty or populated slice")
	}
}

func TestInventoryReturnsEmptySlicesWhenNothingDiscoveredOrLoadFails(t *testing.T) {
	root := t.TempDir()
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
	)

	emptyInventory, err := manager.Inventory("terraform")
	if err != nil {
		t.Fatalf("Inventory empty: %v", err)
	}
	if emptyInventory.Tools == nil {
		t.Fatal("expected empty inventory Tools to be []")
	}
	if emptyInventory.Checks == nil {
		t.Fatal("expected empty inventory Checks to be []")
	}

	if err := os.MkdirAll(filepath.Join(root, ".iac-studio"), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, ".iac-studio", toolInventoryName), []byte("{"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	invalidInventory, err := manager.Inventory("terraform")
	if err != nil {
		t.Fatalf("Inventory invalid: %v", err)
	}
	if invalidInventory.Tools == nil {
		t.Fatal("expected invalid inventory Tools to be []")
	}
	if len(invalidInventory.Checks) != 1 {
		t.Fatalf("expected one warning check, got %+v", invalidInventory.Checks)
	}
}

func TestInventoryWarningDoesNotExposeInventoryPath(t *testing.T) {
	root := t.TempDir()
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
	)
	inventoryDir := filepath.Join(root, ".iac-studio")
	if err := os.MkdirAll(inventoryDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	manager.inventoryPath = inventoryDir

	inventory, err := manager.Inventory("terraform")
	if err != nil {
		t.Fatalf("Inventory: %v", err)
	}
	messages := checkMessages(inventory.Checks, "inventory")
	if len(messages) == 0 {
		t.Fatalf("expected inventory warning checks, got %+v", inventory.Checks)
	}
	for _, message := range messages {
		if strings.Contains(message, root) || strings.Contains(message, manager.inventoryPath) {
			t.Fatalf("inventory warning leaked path %q in %q", root, message)
		}
	}
}

func TestSchemaReviewReasonMatchesRediscoveryFlow(t *testing.T) {
	decision := decideToolWithAllowlist("aws", "", "list_buckets", RiskReadOnly, "new", ToolAllowlist{})

	if strings.Contains(decision.Reason, "re-reviewed") {
		t.Fatalf("schema review reason mentions unavailable review flow: %q", decision.Reason)
	}
	if !strings.Contains(decision.Reason, "later discovery confirms the same schema") {
		t.Fatalf("schema review reason does not explain stable rediscovery flow: %q", decision.Reason)
	}
}

func TestWriteToolDiscoveryRequestsReturnsEncodeError(t *testing.T) {
	errEncode := errors.New("encode failed")

	err := writeToolDiscoveryRequests(json.NewEncoder(failingWriter{err: errEncode}), noopCloser{})

	if !errors.Is(err, errEncode) {
		t.Fatalf("expected wrapped encode error, got %v", err)
	}
	if !strings.Contains(err.Error(), "write initialize request") {
		t.Fatalf("expected write context in error, got %v", err)
	}
}

func TestWriteToolDiscoveryRequestsReturnsCloseError(t *testing.T) {
	errClose := errors.New("close failed")
	var buf bytes.Buffer

	err := writeToolDiscoveryRequests(json.NewEncoder(&buf), errorCloser{err: errClose})

	if !errors.Is(err, errClose) {
		t.Fatalf("expected wrapped close error, got %v", err)
	}
	if !strings.Contains(err.Error(), "close discovery stdin") {
		t.Fatalf("expected close context in error, got %v", err)
	}
	if !strings.Contains(buf.String(), `"method":"tools/list"`) {
		t.Fatalf("expected tools/list request to be written before close, got %q", buf.String())
	}
}

func hasCheck(checks []Check, name, status string) bool {
	for _, check := range checks {
		if check.Name == name && check.Status == status {
			return true
		}
	}
	return false
}

func checkMessages(checks []Check, name string) []string {
	messages := []string{}
	for _, check := range checks {
		if check.Name == name {
			messages = append(messages, check.Message)
		}
	}
	return messages
}

func assertInventoryErrorSanitized(t *testing.T, err error, sensitive ...string) {
	t.Helper()
	if err == nil {
		t.Fatal("expected error")
	}
	message := err.Error()
	for _, value := range sensitive {
		if value != "" && strings.Contains(message, value) {
			t.Fatalf("inventory error leaked %q in %q", value, message)
		}
	}
}

type failingWriter struct {
	err error
}

func (w failingWriter) Write([]byte) (int, error) {
	return 0, w.err
}

type noopCloser struct{}

func (noopCloser) Close() error {
	return nil
}

type errorCloser struct {
	err error
}

func (c errorCloser) Close() error {
	return c.err
}
