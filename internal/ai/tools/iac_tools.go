package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/iac-studio/iac-studio/internal/parser"
	"github.com/iac-studio/iac-studio/internal/policy/engines"
	"github.com/iac-studio/iac-studio/internal/registry"
	"github.com/iac-studio/iac-studio/internal/security/scanners"
)

// IaCToolDeps bundles everything the IaC tools need to do real work —
// passed once at registration time so individual handlers stay simple and
// test-friendly (tests inject fakes via this struct).
type IaCToolDeps struct {
	// ProjectDir is the absolute path the agent is operating on. Every
	// tool call is scoped here — handlers never reach outside this
	// directory so a rogue model prompt can't traverse to other projects.
	ProjectDir string
	// PolicyEngines is the set of engines run by run_policy. Pass the
	// same slice registerPolicyRoutes already uses so the agent sees the
	// same results the /api/policy/run endpoint does.
	PolicyEngines []engines.PolicyEngine
	// Scanners is the set of security scanners run by run_scan, same
	// shape as PolicyEngines — one registry shared across HTTP and agent.
	Scanners []scanners.Scanner
	// RegistryClient is used by search_registry.
	RegistryClient *registry.Client
}

// RegisterIaCTools adds every built-in tool to reg. Pass the same Registry
// to Runner so the agent can actually call them.
func RegisterIaCTools(reg *Registry, deps IaCToolDeps) {
	reg.Register(newListResourcesTool(deps))
	reg.Register(newGetResourceTool(deps))
	reg.Register(newWriteHCLTool(deps))
	reg.Register(newRunPolicyTool(deps))
	reg.Register(newRunScanTool(deps))
	reg.Register(newSearchRegistryTool(deps))
	reg.Register(newReadPlanTool(deps))
}

// ─── list_resources ────────────────────────────────────────────────────

type listResourcesArgs struct {
	// Type optionally filters to a single resource type (e.g. "aws_s3_bucket").
	Type string `json:"type,omitempty"`
}

type listResourcesResult struct {
	Resources []listedResource `json:"resources"`
}

type listedResource struct {
	ID   string `json:"id"`
	Type string `json:"type"`
	Name string `json:"name"`
	File string `json:"file"`
}

func newListResourcesTool(deps IaCToolDeps) Tool {
	return New(
		"list_resources",
		"List every Terraform resource in the project, optionally filtered by type. Use this before calling write_hcl so you don't duplicate existing resources.",
		`{
  "type": "object",
  "properties": {
    "type": {"type": "string", "description": "Optional resource type filter, e.g. aws_s3_bucket"}
  }
}`,
		func(ctx context.Context, raw json.RawMessage) (any, error) {
			var args listResourcesArgs
			if len(raw) > 0 {
				if err := json.Unmarshal(raw, &args); err != nil {
					return nil, fmt.Errorf("list_resources: %w", err)
				}
			}
			p := &parser.HCLParser{}
			all, err := p.ParseDir(deps.ProjectDir)
			if err != nil {
				return nil, err
			}
			out := listResourcesResult{Resources: []listedResource{}}
			for _, r := range all {
				if r.BlockType != "resource" {
					continue
				}
				if args.Type != "" && r.Type != args.Type {
					continue
				}
				scrubbed := scrubResource(r, deps.ProjectDir)
				out.Resources = append(out.Resources, listedResource{
					ID: r.ID, Type: r.Type, Name: r.Name, File: scrubbed.File,
				})
			}
			return out, nil
		},
	)
}

// ─── get_resource ──────────────────────────────────────────────────────

type getResourceArgs struct {
	ID string `json:"id"` // "aws_s3_bucket.data"
}

func newGetResourceTool(deps IaCToolDeps) Tool {
	return New(
		"get_resource",
		"Fetch the full definition of a single resource by ID (e.g. \"aws_s3_bucket.data\"). Use this to read current property values before proposing a change.",
		`{
  "type": "object",
  "required": ["id"],
  "properties": {
    "id": {"type": "string", "description": "Resource ID, e.g. aws_s3_bucket.data"}
  }
}`,
		func(ctx context.Context, raw json.RawMessage) (any, error) {
			var args getResourceArgs
			if err := json.Unmarshal(raw, &args); err != nil {
				return nil, fmt.Errorf("get_resource: %w", err)
			}
			if args.ID == "" {
				return nil, fmt.Errorf("get_resource: id is required")
			}
			p := &parser.HCLParser{}
			all, err := p.ParseDir(deps.ProjectDir)
			if err != nil {
				return nil, err
			}
			for _, r := range all {
				if r.ID == args.ID {
					return scrubResource(r, deps.ProjectDir), nil
				}
			}
			return nil, fmt.Errorf("resource not found: %s", args.ID)
		},
	)
}

// ─── write_hcl ─────────────────────────────────────────────────────────

type writeHCLArgs struct {
	// RelPath is the target file, relative to the project root. Must stay
	// inside the project — absolute paths and ../ escapes are refused.
	RelPath string `json:"rel_path"`
	// Content is the full file body to write. Agents tend to hand back
	// whole files rather than patches; we accept that and do a safe
	// atomic replace.
	Content string `json:"content"`
}

type writeHCLResult struct {
	Path    string `json:"path"`
	Bytes   int    `json:"bytes"`
	Message string `json:"message"`
}

func newWriteHCLTool(deps IaCToolDeps) Tool {
	return New(
		"write_hcl",
		"Write a Terraform file under the project. rel_path must stay inside the project root (no absolute paths, no ../). Content replaces the file atomically; use list_resources first to read current state.",
		`{
  "type": "object",
  "required": ["rel_path", "content"],
  "properties": {
    "rel_path": {"type": "string", "description": "Target .tf file relative to project root"},
    "content":  {"type": "string", "description": "Full file contents"}
  }
}`,
		func(ctx context.Context, raw json.RawMessage) (any, error) {
			var args writeHCLArgs
			if err := json.Unmarshal(raw, &args); err != nil {
				return nil, fmt.Errorf("write_hcl: %w", err)
			}
			// Empty content is a legitimate outcome (clearing a file /
			// creating a placeholder), so only rel_path is required.
			if args.RelPath == "" {
				return nil, fmt.Errorf("write_hcl: rel_path is required")
			}
			full, err := scopePathWithinProject(deps.ProjectDir, args.RelPath)
			if err != nil {
				return nil, err
			}
			if !strings.HasSuffix(full, ".tf") {
				return nil, fmt.Errorf("write_hcl: rel_path must end in .tf")
			}
			if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
				return nil, err
			}
			// Before touching disk, walk each existing path component and
			// refuse if any is a symlink. Containment via scopePathWithinProject
			// is lexical; this is the runtime check that keeps a pre-existing
			// in-project symlink from redirecting the write outside the root.
			if err := refuseSymlinkOnPath(deps.ProjectDir, full); err != nil {
				return nil, err
			}
			// Atomic write: CreateTemp in the same dir → write → rename.
			// A unique temp filename lets concurrent writes to the same
			// target coexist without clobbering each other's .tmp file.
			dir := filepath.Dir(full)
			base := filepath.Base(full)
			tmpFile, err := os.CreateTemp(dir, base+".*.tmp")
			if err != nil {
				return nil, err
			}
			tmp := tmpFile.Name()
			// Remove the temp file unconditionally on exit — a successful
			// Rename makes the Remove a no-op (file no longer at tmp path).
			defer func() { _ = os.Remove(tmp) }()
			if _, err := tmpFile.Write([]byte(args.Content)); err != nil {
				_ = tmpFile.Close()
				return nil, err
			}
			if err := tmpFile.Chmod(0o644); err != nil {
				_ = tmpFile.Close()
				return nil, err
			}
			if err := tmpFile.Close(); err != nil {
				return nil, err
			}
			if err := os.Rename(tmp, full); err != nil {
				return nil, err
			}
			return writeHCLResult{
				Path:    args.RelPath,
				Bytes:   len(args.Content),
				Message: fmt.Sprintf("wrote %d bytes to %s", len(args.Content), args.RelPath),
			}, nil
		},
	)
}

// ─── run_policy ────────────────────────────────────────────────────────

func newRunPolicyTool(deps IaCToolDeps) Tool {
	return New(
		"run_policy",
		"Evaluate every configured policy engine (builtin, OPA, Conftest, Sentinel) against the current project. Returns findings grouped by engine and a blocking flag when any error-severity finding exists.",
		`{"type": "object", "properties": {}}`,
		func(ctx context.Context, raw json.RawMessage) (any, error) {
			resources := parseResources(deps.ProjectDir)
			// Read tfplan.json only if it's a regular file — a symlink
			// could be used to exfiltrate arbitrary host files via the
			// next tool_result sent to the LLM. Missing file stays
			// silent so plan-less engines like the builtin still run.
			planJSON, err := readRegularFile(filepath.Join(deps.ProjectDir, "tfplan.json"))
			if err != nil && !os.IsNotExist(err) {
				return nil, err
			}
			results := engines.RunAll(ctx, deps.PolicyEngines, engines.EvalInput{
				ProjectDir: deps.ProjectDir,
				Resources:  resources,
				PlanJSON:   planJSON,
			})
			findings := engines.MergeFindings(results)
			blocking := false
			for _, f := range findings {
				if f.Severity.IsBlocking() {
					blocking = true
					break
				}
			}
			return map[string]any{
				"results":  results,
				"findings": findings,
				"blocking": blocking,
			}, nil
		},
	)
}

// ─── run_scan ──────────────────────────────────────────────────────────

func newRunScanTool(deps IaCToolDeps) Tool {
	return New(
		"run_scan",
		"Run every configured security scanner (graph, Checkov, Trivy, Terrascan, KICS) against the current project. Returns a merged findings feed sorted severity-first.",
		`{"type": "object", "properties": {}}`,
		func(ctx context.Context, raw json.RawMessage) (any, error) {
			resources := parseResources(deps.ProjectDir)
			results := scanners.RunAll(ctx, deps.Scanners, scanners.ScanInput{
				ProjectDir: deps.ProjectDir,
				Resources:  resources,
				Tool:       "terraform",
			})
			findings := scanners.MergeFindings(results)
			blocking := false
			for _, f := range findings {
				if scanners.IsBlocking(f.Severity) {
					blocking = true
					break
				}
			}
			return map[string]any{
				"results":  results,
				"findings": findings,
				"blocking": blocking,
			}, nil
		},
	)
}

// ─── search_registry ───────────────────────────────────────────────────

type searchRegistryArgs struct {
	Query string `json:"query"`
	Limit int    `json:"limit,omitempty"`
}

func newSearchRegistryTool(deps IaCToolDeps) Tool {
	return New(
		"search_registry",
		"Search the public Terraform Registry for modules matching a free-text query. Returns up to `limit` modules (default 10, max 100). Use before proposing a new module block so you pin a known-good source.",
		`{
  "type": "object",
  "required": ["query"],
  "properties": {
    "query": {"type": "string"},
    "limit": {"type": "integer", "minimum": 1, "maximum": 100}
  }
}`,
		func(ctx context.Context, raw json.RawMessage) (any, error) {
			var args searchRegistryArgs
			if err := json.Unmarshal(raw, &args); err != nil {
				return nil, fmt.Errorf("search_registry: %w", err)
			}
			// Models occasionally forget required fields even when the
			// schema marks them required — surface an explicit error
			// rather than forwarding an empty query upstream.
			if strings.TrimSpace(args.Query) == "" {
				return nil, fmt.Errorf("search_registry: query is required")
			}
			if deps.RegistryClient == nil {
				return nil, fmt.Errorf("search_registry: no registry client configured")
			}
			limit := args.Limit
			if limit <= 0 {
				limit = 10
			}
			if limit > 100 {
				limit = 100
			}
			return deps.RegistryClient.Search(ctx, args.Query, limit)
		},
	)
}

// ─── read_plan ─────────────────────────────────────────────────────────

func newReadPlanTool(deps IaCToolDeps) Tool {
	return New(
		"read_plan",
		"Read the project's current tfplan.json (produced by `terraform show -json tfplan`). Returns the raw JSON so downstream tools or the model can reason about planned changes. Errors clearly when no plan has been run yet.",
		`{"type": "object", "properties": {}}`,
		func(ctx context.Context, raw json.RawMessage) (any, error) {
			data, err := readRegularFile(filepath.Join(deps.ProjectDir, "tfplan.json"))
			if err != nil {
				if os.IsNotExist(err) {
					return nil, fmt.Errorf("no tfplan.json — run terraform plan + `terraform show -json tfplan > tfplan.json` first")
				}
				return nil, err
			}
			var out any
			if err := json.Unmarshal(data, &out); err != nil {
				return nil, fmt.Errorf("tfplan.json is not valid JSON: %w", err)
			}
			return out, nil
		},
	)
}

// ─── shared helpers ────────────────────────────────────────────────────

// readRegularFile reads path only if it's a regular file — not a symlink,
// not a device, not a fifo. Any of those could otherwise be used to
// exfiltrate arbitrary host content through a tool_result; Lstat + mode
// check is the cheapest runtime safeguard.
func readRegularFile(path string) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("refusing to follow symlink: %s", path)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("not a regular file: %s", path)
	}
	return os.ReadFile(path)
}

// scrubResource returns a copy of r with the File field rewritten to the
// project-relative form, so tool_results sent to the LLM don't leak the
// host's absolute filesystem layout.
func scrubResource(r parser.Resource, projectDir string) parser.Resource {
	out := r
	if r.File == "" {
		return out
	}
	if rel, err := filepath.Rel(projectDir, r.File); err == nil && !strings.HasPrefix(rel, "..") {
		out.File = rel
	} else {
		// Couldn't relativize cleanly → drop the path entirely rather
		// than leak the host layout.
		out.File = filepath.Base(r.File)
	}
	return out
}

// parseResources is a best-effort parse used by tools that don't require
// perfect fidelity — ignores errors and returns whatever parsed cleanly so
// a single bad .tf file doesn't blank the whole tool invocation.
func parseResources(projectDir string) []parser.Resource {
	p := &parser.HCLParser{}
	resources, _ := p.ParseDir(projectDir)
	return resources
}

// refuseSymlinkOnPath walks each existing path component from projectDir
// toward full and returns an error if any of them is a symlink. Used as
// a runtime complement to scopePathWithinProject's lexical check — a
// malicious or accidental symlink placed inside the project directory
// would otherwise redirect a follow-up Write through it.
//
// Non-existing components (the target file and any unmaterialised parent
// dirs created by MkdirAll) aren't checked — os.Lstat returns IsNotExist
// for them and we stop. The parents that DO exist at the time of write
// are the only ones that can redirect the write.
func refuseSymlinkOnPath(projectDir, full string) error {
	// Canonicalize the project root so the walk stays inside it — macOS's
	// /var → /private/var symlink would otherwise trigger on the very
	// first segment when full was itself built under the canonical form
	// by scopePathWithinProject.
	canon, err := filepath.EvalSymlinks(projectDir)
	if err != nil {
		canon = projectDir
	}
	rel, err := filepath.Rel(canon, full)
	if err != nil {
		return err
	}
	segments := strings.Split(rel, string(filepath.Separator))
	cur := canon
	// Walk intermediate dirs only — the final element is the file we're
	// about to write and it may not exist yet.
	for _, seg := range segments[:len(segments)-1] {
		cur = filepath.Join(cur, seg)
		info, err := os.Lstat(cur)
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return fmt.Errorf("lstat %s: %w", cur, err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("refusing to follow symlink: %s", cur)
		}
	}
	return nil
}

// scopePathWithinProject rejects absolute paths and any relative path that
// resolves outside the project root. This is the single choke point for
// write operations — every tool that touches disk must use it so a model
// that's been prompt-injected can't cause writes outside the project.
//
// Containment is checked lexically (filepath.Clean + ".." prefix) AND
// canonically (EvalSymlinks on the project root, then build the target
// under that canonical path). Building absFull from the canonicalised
// root avoids the macOS /var → /private/var mismatch that would otherwise
// make any target look like it "escapes" the root when the parent of the
// target doesn't exist yet (can't EvalSymlinks a path that isn't on disk).
func scopePathWithinProject(projectDir, rel string) (string, error) {
	if filepath.IsAbs(rel) {
		return "", fmt.Errorf("path must be relative: %q", rel)
	}
	cleaned := filepath.Clean(rel)
	if cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path escapes project root: %q", rel)
	}
	absRoot, err := filepath.Abs(projectDir)
	if err != nil {
		return "", err
	}
	if eval, err := filepath.EvalSymlinks(absRoot); err == nil {
		absRoot = eval
	}
	// Build the target under the canonical root rather than joining against
	// the caller-supplied projectDir — keeps both sides of the containment
	// check on the same symlink side of the filesystem.
	absFull := filepath.Join(absRoot, cleaned)
	rel2, err := filepath.Rel(absRoot, absFull)
	if err != nil {
		return "", err
	}
	if rel2 == ".." || strings.HasPrefix(rel2, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path escapes project root: %q", rel)
	}
	return absFull, nil
}
