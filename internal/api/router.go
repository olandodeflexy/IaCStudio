package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/iac-studio/iac-studio/internal/ai"
	"github.com/iac-studio/iac-studio/internal/ai/providers"
	"github.com/iac-studio/iac-studio/internal/catalog"
	"github.com/iac-studio/iac-studio/internal/cloudconnections"
	"github.com/iac-studio/iac-studio/internal/drift"
	"github.com/iac-studio/iac-studio/internal/exporter"
	"github.com/iac-studio/iac-studio/internal/generator"
	"github.com/iac-studio/iac-studio/internal/importer"
	"github.com/iac-studio/iac-studio/internal/mcpairlock"
	"github.com/iac-studio/iac-studio/internal/parser"
	iacplan "github.com/iac-studio/iac-studio/internal/plan"
	"github.com/iac-studio/iac-studio/internal/project"
	pulumigen "github.com/iac-studio/iac-studio/internal/pulumi"
	"github.com/iac-studio/iac-studio/internal/recovery"
	"github.com/iac-studio/iac-studio/internal/registry"
	"github.com/iac-studio/iac-studio/internal/review"
	"github.com/iac-studio/iac-studio/internal/runner"
	"github.com/iac-studio/iac-studio/internal/scaffold"
	"github.com/iac-studio/iac-studio/internal/security"
	"github.com/iac-studio/iac-studio/internal/watcher"
)

// safeProjectPath validates a project name and returns its absolute path under projectsDir.
// It rejects names containing path separators, "..", or other traversal attempts.
func safeProjectPath(projectsDir, name string) (string, error) {
	// Reject empty, dot-prefixed, or names with path separators
	if name == "" || name == "." || name == ".." ||
		strings.ContainsAny(name, `/\`) ||
		strings.Contains(name, "..") {
		return "", fmt.Errorf("invalid project name: %q", name)
	}
	// Only allow alphanumeric, hyphens, and underscores
	for _, r := range name {
		if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9') || r == '-' || r == '_') {
			return "", fmt.Errorf("invalid project name: %q (only alphanumeric, hyphens, underscores)", name)
		}
	}
	resolved := filepath.Join(projectsDir, name)
	// Resolve symlinks so a symlink at ~/iac-projects/evil -> /etc/ is caught
	// (and so macOS's /var/folders -> /private/var/folders symlink doesn't
	// cause httptest-based tests to trip the escape check below).
	//
	// filepath.Abs errors surface explicitly — a failure would leave an
	// empty absProjects, which would then let any resolved path pass the
	// HasPrefix check and weaken the symlink-escape protection.
	absProjects, err := filepath.Abs(projectsDir)
	if err != nil {
		return "", fmt.Errorf("resolve projects dir: %w", err)
	}
	if evalProjects, err := filepath.EvalSymlinks(absProjects); err == nil {
		absProjects = evalProjects
	}
	absResolved, err := filepath.Abs(resolved)
	if err != nil {
		return "", fmt.Errorf("resolve project path: %w", err)
	}
	// If the directory already exists, resolve symlinks in the actual path.
	if evalResolved, err := filepath.EvalSymlinks(resolved); err == nil {
		if absEval, absErr := filepath.Abs(evalResolved); absErr == nil {
			absResolved = absEval
		}
	}
	if !strings.HasPrefix(absResolved, absProjects+string(filepath.Separator)) {
		return "", fmt.Errorf("project path escapes root: %q", name)
	}
	return resolved, nil
}

// safeSubdir resolves a subdirectory beneath projectPath while
// enforcing the same traversal + containment guarantees safeProjectPath
// offers at the project level. Each path segment is validated
// (alphanumeric + hyphen + underscore, no dots, no separators) and
// the final absolute path must stay inside projectPath after symlink
// resolution.
//
// Used by the /api/projects/{name}/run endpoint to rebase execution
// into environments/<env>/ for layered-v1 layouts so the runner finds
// Pulumi.yaml / main.tf in the right workdir.
func safePathSegment(seg string) error {
	if seg == "" || seg == "." || seg == ".." ||
		strings.ContainsAny(seg, `/\`) ||
		strings.Contains(seg, "..") {
		return fmt.Errorf("invalid path segment: %q", seg)
	}
	for _, r := range seg {
		if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9') || r == '-' || r == '_') {
			return fmt.Errorf("invalid path segment: %q (only alphanumeric, hyphens, underscores)", seg)
		}
	}
	return nil
}

func safeSubdir(projectPath string, segments ...string) (string, error) {
	for _, seg := range segments {
		if err := safePathSegment(seg); err != nil {
			return "", err
		}
	}
	joined := filepath.Join(append([]string{projectPath}, segments...)...)
	absProject, err := filepath.Abs(projectPath)
	if err != nil {
		return "", err
	}
	if eval, err := filepath.EvalSymlinks(absProject); err == nil {
		absProject = eval
	}
	absJoined, err := filepath.Abs(joined)
	if err != nil {
		return "", err
	}
	if eval, err := filepath.EvalSymlinks(joined); err == nil {
		if abs, absErr := filepath.Abs(eval); absErr == nil {
			absJoined = abs
		}
	}
	if !strings.HasPrefix(absJoined, absProject+string(filepath.Separator)) {
		return "", fmt.Errorf("subdir escapes project root")
	}
	info, err := os.Stat(joined)
	if err != nil {
		// Don't surface the underlying os.Stat error — it carries the
		// absolute filesystem path which we don't want bubbling up to
		// HTTP clients. Server-side log keeps the detail for ops
		// debugging.
		log.Printf("safeSubdir: stat %s: %v", joined, err)
		if os.IsNotExist(err) {
			return "", fmt.Errorf("subdir does not exist")
		}
		return "", fmt.Errorf("subdir is not accessible")
	}
	// The result is used as cmd.Dir — passing a file would produce a
	// confusing 'not a directory' error mid-exec. Reject here so the
	// 400 carries a targeted message. Don't include the path; the
	// caller already knows what they passed in.
	if !info.IsDir() {
		log.Printf("safeSubdir: not a directory: %s", joined)
		return "", fmt.Errorf("subdir is not a directory")
	}
	return joined, nil
}

func safeProjectFile(projectPath, requested string, allowedExts ...string) (string, error) {
	if requested == "" {
		return "", fmt.Errorf("file path is required")
	}
	target := requested
	if !filepath.IsAbs(target) {
		target = filepath.Join(projectPath, target)
	}
	target = filepath.Clean(target)

	absProject, err := filepath.Abs(projectPath)
	if err != nil {
		return "", fmt.Errorf("resolve project root: %w", err)
	}
	if eval, err := filepath.EvalSymlinks(absProject); err == nil {
		absProject = eval
	}

	absTarget, err := filepath.Abs(target)
	if err != nil {
		return "", fmt.Errorf("resolve file path: %w", err)
	}

	if len(allowedExts) > 0 {
		ext := filepath.Ext(absTarget)
		allowed := false
		for _, allowedExt := range allowedExts {
			if ext == allowedExt {
				allowed = true
				break
			}
		}
		if !allowed {
			return "", fmt.Errorf("unsupported file extension: %s", ext)
		}
	}

	parent := filepath.Dir(absTarget)
	evalParent, err := filepath.EvalSymlinks(parent)
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("file parent directory does not exist")
		}
		return "", fmt.Errorf("file parent directory is not accessible")
	}
	evalParent, err = filepath.Abs(evalParent)
	if err != nil {
		return "", fmt.Errorf("resolve file parent: %w", err)
	}
	evalTargetPath := filepath.Join(evalParent, filepath.Base(absTarget))
	parentRel, err := filepath.Rel(absProject, evalParent)
	if err != nil || strings.HasPrefix(parentRel, ".."+string(filepath.Separator)) || parentRel == ".." {
		return "", fmt.Errorf("file parent escapes project root")
	}
	targetRel, err := filepath.Rel(absProject, evalTargetPath)
	if err != nil || targetRel == "." || strings.HasPrefix(targetRel, ".."+string(filepath.Separator)) || targetRel == ".." {
		return "", fmt.Errorf("file path escapes project root")
	}

	if evalTarget, err := filepath.EvalSymlinks(absTarget); err == nil {
		evalTarget, err = filepath.Abs(evalTarget)
		if err != nil {
			return "", fmt.Errorf("resolve existing file: %w", err)
		}
		targetRel, err := filepath.Rel(absProject, evalTarget)
		if err != nil || strings.HasPrefix(targetRel, ".."+string(filepath.Separator)) || targetRel == ".." {
			return "", fmt.Errorf("existing file escapes project root")
		}
	}

	return absTarget, nil
}

func allowedGeneratedExtensions(tool, defaultExt string) []string {
	if tool == "ansible" {
		return []string{".yml", ".yaml"}
	}
	return []string{defaultExt}
}

func generateForSync(gen generator.Generator, resources []parser.Resource, includeProviders bool) (string, error) {
	if !includeProviders {
		if resourcesOnly, ok := gen.(interface {
			GenerateResourcesOnly([]parser.Resource) (string, error)
		}); ok {
			return resourcesOnly.GenerateResourcesOnly(resources)
		}
	}
	return gen.Generate(resources)
}

type syncEdge struct {
	From  string `json:"from"`  // source node ID
	To    string `json:"to"`    // target node ID
	Field string `json:"field"` // connection field (e.g., "vpc_id")
}

type syncRequest struct {
	Resources []parser.Resource `json:"resources"`
	Code      *string           `json:"code,omitempty"`
	File      string            `json:"file,omitempty"`
	Edges     []syncEdge        `json:"edges"`
}

func materializeSyncEdges(resources []parser.Resource, edges []syncEdge) {
	if len(edges) == 0 {
		return
	}
	idIndex := make(map[string]int)
	for i, r := range resources {
		idIndex[r.ID] = i
	}
	for _, edge := range edges {
		fromIdx, fromOK := idIndex[edge.From]
		toIdx, toOK := idIndex[edge.To]
		if fromOK && toOK {
			if resources[fromIdx].Properties == nil {
				resources[fromIdx].Properties = make(map[string]interface{})
			}
			key := "__edge_" + edge.Field
			target := resources[toIdx].Name
			if existing, ok := resources[fromIdx].Properties[key]; ok {
				resources[fromIdx].Properties[key] = appendSyncEdgeTarget(existing, target)
				continue
			}
			resources[fromIdx].Properties[key] = target
		}
	}
}

func appendSyncEdgeTarget(existing interface{}, target string) interface{} {
	switch v := existing.(type) {
	case string:
		if v == target {
			return v
		}
		return []string{v, target}
	case []string:
		for _, existingTarget := range v {
			if existingTarget == target {
				return v
			}
		}
		return append(v, target)
	case []interface{}:
		for _, existingTarget := range v {
			if s, ok := existingTarget.(string); ok && s == target {
				return v
			}
		}
		return append(v, target)
	default:
		return []string{target}
	}
}

func pulumiEnvDir(projectPath, env string) (string, error) {
	if env != "" {
		return safeSubdir(projectPath, "environments", env)
	}
	if _, err := os.Stat(filepath.Join(projectPath, "Pulumi.yaml")); err == nil {
		return projectPath, nil
	}
	entries, err := os.ReadDir(filepath.Join(projectPath, "environments"))
	if err != nil {
		return projectPath, nil
	}
	var envs []string
	for _, entry := range entries {
		if entry.IsDir() {
			envs = append(envs, entry.Name())
		}
	}
	if len(envs) == 1 {
		return safeSubdir(projectPath, "environments", envs[0])
	}
	if len(envs) > 1 {
		return "", fmt.Errorf("env query parameter is required for layered pulumi projects")
	}
	return projectPath, nil
}

type projectToolDescriptor struct {
	Layout           string            `json:"layout"`
	Tool             string            `json:"tool"`
	Environments     []string          `json:"environments"`
	EnvironmentTools map[string]string `json:"environment_tools"`
	Drift            struct {
		Suppressions []drift.SuppressionRule `json:"suppressions"`
	} `json:"drift"`
}

func readProjectToolDescriptor(projectPath string) (projectToolDescriptor, error) {
	var descriptor projectToolDescriptor
	data, err := os.ReadFile(filepath.Join(projectPath, ".iac-studio.json"))
	if err != nil {
		return descriptor, err
	}
	if err := json.Unmarshal(data, &descriptor); err != nil {
		return descriptor, err
	}
	return descriptor, nil
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

func effectiveProjectTool(projectPath, requestedTool, env string) string {
	requestedDefaulted := false
	if requestedTool == "" {
		requestedTool = "terraform"
		requestedDefaulted = true
	}
	descriptor, err := readProjectToolDescriptor(projectPath)
	if err != nil {
		return requestedTool
	}
	if env != "" {
		if tool := descriptor.EnvironmentTools[env]; concreteTool(tool) {
			return tool
		}
	}
	if requestedDefaulted && descriptorTool(descriptor.Tool) {
		return descriptor.Tool
	}
	if requestedTool != "multi" {
		return requestedTool
	}
	if concreteTool(descriptor.Tool) {
		return descriptor.Tool
	}
	return requestedTool
}

func hybridToolResolutionMessage(missingEnvMessage, env string) string {
	if env == "" {
		return missingEnvMessage
	}
	return fmt.Sprintf("unresolved hybrid tool for env %q; check .iac-studio.json environment_tools", env)
}

type projectDriftRequest struct {
	Tool         string
	Env          string
	ConnectionID string
}

type projectDriftRun struct {
	Report      *drift.DriftReport
	Resources   []parser.Resource
	ProjectPath string
	WorkDir     string
	Tool        string
	Env         string
}

func runProjectDrift(projectsDir, name string, req projectDriftRequest, detector *drift.Detector, cloudConnections *cloudconnections.Manager) (*projectDriftRun, int, string) {
	projectPath, err := safeProjectPath(projectsDir, name)
	if err != nil {
		return nil, http.StatusBadRequest, err.Error()
	}

	connectionID := strings.TrimSpace(req.ConnectionID)
	var connection *cloudconnections.Connection
	if connectionID != "" {
		if cloudConnections == nil {
			return nil, http.StatusInternalServerError, "cloud connections are not available"
		}
		connection, err = cloudConnections.Get(connectionID)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return nil, http.StatusNotFound, "cloud connection not found"
			}
			return nil, http.StatusInternalServerError, "load cloud connection: " + err.Error()
		}
	}

	tool := effectiveProjectTool(projectPath, req.Tool, req.Env)
	if tool == "multi" {
		return nil, http.StatusBadRequest, hybridToolResolutionMessage("env is required when detecting drift for hybrid projects", req.Env)
	}
	if tool != "terraform" && tool != "opentofu" {
		return nil, http.StatusBadRequest, "drift detection currently supports Terraform and OpenTofu state"
	}

	workDir := projectPath
	if req.Env != "" {
		subPath, subErr := safeSubdir(projectPath, "environments", req.Env)
		if subErr != nil {
			return nil, http.StatusBadRequest, "invalid env: " + subErr.Error()
		}
		workDir = subPath
	}

	p := parser.ForTool(tool)
	resources, err := p.ParseDir(workDir)
	if err != nil {
		return nil, http.StatusInternalServerError, err.Error()
	}
	codeResources := make(map[string]map[string]interface{})
	for _, res := range resources {
		codeResources[res.Type+"."+res.Name] = res.Properties
	}

	var suppressions []drift.SuppressionRule
	if descriptor, descriptorErr := readProjectToolDescriptor(projectPath); descriptorErr == nil {
		suppressions = descriptor.Drift.Suppressions
	}
	report, err := detector.DetectWithOptions(workDir, codeResources, drift.DetectOptions{
		Env:          req.Env,
		Suppressions: suppressions,
	})
	if err != nil {
		return nil, http.StatusInternalServerError, err.Error()
	}
	if connection != nil {
		report.ConnectionID = connection.ID
		report.ConnectionName = connection.Name
		report.ConnectionProvider = connection.Provider
	}
	return &projectDriftRun{
		Report:      report,
		Resources:   resources,
		ProjectPath: projectPath,
		WorkDir:     workDir,
		Tool:        tool,
		Env:         req.Env,
	}, http.StatusOK, ""
}

func driftResourceLocations(workDir string, resources []parser.Resource) map[string]drift.ResourceLocation {
	locations := make(map[string]drift.ResourceLocation, len(resources))
	for _, res := range resources {
		addr := res.Type + "." + res.Name
		locations[addr] = drift.ResourceLocation{
			File: relativeSourcePath(workDir, res.File),
			Line: res.Line,
		}
	}
	return locations
}

func buildProjectDriftRemediationProposal(name, mode string, run *projectDriftRun) (drift.RemediationProposal, error) {
	return drift.BuildRemediationProposal(drift.RemediationInput{
		ProjectName: name,
		Tool:        run.Tool,
		Env:         run.Env,
		Mode:        mode,
		Findings:    run.Report.Findings,
		Locations:   driftResourceLocations(run.WorkDir, run.Resources),
	})
}

func relativeSourcePath(root, path string) string {
	if strings.TrimSpace(path) == "" {
		return ""
	}
	rel, err := filepath.Rel(root, path)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return filepath.ToSlash(filepath.Base(path))
	}
	return filepath.ToSlash(rel)
}

func writeRenderedRemediationArtifacts(projectPath string, rendered []drift.RenderedRemediationArtifact) error {
	for _, artifact := range rendered {
		if err := writeGeneratedArtifactFile(projectPath, artifact.Path, artifact.Content); err != nil {
			return fmt.Errorf("writing remediation artifact %s: %w", artifact.Path, err)
		}
	}
	return nil
}

func writeRenderedRollbackArtifacts(projectPath string, rendered []recovery.RenderedRollbackArtifact) error {
	for _, artifact := range rendered {
		if err := writeGeneratedArtifactFile(projectPath, artifact.Path, artifact.Content); err != nil {
			return fmt.Errorf("writing rollback artifact %s: %w", artifact.Path, err)
		}
	}
	return nil
}

func remediationArtifactPaths(rendered []drift.RenderedRemediationArtifact) []string {
	paths := make([]string, 0, len(rendered))
	for _, artifact := range rendered {
		paths = append(paths, artifact.Path)
	}
	return paths
}

func rollbackArtifactPaths(rendered []recovery.RenderedRollbackArtifact) []string {
	paths := make([]string, 0, len(rendered))
	for _, artifact := range rendered {
		paths = append(paths, artifact.Path)
	}
	return paths
}

func remediationArtifactBodyPath(rendered []drift.RenderedRemediationArtifact) string {
	for _, artifact := range rendered {
		if artifact.Kind == "pr_body" {
			return artifact.Path
		}
	}
	return ""
}

func rollbackArtifactBodyPath(rendered []recovery.RenderedRollbackArtifact) string {
	for _, artifact := range rendered {
		if artifact.Kind == "proposal" {
			return artifact.Path
		}
	}
	return ""
}

func reviewHandoffStatus(err error) int {
	switch {
	case errors.Is(err, review.ErrBranchExists),
		errors.Is(err, review.ErrDirtyWorktree),
		errors.Is(err, review.ErrNoChanges):
		return http.StatusConflict
	case errors.Is(err, review.ErrInvalidInput),
		errors.Is(err, review.ErrNotGitRepository):
		return http.StatusBadRequest
	default:
		return http.StatusInternalServerError
	}
}

func writeGeneratedArtifactFile(projectPath, relPath, content string) error {
	artifactPath, err := safeReviewArtifactPath(relPath)
	if err != nil {
		return err
	}
	target, err := safeProjectFilePath(projectPath, artifactPath)
	if err != nil {
		return err
	}
	if err := ensureNoSymlinkDir(projectPath, filepath.Dir(target)); err != nil {
		return err
	}
	if info, err := os.Lstat(target); err == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("refusing to overwrite symlink %q", relPath)
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("refusing to overwrite non-regular file %q", relPath)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return os.WriteFile(target, []byte(content), 0o644)
}

func safeReviewArtifactPath(relPath string) (string, error) {
	clean := filepath.ToSlash(filepath.Clean(filepath.FromSlash(relPath)))
	if !strings.HasPrefix(clean, ".iac-studio/remediations/") &&
		!strings.HasPrefix(clean, ".iac-studio/rollbacks/") {
		return "", fmt.Errorf("generated artifact path must stay under .iac-studio review artifacts: %q", relPath)
	}
	return clean, nil
}

func ensureNoSymlinkDir(projectPath, dir string) error {
	rel, err := filepath.Rel(projectPath, dir)
	if err != nil || rel == "." {
		return err
	}
	current := projectPath
	for _, part := range strings.Split(filepath.ToSlash(rel), "/") {
		if part == "" || part == "." {
			continue
		}
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if errors.Is(err, os.ErrNotExist) {
			if err := os.Mkdir(current, 0o755); err != nil && !errors.Is(err, os.ErrExist) {
				return err
			}
			continue
		}
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("refusing to write through symlink directory %q", part)
		}
		if !info.IsDir() {
			return fmt.Errorf("artifact parent %q is not a directory", part)
		}
	}
	return nil
}

func buildProjectRollbackProposal(projectPath, projectName, snapshotID, env string) (recovery.RollbackProposal, int, string) {
	if err := safePathSegment(snapshotID); err != nil {
		return recovery.RollbackProposal{}, http.StatusBadRequest, "invalid snapshot id: " + err.Error()
	}
	if env != "" {
		if err := safePathSegment(env); err != nil {
			return recovery.RollbackProposal{}, http.StatusBadRequest, "invalid env: " + err.Error()
		}
	}
	snapshots, err := recovery.ListSnapshots(projectPath)
	if err != nil {
		return recovery.RollbackProposal{}, http.StatusInternalServerError, err.Error()
	}
	var target *recovery.StateSnapshot
	for i := range snapshots {
		if snapshots[i].ID == snapshotID {
			target = &snapshots[i]
			break
		}
	}
	if target == nil {
		return recovery.RollbackProposal{}, http.StatusNotFound, "snapshot not found"
	}
	if env != "" && target.Env != env {
		return recovery.RollbackProposal{}, http.StatusBadRequest, "snapshot does not belong to requested env"
	}
	var current *recovery.StateSnapshot
	for i := range snapshots {
		if snapshots[i].ID == target.ID {
			continue
		}
		if snapshots[i].Tool == target.Tool && snapshots[i].Env == target.Env {
			current = &snapshots[i]
			break
		}
	}
	proposal, err := recovery.BuildRollbackProposal(recovery.RollbackInput{
		ProjectName:     projectName,
		TargetSnapshot:  *target,
		CurrentSnapshot: current,
	})
	if err != nil {
		return recovery.RollbackProposal{}, http.StatusBadRequest, err.Error()
	}
	return proposal, http.StatusOK, ""
}

func safeProjectFilePath(projectPath, relPath string) (string, error) {
	if strings.TrimSpace(relPath) == "" {
		return "", errors.New("artifact path is required")
	}
	clean := filepath.Clean(filepath.FromSlash(relPath))
	if filepath.IsAbs(clean) || clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(os.PathSeparator)) {
		return "", fmt.Errorf("invalid artifact path %q", relPath)
	}
	target := filepath.Join(projectPath, clean)
	rel, err := filepath.Rel(projectPath, target)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return "", fmt.Errorf("artifact path escapes project: %q", relPath)
	}
	return target, nil
}

func parseProjectResources(projectPath, tool, env string) ([]parser.Resource, error) {
	if tool == "multi" {
		if env == "" {
			return parseHybridProjectResources(projectPath)
		}
		return nil, fmt.Errorf("unresolved hybrid tool for env %q", env)
	}
	if tool == "pulumi" {
		targetDir, envErr := pulumiEnvDir(projectPath, env)
		if envErr != nil {
			return nil, envErr
		}
		return (&pulumigen.TSParser{}).ParseDir(targetDir)
	}
	p := parser.ForTool(tool)
	targetDir := projectPath
	if env != "" {
		subPath, err := safeSubdir(projectPath, "environments", env)
		if err != nil {
			return nil, err
		}
		targetDir = subPath
	}
	return p.ParseDir(targetDir)
}

func parseHybridProjectResources(projectPath string) ([]parser.Resource, error) {
	descriptor, err := readProjectToolDescriptor(projectPath)
	if err != nil {
		return parser.ForTool("terraform").ParseDir(projectPath)
	}
	if len(descriptor.EnvironmentTools) == 0 {
		return parser.ForTool("terraform").ParseDir(projectPath)
	}
	envs := descriptor.Environments
	if len(envs) == 0 {
		for env := range descriptor.EnvironmentTools {
			envs = append(envs, env)
		}
		sort.Strings(envs)
	}
	var resources []parser.Resource
	for _, env := range envs {
		tool := descriptor.EnvironmentTools[env]
		if !concreteTool(tool) {
			continue
		}
		envDir, subErr := safeSubdir(projectPath, "environments", env)
		if subErr != nil {
			return nil, fmt.Errorf("%s: %w", env, subErr)
		}
		var parsed []parser.Resource
		if tool == "pulumi" {
			parsed, err = (&pulumigen.TSParser{}).ParseDir(envDir)
		} else {
			parsed, err = parser.ForTool(tool).ParseDir(envDir)
		}
		if err != nil {
			return nil, fmt.Errorf("parse %s environment %q: %w", tool, env, err)
		}
		resources = append(resources, parsed...)
	}
	return resources, nil
}

func resourceParseErrorStatus(err error) int {
	if err == nil {
		return http.StatusOK
	}
	msg := err.Error()
	if strings.Contains(msg, "subdir") ||
		strings.Contains(msg, "env query parameter") ||
		strings.Contains(msg, "invalid path segment") ||
		strings.Contains(msg, "unresolved hybrid tool") {
		return http.StatusBadRequest
	}
	return http.StatusInternalServerError
}

func simpleRelativeFileName(target string) bool {
	return target != "" && !filepath.IsAbs(target) && !strings.ContainsAny(target, `/\`)
}

func invalidatePlan(projectPaths ...string) {
	planGate.mu.Lock()
	defer planGate.mu.Unlock()
	for _, projectPath := range projectPaths {
		delete(planGate.plans, projectPath)
	}
}

func planInvalidationPaths(projectPath string, targets ...string) []string {
	seen := map[string]bool{projectPath: true}
	paths := []string{projectPath}
	for _, target := range targets {
		envDir, ok := envWorkdirForProjectFile(projectPath, target)
		if !ok || seen[envDir] {
			continue
		}
		seen[envDir] = true
		paths = append(paths, envDir)
	}
	return paths
}

func envWorkdirForProjectFile(projectPath, target string) (string, bool) {
	absTarget := target
	if !filepath.IsAbs(absTarget) {
		absTarget = filepath.Join(projectPath, target)
	}
	rel, err := filepath.Rel(projectPath, absTarget)
	if err != nil || rel == "." || strings.HasPrefix(rel, "..") {
		return "", false
	}
	parts := strings.Split(filepath.ToSlash(rel), "/")
	if len(parts) < 3 || parts[0] != "environments" || parts[1] == "" {
		return "", false
	}
	return filepath.Join(projectPath, "environments", parts[1]), true
}

func handlePulumiSync(w http.ResponseWriter, fw *watcher.FileWatcher, projectPath, env string, body syncRequest) {
	targetDir, err := pulumiEnvDir(projectPath, env)
	if err != nil {
		http.Error(w, "invalid env: "+err.Error(), 400)
		return
	}
	targetFile, pathErr := safeProjectFile(targetDir, "index.ts", ".ts")
	if pathErr != nil {
		http.Error(w, "invalid pulumi index file: "+pathErr.Error(), 400)
		return
	}

	if body.Code != nil {
		target := body.File
		if target == "" {
			target = "index.ts"
		}
		safeTarget, codePathErr := safeProjectFile(targetDir, target, ".ts")
		if codePathErr != nil {
			http.Error(w, "invalid code file: "+codePathErr.Error(), 400)
			return
		}

		fw.Pause(projectPath)
		defer fw.Resume(projectPath)
		invalidatePlan(projectPath, targetDir)

		tmpFile := safeTarget + ".tmp"
		if err := os.WriteFile(tmpFile, []byte(*body.Code), 0644); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		if err := os.Rename(tmpFile, safeTarget); err != nil {
			_ = os.Remove(tmpFile)
			http.Error(w, err.Error(), 500)
			return
		}
		responseFile, relErr := filepath.Rel(projectPath, safeTarget)
		if relErr != nil || responseFile == "." || strings.HasPrefix(responseFile, "..") {
			responseFile = filepath.Base(safeTarget)
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"file": responseFile,
			"code": *body.Code,
		})
		return
	}

	resources := body.Resources
	materializeSyncEdges(resources, body.Edges)

	existing := ""
	if data, err := os.ReadFile(targetFile); err == nil {
		existing = string(data)
	}
	code, err := pulumigen.SyncProgram(existing, pulumigen.ProjectConfig{
		Name:      pulumigen.ProjectNameFromDir(targetDir),
		Resources: resources,
	})
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}

	fw.Pause(projectPath)
	defer fw.Resume(projectPath)
	invalidatePlan(projectPath, targetDir)

	tmpFile := targetFile + ".tmp"
	if err := os.WriteFile(tmpFile, []byte(code), 0644); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	if err := os.Rename(tmpFile, targetFile); err != nil {
		_ = os.Remove(tmpFile)
		http.Error(w, err.Error(), 500)
		return
	}
	responseFile, relErr := filepath.Rel(projectPath, targetFile)
	if relErr != nil || responseFile == "." || strings.HasPrefix(responseFile, "..") {
		responseFile = filepath.Base(targetFile)
	}
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"file": responseFile,
		"code": code,
	})
}

// planGate tracks which projects have had a recent plan run.
// Apply/destroy is only allowed after a plan has been run for the same project.
var planGate = struct {
	mu    sync.Mutex
	plans map[string]time.Time // projectPath -> last plan time
}{plans: make(map[string]time.Time)}

// recordPlan marks that a plan was run for a project.
func recordPlan(projectPath string) {
	planGate.mu.Lock()
	planGate.plans[projectPath] = time.Now()
	planGate.mu.Unlock()
}

// hasPlan checks that a plan was run for a project within the last hour.
func hasPlan(projectPath string) bool {
	planGate.mu.Lock()
	defer planGate.mu.Unlock()
	t, ok := planGate.plans[projectPath]
	return ok && time.Since(t) < time.Hour
}

// maxRequestBody is the maximum allowed request body size (1MB).
// Prevents clients from sending oversized payloads to exhaust memory.
const maxRequestBody = 1 << 20

// limitBody wraps r.Body with a MaxBytesReader so oversized payloads
// are rejected before the full body is read into memory.
func limitBody(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBody)
}

// RouterOptions allows callers to provide long-lived services that need
// explicit lifecycle management outside the router.
type RouterOptions struct {
	MCPAirlock *mcpairlock.Manager
}

// NewRouter creates the HTTP router with all endpoints.
func NewRouter(hub *Hub, fw *watcher.FileWatcher, aiClient *ai.Client, run *runner.SafeRunner, projectsDir string) *http.ServeMux {
	return NewRouterWithOptions(hub, fw, aiClient, run, projectsDir, RouterOptions{})
}

// NewRouterWithOptions creates the HTTP router with explicit service options.
func NewRouterWithOptions(hub *Hub, fw *watcher.FileWatcher, aiClient *ai.Client, run *runner.SafeRunner, projectsDir string, opts RouterOptions) *http.ServeMux {
	mux := http.NewServeMux()

	// Health
	mux.HandleFunc("GET /api/health", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok", "version": "0.1.0"})
	})

	// List available IaC tools detected on this machine
	mux.HandleFunc("GET /api/tools", func(w http.ResponseWriter, r *http.Request) {
		tools := run.DetectTools()
		_ = json.NewEncoder(w).Encode(tools)
	})

	// Resource catalog — returns all resources for a tool, optionally filtered by provider
	mux.HandleFunc("GET /api/catalog", func(w http.ResponseWriter, r *http.Request) {
		tool := r.URL.Query().Get("tool")
		if tool == "" {
			tool = "terraform"
		}
		provider := r.URL.Query().Get("provider") // optional: "aws", "google", "azurerm"
		var cat catalog.Catalog
		if provider != "" {
			cat = catalog.GetCatalogByProvider(tool, provider)
		} else {
			cat = catalog.GetCatalog(tool)
		}
		_ = json.NewEncoder(w).Encode(cat)
	})

	// List projects
	mux.HandleFunc("GET /api/projects", func(w http.ResponseWriter, r *http.Request) {
		entries, err := os.ReadDir(projectsDir)
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		projects := []map[string]string{}
		for _, e := range entries {
			if e.IsDir() {
				projects = append(projects, map[string]string{
					"name": e.Name(),
					"path": filepath.Join(projectsDir, e.Name()),
				})
			}
		}
		_ = json.NewEncoder(w).Encode(projects)
	})

	// Create project
	mux.HandleFunc("POST /api/projects", func(w http.ResponseWriter, r *http.Request) {
		limitBody(w, r)
		var req struct {
			Name string `json:"name"`
			Tool string `json:"tool"` // terraform | opentofu | ansible
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid request", 400)
			return
		}

		projectPath, err := safeProjectPath(projectsDir, req.Name)
		if err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		if entries, err := os.ReadDir(projectPath); err == nil && len(entries) > 0 {
			http.Error(w, "project already exists", http.StatusConflict)
			return
		} else if err != nil && !os.IsNotExist(err) {
			http.Error(w, err.Error(), 500)
			return
		}
		if err := os.MkdirAll(projectPath, 0755); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}

		// Generate initial files based on tool
		gen := generator.ForTool(req.Tool)
		if err := gen.WriteScaffold(projectPath); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}

		// Start watching the project directory
		_ = fw.Watch(projectPath)

		_ = json.NewEncoder(w).Encode(map[string]string{
			"name": req.Name,
			"path": projectPath,
			"tool": req.Tool,
		})
	})

	// List registered blueprints (opinionated project layouts).
	// See internal/scaffold for the Blueprint interface and bundled blueprints.
	mux.HandleFunc("GET /api/blueprints", func(w http.ResponseWriter, r *http.Request) {
		type bpView struct {
			ID          string           `json:"id"`
			Name        string           `json:"name"`
			Description string           `json:"description"`
			Tool        string           `json:"tool"`
			Inputs      []scaffold.Input `json:"inputs"`
		}
		list := scaffold.Default.List()
		out := make([]bpView, 0, len(list))
		for _, bp := range list {
			out = append(out, bpView{
				ID:          bp.ID(),
				Name:        bp.Name(),
				Description: bp.Description(),
				Tool:        bp.Tool(),
				Inputs:      bp.Inputs(),
			})
		}
		_ = json.NewEncoder(w).Encode(out)
	})

	// Render a blueprint into a new project directory.
	// Body: {"name": "...", "values": {...blueprint-specific inputs...}}
	// The "name" doubles as the project directory name; "values.project_name"
	// is auto-filled from "name" when not explicitly set.
	mux.HandleFunc("POST /api/blueprints/{id}/render", func(w http.ResponseWriter, r *http.Request) {
		limitBody(w, r)
		id := r.PathValue("id")
		bp, ok := scaffold.Default.Get(id)
		if !ok {
			http.Error(w, "unknown blueprint: "+id, 404)
			return
		}
		var req struct {
			Name   string         `json:"name"`
			Values map[string]any `json:"values"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid request", 400)
			return
		}
		if req.Values == nil {
			req.Values = map[string]any{}
		}
		if _, has := req.Values["project_name"]; !has {
			// safeProjectPath accepts underscores and mixed case for directory
			// names, but blueprints apply stricter rules (lowercase + hyphens)
			// on project_name since it lands inside HCL and cloud resource
			// identifiers. Normalise here so a valid-on-disk name doesn't
			// unexpectedly fail blueprint validation.
			req.Values["project_name"] = strings.ReplaceAll(strings.ToLower(req.Name), "_", "-")
		}

		projectPath, err := safeProjectPath(projectsDir, req.Name)
		if err != nil {
			http.Error(w, err.Error(), 400)
			return
		}

		// Render first so an input-validation error (400) never leaves an
		// empty project directory behind. Only create the directory once we
		// know we have files to write.
		files, err := bp.Render(req.Values)
		if err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		if err := os.MkdirAll(projectPath, 0755); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		if err := scaffold.Write(projectPath, files); err != nil {
			// Map scaffold error kinds to meaningful HTTP status codes.
			//  - Existing file / symlinked root: 409 Conflict (precondition).
			//  - Blueprint bug (duplicate or invalid emitted path): 500.
			//  - Anything else (I/O, permissions): 500.
			status := http.StatusInternalServerError
			switch {
			case errors.Is(err, scaffold.ErrConflict),
				errors.Is(err, scaffold.ErrSymlinkInRoot):
				status = http.StatusConflict
			case errors.Is(err, scaffold.ErrInvalidPath),
				errors.Is(err, scaffold.ErrDuplicatePath):
				status = http.StatusInternalServerError
			}
			http.Error(w, err.Error(), status)
			return
		}

		_ = fw.Watch(projectPath)

		paths := make([]string, len(files))
		for i, f := range files {
			paths[i] = f.Path
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"name":      req.Name,
			"path":      projectPath,
			"blueprint": bp.ID(),
			"tool":      bp.Tool(),
			"files":     paths,
		})
	})

	// Parse project files and return resource graph
	mux.HandleFunc("GET /api/projects/{name}/resources", func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		requestedTool := r.URL.Query().Get("tool")
		env := r.URL.Query().Get("env")
		projectPath, err := safeProjectPath(projectsDir, name)
		if err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		tool := effectiveProjectTool(projectPath, requestedTool, env)
		resources, err := parseProjectResources(projectPath, tool, env)
		if err != nil {
			http.Error(w, err.Error(), resourceParseErrorStatus(err))
			return
		}
		_ = json.NewEncoder(w).Encode(resources)
	})

	// Sync resources from UI to disk
	mux.HandleFunc("POST /api/projects/{name}/sync", func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		requestedTool := r.URL.Query().Get("tool")
		env := r.URL.Query().Get("env")
		projectPath, err := safeProjectPath(projectsDir, name)
		if err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		limitBody(w, r)
		var body syncRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "invalid request body", 400)
			return
		}
		tool := effectiveProjectTool(projectPath, requestedTool, env)
		if tool == "multi" {
			http.Error(w, hybridToolResolutionMessage("env query parameter is required for hybrid project sync", env), 400)
			return
		}
		if tool == "pulumi" {
			handlePulumiSync(w, fw, projectPath, env, body)
			return
		}

		gen := generator.ForTool(tool)
		ext := gen.FileExtension()
		allowedExts := allowedGeneratedExtensions(tool, ext)
		syncWorkdir := projectPath
		if env != "" {
			subPath, subErr := safeSubdir(projectPath, "environments", env)
			if subErr != nil {
				http.Error(w, "invalid env: "+subErr.Error(), 400)
				return
			}
			syncWorkdir = subPath
		}

		if body.Code != nil {
			target := body.File
			if target == "" {
				target = filepath.Join(syncWorkdir, "main"+ext)
			} else if env != "" && simpleRelativeFileName(target) {
				target = filepath.Join(syncWorkdir, target)
			}
			safeTarget, pathErr := safeProjectFile(projectPath, target, allowedExts...)
			if pathErr != nil {
				http.Error(w, "invalid code file: "+pathErr.Error(), 400)
				return
			}

			// Pause watcher to avoid echo
			fw.Pause(projectPath)
			defer fw.Resume(projectPath)

			// Invalidate plan gate — code changed, previous plan is stale
			invalidatePlan(planInvalidationPaths(projectPath, safeTarget)...)

			tmpFile := safeTarget + ".tmp"
			if err := os.WriteFile(tmpFile, []byte(*body.Code), 0644); err != nil {
				http.Error(w, err.Error(), 500)
				return
			}
			if err := os.Rename(tmpFile, safeTarget); err != nil {
				_ = os.Remove(tmpFile) // best-effort cleanup on failure
				http.Error(w, err.Error(), 500)
				return
			}
			responseFile, relErr := filepath.Rel(projectPath, safeTarget)
			if relErr != nil || responseFile == "." || strings.HasPrefix(responseFile, "..") {
				responseFile = filepath.Base(safeTarget)
			}

			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"file": responseFile,
				"code": *body.Code,
			})
			return
		}
		resources := body.Resources

		// Materialize edges into resource properties so the generator knows
		// exactly which target instance to reference (not just "first of type").
		materializeSyncEdges(resources, body.Edges)

		code, err := gen.Generate(resources)
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}

		// Pause watcher to avoid echo
		fw.Pause(projectPath)
		defer fw.Resume(projectPath)

		// Group resources by source file so we write back to original files.
		// Resources without a source file go to main.tf/main.yml.
		fileGroups := make(map[string][]parser.Resource)
		for _, r := range resources {
			target := r.File
			if target == "" {
				target = filepath.Join(syncWorkdir, "main"+ext)
			} else if env != "" && simpleRelativeFileName(target) {
				target = filepath.Join(syncWorkdir, target)
			}
			safeTarget, pathErr := safeProjectFile(projectPath, target, allowedExts...)
			if pathErr != nil {
				http.Error(w, "invalid resource file: "+pathErr.Error(), 400)
				return
			}
			fileGroups[safeTarget] = append(fileGroups[safeTarget], r)
		}

		// If all resources have no file origin, write to main file
		if len(fileGroups) == 0 {
			mainFile, pathErr := safeProjectFile(projectPath, filepath.Join(syncWorkdir, "main"+ext), allowedExts...)
			if pathErr != nil {
				http.Error(w, "invalid main file: "+pathErr.Error(), 400)
				return
			}
			fileGroups[mainFile] = resources
		}

		// Invalidate plan gate — code changed, previous plan is stale
		targets := make([]string, 0, len(fileGroups))
		for file := range fileGroups {
			targets = append(targets, file)
		}
		invalidatePlan(planInvalidationPaths(projectPath, targets...)...)

		// Read preserved blocks from existing files (variables, outputs, etc.)
		p := parser.ForTool(tool)
		preservedByFile := make(map[string][]parser.PreservedBlock)
		projectHasProvider := false
		if hclParser, ok := p.(*parser.HCLParser); ok && tool != "ansible" {
			existingFiles, _ := filepath.Glob(filepath.Join(syncWorkdir, "*.tf"))
			for _, f := range existingFiles {
				result, err := hclParser.ParseFileFull(f)
				if err != nil || result == nil {
					continue
				}
				absFile, pathErr := safeProjectFile(projectPath, f, allowedExts...)
				if pathErr != nil {
					continue
				}
				preservedByFile[absFile] = result.PreservedBlocks
				for _, b := range result.PreservedBlocks {
					if b.Type == "provider" {
						projectHasProvider = true
						break
					}
				}
			}
		}

		// Write each file atomically (temp file + rename)
		var mainCode string
		rootMainFile, pathErr := safeProjectFile(projectPath, filepath.Join(syncWorkdir, "main"+ext), allowedExts...)
		if pathErr != nil {
			http.Error(w, "invalid main file: "+pathErr.Error(), 400)
			return
		}
		for file, fileResources := range fileGroups {
			includeProviders := !projectHasProvider && file == rootMainFile
			fileCode, err := generateForSync(gen, fileResources, includeProviders)
			if err != nil {
				continue
			}

			// Prepend preserved blocks for this file
			if blocks := preservedByFile[file]; len(blocks) > 0 {
				var preserved strings.Builder
				for _, b := range blocks {
					preserved.WriteString(b.Content)
					preserved.WriteString("\n\n")
				}
				fileCode = preserved.String() + fileCode
			}

			// Atomic write: write to temp file, then rename
			tmpFile := file + ".tmp"
			if err := os.WriteFile(tmpFile, []byte(fileCode), 0644); err != nil {
				http.Error(w, err.Error(), 500)
				return
			}
			if err := os.Rename(tmpFile, file); err != nil {
				_ = os.Remove(tmpFile) // best-effort cleanup on failure
				http.Error(w, err.Error(), 500)
				return
			}

			if file == rootMainFile {
				mainCode = fileCode
			}
		}

		// If mainCode is empty, use the full generated code
		if mainCode == "" {
			mainCode = code
		}
		responseFile, relErr := filepath.Rel(projectPath, rootMainFile)
		if relErr != nil || responseFile == "." || strings.HasPrefix(responseFile, "..") {
			responseFile = filepath.Base(rootMainFile)
		}

		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"file": responseFile,
			"code": mainCode,
		})
	})

	cloudConnections := cloudconnections.NewManager(projectsDir)
	mcpAirlock := opts.MCPAirlock
	if mcpAirlock == nil {
		mcpAirlock = mcpairlock.NewManager(projectsDir)
	}

	// Run IaC command (init, plan, apply)
	mux.HandleFunc("POST /api/projects/{name}/run", func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		projectPath, err := safeProjectPath(projectsDir, name)
		if err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		projectRoot := projectPath

		limitBody(w, r)
		var req struct {
			Tool     string `json:"tool"`
			Command  string `json:"command"`  // init | plan | apply | check | playbook
			Approved bool   `json:"approved"` // must be true for apply/destroy
			// Acknowledged explicitly overrides the policy gate — the caller
			// is telling us they've read the findings and still want to
			// proceed. Logged server-side so the override is audit-trailable.
			Acknowledged bool `json:"acknowledged"`
			// RiskAcknowledged explicitly overrides the semantic plan risk
			// gate after the caller has reviewed risky, destructive, or
			// unknown changes. Kept separate from policy acknowledgement so
			// the audit trail can distinguish plan semantics from policy
			// violations.
			RiskAcknowledged bool `json:"risk_acknowledged"`
			// Env names the environment subdirectory to execute in for
			// layered-v1 projects (environments/<env>/...). Empty runs
			// commands at the project root (flat layout). Validated as a
			// safe path segment so a bad value can't traverse.
			Env          string `json:"env,omitempty"`
			ConnectionID string `json:"connection_id,omitempty"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid request", 400)
			return
		}

		effectiveTool := effectiveProjectTool(projectPath, req.Tool, req.Env)
		if effectiveTool == "multi" {
			http.Error(w, hybridToolResolutionMessage("env is required when running commands for hybrid projects", req.Env), 400)
			return
		}

		// When Env is set, run from environments/<env>
		// so the runner finds Pulumi.yaml / main.tf in the right
		// working directory. The subdir must exist and be contained
		// in projectPath — safeSubdir below rejects traversal and
		// rejects paths that point at a file instead of a directory.
		runPath := projectPath
		if req.Env != "" {
			subPath, subErr := safeSubdir(projectPath, "environments", req.Env)
			if subErr != nil {
				http.Error(w, "invalid env: "+subErr.Error(), 400)
				return
			}
			runPath = subPath
		}

		var commandEnv map[string]string
		var connectionSummary cloudconnections.PublicConnection
		if req.ConnectionID != "" {
			connection, connErr := cloudConnections.Get(req.ConnectionID)
			if connErr != nil {
				if errors.Is(connErr, os.ErrNotExist) {
					http.Error(w, "connection not found", 404)
					return
				}
				http.Error(w, connErr.Error(), 500)
				return
			}
			testResult, testErr := cloudConnections.Test(req.ConnectionID)
			if testErr != nil {
				http.Error(w, testErr.Error(), 500)
				return
			}
			if !testResult.OK {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusBadRequest)
				_ = json.NewEncoder(w).Encode(map[string]any{
					"error":  "connection_not_ready",
					"detail": testResult.Summary,
					"checks": testResult.Checks,
				})
				return
			}
			commandEnv = cloudconnections.CommandEnvironment(*connection)
			connectionSummary = testResult.Connection
		}

		// Block apply/destroy unless:
		// 1. A plan was run for this project within the last hour (server-verified)
		// 2. The client explicitly confirms (approved:true)
		// 3. No error-severity policy findings exist, OR the client sets
		//    acknowledged:true after reading the findings.
		if run.RequiresApproval(req.Command) {
			if !hasPlan(runPath) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusConflict)
				_ = json.NewEncoder(w).Encode(map[string]string{
					"error":  "plan_required",
					"detail": "run plan first — no plan has been run for this project recently",
				})
				return
			}
			if !req.Approved {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusConflict)
				_ = json.NewEncoder(w).Encode(map[string]string{
					"error":  "approval_required",
					"detail": "plan exists — re-submit with approved:true to proceed",
				})
				return
			}
			if planSupportsSemanticClassification(effectiveTool) {
				classification := classifySavedPlan(runPath)
				if req.Command == "destroy" {
					classification = iacplan.UnknownClassification("destroy does not consume the saved apply plan; review the destructive command separately")
				}
				if classification.Summary.RequiresAcknowledgment && !req.RiskAcknowledged {
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusConflict)
					_ = json.NewEncoder(w).Encode(map[string]any{
						"error":          "plan_risk_blocked",
						"detail":         "semantic plan classifier found risky, destructive, or unknown changes — re-submit with risk_acknowledged:true to proceed",
						"classification": classification,
					})
					return
				}
				if req.RiskAcknowledged {
					log.Printf("apply gate: semantic plan risk acknowledged by client for %s (command=%s tool=%s)", name, req.Command, effectiveTool)
				}
			}
			if !req.Acknowledged {
				// Walk every available engine against the project so we can
				// surface blocking findings before the apply runs. On any
				// error (engine crash, missing binary, malformed plan) we
				// fall through to execution — apply should not be gated by
				// a broken policy engine.
				if findings, blocking := evaluateBlockingPolicies(r.Context(), projectRoot, runPath, effectiveTool); blocking {
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusConflict)
					_ = json.NewEncoder(w).Encode(map[string]any{
						"error":    "policy_blocked",
						"detail":   "policy engine returned error-severity findings — re-submit with acknowledged:true to override",
						"findings": findings,
					})
					return
				}
			} else {
				log.Printf("apply gate: policy findings acknowledged by client for %s (command=%s tool=%s)", name, req.Command, effectiveTool)
			}
		}

		if commandProducesPlan(req.Command) {
			invalidatePlan(runPath)
			if planSupportsSemanticClassification(effectiveTool) {
				_ = os.Remove(filepath.Join(runPath, savedPlanJSONFile))
			}
		}

		// Execute in background. Use context.Background() — not r.Context() —
		// because the HTTP handler returns 202 immediately, which would cancel
		// a request-scoped context and kill the command. SafeRunner applies its
		// own per-command timeout (init=5m, plan=10m, apply=30m).
		go func() {
			result, err := run.ExecuteWithEnv(context.Background(), runPath, effectiveTool, req.Command, req.Env, commandEnv)
			var classification *iacplan.ClassificationResult
			var snapshot *recovery.StateSnapshot
			var snapshotErr error
			// Only record a successful plan — failed/cancelled plans don't count.
			// 'preview' is Pulumi's equivalent of terraform plan; without it
			// here, a pulumi up following a successful preview would be
			// blocked with 'plan_required'. runPath reflects any env rebase
			// so dev + prod track their plan state
			// independently.
			if err == nil && commandProducesPlan(req.Command) {
				if planSupportsSemanticClassification(effectiveTool) {
					exported, exportErr := run.ExportSavedPlanJSON(context.Background(), runPath, effectiveTool, commandEnv)
					if exportErr != nil {
						classification = iacplan.UnknownClassification("saved plan JSON export failed; rerun plan before applying")
						_ = os.Remove(filepath.Join(runPath, savedPlanJSONFile))
					} else if classified, classifyErr := iacplan.New().ClassifyFullPlan(exported.Output); classifyErr != nil {
						classification = iacplan.UnknownClassification("saved plan JSON could not be classified; rerun plan before applying")
						_ = os.Remove(filepath.Join(runPath, savedPlanJSONFile))
					} else if writeErr := os.WriteFile(filepath.Join(runPath, savedPlanJSONFile), []byte(exported.Output), 0600); writeErr != nil {
						classification = iacplan.UnknownClassification("saved plan JSON could not be written; rerun plan before applying")
						_ = os.Remove(filepath.Join(runPath, savedPlanJSONFile))
					} else {
						classification = classified
					}
					if result != nil {
						result.Output = appendPlanClassificationOutput(result.Output, classification)
					}
				}
				recordPlan(runPath)
			}
			if err == nil && commandRecordsSnapshot(req.Command) {
				recorded, recordErr := recovery.RecordSnapshot(projectRoot, runPath, recovery.SnapshotInput{
					Project: name,
					Tool:    effectiveTool,
					Env:     req.Env,
					Command: req.Command,
				}, time.Now().UTC())
				if recordErr != nil {
					snapshotErr = recordErr
					log.Printf("state snapshot: failed to record metadata for %s (command=%s tool=%s): %v", name, req.Command, effectiveTool, recordErr)
				} else {
					snapshot = &recorded
				}
			}
			msg := map[string]interface{}{
				"type":    "terminal",
				"project": name,
			}
			if classification != nil {
				msg["plan_classification"] = classification
			}
			if snapshot != nil {
				msg["state_snapshot"] = snapshot
			}
			if snapshotErr != nil {
				msg["state_snapshot_error"] = snapshotErr.Error()
			}
			if connectionSummary.ID != "" {
				msg["connection"] = map[string]string{
					"id":       connectionSummary.ID,
					"name":     connectionSummary.Name,
					"provider": connectionSummary.Provider,
				}
			}
			if result != nil {
				msg["output"] = result.Output
				msg["status"] = result.Status
				msg["duration"] = result.Duration.String()
			}
			if err != nil {
				msg["error"] = err.Error()
			}
			data, _ := json.Marshal(msg)
			hub.Broadcast(data)
		}()

		w.WriteHeader(http.StatusAccepted)
		response := map[string]any{"status": "running"}
		if connectionSummary.ID != "" {
			response["connection"] = connectionSummary
		}
		_ = json.NewEncoder(w).Encode(response)
	})

	// Kill a running command
	mux.HandleFunc("POST /api/projects/{name}/kill", func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		projectPath, err := safeProjectPath(projectsDir, name)
		if err != nil {
			http.Error(w, err.Error(), 400)
			return
		}

		// SafeRunner keys active executions by the exact workdir the
		// run handler passed in. When /run was invoked with env set,
		// that workdir was rebased to environments/<env> — so kill
		// must be able to rebase the same way to find the execution.
		// Env is optional on kill; an empty body still works for
		// project-root runs.
		limitBody(w, r)
		var req struct {
			Env string `json:"env,omitempty"`
		}
		// A missing body (EOF) is fine — kill defaults to the project
		// root. Any other decode failure is a client error; treating
		// it as "no env" would silently target the wrong execution.
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
			http.Error(w, "invalid request body: "+err.Error(), 400)
			return
		}
		if req.Env != "" {
			sub, subErr := safeSubdir(projectPath, "environments", req.Env)
			if subErr != nil {
				http.Error(w, "invalid env: "+subErr.Error(), 400)
				return
			}
			projectPath = sub
		}

		if err := run.Kill(projectPath); err != nil {
			http.Error(w, err.Error(), 404)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "cancelled"})
	})

	// AI chat
	mux.HandleFunc("POST /api/ai/chat", func(w http.ResponseWriter, r *http.Request) {
		limitBody(w, r)
		var req ai.ChatRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid request", 400)
			return
		}

		// Populate RAG context when a project is named + indexed. Failures
		// are swallowed — chat degrades to ungrounded rather than erroring.
		if req.Project != "" {
			if projectPath, err := safeProjectPath(projectsDir, req.Project); err == nil {
				req.ProjectContext = sharedRAG.Context(r.Context(), projectPath, req.Message, 5)
			}
		}

		response, resources, err := aiClient.GenerateIaC(r.Context(), req)
		if err != nil {
			log.Printf("AI unavailable, using pattern matching: %v", err)
			response, resources = ai.PatternMatch(req.Message, req.Tool, req.Provider)
		}

		// Also return suggestions for what to add next
		suggestions := ai.SuggestNext(req.Tool, req.Provider, req.Canvas)

		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"message":     response,
			"resources":   resources,
			"suggestions": suggestions,
		})
	})

	// Streaming AI chat via Server-Sent Events.
	// Event types emitted:
	//   - "delta"     — {text: "..."}        for every incremental chunk
	//   - "complete"  — {message, resources, suggestions}  on successful finish
	//   - "error"     — {error: "..."}       when the provider call fails
	// The non-streaming /api/ai/chat handler above is retained so older
	// clients keep working; new clients should prefer this endpoint.
	mux.HandleFunc("POST /api/ai/chat/stream", func(w http.ResponseWriter, r *http.Request) {
		limitBody(w, r)
		var req ai.ChatRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid request", 400)
			return
		}

		// Grounding on the project's own code — same degrade-silent path
		// as the non-streaming endpoint.
		if req.Project != "" {
			if projectPath, err := safeProjectPath(projectsDir, req.Project); err == nil {
				req.ProjectContext = sharedRAG.Context(r.Context(), projectPath, req.Message, 5)
			}
		}

		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "streaming unsupported by this server", 500)
			return
		}
		if err := http.NewResponseController(w).SetWriteDeadline(time.Time{}); err != nil && !errors.Is(err, http.ErrNotSupported) {
			http.Error(w, "failed to enable streaming", 500)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.Header().Set("X-Accel-Buffering", "no") // bypass nginx/proxy buffering
		w.WriteHeader(http.StatusOK)

		ctx, cancel := context.WithCancel(r.Context())
		defer cancel()

		writeEvent := func(event string, payload any) error {
			data, err := json.Marshal(payload)
			if err != nil {
				return err
			}
			if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, data); err != nil {
				return err
			}
			flusher.Flush()
			return nil
		}

		var streamErr error
		onDelta := func(chunk string) {
			if streamErr != nil {
				return
			}
			if err := writeEvent("delta", map[string]string{"text": chunk}); err != nil {
				streamErr = err
				cancel()
			}
		}

		response, resources, err := aiClient.StreamChat(ctx, req, onDelta)
		if streamErr != nil || errors.Is(ctx.Err(), context.Canceled) {
			return
		}
		if err != nil {
			// Notify clients that the provider stream failed using the
			// standard non-terminal error event so they can continue waiting
			// for the deterministic fallback completion below.
			// A write failure here just means the client already disconnected,
			// in which case the fallback below is wasted work but harmless.
			log.Printf("AI stream failed, falling back to pattern match: %v", err)
			_ = writeEvent("error", map[string]string{"error": err.Error()})

			// Fall back to deterministic pattern matching so users aren't
			// left hanging when the provider is unreachable, matching the
			// non-streaming handler's behaviour.
			response, resources = ai.PatternMatch(req.Message, req.Tool, req.Provider)
		}
		suggestions := ai.SuggestNext(req.Tool, req.Provider, req.Canvas)

		if err := writeEvent("complete", map[string]interface{}{
			"message":     response,
			"resources":   resources,
			"suggestions": suggestions,
		}); err != nil {
			cancel()
			return
		}
	})

	// Smart resource suggestions based on canvas state
	mux.HandleFunc("POST /api/ai/suggest", func(w http.ResponseWriter, r *http.Request) {
		limitBody(w, r)
		var req struct {
			Tool     string              `json:"tool"`
			Provider string              `json:"provider"`
			Canvas   []ai.CanvasResource `json:"canvas"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid request", 400)
			return
		}
		suggestions := ai.SuggestNext(req.Tool, req.Provider, req.Canvas)
		_ = json.NewEncoder(w).Encode(suggestions)
	})

	// Analyze plan/apply output and suggest fixes
	mux.HandleFunc("POST /api/ai/fix", func(w http.ResponseWriter, r *http.Request) {
		limitBody(w, r)
		var req ai.PlanFixRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid request", 400)
			return
		}

		fix, err := aiClient.AnalyzePlanOutput(r.Context(), req)
		if err != nil {
			log.Printf("AI unavailable for plan fix, using fallback: %v", err)
			fix = ai.AnalyzePlanFallback(req.Output, req.ExitCode)
		}

		_ = json.NewEncoder(w).Encode(fix)
	})

	// ─── Project State Persistence ───

	pm := project.NewManager(projectsDir)

	// Cloud connection broker - stores named cloud targets for later plan/apply,
	// drift, import, and MCP workflows. Route responses always use public views
	// so secret values are never echoed back to the browser.
	mux.HandleFunc("GET /api/cloud/auth-methods", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string][]string{
			"aws":   cloudconnections.SupportedAuthMethods(cloudconnections.ProviderAWS),
			"azure": cloudconnections.SupportedAuthMethods(cloudconnections.ProviderAzure),
			"gcp":   cloudconnections.SupportedAuthMethods(cloudconnections.ProviderGCP),
		})
	})

	mux.HandleFunc("GET /api/mcp-airlock/servers", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(mcpAirlock.List(r.Context()))
	})

	mux.HandleFunc("POST /api/mcp-airlock/servers/{id}/health", func(w http.ResponseWriter, r *http.Request) {
		status, err := mcpAirlock.Check(r.Context(), r.PathValue("id"))
		if err != nil {
			if errors.Is(err, mcpairlock.ErrUnknownServer) {
				http.Error(w, "mcp airlock server not found", http.StatusNotFound)
				return
			}
			http.Error(w, "mcp airlock health check failed", http.StatusInternalServerError)
			return
		}
		_ = json.NewEncoder(w).Encode(status)
	})

	mux.HandleFunc("POST /api/mcp-airlock/servers/{id}/start", func(w http.ResponseWriter, r *http.Request) {
		status, err := mcpAirlock.Start(r.Context(), r.PathValue("id"))
		if err != nil {
			if errors.Is(err, mcpairlock.ErrUnknownServer) {
				http.Error(w, "mcp airlock server not found", http.StatusNotFound)
				return
			}
			http.Error(w, "mcp airlock start failed", http.StatusInternalServerError)
			return
		}
		_ = json.NewEncoder(w).Encode(status)
	})

	mux.HandleFunc("POST /api/mcp-airlock/servers/{id}/stop", func(w http.ResponseWriter, r *http.Request) {
		status, err := mcpAirlock.Stop(r.Context(), r.PathValue("id"))
		if err != nil {
			if errors.Is(err, mcpairlock.ErrUnknownServer) {
				http.Error(w, "mcp airlock server not found", http.StatusNotFound)
				return
			}
			http.Error(w, "mcp airlock stop failed", http.StatusInternalServerError)
			return
		}
		_ = json.NewEncoder(w).Encode(status)
	})

	mux.HandleFunc("GET /api/cloud/connections", func(w http.ResponseWriter, _ *http.Request) {
		connections, err := cloudConnections.List()
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		_ = json.NewEncoder(w).Encode(connections)
	})

	mux.HandleFunc("POST /api/cloud/connections", func(w http.ResponseWriter, r *http.Request) {
		limitBody(w, r)
		var req cloudconnections.Connection
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid request", 400)
			return
		}
		req.ID = ""
		connection, err := cloudConnections.Save(req)
		if err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(connection)
	})

	mux.HandleFunc("PUT /api/cloud/connections/{id}", func(w http.ResponseWriter, r *http.Request) {
		limitBody(w, r)
		id := r.PathValue("id")
		if _, err := cloudConnections.Get(id); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				http.Error(w, "connection not found", 404)
				return
			}
			http.Error(w, err.Error(), 500)
			return
		}
		var req cloudconnections.Connection
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid request", 400)
			return
		}
		req.ID = id
		connection, err := cloudConnections.Save(req)
		if err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		_ = json.NewEncoder(w).Encode(connection)
	})

	mux.HandleFunc("DELETE /api/cloud/connections/{id}", func(w http.ResponseWriter, r *http.Request) {
		if err := cloudConnections.Delete(r.PathValue("id")); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				http.Error(w, "connection not found", 404)
				return
			}
			http.Error(w, err.Error(), 500)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "deleted"})
	})

	mux.HandleFunc("POST /api/cloud/connections/{id}/test", func(w http.ResponseWriter, r *http.Request) {
		result, err := cloudConnections.Test(r.PathValue("id"))
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				http.Error(w, "connection not found", 404)
				return
			}
			http.Error(w, err.Error(), 500)
			return
		}
		_ = json.NewEncoder(w).Encode(result)
	})

	// List all projects with their saved state
	mux.HandleFunc("GET /api/projects/states", func(w http.ResponseWriter, _ *http.Request) {
		states, err := pm.ListAll()
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		_ = json.NewEncoder(w).Encode(states)
	})

	// Load project state (canvas positions, edges, tool)
	mux.HandleFunc("GET /api/projects/{name}/state", func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		if _, err := safeProjectPath(projectsDir, name); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		state, err := pm.Load(name)
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		if state == nil {
			_ = json.NewEncoder(w).Encode(nil)
			return
		}
		_ = json.NewEncoder(w).Encode(state)
	})

	// Save project state
	mux.HandleFunc("PUT /api/projects/{name}/state", func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		projectPath, err := safeProjectPath(projectsDir, name)
		if err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		limitBody(w, r)
		var state project.State
		if err := json.NewDecoder(r.Body).Decode(&state); err != nil {
			http.Error(w, "invalid request", 400)
			return
		}
		state.Name = name
		state.Path = projectPath
		if err := pm.Save(name, &state); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "saved"})
	})

	// Open project directory in OS file manager
	mux.HandleFunc("POST /api/projects/{name}/reveal", func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		projectPath, err := safeProjectPath(projectsDir, name)
		if err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		if _, err := os.Stat(projectPath); os.IsNotExist(err) {
			http.Error(w, "project directory not found", 404)
			return
		}
		// Detect OS and open file manager
		var cmd *exec.Cmd
		switch runtime.GOOS {
		case "darwin":
			cmd = exec.Command("open", projectPath)
		case "windows":
			cmd = exec.Command("explorer", projectPath)
		default: // linux
			cmd = exec.Command("xdg-open", projectPath)
		}
		if err := cmd.Start(); err != nil {
			http.Error(w, fmt.Sprintf("failed to open: %v", err), 500)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "opened", "path": projectPath})
	})

	// Delete a project (removes directory and state)
	mux.HandleFunc("DELETE /api/projects/{name}", func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		projectPath, err := safeProjectPath(projectsDir, name)
		if err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		// Remove state from manager — best-effort, directory removal is the source of truth.
		_ = pm.Delete(name)
		// Remove the project directory
		if err := os.RemoveAll(projectPath); err != nil {
			http.Error(w, fmt.Sprintf("failed to delete: %v", err), 500)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "deleted", "name": name})
	})

	// ─── AI Settings ───

	// Get current AI provider config
	mux.HandleFunc("GET /api/ai/settings", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(aiClient.GetConfig())
	})

	isLikelyMaskedAPIKey := func(apiKey string) bool {
		return apiKey != "" && (strings.Contains(apiKey, "*") || strings.Contains(apiKey, "•"))
	}

	// Update AI provider config (supports Ollama, OpenAI-compatible, and Anthropic).
	// Type is validated explicitly so a user selecting "anthropic" in the UI
	// isn't silently downgraded to the OpenAI path just because they supplied
	// an API key.
	getConfiguredProviderAPIKey := func(kind providers.Kind, cfg ai.ProviderConfig) string {
		if apiKey := strings.TrimSpace(cfg.APIKey); apiKey != "" {
			return apiKey
		}
		switch kind {
		case providers.KindOpenAI:
			return strings.TrimSpace(os.Getenv("OPENAI_API_KEY"))
		case providers.KindAnthropic:
			return strings.TrimSpace(os.Getenv("ANTHROPIC_API_KEY"))
		default:
			return ""
		}
	}

	hasConfiguredProviderAPIKey := func(kind providers.Kind, cfg ai.ProviderConfig) bool {
		return getConfiguredProviderAPIKey(kind, cfg) != ""
	}

	resolveRequestedAPIKey := func(kind providers.Kind, submitted string, cfg ai.ProviderConfig) (string, error) {
		currentAPIKey := getConfiguredProviderAPIKey(kind, cfg)
		hasExistingAPIKey := hasConfiguredProviderAPIKey(kind, cfg)

		if submitted == "" {
			if hasExistingAPIKey {
				// Keep the existing configured key, regardless of whether it was
				// persisted directly in config or supplied via environment.
				return currentAPIKey, nil
			}
			return "", nil
		}

		if isLikelyMaskedAPIKey(submitted) {
			if hasExistingAPIKey {
				// Treat a masked placeholder as "no change".
				return currentAPIKey, nil
			}
			return "", errors.New("api key placeholder submitted; provide a new api key instead of the masked value")
		}

		return submitted, nil
	}

	mux.HandleFunc("PUT /api/ai/settings", func(w http.ResponseWriter, r *http.Request) {
		limitBody(w, r)
		var req ai.ProviderConfig
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid request", 400)
			return
		}
		req.Type = strings.TrimSpace(req.Type)
		if req.Type == "custom" {
			req.Type = string(providers.KindOpenAI)
		}
		req.Model = strings.TrimSpace(req.Model)
		req.Endpoint = strings.TrimSpace(req.Endpoint)
		req.APIKey = strings.TrimSpace(req.APIKey)

		currentCfg := aiClient.GetConfig()

		if req.Model == "" {
			http.Error(w, "model is required", 400)
			return
		}
		// Only providers with a known built-in public default may omit an
		// endpoint. Others must provide one explicitly.
		kind := providers.Kind(req.Type)
		if kind == "" {
			currentKind := providers.Kind(strings.TrimSpace(currentCfg.Type))
			if currentKind != "" {
				kind = currentKind
			} else if req.APIKey != "" {
				kind = providers.KindOpenAI
			} else {
				kind = providers.KindOllama
			}
		}

		resolvedAPIKey, err := resolveRequestedAPIKey(kind, req.APIKey, currentCfg)
		if err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		req.APIKey = resolvedAPIKey

		switch kind {
		case providers.KindOllama:
			if req.Endpoint == "" {
				http.Error(w, "endpoint is required for ollama", 400)
				return
			}
		case providers.KindOpenAI:
			if req.APIKey == "" && !hasConfiguredProviderAPIKey(kind, currentCfg) {
				http.Error(w, "api key is required for openai", 400)
				return
			}
			// endpoint optional — provider falls back to the public OpenAI default.
		case providers.KindAnthropic:
			if req.APIKey == "" && !hasConfiguredProviderAPIKey(kind, currentCfg) {
				http.Error(w, "api key is required for anthropic", 400)
				return
			}
			// endpoint optional — provider falls back to a public default.
		default:
			http.Error(w, "unsupported provider type: "+req.Type, 400)
			return
		}
		aiClient.UpdateConfigKind(kind, req.Endpoint, req.Model, req.APIKey)
		_ = json.NewEncoder(w).Encode(aiClient.GetConfig())
	})

	// ─── Import & Filesystem Browser ───

	// Browse local filesystem directories
	mux.HandleFunc("GET /api/browse", func(w http.ResponseWriter, r *http.Request) {
		dir := r.URL.Query().Get("path")
		if dir == "" {
			home, _ := os.UserHomeDir()
			dir = home
		}
		entries, err := importer.BrowseDir(dir)
		if err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		// Include parent path for navigation
		parent := filepath.Dir(dir)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"path":    dir,
			"parent":  parent,
			"entries": entries,
		})
	})

	// Scan and import an existing project directory
	mux.HandleFunc("POST /api/import", func(w http.ResponseWriter, r *http.Request) {
		limitBody(w, r)
		var req struct {
			Path string `json:"path"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid request", 400)
			return
		}
		if req.Path == "" {
			http.Error(w, "path is required", 400)
			return
		}

		project, err := importer.ScanProject(req.Path)
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}

		// Start watching the imported project directory
		_ = fw.Watch(req.Path)

		_ = json.NewEncoder(w).Encode(project)
	})

	// AI topology builder — runs async, sends progress via WebSocket, returns result via HTTP
	mux.HandleFunc("POST /api/ai/topology", func(w http.ResponseWriter, r *http.Request) {
		limitBody(w, r)
		var req ai.TopologyRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid request", 400)
			return
		}

		// Send immediate acknowledgment
		w.WriteHeader(http.StatusAccepted)
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "generating"})

		// Run AI generation in background, broadcast result via WebSocket
		go func() {
			// Send progress indicator
			progressMsg, _ := json.Marshal(map[string]string{
				"type":    "ai_progress",
				"status":  "generating",
				"message": "AI is designing your infrastructure...",
			})
			hub.Broadcast(progressMsg)

			msg, resources, err := aiClient.GenerateTopology(context.Background(), req)

			result := map[string]interface{}{
				"type": "ai_topology_result",
			}
			if err != nil {
				result["error"] = err.Error()
				result["message"] = fmt.Sprintf("Topology generation failed: %v", err)
			} else {
				result["message"] = msg
				result["resources"] = resources
			}
			data, _ := json.Marshal(result)
			hub.Broadcast(data)
		}()
	})

	// ─── Security Scanner ───

	secScanner := security.New()

	mux.HandleFunc("POST /api/projects/{name}/security", func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		tool := r.URL.Query().Get("tool")
		projectPath, err := safeProjectPath(projectsDir, name)
		if err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		p := parser.ForTool(tool)
		resources, err := p.ParseDir(projectPath)
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		report := secScanner.Scan(resources)
		_ = json.NewEncoder(w).Encode(report)
	})

	// Security scan from canvas resources (no project dir needed)
	mux.HandleFunc("POST /api/security/scan", func(w http.ResponseWriter, r *http.Request) {
		limitBody(w, r)
		var resources []parser.Resource
		if err := json.NewDecoder(r.Body).Decode(&resources); err != nil {
			http.Error(w, "invalid request", 400)
			return
		}
		report := secScanner.Scan(resources)
		_ = json.NewEncoder(w).Encode(report)
	})

	// ─── Recovery Snapshots ───

	mux.HandleFunc("GET /api/projects/{name}/snapshots", func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		projectPath, err := safeProjectPath(projectsDir, name)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		env := r.URL.Query().Get("env")
		if env != "" {
			if err := safePathSegment(env); err != nil {
				http.Error(w, "invalid env: "+err.Error(), http.StatusBadRequest)
				return
			}
		}
		snapshots, err := recovery.ListSnapshots(projectPath)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if env != "" {
			filtered := snapshots[:0]
			for _, snapshot := range snapshots {
				if snapshot.Env == env {
					filtered = append(filtered, snapshot)
				}
			}
			snapshots = filtered
		}
		_ = json.NewEncoder(w).Encode(snapshots)
	})

	mux.HandleFunc("POST /api/projects/{name}/snapshots/{snapshotID}/rollback", func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		snapshotID := r.PathValue("snapshotID")
		projectPath, err := safeProjectPath(projectsDir, name)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		limitBody(w, r)
		var req struct {
			Env string `json:"env,omitempty"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
			http.Error(w, "invalid request body: "+err.Error(), http.StatusBadRequest)
			return
		}
		if req.Env == "" {
			req.Env = r.URL.Query().Get("env")
		}
		proposal, status, message := buildProjectRollbackProposal(projectPath, name, snapshotID, req.Env)
		if status != http.StatusOK {
			http.Error(w, message, status)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(proposal)
	})

	mux.HandleFunc("POST /api/projects/{name}/snapshots/{snapshotID}/rollback/artifacts", func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		snapshotID := r.PathValue("snapshotID")
		projectPath, err := safeProjectPath(projectsDir, name)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		limitBody(w, r)
		var req struct {
			Env      string                     `json:"env,omitempty"`
			Proposal *recovery.RollbackProposal `json:"proposal,omitempty"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
			http.Error(w, "invalid request body: "+err.Error(), http.StatusBadRequest)
			return
		}
		if req.Env == "" {
			req.Env = r.URL.Query().Get("env")
		}

		proposal, status, message := buildProjectRollbackProposal(projectPath, name, snapshotID, req.Env)
		if status != http.StatusOK {
			http.Error(w, message, status)
			return
		}
		if req.Proposal != nil {
			if req.Proposal.TargetSnapshot.ID != snapshotID {
				http.Error(w, "request proposal must match snapshot id", http.StatusBadRequest)
				return
			}
			// The submitted proposal is only a stale-request guard. Artifacts
			// are always rendered from a fresh server-built proposal so clients
			// cannot weaken fail-closed classification or scrub warnings.
		}
		artifactSet, rendered, err := recovery.RenderRollbackArtifacts(proposal, time.Now().UTC())
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if err := writeRenderedRollbackArtifacts(projectPath, rendered); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(artifactSet)
	})

	mux.HandleFunc("POST /api/projects/{name}/snapshots/{snapshotID}/rollback/pr", func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		snapshotID := r.PathValue("snapshotID")
		projectPath, err := safeProjectPath(projectsDir, name)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		limitBody(w, r)
		var req struct {
			Env      string                     `json:"env,omitempty"`
			Proposal *recovery.RollbackProposal `json:"proposal,omitempty"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
			http.Error(w, "invalid request body: "+err.Error(), http.StatusBadRequest)
			return
		}
		if req.Env == "" {
			req.Env = r.URL.Query().Get("env")
		}

		proposal, status, message := buildProjectRollbackProposal(projectPath, name, snapshotID, req.Env)
		if status != http.StatusOK {
			http.Error(w, message, status)
			return
		}
		if req.Proposal != nil && req.Proposal.TargetSnapshot.ID != snapshotID {
			http.Error(w, "request proposal must match snapshot id", http.StatusBadRequest)
			return
		}
		artifactSet, rendered, err := recovery.RenderRollbackArtifacts(proposal, time.Now().UTC())
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		rollbackFiles := rollbackArtifactPaths(rendered)
		if err := review.EnsureNoUnrelatedChanges(projectPath, rollbackFiles); err != nil {
			http.Error(w, err.Error(), reviewHandoffStatus(err))
			return
		}
		if err := writeRenderedRollbackArtifacts(projectPath, rendered); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		handoff, err := review.CreatePullRequestHandoff(review.PullRequestHandoffInput{
			ProjectPath:   projectPath,
			Title:         proposal.Title,
			Branch:        proposal.Branch,
			CommitMessage: proposal.CommitMessage,
			BodyPath:      rollbackArtifactBodyPath(rendered),
			Files:         rollbackFiles,
		})
		if err != nil {
			http.Error(w, err.Error(), reviewHandoffStatus(err))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(struct {
			Artifacts   recovery.RollbackArtifactSet `json:"artifacts"`
			PullRequest review.PullRequestHandoff    `json:"pull_request"`
		}{
			Artifacts:   artifactSet,
			PullRequest: handoff,
		})
	})

	// ─── Drift Detection ───

	driftDetector := drift.New()

	handleDrift := func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")

		limitBody(w, r)
		var req struct {
			Tool         string `json:"tool,omitempty"`
			Env          string `json:"env,omitempty"`
			ConnectionID string `json:"connection_id,omitempty"`
		}
		if r.Method == http.MethodPost {
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
				http.Error(w, "invalid request body: "+err.Error(), 400)
				return
			}
		}
		if req.Tool == "" {
			req.Tool = r.URL.Query().Get("tool")
		}
		if req.Env == "" {
			req.Env = r.URL.Query().Get("env")
		}
		if req.ConnectionID == "" {
			req.ConnectionID = r.URL.Query().Get("connection_id")
		}

		run, status, message := runProjectDrift(projectsDir, name, projectDriftRequest{
			Tool:         req.Tool,
			Env:          req.Env,
			ConnectionID: req.ConnectionID,
		}, driftDetector, cloudConnections)
		if run == nil {
			http.Error(w, message, status)
			return
		}
		_ = json.NewEncoder(w).Encode(run.Report)
	}
	mux.HandleFunc("GET /api/projects/{name}/drift", handleDrift)
	mux.HandleFunc("POST /api/projects/{name}/drift", handleDrift)

	mux.HandleFunc("POST /api/projects/{name}/drift/remediation", func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		limitBody(w, r)
		var req struct {
			Tool         string `json:"tool,omitempty"`
			Env          string `json:"env,omitempty"`
			ConnectionID string `json:"connection_id,omitempty"`
			Mode         string `json:"mode"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
			http.Error(w, "invalid request body: "+err.Error(), 400)
			return
		}
		if req.Tool == "" {
			req.Tool = r.URL.Query().Get("tool")
		}
		if req.Env == "" {
			req.Env = r.URL.Query().Get("env")
		}
		if req.ConnectionID == "" {
			req.ConnectionID = r.URL.Query().Get("connection_id")
		}

		run, status, message := runProjectDrift(projectsDir, name, projectDriftRequest{
			Tool:         req.Tool,
			Env:          req.Env,
			ConnectionID: req.ConnectionID,
		}, driftDetector, cloudConnections)
		if run == nil {
			http.Error(w, message, status)
			return
		}
		proposal, err := buildProjectDriftRemediationProposal(name, req.Mode, run)
		if err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(proposal)
	})

	mux.HandleFunc("POST /api/projects/{name}/drift/remediation/artifacts", func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		limitBody(w, r)
		var req struct {
			Tool         string                     `json:"tool,omitempty"`
			Env          string                     `json:"env,omitempty"`
			ConnectionID string                     `json:"connection_id,omitempty"`
			Mode         string                     `json:"mode"`
			Proposal     *drift.RemediationProposal `json:"proposal,omitempty"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
			http.Error(w, "invalid request body: "+err.Error(), 400)
			return
		}
		if req.Tool == "" {
			req.Tool = r.URL.Query().Get("tool")
		}
		if req.Env == "" {
			req.Env = r.URL.Query().Get("env")
		}
		if req.ConnectionID == "" {
			req.ConnectionID = r.URL.Query().Get("connection_id")
		}

		projectPath, err := safeProjectPath(projectsDir, name)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		mode := req.Mode
		if req.Proposal != nil {
			if mode != "" && mode != req.Proposal.Mode {
				http.Error(w, "request mode must match proposal mode", http.StatusBadRequest)
				return
			}
			mode = req.Proposal.Mode
		}
		run, status, message := runProjectDrift(projectsDir, name, projectDriftRequest{
			Tool:         req.Tool,
			Env:          req.Env,
			ConnectionID: req.ConnectionID,
		}, driftDetector, cloudConnections)
		if run == nil {
			http.Error(w, message, status)
			return
		}
		proposal, err := buildProjectDriftRemediationProposal(name, mode, run)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		artifactSet, rendered, err := drift.RenderRemediationArtifacts(proposal, time.Now().UTC())
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if err := writeRenderedRemediationArtifacts(projectPath, rendered); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(artifactSet)
	})

	mux.HandleFunc("POST /api/projects/{name}/drift/remediation/pr", func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		limitBody(w, r)
		var req struct {
			Tool         string                     `json:"tool,omitempty"`
			Env          string                     `json:"env,omitempty"`
			ConnectionID string                     `json:"connection_id,omitempty"`
			Mode         string                     `json:"mode"`
			Proposal     *drift.RemediationProposal `json:"proposal,omitempty"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
			http.Error(w, "invalid request body: "+err.Error(), http.StatusBadRequest)
			return
		}
		if req.Tool == "" {
			req.Tool = r.URL.Query().Get("tool")
		}
		if req.Env == "" {
			req.Env = r.URL.Query().Get("env")
		}
		if req.ConnectionID == "" {
			req.ConnectionID = r.URL.Query().Get("connection_id")
		}

		projectPath, err := safeProjectPath(projectsDir, name)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		mode := req.Mode
		if req.Proposal != nil {
			if mode != "" && mode != req.Proposal.Mode {
				http.Error(w, "request mode must match proposal mode", http.StatusBadRequest)
				return
			}
			mode = req.Proposal.Mode
		}
		run, status, message := runProjectDrift(projectsDir, name, projectDriftRequest{
			Tool:         req.Tool,
			Env:          req.Env,
			ConnectionID: req.ConnectionID,
		}, driftDetector, cloudConnections)
		if run == nil {
			http.Error(w, message, status)
			return
		}
		proposal, err := buildProjectDriftRemediationProposal(name, mode, run)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		artifactSet, rendered, err := drift.RenderRemediationArtifacts(proposal, time.Now().UTC())
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		remediationFiles := remediationArtifactPaths(rendered)
		if err := review.EnsureNoUnrelatedChanges(projectPath, remediationFiles); err != nil {
			http.Error(w, err.Error(), reviewHandoffStatus(err))
			return
		}
		if err := writeRenderedRemediationArtifacts(projectPath, rendered); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		handoff, err := review.CreatePullRequestHandoff(review.PullRequestHandoffInput{
			ProjectPath:   projectPath,
			Title:         proposal.Title,
			Branch:        proposal.Branch,
			CommitMessage: proposal.CommitMessage,
			BodyPath:      remediationArtifactBodyPath(rendered),
			Files:         remediationFiles,
		})
		if err != nil {
			http.Error(w, err.Error(), reviewHandoffStatus(err))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(struct {
			Artifacts   drift.RemediationArtifactSet `json:"artifacts"`
			PullRequest review.PullRequestHandoff    `json:"pull_request"`
		}{
			Artifacts:   artifactSet,
			PullRequest: handoff,
		})
	})

	// ─── Multi-Format Export ───

	exp := exporter.New()

	mux.HandleFunc("GET /api/export/formats", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(exp.SupportedFormats())
	})

	mux.HandleFunc("POST /api/export", func(w http.ResponseWriter, r *http.Request) {
		limitBody(w, r)
		var req struct {
			Format    string            `json:"format"`
			Resources []parser.Resource `json:"resources"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid request", 400)
			return
		}
		result, err := exp.Export(req.Format, req.Resources)
		if err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		_ = json.NewEncoder(w).Encode(result)
	})

	// WebSocket for live sync
	mux.HandleFunc("GET /ws", func(w http.ResponseWriter, r *http.Request) {
		ServeWS(hub, w, r)
	})

	// Policy engines — builtin + OPA (embedded) + Conftest + Sentinel (shell-out).
	registerPolicyRoutes(mux, projectsDir)

	// Semantic plan classifier — turns Terraform/OpenTofu plan JSON into
	// safe/risky/destructive/unknown reviewer summaries.
	registerPlanClassificationRoutes(mux, projectsDir)

	// Security scanner plugins — graph + Checkov + Trivy + Terrascan + KICS.
	registerScannerRoutes(mux, projectsDir)

	// Terraform modules — introspect local modules + proxy the registry.
	regClient := registry.New(registry.Config{})
	registerModuleRoutes(mux, projectsDir, regClient)

	// AI agent — tool-use orchestrator that drives list_resources, run_policy,
	// run_scan, write_hcl, etc. against the configured Anthropic provider.
	registerAgentRoutes(mux, projectsDir, aiClient, regClient)

	// RAG — build & query a per-project embedding index so chat / topology
	// / fix responses are grounded on the project's own code instead of
	// generic best-practice knowledge.
	registerRAGRoutes(mux, projectsDir, aiClient)

	// Vision — diagram-to-topology endpoint; multimodal provider call
	// through Anthropic's image content blocks.
	registerVisionRoutes(mux, aiClient)

	return mux
}

// allowedOrigins is populated at startup from the server's actual bind address.
var allowedOrigins = struct {
	mu   sync.RWMutex
	list map[string]bool
}{list: map[string]bool{}}

// InitAllowedOrigins builds the origin allowlist from the server's host and port.
// Called once at startup so the list matches the actual deployment.
func InitAllowedOrigins(host string, port int) {
	serverPort := fmt.Sprintf("%d", port)
	isWildcardBind := host == "0.0.0.0" || host == "::" || host == ""
	origins := map[string]bool{}

	// Always allow localhost variants
	for _, h := range []string{"localhost", "127.0.0.1"} {
		origins[localHTTPOrigin(h, serverPort)] = true
	}
	// If binding a specific host, allow that too
	if !isWildcardBind {
		origins[localHTTPOrigin(host, serverPort)] = true
	}
	// Also allow the Vite dev server (port 5173) for development
	for _, h := range []string{"localhost", "127.0.0.1"} {
		origins[localHTTPOrigin(h, "5173")] = true
	}

	allowedOrigins.mu.Lock()
	allowedOrigins.list = origins
	allowedOrigins.mu.Unlock()
}

func localHTTPOrigin(host, port string) string {
	host = strings.TrimPrefix(strings.TrimSuffix(host, "]"), "[")
	return "http://" + net.JoinHostPort(host, port)
}

// IsAllowedOrigin checks whether an origin is in the allowlist.
func IsAllowedOrigin(origin string) bool {
	allowedOrigins.mu.RLock()
	defer allowedOrigins.mu.RUnlock()
	return allowedOrigins.list[origin]
}

// CORS restricts cross-origin requests to the localhost allowlist.
func CORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin != "" {
			w.Header().Set("Vary", "Origin")
			if !IsAllowedOrigin(origin) {
				http.Error(w, "origin not allowed", http.StatusForbidden)
				return
			}
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		}
		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}
		next.ServeHTTP(w, r)
	})
}
