// Package opa runs OPA/Rego policies in-process via the embedded rego library.
//
// The adapter:
//   - walks <projectDir>/policies/opa/*.rego at evaluation time so users can
//     edit policies without restarting the server;
//   - takes the Terraform plan JSON as the document under review;
//   - runs the Conftest-style rule names — deny, violation, warn — and maps
//     them to engine.Findings with the conventional severities;
//   - returns a clear "plan JSON required" error when called without a plan,
//     since OPA has nothing to evaluate against the resource graph alone.
//
// We use the open-policy-agent/opa/rego library rather than shelling out to
// `opa eval` so users don't need a separate binary on PATH.
package opa

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/iac-studio/iac-studio/internal/policy/engines"
	"github.com/open-policy-agent/opa/v1/ast"
	"github.com/open-policy-agent/opa/v1/rego"
)

// PoliciesDir is the relative path under a project root where Rego files
// live. Mirrors the layered-terraform blueprint's layout.
const PoliciesDir = "policies/opa"

// ruleSeverity captures the Conftest convention for naming Rego rules.
// "deny" and "violation" are blocking; "warn" is informational.
var ruleSeverity = map[string]engines.Severity{
	"deny":      engines.SeverityError,
	"violation": engines.SeverityError,
	"warn":      engines.SeverityWarning,
}

type opaEngine struct{}

// New returns the OPA PolicyEngine. The constructor takes no arguments
// because the adapter is stateless — every Evaluate call walks the project
// directory fresh.
func New() engines.PolicyEngine { return &opaEngine{} }

func (o *opaEngine) Name() string   { return "opa" }
func (o *opaEngine) Available() bool { return true } // embedded library, always present

func (o *opaEngine) Evaluate(ctx context.Context, in engines.EvalInput) (engines.Result, error) {
	res := engines.Result{Engine: o.Name(), Available: true}

	if in.ProjectDir == "" {
		res.Error = "OPA engine requires a project directory"
		return res, nil
	}
	files, err := discoverPolicyFiles(in.ProjectDir)
	if err != nil {
		res.Error = err.Error()
		return res, err
	}
	if len(files) == 0 {
		// No policies authored — surface as available-but-quiet.
		return res, nil
	}
	if len(in.PlanJSON) == 0 {
		res.Error = "OPA engine requires Terraform plan JSON; run `terraform plan -out=tfplan && terraform show -json tfplan` first"
		return res, nil
	}

	var planDoc any
	// rego accepts the JSON as either []byte or a decoded any; passing the
	// decoded form lets the library skip an internal re-parse.
	if err := jsonUnmarshal(in.PlanJSON, &planDoc); err != nil {
		res.Error = fmt.Sprintf("plan JSON is not valid JSON: %v", err)
		return res, err
	}

	for _, file := range files {
		findings, err := evalFile(ctx, file, planDoc)
		if err != nil {
			// One bad policy file shouldn't blank the whole run — record the
			// error and continue with the others. The first error is also
			// returned so callers can log it.
			res.Error = fmt.Sprintf("%s: %v", filepath.Base(file), err)
			continue
		}
		res.Findings = append(res.Findings, findings...)
	}
	return res, nil
}

// discoverPolicyFiles returns every .rego file under <projectDir>/policies/opa,
// sorted for deterministic output.
func discoverPolicyFiles(projectDir string) ([]string, error) {
	root := filepath.Join(projectDir, PoliciesDir)
	info, err := os.Stat(root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("%s is not a directory", root)
	}
	matches, err := filepath.Glob(filepath.Join(root, "*.rego"))
	if err != nil {
		return nil, err
	}
	sort.Strings(matches)
	return matches, nil
}

// evalFile compiles one Rego file and runs every supported rule name against
// the plan. The Rego package is auto-detected from the file's `package` line
// so users can name packages whatever they want as long as they declare the
// rules with the standard Conftest names.
func evalFile(ctx context.Context, path string, planDoc any) ([]engines.Finding, error) {
	src, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	pkg, err := readPackageName(src)
	if err != nil {
		return nil, err
	}

	var findings []engines.Finding
	// Sort rule names so multiple findings from the same file have stable order.
	ruleNames := make([]string, 0, len(ruleSeverity))
	for name := range ruleSeverity {
		ruleNames = append(ruleNames, name)
	}
	sort.Strings(ruleNames)

	for _, ruleName := range ruleNames {
		query := fmt.Sprintf("data.%s.%s", pkg, ruleName)
		r := rego.New(
			rego.Query(query),
			rego.Module(filepath.Base(path), string(src)),
			rego.Input(planDoc),
			// Default to Rego v0 syntax for compatibility with existing
			// Conftest-style policies and the layered-terraform blueprint
			// seeds. Users who prefer v1 syntax can declare `import rego.v1`
			// in their files; the v1 import is honoured even when this
			// package-wide default is v0.
			rego.SetRegoVersion(ast.RegoV0),
		)
		rs, err := r.Eval(ctx)
		if err != nil {
			return nil, fmt.Errorf("eval %s: %w", ruleName, err)
		}
		for _, result := range rs {
			for _, expr := range result.Expressions {
				// Each rule typically returns a string per violation. Rego
				// "deny[msg]" expands to a set; rego.Eval surfaces each
				// element via Expressions[].Value.
				switch v := expr.Value.(type) {
				case []any:
					for _, item := range v {
						findings = append(findings, makeFinding(path, pkg, ruleName, item))
					}
				default:
					if v != nil {
						findings = append(findings, makeFinding(path, pkg, ruleName, v))
					}
				}
			}
		}
	}
	return findings, nil
}

func makeFinding(path, pkg, ruleName string, raw any) engines.Finding {
	msg, ok := raw.(string)
	if !ok {
		msg = fmt.Sprintf("%v", raw)
	}
	sev := ruleSeverity[ruleName]
	return engines.Finding{
		Engine:     "opa",
		PolicyID:   pkg + "." + ruleName,
		PolicyName: pkg + "/" + ruleName,
		Severity:   sev,
		Category:   "compliance",
		Message:    msg,
		PolicyFile: path,
	}
}
