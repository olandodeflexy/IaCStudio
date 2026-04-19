package parser

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
)

// ModuleVariable describes one variable declaration inside a module —
// everything needed to render a form field in the UI or validate a caller's
// bindings against the module's declared interface.
type ModuleVariable struct {
	Name        string      `json:"name"`
	Type        string      `json:"type,omitempty"`        // raw HCL type expression ("string", "map(string)", …)
	Description string      `json:"description,omitempty"`
	Default     interface{} `json:"default,omitempty"`     // typed when evaluable, raw string otherwise
	HasDefault  bool        `json:"has_default"`           // explicit flag — nil is a legal default
	Sensitive   bool        `json:"sensitive,omitempty"`
	File        string      `json:"file"`
	Line        int         `json:"line"`
}

// ModuleOutput describes one output declaration. Value is kept as raw HCL
// because outputs almost always reference resources that don't exist yet at
// parse time (e.g. aws_vpc.this.id).
type ModuleOutput struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Value       string `json:"value,omitempty"`
	Sensitive   bool   `json:"sensitive,omitempty"`
	File        string `json:"file"`
	Line        int    `json:"line"`
}

// ModuleInterface captures the declared surface of a module — its variables
// (inputs) and outputs — without touching its resources. Callers compare
// this against the Inputs field on a Module block to detect missing or
// misnamed bindings.
type ModuleInterface struct {
	SourceDir string           `json:"source_dir"`
	Variables []ModuleVariable `json:"variables"`
	Outputs   []ModuleOutput   `json:"outputs"`
}

// InspectLocalModule walks a local module directory, parses every .tf file,
// and extracts variable + output declarations. It's tolerant of partial
// directories (a single .tf file is enough) and never descends into
// subdirectories — nested modules have their own InspectLocalModule call.
//
// Errors surface real problems (unreadable directory, malformed HCL) but a
// module with zero variables or outputs is a valid minimal case and returns
// an empty ModuleInterface with no error.
func InspectLocalModule(sourceDir string) (*ModuleInterface, error) {
	entries, err := os.ReadDir(sourceDir)
	if err != nil {
		return nil, fmt.Errorf("read module dir: %w", err)
	}
	iface := &ModuleInterface{SourceDir: sourceDir}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".tf") {
			continue
		}
		// Skip override files — they modify other files' content rather
		// than declaring new variables/outputs. Terraform treats
		// *_override.tf specially; we do too.
		if strings.HasSuffix(e.Name(), "_override.tf") || e.Name() == "override.tf" {
			continue
		}
		path := filepath.Join(sourceDir, e.Name())
		vars, outs, err := inspectOneFile(path)
		if err != nil {
			return nil, err
		}
		iface.Variables = append(iface.Variables, vars...)
		iface.Outputs = append(iface.Outputs, outs...)
	}
	return iface, nil
}

// inspectOneFile parses a single .tf file and extracts its variable +
// output blocks. Resource / data / module / locals / provider / terraform
// blocks are ignored — InspectLocalModule is only interested in the module's
// public surface.
func inspectOneFile(path string) ([]ModuleVariable, []ModuleOutput, error) {
	src, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, err
	}
	file, diags := hclsyntax.ParseConfig(src, path, hcl.Pos{Line: 1, Column: 1})
	if diags.HasErrors() {
		return nil, nil, fmt.Errorf("HCL parse %s: %s", path, diags.Error())
	}
	srcLines := strings.Split(string(src), "\n")
	body := file.Body.(*hclsyntax.Body)

	var vars []ModuleVariable
	var outs []ModuleOutput

	for _, block := range body.Blocks {
		startLine := block.DefRange().Start.Line
		switch block.Type {
		case "variable":
			if len(block.Labels) != 1 {
				continue
			}
			v := ModuleVariable{Name: block.Labels[0], File: path, Line: startLine}
			attrs, _ := block.Body.JustAttributes()
			for name, attr := range attrs {
				raw := extractRawExpression(srcLines, attr.Expr.Range())
				val, vdiags := attr.Expr.Value(nil)
				switch name {
				case "type":
					// Type is a type expression ("string", "map(string)",
					// …) that can't be evaluated as a value — always take
					// the raw HCL text.
					v.Type = raw
				case "description":
					if !vdiags.HasErrors() {
						if s, ok := ctyToInterface(val).(string); ok {
							v.Description = s
							continue
						}
					}
					v.Description = raw
				case "default":
					v.HasDefault = true
					if !vdiags.HasErrors() {
						v.Default = ctyToInterface(val)
					} else {
						v.Default = raw
					}
				case "sensitive":
					if !vdiags.HasErrors() {
						if b, ok := ctyToInterface(val).(bool); ok {
							v.Sensitive = b
						}
					}
				}
			}
			vars = append(vars, v)

		case "output":
			if len(block.Labels) != 1 {
				continue
			}
			o := ModuleOutput{Name: block.Labels[0], File: path, Line: startLine}
			attrs, _ := block.Body.JustAttributes()
			for name, attr := range attrs {
				raw := extractRawExpression(srcLines, attr.Expr.Range())
				val, vdiags := attr.Expr.Value(nil)
				switch name {
				case "description":
					if !vdiags.HasErrors() {
						if s, ok := ctyToInterface(val).(string); ok {
							o.Description = s
							continue
						}
					}
					o.Description = raw
				case "value":
					// Outputs almost always reference resources — keep as
					// raw HCL so the UI can pretty-print the expression.
					o.Value = raw
				case "sensitive":
					if !vdiags.HasErrors() {
						if b, ok := ctyToInterface(val).(bool); ok {
							o.Sensitive = b
						}
					}
				}
			}
			outs = append(outs, o)
		}
	}
	return vars, outs, nil
}
