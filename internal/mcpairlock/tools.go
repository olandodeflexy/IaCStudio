package mcpairlock

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const toolInventoryName = "mcp-airlock-tools.json"

// ToolRisk is the Airlock risk class for one external MCP tool.
type ToolRisk string

const (
	RiskReadOnly        ToolRisk = "read_only"
	RiskGenerateCode    ToolRisk = "generate_code"
	RiskModifyWorkspace ToolRisk = "modify_workspace"
	RiskCloudMutation   ToolRisk = "cloud_mutation"
	RiskSecretSensitive ToolRisk = "secret_sensitive"
	RiskDestructive     ToolRisk = "destructive"
	RiskUnknown         ToolRisk = "unknown"
)

// ToolAllowlist stores explicit external MCP tool exceptions. Project entries
// are scoped more narrowly than server entries.
type ToolAllowlist struct {
	ServerTools  map[string][]string            `json:"server_tools,omitempty"`
	ProjectTools map[string]map[string][]string `json:"project_tools,omitempty"`
}

// DiscoveredTool is one raw MCP tool returned by tools/list.
type DiscoveredTool struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	InputSchema map[string]any `json:"inputSchema,omitempty"`
	Annotations map[string]any `json:"annotations,omitempty"`
}

// DiscoveryProbeResult is the sanitized result from a tools/list probe.
type DiscoveryProbeResult struct {
	Tools    []DiscoveredTool
	Output   string
	Err      error
	TimedOut bool
}

// ToolDecision is the fail-closed firewall decision for one external MCP tool.
type ToolDecision struct {
	Status           string   `json:"status"`
	Allowed          bool     `json:"allowed"`
	ApprovalRequired bool     `json:"approval_required"`
	Risk             ToolRisk `json:"risk"`
	Reason           string   `json:"reason"`
	Allowlisted      bool     `json:"allowlisted"`
	UntrustedOutput  bool     `json:"untrusted_output"`
}

// ToolInventoryEntry is the public inventory row shown in the UI and MCP tools.
type ToolInventoryEntry struct {
	ServerID        string       `json:"server_id"`
	Name            string       `json:"name"`
	Description     string       `json:"description,omitempty"`
	InputSchemaHash string       `json:"input_schema_hash"`
	LastSeenAt      string       `json:"last_seen_at"`
	SchemaState     string       `json:"schema_state"`
	Risk            ToolRisk     `json:"risk"`
	Decision        ToolDecision `json:"decision"`
}

// ToolInventory is a snapshot of discovered tools for one server.
type ToolInventory struct {
	ServerID     string               `json:"server_id"`
	DiscoveredAt string               `json:"discovered_at,omitempty"`
	Tools        []ToolInventoryEntry `json:"tools"`
	Checks       []Check              `json:"checks"`
}

type persistedToolInventory struct {
	Allowlist ToolAllowlist                   `json:"allowlist,omitempty"`
	Servers   map[string]persistedServerTools `json:"servers"`
}

type persistedServerTools struct {
	DiscoveredAt string                         `json:"discovered_at,omitempty"`
	Tools        map[string]persistedToolRecord `json:"tools"`
}

type persistedToolRecord struct {
	Description     string   `json:"description,omitempty"`
	InputSchemaHash string   `json:"input_schema_hash"`
	LastSeenAt      string   `json:"last_seen_at"`
	SchemaState     string   `json:"schema_state,omitempty"`
	Risk            ToolRisk `json:"risk"`
}

func WithToolDiscoverer(discoverer ToolDiscoveryFunc) Option {
	return func(m *Manager) {
		if discoverer != nil {
			m.discoverer = discoverer
		}
	}
}

func WithToolAllowlist(allowlist ToolAllowlist) Option {
	return func(m *Manager) {
		m.allowlist = copyAllowlist(allowlist)
	}
}

// DiscoverTools launches a bounded tools/list probe and persists the observed
// tool schemas. Unknown or changed tools are visible and fail closed until
// classified or explicitly allowed.
func (m *Manager) DiscoverTools(ctx context.Context, id string) (ToolInventory, error) {
	definition, ok := m.lookup(id)
	if !ok {
		return ToolInventory{}, ErrUnknownServer
	}
	status := m.passiveStatus(definition)
	inventory := ToolInventory{
		ServerID: id,
		Tools:    []ToolInventoryEntry{},
		Checks:   append([]Check{}, status.Checks...),
	}
	if status.State != "available" {
		inventory.Checks = append(inventory.Checks, Check{Name: "tool_discovery", Status: "error", Message: status.Summary})
		return inventory, nil
	}

	result := m.discoverer(ctx, definition, m.timeout)
	if result.TimedOut {
		inventory.Checks = append(inventory.Checks, Check{Name: "tool_discovery", Status: "error", Message: "tools/list probe timed out"})
		return inventory, nil
	}
	if result.Err != nil {
		message := result.Err.Error()
		if output := redactOutput(result.Output); output != "" {
			message = fmt.Sprintf("%s: %s", message, output)
		}
		inventory.Checks = append(inventory.Checks, Check{Name: "tool_discovery", Status: "error", Message: message})
		return inventory, nil
	}

	now := time.Now().UTC()
	m.inventoryMu.Lock()
	defer m.inventoryMu.Unlock()
	previous, err := m.loadInventoryUnlocked()
	if err != nil {
		inventory.Checks = append(inventory.Checks, Check{Name: "inventory", Status: "warn", Message: err.Error()})
		return inventory, nil
	}
	inventory.DiscoveredAt = now.Format(time.RFC3339)
	inventory.Tools = make([]ToolInventoryEntry, 0, len(result.Tools))
	seen := map[string]persistedToolRecord{}
	allowlist := m.mergedAllowlist(previous.Allowlist)
	for _, tool := range result.Tools {
		name := strings.TrimSpace(tool.Name)
		if name == "" {
			continue
		}
		hash := schemaHash(tool.InputSchema)
		risk := ClassifyTool(tool)
		prior, hadPrior := previous.Servers[id].Tools[name]
		state := "new"
		if hadPrior {
			state = "known"
			if prior.InputSchemaHash != hash {
				state = "changed"
			}
		}
		decision := decideToolWithAllowlist(id, "", name, risk, state, allowlist)
		entry := ToolInventoryEntry{
			ServerID:        id,
			Name:            name,
			Description:     strings.TrimSpace(tool.Description),
			InputSchemaHash: hash,
			LastSeenAt:      inventory.DiscoveredAt,
			SchemaState:     state,
			Risk:            risk,
			Decision:        decision,
		}
		inventory.Tools = append(inventory.Tools, entry)
		seen[name] = persistedToolRecord{
			Description:     entry.Description,
			InputSchemaHash: entry.InputSchemaHash,
			LastSeenAt:      entry.LastSeenAt,
			SchemaState:     entry.SchemaState,
			Risk:            entry.Risk,
		}
	}
	sortToolEntries(inventory.Tools)
	if len(inventory.Tools) == 0 {
		inventory.Checks = append(inventory.Checks, Check{Name: "tool_discovery", Status: "warn", Message: "tools/list returned no tools"})
	} else {
		inventory.Checks = append(inventory.Checks, Check{Name: "tool_discovery", Status: "pass", Message: fmt.Sprintf("discovered %d external MCP tools", len(inventory.Tools))})
	}

	previous.Servers[id] = persistedServerTools{
		DiscoveredAt: inventory.DiscoveredAt,
		Tools:        seen,
	}
	if err := m.saveInventoryUnlocked(previous); err != nil {
		inventory.Checks = append(inventory.Checks, Check{Name: "inventory", Status: "warn", Message: err.Error()})
		return inventory, nil
	}
	return inventory, nil
}

// Inventory returns the last persisted inventory for one server.
func (m *Manager) Inventory(id string) (ToolInventory, error) {
	if _, ok := m.lookup(id); !ok {
		return ToolInventory{}, ErrUnknownServer
	}
	snapshot, err := m.loadInventory()
	if err != nil {
		return ToolInventory{
			ServerID: id,
			Tools:    []ToolInventoryEntry{},
			Checks:   []Check{{Name: "inventory", Status: "warn", Message: err.Error()}},
		}, nil
	}
	server := snapshot.Servers[id]
	allowlist := m.mergedAllowlist(snapshot.Allowlist)
	inventory := ToolInventory{
		ServerID:     id,
		DiscoveredAt: server.DiscoveredAt,
		Tools:        make([]ToolInventoryEntry, 0, len(server.Tools)),
		Checks:       []Check{},
	}
	for name, record := range server.Tools {
		risk := record.Risk
		if risk == "" {
			risk = RiskUnknown
		}
		entry := ToolInventoryEntry{
			ServerID:        id,
			Name:            name,
			Description:     record.Description,
			InputSchemaHash: record.InputSchemaHash,
			LastSeenAt:      record.LastSeenAt,
			SchemaState:     normalizedSchemaState(record.SchemaState),
			Risk:            risk,
			Decision:        decideToolWithAllowlist(id, "", name, risk, normalizedSchemaState(record.SchemaState), allowlist),
		}
		inventory.Tools = append(inventory.Tools, entry)
	}
	sortToolEntries(inventory.Tools)
	if len(inventory.Tools) == 0 {
		inventory.Checks = []Check{{Name: "inventory", Status: "warn", Message: "no tools have been discovered for this server"}}
	}
	return inventory, nil
}

// EvaluateTool returns the firewall decision for one discovered tool.
func (m *Manager) EvaluateTool(serverID, project, toolName string) (ToolInventoryEntry, error) {
	if _, ok := m.lookup(serverID); !ok {
		return ToolInventoryEntry{}, ErrUnknownServer
	}
	toolName = strings.TrimSpace(toolName)
	if toolName == "" {
		return ToolInventoryEntry{}, errors.New("tool_name is required")
	}
	snapshot, err := m.loadInventory()
	if err != nil {
		return ToolInventoryEntry{}, err
	}
	return m.evaluateToolFromSnapshot(snapshot, serverID, project, toolName), nil
}

// SetToolAllowlist updates the persisted server or project allowlist for one
// external tool and returns the resulting firewall decision.
func (m *Manager) SetToolAllowlist(serverID, project, toolName string, allowed bool) (ToolInventoryEntry, error) {
	if _, ok := m.lookup(serverID); !ok {
		return ToolInventoryEntry{}, ErrUnknownServer
	}
	toolName = strings.TrimSpace(toolName)
	if toolName == "" {
		return ToolInventoryEntry{}, errors.New("tool_name is required")
	}
	m.inventoryMu.Lock()
	defer m.inventoryMu.Unlock()
	snapshot, err := m.loadInventoryUnlocked()
	if err != nil {
		return ToolInventoryEntry{}, err
	}
	ensureAllowlist(&snapshot.Allowlist)
	if allowed {
		addAllowlistTool(&snapshot.Allowlist, serverID, project, toolName)
	} else {
		removeAllowlistTool(&snapshot.Allowlist, serverID, project, toolName)
	}
	if err := m.saveInventoryUnlocked(snapshot); err != nil {
		return ToolInventoryEntry{}, err
	}
	return m.evaluateToolFromSnapshot(snapshot, serverID, project, toolName), nil
}

// DecideTool applies Airlock's fail-closed firewall policy.
func (m *Manager) DecideTool(serverID, project, toolName string, risk ToolRisk, schemaState string) ToolDecision {
	allowlist := copyAllowlist(m.allowlist)
	if snapshot, err := m.loadInventory(); err == nil {
		mergeAllowlist(&allowlist, snapshot.Allowlist)
	}
	return decideToolWithAllowlist(serverID, project, toolName, risk, schemaState, allowlist)
}

func decideToolWithAllowlist(serverID, project, toolName string, risk ToolRisk, schemaState string, allowlist ToolAllowlist) ToolDecision {
	if risk == "" {
		risk = RiskUnknown
	}
	schemaState = normalizedSchemaState(schemaState)
	allowlisted := isToolAllowlisted(allowlist, serverID, project, toolName)
	decision := ToolDecision{
		Risk:            risk,
		Allowlisted:     allowlisted,
		UntrustedOutput: true,
	}
	if schemaReviewRequired(schemaState) {
		if !allowlisted {
			decision.Status = "blocked"
			decision.Reason = schemaState + " external MCP tool schemas are blocked until re-reviewed or explicitly allowlisted"
			return decision
		}
		decision.Status = "approval_required"
		decision.ApprovalRequired = true
		decision.Reason = "allowlisted " + schemaState + " external MCP tool schemas still require explicit approval"
		return decision
	}
	switch risk {
	case RiskReadOnly:
		decision.Status = "allowed"
		decision.Allowed = true
		decision.Reason = "read-only tools are allowed by default; output remains untrusted"
	case RiskGenerateCode:
		if !allowlisted {
			decision.Status = "blocked"
			decision.Reason = "generate-code tools require an explicit server or project allowlist"
		} else {
			decision.Status = "approval_required"
			decision.ApprovalRequired = true
			decision.Reason = "generate-code tools require explicit user approval before execution"
		}
	case RiskModifyWorkspace, RiskCloudMutation, RiskSecretSensitive, RiskDestructive:
		if !allowlisted {
			decision.Status = "blocked"
			decision.Reason = string(risk) + " tools require an explicit server or project allowlist"
		} else {
			decision.Status = "approval_required"
			decision.ApprovalRequired = true
			decision.Reason = string(risk) + " tools require explicit user approval before execution"
		}
	default:
		if !allowlisted {
			decision.Status = "blocked"
			decision.Reason = "unknown external MCP tools are blocked until classified or explicitly allowlisted"
		} else {
			decision.Status = "approval_required"
			decision.ApprovalRequired = true
			decision.Reason = "allowlisted unknown tools still require explicit approval"
		}
	}
	return decision
}

func (m *Manager) mergedAllowlist(persisted ToolAllowlist) ToolAllowlist {
	allowlist := copyAllowlist(m.allowlist)
	mergeAllowlist(&allowlist, persisted)
	return allowlist
}

func (m *Manager) evaluateToolFromSnapshot(snapshot persistedToolInventory, serverID, project, toolName string) ToolInventoryEntry {
	allowlist := m.mergedAllowlist(snapshot.Allowlist)
	for name, record := range snapshot.Servers[serverID].Tools {
		if name != toolName {
			continue
		}
		risk := record.Risk
		if risk == "" {
			risk = RiskUnknown
		}
		schemaState := normalizedSchemaState(record.SchemaState)
		return ToolInventoryEntry{
			ServerID:        serverID,
			Name:            name,
			Description:     record.Description,
			InputSchemaHash: record.InputSchemaHash,
			LastSeenAt:      record.LastSeenAt,
			SchemaState:     schemaState,
			Risk:            risk,
			Decision:        decideToolWithAllowlist(serverID, project, toolName, risk, schemaState, allowlist),
		}
	}
	risk := RiskUnknown
	return ToolInventoryEntry{
		ServerID:    serverID,
		Name:        toolName,
		SchemaState: "unknown",
		Risk:        risk,
		Decision:    decideToolWithAllowlist(serverID, project, toolName, risk, "unknown", allowlist),
	}
}

// ClassifyTool assigns a conservative risk class from MCP metadata.
func ClassifyTool(tool DiscoveredTool) ToolRisk {
	schemaText := schemaText(tool.InputSchema)
	text := strings.ToLower(strings.Join([]string{tool.Name, tool.Description, schemaText}, " "))
	if containsAny(text, "destroy", "delete", "remove", "terminate", "drop ", "revoke", "detach", "decommission", "purge") {
		return RiskDestructive
	}
	if containsAny(text, "secret", "token", "credential", "private_key", "private key", "session", "assume_role", "assume role", "client_secret", "access key") {
		return RiskSecretSensitive
	}
	if containsAny(text, "apply", "deploy", "create", "update", "put ", "patch", "authorize", "ingress", "egress", "security group", "iam policy", "route table") {
		return RiskCloudMutation
	}
	if containsAny(text, "write file", "workspace", "edit", "modify file", "commit", "pull request", "pr branch", "rename", "move file") {
		return RiskModifyWorkspace
	}
	if containsAny(text, "generate", "template", "hcl", "terraform code", "rego", "policy code", "yaml", "json patch") {
		return RiskGenerateCode
	}
	if readOnlyAnnotation(tool.Annotations) || strings.HasPrefix(strings.ToLower(tool.Name), "get") || strings.HasPrefix(strings.ToLower(tool.Name), "list") || strings.HasPrefix(strings.ToLower(tool.Name), "describe") {
		return RiskReadOnly
	}
	if containsAny(text, "read", "lookup", "search", "show", "inspect", "query", "documentation", "docs", "registry", "provider", "module") {
		return RiskReadOnly
	}
	return RiskUnknown
}

func defaultToolDiscoverer(ctx context.Context, definition ServerDefinition, timeout time.Duration) DiscoveryProbeResult {
	probeCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(probeCtx, definition.Command, definition.Args...)
	cmd.Dir = os.TempDir()
	cmd.Env = minimalEnv()
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return DiscoveryProbeResult{Err: err}
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return DiscoveryProbeResult{Err: err}
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return DiscoveryProbeResult{Err: err}
	}
	if err := cmd.Start(); err != nil {
		return DiscoveryProbeResult{Err: err}
	}

	var stderrBuf bytes.Buffer
	doneErr := make(chan error, 1)
	go func() {
		_, _ = io.Copy(&limitedBuffer{buf: &stderrBuf, limit: 2048}, stderr)
		doneErr <- cmd.Wait()
	}()

	encoder := json.NewEncoder(stdin)
	encoder.SetEscapeHTML(false)
	if err := writeToolDiscoveryRequests(encoder, stdin); err != nil {
		cancel()
		waitErr := <-doneErr
		return DiscoveryProbeResult{Output: stderrBuf.String(), Err: errors.Join(err, waitErr)}
	}

	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 0, 64*1024), 5*1024*1024)
	for scanner.Scan() {
		var response struct {
			ID     int `json:"id"`
			Result struct {
				Tools []DiscoveredTool `json:"tools"`
			} `json:"result"`
			Error *struct {
				Message string `json:"message"`
				Data    any    `json:"data,omitempty"`
			} `json:"error,omitempty"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &response); err != nil {
			continue
		}
		if response.ID != 2 {
			continue
		}
		if response.Error != nil {
			cancel()
			<-doneErr
			return DiscoveryProbeResult{Output: stderrBuf.String(), Err: fmt.Errorf("tools/list failed: %s", response.Error.Message)}
		}
		cancel()
		<-doneErr
		return DiscoveryProbeResult{Tools: response.Result.Tools, Output: stderrBuf.String()}
	}
	if err := scanner.Err(); err != nil {
		cancel()
		<-doneErr
		return DiscoveryProbeResult{Output: stderrBuf.String(), Err: err, TimedOut: errors.Is(probeCtx.Err(), context.DeadlineExceeded)}
	}
	err = <-doneErr
	if errors.Is(probeCtx.Err(), context.DeadlineExceeded) {
		return DiscoveryProbeResult{Output: stderrBuf.String(), Err: err, TimedOut: true}
	}
	if err != nil {
		return DiscoveryProbeResult{Output: stderrBuf.String(), Err: err}
	}
	return DiscoveryProbeResult{Output: stderrBuf.String(), Err: errors.New("tools/list response not received")}
}

func writeToolDiscoveryRequests(encoder *json.Encoder, stdin io.Closer) error {
	if err := encoder.Encode(map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "initialize",
		"params": map[string]any{
			"protocolVersion": "2025-06-18",
			"capabilities":    map[string]any{},
			"clientInfo":      map[string]any{"name": "iac-studio-airlock", "version": "0"},
		},
	}); err != nil {
		return fmt.Errorf("write initialize request: %w", err)
	}
	if err := encoder.Encode(map[string]any{"jsonrpc": "2.0", "method": "notifications/initialized"}); err != nil {
		return fmt.Errorf("write initialized notification: %w", err)
	}
	if err := encoder.Encode(map[string]any{"jsonrpc": "2.0", "id": 2, "method": "tools/list", "params": map[string]any{}}); err != nil {
		return fmt.Errorf("write tools/list request: %w", err)
	}
	if err := stdin.Close(); err != nil {
		return fmt.Errorf("close discovery stdin: %w", err)
	}
	return nil
}

func (m *Manager) loadInventory() (persistedToolInventory, error) {
	m.inventoryMu.Lock()
	defer m.inventoryMu.Unlock()
	return m.loadInventoryUnlocked()
}

func (m *Manager) loadInventoryUnlocked() (persistedToolInventory, error) {
	snapshot := persistedToolInventory{Servers: map[string]persistedServerTools{}}
	if strings.TrimSpace(m.inventoryPath) == "" {
		return snapshot, nil
	}
	data, err := os.ReadFile(m.inventoryPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return snapshot, nil
		}
		return snapshot, fmt.Errorf("read Airlock tool inventory: %w", err)
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return snapshot, nil
	}
	if err := json.Unmarshal(data, &snapshot); err != nil {
		return snapshot, fmt.Errorf("decode Airlock tool inventory: %w", err)
	}
	if snapshot.Servers == nil {
		snapshot.Servers = map[string]persistedServerTools{}
	}
	ensureAllowlist(&snapshot.Allowlist)
	for id, server := range snapshot.Servers {
		if server.Tools == nil {
			server.Tools = map[string]persistedToolRecord{}
			snapshot.Servers[id] = server
		}
	}
	return snapshot, nil
}

func (m *Manager) saveInventoryUnlocked(snapshot persistedToolInventory) error {
	if strings.TrimSpace(m.inventoryPath) == "" {
		return nil
	}
	data, err := json.MarshalIndent(snapshot, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal Airlock tool inventory: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(m.inventoryPath), 0o755); err != nil {
		return fmt.Errorf("create Airlock inventory directory: %w", err)
	}
	if err := os.WriteFile(m.inventoryPath, append(data, '\n'), 0o600); err != nil {
		return fmt.Errorf("write Airlock tool inventory: %w", err)
	}
	return nil
}

func inventoryPath(projectsDir string) string {
	projectsDir = strings.TrimSpace(projectsDir)
	if projectsDir == "" {
		return ""
	}
	return filepath.Join(projectsDir, ".iac-studio", toolInventoryName)
}

func schemaHash(schema map[string]any) string {
	data, err := json.Marshal(schema)
	if err != nil {
		data = []byte(fmt.Sprintf("%v", schema))
	}
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func schemaText(schema map[string]any) string {
	data, err := json.Marshal(schema)
	if err != nil {
		return fmt.Sprintf("%v", schema)
	}
	return string(data)
}

func normalizedSchemaState(state string) string {
	switch trimmed := strings.TrimSpace(state); trimmed {
	case "new", "changed", "unknown":
		return trimmed
	default:
		return "known"
	}
}

func schemaReviewRequired(state string) bool {
	return state == "new" || state == "changed"
}

func readOnlyAnnotation(annotations map[string]any) bool {
	if annotations == nil {
		return false
	}
	value, ok := annotations["readOnlyHint"]
	if !ok {
		return false
	}
	hint, ok := value.(bool)
	return ok && hint
}

func containsAny(value string, needles ...string) bool {
	for _, needle := range needles {
		if strings.Contains(value, needle) {
			return true
		}
	}
	return false
}

func isToolAllowlisted(allowlist ToolAllowlist, serverID, project, toolName string) bool {
	serverID = strings.TrimSpace(serverID)
	project = strings.TrimSpace(project)
	toolName = strings.TrimSpace(toolName)
	if containsString(allowlist.ServerTools[serverID], toolName) {
		return true
	}
	if project != "" {
		if serverTools := allowlist.ProjectTools[project]; containsString(serverTools[serverID], toolName) {
			return true
		}
	}
	return false
}

func ensureAllowlist(allowlist *ToolAllowlist) {
	if allowlist.ServerTools == nil {
		allowlist.ServerTools = map[string][]string{}
	}
	if allowlist.ProjectTools == nil {
		allowlist.ProjectTools = map[string]map[string][]string{}
	}
}

func mergeAllowlist(dst *ToolAllowlist, src ToolAllowlist) {
	ensureAllowlist(dst)
	for server, tools := range src.ServerTools {
		for _, tool := range tools {
			addAllowlistTool(dst, server, "", tool)
		}
	}
	for project, servers := range src.ProjectTools {
		for server, tools := range servers {
			for _, tool := range tools {
				addAllowlistTool(dst, server, project, tool)
			}
		}
	}
}

func addAllowlistTool(allowlist *ToolAllowlist, serverID, project, toolName string) {
	ensureAllowlist(allowlist)
	serverID = strings.TrimSpace(serverID)
	project = strings.TrimSpace(project)
	toolName = strings.TrimSpace(toolName)
	if toolName == "" {
		return
	}
	if project == "" {
		if !containsString(allowlist.ServerTools[serverID], toolName) {
			allowlist.ServerTools[serverID] = append(allowlist.ServerTools[serverID], toolName)
		}
		return
	}
	if allowlist.ProjectTools[project] == nil {
		allowlist.ProjectTools[project] = map[string][]string{}
	}
	if !containsString(allowlist.ProjectTools[project][serverID], toolName) {
		allowlist.ProjectTools[project][serverID] = append(allowlist.ProjectTools[project][serverID], toolName)
	}
}

func removeAllowlistTool(allowlist *ToolAllowlist, serverID, project, toolName string) {
	ensureAllowlist(allowlist)
	serverID = strings.TrimSpace(serverID)
	project = strings.TrimSpace(project)
	toolName = strings.TrimSpace(toolName)
	if project == "" {
		allowlist.ServerTools[serverID] = removeString(allowlist.ServerTools[serverID], toolName)
		return
	}
	if allowlist.ProjectTools[project] == nil {
		return
	}
	allowlist.ProjectTools[project][serverID] = removeString(allowlist.ProjectTools[project][serverID], toolName)
}

func copyAllowlist(in ToolAllowlist) ToolAllowlist {
	out := ToolAllowlist{
		ServerTools:  map[string][]string{},
		ProjectTools: map[string]map[string][]string{},
	}
	for server, tools := range in.ServerTools {
		out.ServerTools[server] = append([]string{}, tools...)
	}
	for project, servers := range in.ProjectTools {
		out.ProjectTools[project] = map[string][]string{}
		for server, tools := range servers {
			out.ProjectTools[project][server] = append([]string{}, tools...)
		}
	}
	return out
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if strings.TrimSpace(value) == target {
			return true
		}
	}
	return false
}

func removeString(values []string, target string) []string {
	if len(values) == 0 {
		return values
	}
	out := values[:0]
	for _, value := range values {
		if strings.TrimSpace(value) != target {
			out = append(out, value)
		}
	}
	return out
}

func sortToolEntries(entries []ToolInventoryEntry) {
	sort.SliceStable(entries, func(i, j int) bool {
		if entries[i].Risk != entries[j].Risk {
			return entries[i].Risk < entries[j].Risk
		}
		return entries[i].Name < entries[j].Name
	})
}

type limitedBuffer struct {
	buf   *bytes.Buffer
	limit int
}

func (w *limitedBuffer) Write(p []byte) (int, error) {
	remaining := w.limit - w.buf.Len()
	if remaining > 0 {
		if len(p) > remaining {
			_, _ = w.buf.Write(p[:remaining])
		} else {
			_, _ = w.buf.Write(p)
		}
	}
	return len(p), nil
}
