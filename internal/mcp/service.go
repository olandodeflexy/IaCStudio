package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/iac-studio/iac-studio/internal/cloudconnections"
	"github.com/iac-studio/iac-studio/internal/drift"
	"github.com/iac-studio/iac-studio/internal/parser"
	iacplan "github.com/iac-studio/iac-studio/internal/plan"
	"github.com/iac-studio/iac-studio/internal/policy"
	"github.com/iac-studio/iac-studio/internal/project"
	pulumiparser "github.com/iac-studio/iac-studio/internal/pulumi"
	"github.com/iac-studio/iac-studio/internal/recovery"
	"github.com/iac-studio/iac-studio/internal/review"
)

type projectArgs struct {
	Project      string `json:"project"`
	Tool         string `json:"tool,omitempty"`
	Env          string `json:"env,omitempty"`
	ConnectionID string `json:"connection_id,omitempty"`
}

type projectContext struct {
	Name        string
	ProjectPath string
	WorkDir     string
	Tool        string
	Env         string
	Descriptor  *projectDescriptor
	Resources   []parser.Resource
}

type projectDescriptor struct {
	Name             string            `json:"name,omitempty"`
	Tool             string            `json:"tool,omitempty"`
	Layout           string            `json:"layout,omitempty"`
	Blueprint        string            `json:"blueprint,omitempty"`
	ProjectName      string            `json:"project_name,omitempty"`
	Cloud            string            `json:"cloud,omitempty"`
	Environments     []string          `json:"environments,omitempty"`
	EnvironmentTools map[string]string `json:"environment_tools,omitempty"`
	Tags             map[string]string `json:"tags,omitempty"`
	Drift            struct {
		Suppressions []drift.SuppressionRule `json:"suppressions,omitempty"`
	} `json:"drift,omitempty"`
}

func (s *Server) handleListProjects(_ context.Context, _ json.RawMessage) toolResponse {
	manager := project.NewManager(s.projectsDir)
	states, err := manager.ListAll()
	if err != nil {
		if os.IsNotExist(err) {
			states = []*project.State{}
		} else {
			return errResponse("list_projects", err, AuditDecision{Tool: "list_projects"})
		}
	}
	sort.SliceStable(states, func(i, j int) bool { return states[i].Name < states[j].Name })
	projects := make([]map[string]any, 0, len(states))
	for _, state := range states {
		projects = append(projects, map[string]any{
			"name":         state.Name,
			"path":         state.Path,
			"tool":         state.Tool,
			"layout":       state.Layout,
			"cloud":        state.Cloud,
			"environments": state.Environments,
			"updated_at":   state.UpdatedAt,
		})
	}
	return toolResponse{
		Result: structuredResult(map[string]any{"projects": projects}),
		Audit:  AuditDecision{Tool: "list_projects", Decision: "allowed"},
	}
}

func (s *Server) handleInspectProject(_ context.Context, raw json.RawMessage) toolResponse {
	var args projectArgs
	if err := decode(raw, &args); err != nil {
		return errResponse("inspect_project", err, AuditDecision{Tool: "inspect_project"})
	}
	ctx, err := s.loadProject(args)
	if err != nil {
		return errResponse("inspect_project", err, projectAudit("inspect_project", args))
	}
	snapshots, err := recovery.ListSnapshots(ctx.ProjectPath)
	if err != nil {
		return errResponse("inspect_project", err, projectAudit("inspect_project", args))
	}
	return toolResponse{
		Result: structuredResult(map[string]any{
			"name":           ctx.Name,
			"path":           ctx.ProjectPath,
			"work_dir":       ctx.WorkDir,
			"tool":           ctx.Tool,
			"env":            ctx.Env,
			"descriptor":     ctx.Descriptor,
			"resource_count": len(ctx.Resources),
			"resources":      relativeResources(ctx.ProjectPath, ctx.Resources),
			"snapshots":      snapshots,
		}),
		Audit: projectAudit("inspect_project", args),
	}
}

func (s *Server) handleListCloudConnections(_ context.Context, _ json.RawMessage) toolResponse {
	connections, err := s.cloudConnections.List()
	if err != nil {
		return errResponse("list_cloud_connections", err, AuditDecision{Tool: "list_cloud_connections"})
	}
	return toolResponse{
		Result: structuredResult(map[string]any{"connections": connections}),
		Audit:  AuditDecision{Tool: "list_cloud_connections", Decision: "allowed"},
	}
}

func (s *Server) handleInspectConnectionScope(_ context.Context, raw json.RawMessage) toolResponse {
	var args struct {
		ConnectionID string `json:"connection_id"`
	}
	if err := decode(raw, &args); err != nil {
		return errResponse("inspect_connection_scope", err, AuditDecision{Tool: "inspect_connection_scope"})
	}
	if err := requireNonEmpty("connection_id", args.ConnectionID); err != nil {
		return errResponse("inspect_connection_scope", err, AuditDecision{Tool: "inspect_connection_scope", ConnectionID: args.ConnectionID})
	}
	connection, err := s.cloudConnections.Get(args.ConnectionID)
	if err != nil {
		return errResponse("inspect_connection_scope", err, AuditDecision{Tool: "inspect_connection_scope", ConnectionID: args.ConnectionID})
	}
	result, err := s.cloudConnections.Test(args.ConnectionID)
	if err != nil {
		return errResponse("inspect_connection_scope", err, AuditDecision{Tool: "inspect_connection_scope", ConnectionID: args.ConnectionID})
	}
	env := cloudconnections.CommandEnvironment(*connection)
	keys := make([]string, 0, len(env))
	for key := range env {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return toolResponse{
		Result: structuredResult(map[string]any{
			"connection":       result.Connection,
			"ready":            result.OK,
			"checks":           result.Checks,
			"summary":          result.Summary,
			"command_env_keys": keys,
			"scope":            "Credentials remain inside IaC Studio's cloud connection broker; MCP responses expose names, providers, readiness, and environment variable keys only.",
		}),
		Audit: AuditDecision{Tool: "inspect_connection_scope", ConnectionID: args.ConnectionID, Decision: "allowed"},
	}
}

func (s *Server) handleGeneratePlan(ctx context.Context, raw json.RawMessage) toolResponse {
	var args projectArgs
	if err := decode(raw, &args); err != nil {
		return errResponse("generate_plan", err, AuditDecision{Tool: "generate_plan"})
	}
	projectCtx, err := s.loadProject(args)
	if err != nil {
		return errResponse("generate_plan", err, projectAudit("generate_plan", args))
	}
	env, connSummary, err := s.commandEnvironment(args.ConnectionID)
	if err != nil {
		return errResponse("generate_plan", err, projectAudit("generate_plan", args))
	}
	result, runErr := s.run.ExecuteWithEnv(ctx, projectCtx.WorkDir, projectCtx.Tool, "plan", projectCtx.Env, env)
	payload := map[string]any{
		"project":    projectCtx.Name,
		"tool":       projectCtx.Tool,
		"env":        projectCtx.Env,
		"work_dir":   projectCtx.WorkDir,
		"connection": connSummary,
		"result":     result,
	}
	if runErr != nil {
		payload["error"] = runErr.Error()
		return toolResponse{
			Result: errorResult(runErr.Error(), payload),
			Audit:  projectAudit("generate_plan", args).withError(runErr),
		}
	}
	return toolResponse{
		Result: structuredResult(payload),
		Audit:  projectAudit("generate_plan", args),
	}
}

func (s *Server) handleClassifyPlan(_ context.Context, raw json.RawMessage) toolResponse {
	var args struct {
		PlanJSON string `json:"plan_json,omitempty"`
		Project  string `json:"project,omitempty"`
		PlanPath string `json:"plan_path,omitempty"`
	}
	if err := decode(raw, &args); err != nil {
		return errResponse("classify_plan", err, AuditDecision{Tool: "classify_plan"})
	}
	planJSON := strings.TrimSpace(args.PlanJSON)
	if planJSON == "" && args.Project != "" {
		projectPath, err := safeProjectPath(s.projectsDir, args.Project)
		if err != nil {
			return errResponse("classify_plan", err, AuditDecision{Tool: "classify_plan", Project: args.Project})
		}
		planPath := args.PlanPath
		if strings.TrimSpace(planPath) == "" {
			planPath = "tfplan.json"
		}
		path, err := safeProjectFile(projectPath, planPath)
		if err != nil {
			return errResponse("classify_plan", err, AuditDecision{Tool: "classify_plan", Project: args.Project})
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return errResponse("classify_plan", err, AuditDecision{Tool: "classify_plan", Project: args.Project})
		}
		planJSON = string(data)
	}
	if strings.TrimSpace(planJSON) == "" {
		err := errors.New("plan_json or project plan_path is required")
		return errResponse("classify_plan", err, AuditDecision{Tool: "classify_plan", Project: args.Project})
	}
	classification, err := iacplan.New().ClassifyFullPlan(planJSON)
	if err != nil {
		return errResponse("classify_plan", err, AuditDecision{Tool: "classify_plan", Project: args.Project})
	}
	return toolResponse{
		Result: structuredResult(classification),
		Audit:  AuditDecision{Tool: "classify_plan", Project: args.Project, Decision: "allowed"},
	}
}

func (s *Server) handleRunPolicyCheck(_ context.Context, raw json.RawMessage) toolResponse {
	var args projectArgs
	if err := decode(raw, &args); err != nil {
		return errResponse("run_policy_check", err, AuditDecision{Tool: "run_policy_check"})
	}
	ctx, err := s.loadProject(args)
	if err != nil {
		return errResponse("run_policy_check", err, projectAudit("run_policy_check", args))
	}
	report := policy.New().Evaluate(ctx.Resources)
	return toolResponse{
		Result: structuredResult(map[string]any{
			"project": ctx.Name,
			"tool":    ctx.Tool,
			"env":     ctx.Env,
			"report":  report,
		}),
		Audit: projectAudit("run_policy_check", args),
	}
}

func (s *Server) handleScanDrift(_ context.Context, raw json.RawMessage) toolResponse {
	var args projectArgs
	if err := decode(raw, &args); err != nil {
		return errResponse("scan_drift", err, AuditDecision{Tool: "scan_drift"})
	}
	run, err := s.runDrift(args)
	if err != nil {
		return errResponse("scan_drift", err, projectAudit("scan_drift", args))
	}
	return toolResponse{
		Result: structuredResult(run.Report),
		Audit:  projectAudit("scan_drift", args),
	}
}

func (s *Server) handleExplainResource(_ context.Context, raw json.RawMessage) toolResponse {
	var args struct {
		Project string `json:"project"`
		Address string `json:"address"`
		Tool    string `json:"tool,omitempty"`
		Env     string `json:"env,omitempty"`
	}
	if err := decode(raw, &args); err != nil {
		return errResponse("explain_resource", err, AuditDecision{Tool: "explain_resource"})
	}
	if err := firstErr(requireNonEmpty("project", args.Project), requireNonEmpty("address", args.Address)); err != nil {
		return errResponse("explain_resource", err, AuditDecision{Tool: "explain_resource", Project: args.Project})
	}
	projectCtx, err := s.loadProject(projectArgs{Project: args.Project, Tool: args.Tool, Env: args.Env})
	if err != nil {
		return errResponse("explain_resource", err, AuditDecision{Tool: "explain_resource", Project: args.Project})
	}
	var found *parser.Resource
	for i := range projectCtx.Resources {
		if resourceAddress(projectCtx.Resources[i]) == args.Address || projectCtx.Resources[i].ID == args.Address {
			found = &projectCtx.Resources[i]
			break
		}
	}
	if found == nil {
		return errResponse("explain_resource", fmt.Errorf("resource not found: %s", args.Address), AuditDecision{Tool: "explain_resource", Project: args.Project})
	}
	report := policy.New().Evaluate([]parser.Resource{*found})
	relative := relativeResources(projectCtx.ProjectPath, []parser.Resource{*found})
	return toolResponse{
		Result: structuredResult(map[string]any{
			"project":          projectCtx.Name,
			"tool":             projectCtx.Tool,
			"env":              projectCtx.Env,
			"resource":         relative[0],
			"policy_findings":  report.Violations,
			"review_guidance":  resourceReviewGuidance(*found, report),
			"source_reference": fmt.Sprintf("%s:%d", relative[0].File, relative[0].Line),
		}),
		Audit: AuditDecision{Tool: "explain_resource", Project: args.Project, Decision: "allowed"},
	}
}

func (s *Server) handleSummarizeRecentChanges(_ context.Context, raw json.RawMessage) toolResponse {
	var args struct {
		Project string `json:"project"`
		Limit   int    `json:"limit,omitempty"`
	}
	if err := decode(raw, &args); err != nil {
		return errResponse("summarize_recent_changes", err, AuditDecision{Tool: "summarize_recent_changes"})
	}
	if err := requireNonEmpty("project", args.Project); err != nil {
		return errResponse("summarize_recent_changes", err, AuditDecision{Tool: "summarize_recent_changes", Project: args.Project})
	}
	if args.Limit <= 0 || args.Limit > 25 {
		args.Limit = 5
	}
	projectPath, err := safeProjectPath(s.projectsDir, args.Project)
	if err != nil {
		return errResponse("summarize_recent_changes", err, AuditDecision{Tool: "summarize_recent_changes", Project: args.Project})
	}
	summary := s.projectHistory(projectPath, args.Limit)
	return toolResponse{
		Result: structuredResult(summary),
		Audit:  AuditDecision{Tool: "summarize_recent_changes", Project: args.Project, Decision: "allowed"},
	}
}

func (s *Server) handleProposeIaCChange(_ context.Context, raw json.RawMessage) toolResponse {
	var args struct {
		Project       string `json:"project"`
		ChangeRequest string `json:"change_request"`
		Tool          string `json:"tool,omitempty"`
		Env           string `json:"env,omitempty"`
	}
	if err := decode(raw, &args); err != nil {
		return errResponse("propose_iac_change", err, AuditDecision{Tool: "propose_iac_change"})
	}
	if err := firstErr(requireNonEmpty("project", args.Project), requireNonEmpty("change_request", args.ChangeRequest)); err != nil {
		return errResponse("propose_iac_change", err, AuditDecision{Tool: "propose_iac_change", Project: args.Project})
	}
	projectCtx, err := s.loadProject(projectArgs{Project: args.Project, Tool: args.Tool, Env: args.Env})
	if err != nil {
		return errResponse("propose_iac_change", err, AuditDecision{Tool: "propose_iac_change", Project: args.Project})
	}
	risk, cues := classifyChangeRequest(args.ChangeRequest)
	proposal := map[string]any{
		"project":        projectCtx.Name,
		"tool":           projectCtx.Tool,
		"env":            projectCtx.Env,
		"title":          fmt.Sprintf("Propose IaC change for %s", projectCtx.Name),
		"change_request": args.ChangeRequest,
		"risk":           risk,
		"risk_cues":      cues,
		"resource_count": len(projectCtx.Resources),
		"mutates_files":  false,
		"recommended_flow": []string{
			"Generate or edit IaC in a branch.",
			"Run plan and semantic classification.",
			"Run policy and security checks.",
			"Open a PR with the plan summary and risk classification.",
		},
	}
	return toolResponse{
		Result: structuredResult(proposal),
		Audit:  AuditDecision{Tool: "propose_iac_change", Project: args.Project, Decision: "proposal_generated"},
	}
}

func (s *Server) handleProposeDriftRemediation(_ context.Context, raw json.RawMessage) toolResponse {
	var args struct {
		Project      string `json:"project"`
		Mode         string `json:"mode"`
		Tool         string `json:"tool,omitempty"`
		Env          string `json:"env,omitempty"`
		ConnectionID string `json:"connection_id,omitempty"`
	}
	if err := decode(raw, &args); err != nil {
		return errResponse("propose_drift_remediation", err, AuditDecision{Tool: "propose_drift_remediation"})
	}
	run, proposal, err := s.driftProposal(projectArgs{Project: args.Project, Tool: args.Tool, Env: args.Env, ConnectionID: args.ConnectionID}, args.Mode)
	if err != nil {
		return errResponse("propose_drift_remediation", err, AuditDecision{Tool: "propose_drift_remediation", Project: args.Project, ConnectionID: args.ConnectionID})
	}
	return toolResponse{
		Result: structuredResult(map[string]any{
			"drift":    run.Report,
			"proposal": proposal,
		}),
		Audit: AuditDecision{Tool: "propose_drift_remediation", Project: args.Project, ConnectionID: args.ConnectionID, Decision: "proposal_generated"},
	}
}

func (s *Server) handleOpenRemediationPR(_ context.Context, raw json.RawMessage) toolResponse {
	var args struct {
		Project       string `json:"project"`
		Mode          string `json:"mode"`
		Tool          string `json:"tool,omitempty"`
		Env           string `json:"env,omitempty"`
		ConnectionID  string `json:"connection_id,omitempty"`
		ApprovalToken string `json:"approval_token,omitempty"`
	}
	if err := decode(raw, &args); err != nil {
		return errResponse("open_remediation_pr", err, AuditDecision{Tool: "open_remediation_pr"})
	}
	audit := AuditDecision{
		Tool:             "open_remediation_pr",
		Project:          args.Project,
		ConnectionID:     args.ConnectionID,
		ApprovalRequired: true,
	}
	if !s.approved(args.ApprovalToken) {
		audit.Decision = "approval_required"
		return toolResponse{Result: approvalRequiredResult("open_remediation_pr", "writing review artifacts and creating a local PR branch mutates the project repository"), Audit: audit}
	}
	audit.Approved = true
	run, proposal, err := s.driftProposal(projectArgs{Project: args.Project, Tool: args.Tool, Env: args.Env, ConnectionID: args.ConnectionID}, args.Mode)
	if err != nil {
		return errResponse("open_remediation_pr", err, audit)
	}
	artifactSet, rendered, err := drift.RenderRemediationArtifacts(proposal, s.now())
	if err != nil {
		return errResponse("open_remediation_pr", err, audit)
	}
	for _, artifact := range rendered {
		if err := writeArtifactFile(run.ProjectPath, artifact.Path, artifact.Content); err != nil {
			return errResponse("open_remediation_pr", err, audit)
		}
	}
	files := make([]string, 0, len(rendered))
	for _, artifact := range rendered {
		files = append(files, artifact.Path)
	}
	handoff, err := review.CreatePullRequestHandoff(review.PullRequestHandoffInput{
		ProjectPath:   run.ProjectPath,
		Title:         proposal.Title,
		Branch:        proposal.Branch,
		CommitMessage: proposal.CommitMessage,
		BodyPath:      remediationBodyPath(rendered),
		Files:         files,
	})
	if err != nil {
		return errResponse("open_remediation_pr", err, audit)
	}
	audit.Decision = "approved_handoff_created"
	audit.Mutated = true
	return toolResponse{
		Result: structuredResult(map[string]any{
			"artifacts":    artifactSet,
			"pull_request": handoff,
		}),
		Audit: audit,
	}
}

func (s *Server) handleGenerateRunbook(_ context.Context, raw json.RawMessage) toolResponse {
	var args struct {
		Project      string `json:"project"`
		Incident     string `json:"incident,omitempty"`
		Tool         string `json:"tool,omitempty"`
		Env          string `json:"env,omitempty"`
		ConnectionID string `json:"connection_id,omitempty"`
	}
	if err := decode(raw, &args); err != nil {
		return errResponse("generate_runbook", err, AuditDecision{Tool: "generate_runbook"})
	}
	projectCtx, err := s.loadProject(projectArgs{Project: args.Project, Tool: args.Tool, Env: args.Env, ConnectionID: args.ConnectionID})
	if err != nil {
		return errResponse("generate_runbook", err, AuditDecision{Tool: "generate_runbook", Project: args.Project, ConnectionID: args.ConnectionID})
	}
	history := s.projectHistory(projectCtx.ProjectPath, 5)
	var driftReport any
	if run, driftErr := s.runDrift(projectArgs{Project: args.Project, Tool: args.Tool, Env: args.Env, ConnectionID: args.ConnectionID}); driftErr == nil {
		driftReport = run.Report
	} else {
		driftReport = map[string]any{"error": driftErr.Error()}
	}
	runbook := renderRunbook(projectCtx, args.Incident, history, driftReport)
	return toolResponse{
		Result: structuredResult(map[string]any{
			"project": projectCtx.Name,
			"tool":    projectCtx.Tool,
			"env":     projectCtx.Env,
			"runbook": runbook,
			"history": history,
			"drift":   driftReport,
		}),
		Audit: AuditDecision{Tool: "generate_runbook", Project: args.Project, ConnectionID: args.ConnectionID, Decision: "runbook_generated"},
	}
}

func (s *Server) handleHighRisk(tool string) toolHandler {
	return func(_ context.Context, raw json.RawMessage) toolResponse {
		var args struct {
			Project       string `json:"project,omitempty"`
			ConnectionID  string `json:"connection_id,omitempty"`
			ApprovalToken string `json:"approval_token,omitempty"`
			Reason        string `json:"reason,omitempty"`
		}
		if err := decode(raw, &args); err != nil {
			return errResponse(tool, err, AuditDecision{Tool: tool})
		}
		audit := AuditDecision{
			Tool:             tool,
			Project:          args.Project,
			ConnectionID:     args.ConnectionID,
			ApprovalRequired: true,
		}
		if !s.approved(args.ApprovalToken) {
			audit.Decision = "approval_required"
			return toolResponse{Result: approvalRequiredResult(tool, highRiskReason(tool)), Audit: audit}
		}
		audit.Approved = true
		audit.Decision = "approved_handoff_required"
		return toolResponse{
			Result: structuredResult(map[string]any{
				"status":   "approved_handoff_required",
				"tool":     tool,
				"executed": false,
				"reason":   "The local MCP foundation records approval but does not directly execute high-risk infrastructure mutations. Generate a plan, classify it, run policy checks, and complete the action through the IaC Studio UI/API approval flow.",
			}),
			Audit: audit,
		}
	}
}

func (s *Server) loadProject(args projectArgs) (*projectContext, error) {
	if err := requireNonEmpty("project", args.Project); err != nil {
		return nil, err
	}
	projectPath, err := safeProjectPath(s.projectsDir, args.Project)
	if err != nil {
		return nil, err
	}
	descriptor, _ := readProjectDescriptor(projectPath)
	tool := effectiveTool(descriptor, args.Tool, args.Env)
	if tool == "multi" {
		return nil, fmt.Errorf("env is required to resolve a concrete tool for hybrid projects")
	}
	if tool == "" {
		tool = "terraform"
	}
	workDir := projectPath
	if args.Env != "" {
		if err := safePathSegment(args.Env); err != nil {
			return nil, err
		}
		workDir = filepath.Join(projectPath, "environments", args.Env)
		info, err := os.Stat(workDir)
		if err != nil {
			return nil, fmt.Errorf("environment %q is not accessible: %w", args.Env, err)
		}
		if !info.IsDir() {
			return nil, fmt.Errorf("environment %q is not a directory", args.Env)
		}
	}
	resources, err := parseResources(workDir, tool)
	if err != nil {
		return nil, err
	}
	return &projectContext{
		Name:        args.Project,
		ProjectPath: projectPath,
		WorkDir:     workDir,
		Tool:        tool,
		Env:         args.Env,
		Descriptor:  descriptor,
		Resources:   resources,
	}, nil
}

func (s *Server) runDrift(args projectArgs) (*driftRun, error) {
	projectCtx, err := s.loadProject(args)
	if err != nil {
		return nil, err
	}
	if projectCtx.Tool != "terraform" && projectCtx.Tool != "opentofu" {
		return nil, fmt.Errorf("drift detection currently supports Terraform and OpenTofu state")
	}
	var conn *cloudconnections.Connection
	if strings.TrimSpace(args.ConnectionID) != "" {
		conn, err = s.cloudConnections.Get(args.ConnectionID)
		if err != nil {
			return nil, fmt.Errorf("load cloud connection: %w", err)
		}
	}
	codeResources := make(map[string]map[string]interface{}, len(projectCtx.Resources))
	for _, res := range projectCtx.Resources {
		codeResources[resourceAddress(res)] = res.Properties
	}
	var suppressions []drift.SuppressionRule
	if projectCtx.Descriptor != nil {
		suppressions = projectCtx.Descriptor.Drift.Suppressions
	}
	report, err := drift.New().DetectWithOptions(projectCtx.WorkDir, codeResources, drift.DetectOptions{
		Env:          args.Env,
		Suppressions: suppressions,
	})
	if err != nil {
		return nil, err
	}
	if conn != nil {
		report.ConnectionID = conn.ID
		report.ConnectionName = conn.Name
		report.ConnectionProvider = conn.Provider
	}
	return &driftRun{
		projectContext: *projectCtx,
		Report:         report,
	}, nil
}

type driftRun struct {
	projectContext
	Report *drift.DriftReport
}

func (s *Server) driftProposal(args projectArgs, mode string) (*driftRun, drift.RemediationProposal, error) {
	if err := firstErr(requireNonEmpty("project", args.Project), requireNonEmpty("mode", mode)); err != nil {
		return nil, drift.RemediationProposal{}, err
	}
	run, err := s.runDrift(args)
	if err != nil {
		return nil, drift.RemediationProposal{}, err
	}
	proposal, err := drift.BuildRemediationProposal(drift.RemediationInput{
		ProjectName: args.Project,
		Tool:        run.Tool,
		Env:         run.Env,
		Mode:        mode,
		Findings:    run.Report.Findings,
		Locations:   driftLocations(run.WorkDir, run.Resources),
	})
	if err != nil {
		return nil, drift.RemediationProposal{}, err
	}
	return run, proposal, nil
}

func (s *Server) commandEnvironment(connectionID string) (map[string]string, any, error) {
	if strings.TrimSpace(connectionID) == "" {
		return nil, nil, nil
	}
	connection, err := s.cloudConnections.Get(connectionID)
	if err != nil {
		return nil, nil, err
	}
	env := cloudconnections.CommandEnvironment(*connection)
	keys := make([]string, 0, len(env))
	for key := range env {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	public := map[string]any{
		"id":               connection.ID,
		"name":             connection.Name,
		"provider":         connection.Provider,
		"auth_method":      connection.AuthMethod,
		"region":           connection.Region,
		"command_env_keys": keys,
	}
	return env, public, nil
}

func parseResources(dir, tool string) ([]parser.Resource, error) {
	switch tool {
	case "pulumi":
		return (&pulumiparser.TSParser{}).ParseDir(dir)
	default:
		return parser.ForTool(tool).ParseDir(dir)
	}
}

func readProjectDescriptor(projectPath string) (*projectDescriptor, error) {
	data, err := os.ReadFile(filepath.Join(projectPath, ".iac-studio.json"))
	if err != nil {
		return nil, err
	}
	var descriptor projectDescriptor
	if err := json.Unmarshal(data, &descriptor); err != nil {
		return nil, err
	}
	return &descriptor, nil
}

func effectiveTool(descriptor *projectDescriptor, requestedTool, env string) string {
	requestedTool = strings.TrimSpace(requestedTool)
	if requestedTool == "" {
		requestedTool = "terraform"
	}
	if descriptor == nil {
		return requestedTool
	}
	if env != "" {
		if tool := descriptor.EnvironmentTools[env]; concreteTool(tool) {
			return tool
		}
	}
	if requestedTool == "terraform" && descriptorTool(descriptor.Tool) {
		return descriptor.Tool
	}
	if requestedTool == "multi" && concreteTool(descriptor.Tool) {
		return descriptor.Tool
	}
	return requestedTool
}

func concreteTool(tool string) bool {
	switch tool {
	case "terraform", "opentofu", "pulumi", "ansible":
		return true
	default:
		return false
	}
}

func descriptorTool(tool string) bool {
	return tool == "multi" || concreteTool(tool)
}

func safeProjectPath(projectsDir, name string) (string, error) {
	if err := safePathSegment(name); err != nil {
		return "", fmt.Errorf("invalid project name: %w", err)
	}
	resolved := filepath.Join(projectsDir, name)
	absProjects, err := filepath.Abs(projectsDir)
	if err != nil {
		return "", fmt.Errorf("resolve projects dir: %w", err)
	}
	if eval, err := filepath.EvalSymlinks(absProjects); err == nil {
		absProjects = eval
	}
	absResolved, err := filepath.Abs(resolved)
	if err != nil {
		return "", fmt.Errorf("resolve project path: %w", err)
	}
	if eval, err := filepath.EvalSymlinks(resolved); err == nil {
		if abs, absErr := filepath.Abs(eval); absErr == nil {
			absResolved = abs
		}
	}
	rel, err := filepath.Rel(absProjects, absResolved)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("project path escapes root")
	}
	return resolved, nil
}

func safePathSegment(value string) error {
	if value == "" || value == "." || value == ".." || strings.Contains(value, "..") || strings.ContainsAny(value, `/\`) {
		return fmt.Errorf("unsafe path segment %q", value)
	}
	for _, r := range value {
		if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_') {
			return fmt.Errorf("unsafe path segment %q", value)
		}
	}
	return nil
}

func safeProjectFile(projectPath, requested string) (string, error) {
	if strings.TrimSpace(requested) == "" {
		return "", fmt.Errorf("file path is required")
	}
	clean := filepath.Clean(filepath.FromSlash(requested))
	if filepath.IsAbs(clean) || clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("unsafe project file path")
	}
	target := filepath.Join(projectPath, clean)
	absProject, err := filepath.Abs(projectPath)
	if err != nil {
		return "", err
	}
	absTarget, err := filepath.Abs(target)
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(absProject, absTarget)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("file path escapes project root")
	}
	return absTarget, nil
}

func relativeResources(projectPath string, resources []parser.Resource) []parser.Resource {
	out := make([]parser.Resource, len(resources))
	for i, res := range resources {
		out[i] = res
		if res.File != "" {
			if rel, err := filepath.Rel(projectPath, res.File); err == nil && rel != "." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
				out[i].File = filepath.ToSlash(rel)
			} else {
				out[i].File = filepath.ToSlash(filepath.Base(res.File))
			}
		}
	}
	return out
}

func resourceAddress(res parser.Resource) string {
	if strings.TrimSpace(res.ID) != "" {
		return res.ID
	}
	if strings.TrimSpace(res.Type) == "" {
		return res.Name
	}
	if strings.TrimSpace(res.Name) == "" {
		return res.Type
	}
	return res.Type + "." + res.Name
}

func driftLocations(workDir string, resources []parser.Resource) map[string]drift.ResourceLocation {
	locations := make(map[string]drift.ResourceLocation, len(resources))
	for _, res := range resources {
		file := filepath.ToSlash(filepath.Base(res.File))
		if rel, err := filepath.Rel(workDir, res.File); err == nil && rel != "." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			file = filepath.ToSlash(rel)
		}
		locations[resourceAddress(res)] = drift.ResourceLocation{File: file, Line: res.Line}
	}
	return locations
}

func projectAudit(tool string, args projectArgs) AuditDecision {
	return AuditDecision{Tool: tool, Project: args.Project, ConnectionID: args.ConnectionID, Decision: "allowed"}
}

func (a AuditDecision) withError(err error) AuditDecision {
	a.Decision = "error"
	a.Error = err.Error()
	return a
}

func resourceReviewGuidance(res parser.Resource, report *policy.PolicyReport) []string {
	guidance := []string{
		"Review provider defaults and dependencies before applying changes.",
	}
	switch {
	case strings.Contains(res.Type, "iam"):
		guidance = append(guidance, "Check principals, actions, resources, conditions, and trust relationships.")
	case strings.Contains(res.Type, "security_group"), strings.Contains(res.Type, "firewall"), strings.Contains(res.Type, "route"):
		guidance = append(guidance, "Check public CIDRs, ports, protocols, routes, and exposure boundaries.")
	case strings.Contains(res.Type, "db"), strings.Contains(res.Type, "bucket"), strings.Contains(res.Type, "volume"):
		guidance = append(guidance, "Check encryption, retention, deletion protection, backups, and cost impact.")
	}
	if report != nil && len(report.Violations) > 0 {
		guidance = append(guidance, "Resolve policy findings or document an explicit exception before approval.")
	}
	return guidance
}

func classifyChangeRequest(request string) (string, []string) {
	lower := strings.ToLower(request)
	cues := []string{}
	risk := "unknown"
	for _, cue := range []string{"delete", "destroy", "replace", "public", "0.0.0.0/0", "::/0", "iam", "role", "policy", "security group", "firewall", "database", "rds", "bucket"} {
		if strings.Contains(lower, cue) {
			cues = append(cues, cue)
		}
	}
	if len(cues) == 0 {
		risk = "review_required"
	} else {
		risk = "risky"
		for _, cue := range cues {
			if cue == "delete" || cue == "destroy" || cue == "replace" {
				risk = "destructive"
				break
			}
		}
	}
	return risk, cues
}

func (s *Server) projectHistory(projectPath string, limit int) map[string]any {
	out := map[string]any{}
	if limit <= 0 {
		limit = 5
	}
	if commits, err := gitLines(projectPath, "log", "--oneline", fmt.Sprintf("-%d", limit)); err == nil {
		out["git_commits"] = commits
	} else {
		out["git_error"] = err.Error()
	}
	if changed, err := gitLines(projectPath, "status", "--short"); err == nil {
		out["git_status"] = changed
	}
	if snapshots, err := recovery.ListSnapshots(projectPath); err == nil {
		if len(snapshots) > limit {
			snapshots = snapshots[:limit]
		}
		out["snapshots"] = snapshots
	}
	out["review_artifacts"] = listReviewArtifacts(projectPath)
	return out
}

func gitLines(projectPath string, args ...string) ([]string, error) {
	cmd := exec.Command("git", append([]string{"-C", projectPath}, args...)...)
	data, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		if strings.TrimSpace(line) != "" {
			out = append(out, line)
		}
	}
	return out, nil
}

func listReviewArtifacts(projectPath string) []string {
	roots := []string{
		filepath.Join(projectPath, ".iac-studio", "remediations"),
		filepath.Join(projectPath, ".iac-studio", "rollbacks"),
	}
	var artifacts []string
	for _, root := range roots {
		_ = filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
			if err != nil || entry.IsDir() {
				return nil
			}
			if rel, relErr := filepath.Rel(projectPath, path); relErr == nil {
				artifacts = append(artifacts, filepath.ToSlash(rel))
			}
			return nil
		})
	}
	sort.Strings(artifacts)
	return artifacts
}

func writeArtifactFile(projectPath, relPath, content string) error {
	if !strings.HasPrefix(relPath, ".iac-studio/remediations/") {
		return fmt.Errorf("unsupported artifact path: %s", relPath)
	}
	target, err := safeProjectFile(projectPath, relPath)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	return os.WriteFile(target, []byte(content), 0o644)
}

func remediationBodyPath(rendered []drift.RenderedRemediationArtifact) string {
	for _, artifact := range rendered {
		if artifact.Kind == "pr_body" {
			return artifact.Path
		}
	}
	if len(rendered) == 0 {
		return ""
	}
	return rendered[0].Path
}

func renderRunbook(ctx *projectContext, incident string, history map[string]any, driftReport any) string {
	var b strings.Builder
	b.WriteString("# IaC Studio Runbook: ")
	b.WriteString(ctx.Name)
	b.WriteString("\n\n")
	if strings.TrimSpace(incident) != "" {
		b.WriteString("## Incident context\n")
		b.WriteString(strings.TrimSpace(incident))
		b.WriteString("\n\n")
	}
	b.WriteString("## Scope\n")
	b.WriteString(fmt.Sprintf("- Project: `%s`\n", ctx.Name))
	b.WriteString(fmt.Sprintf("- Tool: `%s`\n", ctx.Tool))
	if ctx.Env != "" {
		b.WriteString(fmt.Sprintf("- Environment: `%s`\n", ctx.Env))
	}
	b.WriteString(fmt.Sprintf("- Resources parsed: `%d`\n\n", len(ctx.Resources)))
	b.WriteString("## Triage sequence\n")
	b.WriteString("1. Inspect recent commits, generated remediation artifacts, and snapshots.\n")
	b.WriteString("2. Run a fresh plan and semantic classification.\n")
	b.WriteString("3. Run policy and security checks before approval.\n")
	b.WriteString("4. Run drift scan again after remediation to confirm closure.\n")
	b.WriteString("5. Apply only through the IaC Studio approval flow when risk is acknowledged.\n\n")
	b.WriteString("## Captured context\n")
	data, _ := json.MarshalIndent(map[string]any{"history": history, "drift": driftReport}, "", "  ")
	b.Write(data)
	b.WriteByte('\n')
	return b.String()
}

func highRiskReason(tool string) string {
	switch tool {
	case "apply":
		return "apply can create, update, replace, or delete cloud resources"
	case "destroy":
		return "destroy can remove cloud resources and data"
	case "assume_role":
		return "role assumption changes cloud identity and access scope"
	case "modify_connection":
		return "cloud connection changes can add or replace credentials"
	case "open_public_network_access":
		return "public network exposure can expand reachability to the internet"
	default:
		return "high-risk infrastructure action"
	}
}

func utcNow() time.Time {
	return time.Now().UTC()
}
