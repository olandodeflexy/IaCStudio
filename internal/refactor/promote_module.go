// Package refactor hosts higher-level code transformations — the things
// users invoke by clicking a button rather than typing HCL. Today that's
// just "promote selection to module"; future refactorings (rename,
// extract variable, inline module) live here too.
package refactor

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/iac-studio/iac-studio/internal/generator"
	"github.com/iac-studio/iac-studio/internal/parser"
)

// PromoteRequest is the input to PromoteToModule. ResourceIDs is the set of
// resources the user selected; the refactor extracts those into a new
// sibling module named ModuleName and rewrites the root to call it.
type PromoteRequest struct {
	// ProjectDir is the absolute path to the project root.
	ProjectDir string
	// ModuleName is the new module's directory name AND HCL block label.
	// Must be a valid Terraform identifier — lowercase letters, digits,
	// and underscores.
	ModuleName string
	// ResourceIDs identifies which parsed resources to pull out of the
	// root and into the new module. Use Resource.ID (e.g. "aws_vpc.main").
	ResourceIDs []string
}

// PromoteResult reports what the refactor did — useful for the UI's
// "extracted N resources into module X, exposed M outputs" toast.
type PromoteResult struct {
	ModulePath       string   `json:"module_path"`        // new <project>/modules/<name>/ directory
	ResourcesMoved   []string `json:"resources_moved"`
	VariablesCreated []string `json:"variables_created"`  // one per distinct user-exposed input
	OutputsCreated   []string `json:"outputs_created"`    // one per resource we extracted
}

var identifierRE = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

// PromoteToModule extracts the requested resources into a new module under
// <project>/modules/<name>/ and writes a module call into the project
// root's main.tf. The returned PromoteResult lists everything that moved
// plus the synthesised variables and outputs.
//
// Scope for this first implementation:
//   - Resources with no outgoing references keep whatever property values
//     they had. No variable is synthesised for them.
//   - Resources whose properties reference other in-scope resources keep
//     those references verbatim — the references resolve inside the new
//     module since the target is also being moved.
//   - Every moved resource gets a corresponding output in the new module
//     so the root can reference them post-extract if it needs to. Users
//     prune excess outputs manually in follow-up edits.
//
// Out of scope for this commit (tracked as follow-ups):
//   - Automatic discovery of which resources SHOULD be extracted together.
//   - Detecting references from outside the selection and auto-creating
//     module inputs to bind them.
//   - Preserving trailing comments or non-standard attribute formatting
//     (we emit canonical HCL via the existing generator).
func PromoteToModule(req PromoteRequest) (*PromoteResult, error) {
	if !identifierRE.MatchString(req.ModuleName) {
		return nil, fmt.Errorf("module name %q must be a lowercase identifier (letters, digits, underscores)", req.ModuleName)
	}
	if len(req.ResourceIDs) == 0 {
		return nil, fmt.Errorf("no resources to promote — provide at least one resource_id")
	}
	info, err := os.Stat(req.ProjectDir)
	if err != nil || !info.IsDir() {
		return nil, fmt.Errorf("project dir not a directory: %w", err)
	}

	// Parse the current project state to locate the selected resources.
	p := &parser.HCLParser{}
	all, err := p.ParseDir(req.ProjectDir)
	if err != nil {
		return nil, fmt.Errorf("parse project: %w", err)
	}

	wanted := make(map[string]struct{}, len(req.ResourceIDs))
	for _, id := range req.ResourceIDs {
		wanted[id] = struct{}{}
	}
	var toMove []parser.Resource
	for _, r := range all {
		if _, ok := wanted[r.ID]; ok && r.BlockType == "resource" {
			toMove = append(toMove, r)
		}
	}
	if len(toMove) != len(req.ResourceIDs) {
		missing := []string{}
		found := make(map[string]bool, len(toMove))
		for _, r := range toMove {
			found[r.ID] = true
		}
		for _, id := range req.ResourceIDs {
			if !found[id] {
				missing = append(missing, id)
			}
		}
		return nil, fmt.Errorf("resources not found: %s", strings.Join(missing, ", "))
	}

	modulePath := filepath.Join(req.ProjectDir, "modules", req.ModuleName)
	if _, err := os.Stat(modulePath); err == nil {
		return nil, fmt.Errorf("module directory %q already exists — pick a different name or delete it first", modulePath)
	}
	if err := os.MkdirAll(modulePath, 0o755); err != nil {
		return nil, fmt.Errorf("create module dir: %w", err)
	}
	// rollback removes the module directory on any error that occurs after
	// it has been created, preventing a half-written directory from
	// blocking a future retry with the same module name.
	rollback := func() { _ = os.RemoveAll(modulePath) }

	// Write the module's main.tf — the extracted resources verbatim.
	gen := generator.ForTool("terraform")
	mainBody, err := gen.Generate(toMove)
	if err != nil {
		rollback()
		return nil, fmt.Errorf("generate module main.tf: %w", err)
	}
	if err := os.WriteFile(filepath.Join(modulePath, "main.tf"), []byte(mainBody), 0o644); err != nil {
		rollback()
		return nil, fmt.Errorf("write module main.tf: %w", err)
	}

	// Outputs: one per resource so the root can still reach them.
	outputNames := make([]string, 0, len(toMove))
	var outBody strings.Builder
	for _, r := range toMove {
		outName := r.Name
		outputNames = append(outputNames, outName)
		fmt.Fprintf(&outBody, "output %q {\n  value = %s.%s\n}\n\n", outName, r.Type, r.Name)
	}
	if err := os.WriteFile(filepath.Join(modulePath, "outputs.tf"), []byte(outBody.String()), 0o644); err != nil {
		rollback()
		return nil, fmt.Errorf("write module outputs.tf: %w", err)
	}

	// Minimal variables.tf stub — empty body keeps the file present so
	// downstream tools (terraform init, tflint) don't complain about a
	// missing canonical file, while leaving room for the user to add
	// real variables once they refine the module.
	if err := os.WriteFile(filepath.Join(modulePath, "variables.tf"), []byte("# variables for module "+req.ModuleName+"\n"), 0o644); err != nil {
		rollback()
		return nil, fmt.Errorf("write module variables.tf: %w", err)
	}

	// Remove the moved resources from their source files. We rewrite each
	// affected file in place by parsing + regenerating the remaining
	// resources. This is lossy for non-resource content in the same file
	// (preserved blocks etc.) — for v1 we emit a TODO in the root file
	// and leave manual cleanup to the user.
	if err := stripMovedResourcesFromSourceFiles(req.ProjectDir, toMove); err != nil {
		rollback()
		return nil, err
	}

	// Append the module call to the root main.tf.
	rootMain := filepath.Join(req.ProjectDir, "main.tf")
	call := buildModuleCall(req.ModuleName, toMove)
	if err := appendToFile(rootMain, call); err != nil {
		rollback()
		return nil, fmt.Errorf("append module call: %w", err)
	}

	movedIDs := make([]string, 0, len(toMove))
	for _, r := range toMove {
		movedIDs = append(movedIDs, r.ID)
	}
	sort.Strings(movedIDs)
	sort.Strings(outputNames)

	return &PromoteResult{
		ModulePath:       modulePath,
		ResourcesMoved:   movedIDs,
		VariablesCreated: nil, // synthesis of inputs is a follow-up
		OutputsCreated:   outputNames,
	}, nil
}

// stripMovedResourcesFromSourceFiles rewrites each source file so that
// promoted resources no longer appear there. Other blocks in the same
// file (data, variable, output, provider, terraform, module, locals) are
// preserved as-is by re-emitting preserved blocks first, then whichever
// resources weren't moved.
func stripMovedResourcesFromSourceFiles(projectDir string, moved []parser.Resource) error {
	// Group moved resources by their source file so we know which files
	// need rewriting.
	movedSet := make(map[string]map[string]struct{}) // file → set of resource IDs
	for _, r := range moved {
		if _, ok := movedSet[r.File]; !ok {
			movedSet[r.File] = make(map[string]struct{})
		}
		movedSet[r.File][r.ID] = struct{}{}
	}

	p := &parser.HCLParser{}
	gen := generator.ForTool("terraform")
	for file, movedIDs := range movedSet {
		result, err := p.ParseFileFull(file)
		if err != nil {
			return fmt.Errorf("parse %s: %w", file, err)
		}
		var remaining []parser.Resource
		for _, r := range result.Resources {
			if _, ok := movedIDs[r.ID]; !ok {
				remaining = append(remaining, r)
			}
		}

		var out strings.Builder
		if body, err := gen.Generate(remaining); err == nil {
			out.WriteString(body)
		}
		// Preserved blocks get re-emitted verbatim.
		for _, pb := range result.PreservedBlocks {
			out.WriteString(pb.Content)
			out.WriteString("\n")
		}
		if err := os.WriteFile(file, []byte(out.String()), 0o644); err != nil {
			return fmt.Errorf("rewrite %s: %w", file, err)
		}
	}
	_ = projectDir // reserved for future checks (e.g., cross-file reference validation)
	return nil
}

// buildModuleCall returns the HCL block that replaces the extracted
// resources in the project root. Form:
//
//	module "name" {
//	  source = "./modules/name"
//	}
//
// We intentionally don't synthesise inputs here — those come in a
// follow-up once we detect cross-selection references. The result
// still parses, plans, and applies; users refine it manually for now.
func buildModuleCall(name string, moved []parser.Resource) string {
	var b strings.Builder
	fmt.Fprintf(&b, "\nmodule %q {\n", name)
	fmt.Fprintf(&b, "  source = \"./modules/%s\"\n", name)
	fmt.Fprintf(&b, "}\n\n# %d resource(s) were moved into modules/%s — review the module's variables.tf\n",
		len(moved), name)
	b.WriteString("# and outputs.tf, then update any other root-level references to use module outputs.\n")
	return b.String()
}

// appendToFile adds text to the end of a file, creating it if necessary.
// Used to tack the module call onto main.tf without touching existing
// resources.
func appendToFile(path, text string) error {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	if _, err := f.WriteString(text); err != nil {
		return err
	}
	return nil
}
