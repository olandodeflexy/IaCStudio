package mcp

func (s *Server) buildTools() ([]Tool, map[string]toolHandler) {
	tools := []Tool{
		readTool("list_projects", "List Projects", "List IaC Studio projects available under the configured projects directory.", objectSchema(nil, nil)),
		readTool("inspect_project", "Inspect Project", "Parse project metadata, resources, snapshots, and environment scope for an IaC Studio project.", objectSchema(map[string]any{
			"project": stringProp("Project name under the IaC Studio projects directory."),
			"tool":    stringProp("Optional IaC tool override: terraform, opentofu, pulumi, or ansible."),
			"env":     stringProp("Optional layered environment name."),
		}, []string{"project"})),
		readTool("list_cloud_connections", "List Cloud Connections", "List saved cloud connections using redacted public metadata only.", objectSchema(nil, nil)),
		readTool("inspect_connection_scope", "Inspect Connection Scope", "Inspect a saved cloud connection's provider, auth method, readiness, and environment variable keys without returning secret values.", objectSchema(map[string]any{
			"connection_id": stringProp("Saved cloud connection ID."),
		}, []string{"connection_id"})),
		readTool("list_mcp_airlock_servers", "List MCP Airlock Servers", "List trusted external MCP servers that IaC Studio can route through Airlock without exposing credentials.", objectSchema(nil, nil)),
		readTool("check_mcp_airlock_server", "Check MCP Airlock Server", "Run a bounded local health check for one trusted external MCP server using a sanitized environment.", objectSchema(map[string]any{
			"server_id": stringProp("Trusted Airlock server ID."),
		}, []string{"server_id"})),
		readTool("discover_mcp_airlock_tools", "Discover MCP Airlock Tools", "Run a bounded tools/list probe against one trusted external MCP server and persist schema fingerprints for firewall review.", objectSchema(map[string]any{
			"server_id": stringProp("Trusted Airlock server ID."),
		}, []string{"server_id"})),
		readTool("evaluate_mcp_airlock_tool", "Evaluate MCP Airlock Tool", "Evaluate the fail-closed Airlock firewall decision for one discovered external MCP tool.", objectSchema(map[string]any{
			"server_id": stringProp("Trusted Airlock server ID."),
			"tool_name": stringProp("External MCP tool name."),
			"project":   stringProp("Optional IaC Studio project for project-scoped allowlist checks."),
		}, []string{"server_id", "tool_name"})),
		proposalTool("call_mcp_airlock_tool", "Call MCP Airlock Tool", "Ask Airlock to route an external MCP tool call. Blocked or unapproved tools are refused before any external server is invoked.", objectSchema(map[string]any{
			"server_id":      stringProp("Trusted Airlock server ID."),
			"tool_name":      stringProp("External MCP tool name."),
			"project":        stringProp("Optional IaC Studio project for project-scoped allowlist checks."),
			"arguments_json": stringProp("JSON-encoded external tool arguments. Not forwarded unless the firewall permits execution."),
			"approval_token": stringProp("Configured local approval token required for approval-gated tools."),
		}, []string{"server_id", "tool_name"})),
		readTool("generate_plan", "Generate Plan", "Run a local plan/preview command through the IaC Studio safe runner. This reads provider state and may write local plan artifacts, but does not apply changes.", objectSchema(projectExecutionProps(), []string{"project"})),
		readTool("classify_plan", "Classify Plan", "Classify Terraform/OpenTofu plan JSON into safe, risky, destructive, and unknown changes.", objectSchema(map[string]any{
			"plan_json": stringProp("Raw terraform show -json output. Use this for direct classification."),
			"project":   stringProp("Optional project name when reading a plan JSON file from disk."),
			"plan_path": stringProp("Optional project-relative plan JSON path. Defaults to tfplan.json when project is provided."),
		}, nil)),
		readTool("run_policy_check", "Run Policy Check", "Run IaC Studio's built-in policy engine against parsed project resources.", objectSchema(projectExecutionProps(), []string{"project"})),
		readTool("scan_drift", "Scan Drift", "Compare code against local Terraform/OpenTofu state and classify drift findings.", objectSchema(projectExecutionProps(), []string{"project"})),
		readTool("explain_resource", "Explain Resource", "Explain one parsed resource, including source location, properties, and policy findings.", objectSchema(map[string]any{
			"project": stringProp("Project name under the IaC Studio projects directory."),
			"address": stringProp("Resource address, for example aws_vpc.main."),
			"tool":    stringProp("Optional IaC tool override."),
			"env":     stringProp("Optional layered environment name."),
		}, []string{"project", "address"})),
		readTool("summarize_recent_changes", "Summarize Recent Changes", "Summarize recent local git changes, snapshots, and generated IaC Studio review artifacts for a project.", objectSchema(map[string]any{
			"project": stringProp("Project name under the IaC Studio projects directory."),
			"limit":   map[string]any{"type": "integer", "description": "Maximum git commits to include.", "minimum": 1, "maximum": 25},
		}, []string{"project"})),
		proposalTool("propose_iac_change", "Propose IaC Change", "Turn a natural-language infrastructure change request into a deterministic review proposal without editing files.", objectSchema(map[string]any{
			"project":        stringProp("Project name under the IaC Studio projects directory."),
			"change_request": stringProp("Human request describing the infrastructure change."),
			"tool":           stringProp("Optional IaC tool override."),
			"env":            stringProp("Optional layered environment name."),
		}, []string{"project", "change_request"})),
		proposalTool("propose_drift_remediation", "Propose Drift Remediation", "Build a PR-ready codify/revert remediation proposal from current drift findings without writing files.", objectSchema(map[string]any{
			"project":       stringProp("Project name under the IaC Studio projects directory."),
			"mode":          enumProp("Remediation mode.", []string{"codify", "revert"}),
			"tool":          stringProp("Optional IaC tool override."),
			"env":           stringProp("Optional layered environment name."),
			"connection_id": stringProp("Optional cloud connection ID for scope labeling."),
		}, []string{"project", "mode"})),
		proposalTool("open_remediation_pr", "Open Remediation PR Handoff", "Generate drift remediation artifacts and a local review branch handoff after explicit approval. It never applies cloud changes.", objectSchema(map[string]any{
			"project":        stringProp("Project name under the IaC Studio projects directory."),
			"mode":           enumProp("Remediation mode.", []string{"codify", "revert"}),
			"tool":           stringProp("Optional IaC tool override."),
			"env":            stringProp("Optional layered environment name."),
			"connection_id":  stringProp("Optional cloud connection ID for scope labeling."),
			"approval_token": stringProp("Configured local approval token required before writing artifacts and creating a review branch."),
		}, []string{"project", "mode"})),
		proposalTool("generate_runbook", "Generate Runbook", "Generate a deterministic remediation runbook from project history, drift, snapshots, and optional incident context.", objectSchema(map[string]any{
			"project":       stringProp("Project name under the IaC Studio projects directory."),
			"incident":      stringProp("Optional incident or operational context."),
			"tool":          stringProp("Optional IaC tool override."),
			"env":           stringProp("Optional layered environment name."),
			"connection_id": stringProp("Optional cloud connection ID for scope labeling."),
		}, []string{"project"})),
		riskyTool("apply", "Apply", "High-risk apply action. The local MCP foundation does not execute applies directly without explicit IaC Studio approval.", objectSchema(highRiskProps(), []string{"project"})),
		riskyTool("destroy", "Destroy", "High-risk destroy action. The local MCP foundation does not execute destroys directly without explicit IaC Studio approval.", objectSchema(highRiskProps(), []string{"project"})),
		riskyTool("assume_role", "Assume Role", "High-risk role assumption request. IaC Studio MCP does not accept raw role escalation through the agent path.", objectSchema(highRiskProps(), nil)),
		riskyTool("modify_connection", "Modify Connection", "High-risk cloud connection mutation. Direct secret entry through MCP is blocked by policy.", objectSchema(highRiskProps(), nil)),
		riskyTool("open_public_network_access", "Open Public Network Access", "High-risk public network exposure request. MCP returns a proposal gate instead of changing infrastructure.", objectSchema(highRiskProps(), []string{"project"})),
	}

	handlers := map[string]toolHandler{
		"list_projects":              s.handleListProjects,
		"inspect_project":            s.handleInspectProject,
		"list_cloud_connections":     s.handleListCloudConnections,
		"inspect_connection_scope":   s.handleInspectConnectionScope,
		"list_mcp_airlock_servers":   s.handleListMCPAirlockServers,
		"check_mcp_airlock_server":   s.handleCheckMCPAirlockServer,
		"discover_mcp_airlock_tools": s.handleDiscoverMCPAirlockTools,
		"evaluate_mcp_airlock_tool":  s.handleEvaluateMCPAirlockTool,
		"call_mcp_airlock_tool":      s.handleCallMCPAirlockTool,
		"generate_plan":              s.handleGeneratePlan,
		"classify_plan":              s.handleClassifyPlan,
		"run_policy_check":           s.handleRunPolicyCheck,
		"scan_drift":                 s.handleScanDrift,
		"explain_resource":           s.handleExplainResource,
		"summarize_recent_changes":   s.handleSummarizeRecentChanges,
		"propose_iac_change":         s.handleProposeIaCChange,
		"propose_drift_remediation":  s.handleProposeDriftRemediation,
		"open_remediation_pr":        s.handleOpenRemediationPR,
		"generate_runbook":           s.handleGenerateRunbook,
		"apply":                      s.handleHighRisk("apply"),
		"destroy":                    s.handleHighRisk("destroy"),
		"assume_role":                s.handleHighRisk("assume_role"),
		"modify_connection":          s.handleHighRisk("modify_connection"),
		"open_public_network_access": s.handleHighRisk("open_public_network_access"),
	}
	return tools, handlers
}

func readTool(name, title, description string, input map[string]any) Tool {
	return Tool{
		Name:        name,
		Title:       title,
		Description: description,
		InputSchema: input,
		Annotations: map[string]any{
			"readOnlyHint":    true,
			"destructiveHint": false,
			"idempotentHint":  true,
			"openWorldHint":   false,
		},
	}
}

func proposalTool(name, title, description string, input map[string]any) Tool {
	return Tool{
		Name:        name,
		Title:       title,
		Description: description,
		InputSchema: input,
		Annotations: map[string]any{
			"readOnlyHint":    false,
			"destructiveHint": false,
			"idempotentHint":  false,
			"openWorldHint":   false,
		},
	}
}

func riskyTool(name, title, description string, input map[string]any) Tool {
	return Tool{
		Name:        name,
		Title:       title,
		Description: description,
		InputSchema: input,
		Annotations: map[string]any{
			"readOnlyHint":    false,
			"destructiveHint": true,
			"idempotentHint":  false,
			"openWorldHint":   true,
		},
	}
}

func objectSchema(properties map[string]any, required []string) map[string]any {
	if properties == nil {
		properties = map[string]any{}
	}
	schema := map[string]any{
		"type":                 "object",
		"properties":           properties,
		"additionalProperties": false,
	}
	if len(required) > 0 {
		schema["required"] = required
	}
	return schema
}

func projectExecutionProps() map[string]any {
	return map[string]any{
		"project":       stringProp("Project name under the IaC Studio projects directory."),
		"tool":          stringProp("Optional IaC tool override."),
		"env":           stringProp("Optional layered environment name."),
		"connection_id": stringProp("Optional saved cloud connection ID."),
	}
}

func highRiskProps() map[string]any {
	props := projectExecutionProps()
	props["approval_token"] = stringProp("Configured local approval token required for high-risk operations.")
	props["reason"] = stringProp("Human-readable reason for requesting this high-risk action.")
	return props
}

func stringProp(description string) map[string]any {
	return map[string]any{"type": "string", "description": description}
}

func enumProp(description string, values []string) map[string]any {
	return map[string]any{"type": "string", "description": description, "enum": values}
}
