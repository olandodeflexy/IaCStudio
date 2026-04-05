package parser

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/zclconf/go-cty/cty"
	"gopkg.in/yaml.v3"
)

// Resource represents a single IaC resource extracted from files.
type Resource struct {
	ID         string                 `json:"id"`
	Type       string                 `json:"type"`
	Name       string                 `json:"name"`
	Properties map[string]interface{} `json:"properties"`
	File       string                 `json:"file"`
	Line       int                    `json:"line"`
	BlockType  string                 `json:"block_type,omitempty"` // "resource", "variable", "output", "data", "module", "locals"
}

// PreservedBlock holds non-resource content from a .tf file that the
// generator must write back unchanged. This prevents data destruction
// when the UI syncs resources back to disk.
type PreservedBlock struct {
	File    string `json:"file"`
	Content string `json:"content"` // raw HCL text of the block
	Type    string `json:"type"`    // "variable", "output", "module", "data", "locals", "provider", "terraform"
	Line    int    `json:"line"`
}

// ParseResult contains both parsed resources and preserved blocks.
type ParseResult struct {
	Resources       []Resource       `json:"resources"`
	PreservedBlocks []PreservedBlock `json:"preserved_blocks"`
}

// Parser reads IaC files and returns structured resources.
type Parser interface {
	ParseDir(dir string) ([]Resource, error)
	ParseFile(path string) ([]Resource, error)
}

// ForTool returns the appropriate parser for the given tool.
func ForTool(tool string) Parser {
	switch tool {
	case "terraform", "opentofu":
		return &HCLParser{}
	case "ansible":
		return &YAMLParser{}
	default:
		return &HCLParser{}
	}
}

// ─── HCL Parser (Terraform / OpenTofu) ───

type HCLParser struct{}

func (p *HCLParser) ParseDir(dir string) ([]Resource, error) {
	var resources []Resource
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(path, ".tf") {
			return err
		}
		parsed, err := p.ParseFile(path)
		if err != nil {
			return fmt.Errorf("parsing %s: %w", path, err)
		}
		resources = append(resources, parsed...)
		return nil
	})
	return resources, err
}

func (p *HCLParser) ParseFile(path string) ([]Resource, error) {
	result, err := p.ParseFileFull(path)
	if err != nil {
		return nil, err
	}
	return result.Resources, nil
}

// ParseFileFull parses a .tf file and returns both resources and preserved blocks.
func (p *HCLParser) ParseFileFull(path string) (*ParseResult, error) {
	src, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	file, diags := hclsyntax.ParseConfig(src, path, hcl.Pos{Line: 1, Column: 1})
	if diags.HasErrors() {
		return nil, fmt.Errorf("HCL parse error: %s", diags.Error())
	}

	result := &ParseResult{}
	srcLines := strings.Split(string(src), "\n")
	body := file.Body.(*hclsyntax.Body)

	for _, block := range body.Blocks {
		startLine := block.DefRange().Start.Line
		endLine := block.CloseBraceRange.Start.Line

		switch block.Type {
		case "resource":
			if len(block.Labels) >= 2 {
				res := Resource{
					ID:         fmt.Sprintf("%s.%s", block.Labels[0], block.Labels[1]),
					Type:       block.Labels[0],
					Name:       block.Labels[1],
					Properties: make(map[string]interface{}),
					File:       path,
					Line:       startLine,
					BlockType:  "resource",
				}

				attrs, _ := block.Body.JustAttributes()
				for name, attr := range attrs {
					// Try to evaluate the expression
					val, valDiags := attr.Expr.Value(nil)
					if !valDiags.HasErrors() {
						res.Properties[name] = ctyToInterface(val)
					} else {
						// Expression uses variables/functions — preserve as raw string
						rawExpr := extractRawExpression(srcLines, attr.Expr.Range())
						res.Properties[name] = rawExpr
					}
				}

				result.Resources = append(result.Resources, res)
			}

		case "data":
			if len(block.Labels) >= 2 {
				res := Resource{
					ID:        fmt.Sprintf("data.%s.%s", block.Labels[0], block.Labels[1]),
					Type:      block.Labels[0],
					Name:      block.Labels[1],
					Properties: make(map[string]interface{}),
					File:      path,
					Line:      startLine,
					BlockType: "data",
				}
				attrs, _ := block.Body.JustAttributes()
				for name, attr := range attrs {
					val, valDiags := attr.Expr.Value(nil)
					if !valDiags.HasErrors() {
						res.Properties[name] = ctyToInterface(val)
					} else {
						res.Properties[name] = extractRawExpression(srcLines, attr.Expr.Range())
					}
				}
				result.Resources = append(result.Resources, res)
			}

		default:
			// Preserve variable, output, module, locals, provider, terraform blocks
			// as raw text so the generator can write them back unchanged.
			if endLine > 0 && endLine <= len(srcLines) && startLine > 0 {
				rawBlock := strings.Join(srcLines[startLine-1:endLine], "\n")
				result.PreservedBlocks = append(result.PreservedBlocks, PreservedBlock{
					File:    path,
					Content: rawBlock,
					Type:    block.Type,
					Line:    startLine,
				})
			}
		}
	}

	return result, nil
}

// extractRawExpression pulls the original source text for an expression
// that couldn't be statically evaluated (uses variables, functions, etc).
func extractRawExpression(srcLines []string, rng hcl.Range) string {
	if rng.Start.Line == rng.End.Line && rng.Start.Line > 0 && rng.Start.Line <= len(srcLines) {
		line := srcLines[rng.Start.Line-1]
		start := rng.Start.Column - 1
		end := rng.End.Column - 1
		if start >= 0 && end <= len(line) && start < end {
			return strings.TrimSpace(line[start:end])
		}
	}
	// Multi-line expression — return first line
	if rng.Start.Line > 0 && rng.Start.Line <= len(srcLines) {
		return strings.TrimSpace(srcLines[rng.Start.Line-1])
	}
	return ""
}

// ctyToInterface converts a cty.Value into a native Go type so that
// booleans, numbers, strings, lists, and maps survive round-trips
// through JSON and back into the generator without losing type info.
func ctyToInterface(raw interface{}) interface{} {
	val, ok := raw.(cty.Value)
	if !ok {
		return fmt.Sprintf("%v", raw)
	}
	if !val.IsKnown() || val.IsNull() {
		return nil
	}

	ty := val.Type()
	switch {
	case ty == cty.String:
		return val.AsString()
	case ty == cty.Number:
		bf := val.AsBigFloat()
		if bf.IsInt() {
			i, _ := bf.Int64()
			return i
		}
		f, _ := bf.Float64()
		return f
	case ty == cty.Bool:
		return val.True()
	case ty.IsListType() || ty.IsTupleType() || ty.IsSetType():
		var out []interface{}
		for it := val.ElementIterator(); it.Next(); {
			_, v := it.Element()
			out = append(out, ctyToInterface(v))
		}
		return out
	case ty.IsMapType() || ty.IsObjectType():
		out := make(map[string]interface{})
		for it := val.ElementIterator(); it.Next(); {
			k, v := it.Element()
			out[k.AsString()] = ctyToInterface(v)
		}
		return out
	default:
		return fmt.Sprintf("%v", val)
	}
}

// ─── YAML Parser (Ansible) ───

type YAMLParser struct{}

func (p *YAMLParser) ParseDir(dir string) ([]Resource, error) {
	var resources []Resource
	var parseErrors []string
	walkErr := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		if strings.HasSuffix(path, ".yml") || strings.HasSuffix(path, ".yaml") {
			parsed, parseErr := p.ParseFile(path)
			if parseErr != nil {
				parseErrors = append(parseErrors, fmt.Sprintf("%s: %v", path, parseErr))
				return nil // keep walking — collect all errors
			}
			resources = append(resources, parsed...)
		}
		return nil
	})
	if walkErr != nil {
		return resources, walkErr
	}
	if len(parseErrors) > 0 {
		return resources, fmt.Errorf("YAML parse errors:\n  %s", strings.Join(parseErrors, "\n  "))
	}
	return resources, nil
}

func (p *YAMLParser) ParseFile(path string) ([]Resource, error) {
	src, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var playbooks []map[string]interface{}
	if err := yaml.Unmarshal(src, &playbooks); err != nil {
		return nil, err
	}

	var resources []Resource
	id := 0

	for _, playbook := range playbooks {
		tasks, ok := playbook["tasks"].([]interface{})
		if !ok {
			continue
		}
		for _, t := range tasks {
			task, ok := t.(map[string]interface{})
			if !ok {
				continue
			}
			id++
			name, _ := task["name"].(string)

			// Find the module (first key that isn't "name", "register", "when", etc.)
			moduleType := ""
			properties := make(map[string]interface{})
			metaKeys := map[string]bool{"name": true, "register": true, "when": true, "notify": true, "tags": true, "become": true}

			for k, v := range task {
				if metaKeys[k] {
					continue
				}
				moduleType = k
				if props, ok := v.(map[string]interface{}); ok {
					properties = props
				}
			}

			if moduleType != "" {
				resources = append(resources, Resource{
					ID:         fmt.Sprintf("task_%d", id),
					Type:       moduleType,
					Name:       name,
					Properties: properties,
					File:       path,
					Line:       id, // Approximate
				})
			}
		}
	}

	return resources, nil
}
