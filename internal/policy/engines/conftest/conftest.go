// Package conftest shells out to the Conftest CLI against <projectDir>/policies/opa.
//
// Conftest is the de-facto way many teams already run OPA/Rego against
// Terraform plans in CI. The embedded OPA adapter in internal/policy/engines/opa
// covers the no-binary case; this adapter covers users who have Conftest
// installed and prefer its extra features (bundles, namespaces, --update).
//
// The adapter is graceful when conftest is not on PATH — Available() returns
// false and Evaluate returns an empty Result with an informative Error rather
// than failing the whole multi-engine run.
package conftest

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/iac-studio/iac-studio/internal/policy/engines"
)

// Binary is the name (or full path) of the conftest CLI. Overridable so tests
// can point at a fake script on disk without polluting PATH.
var Binary = "conftest"

// PoliciesDir mirrors the embedded OPA adapter — same files, different runner.
const PoliciesDir = "policies/opa"

type conftestEngine struct{}

// New returns the Conftest PolicyEngine. Available() probes the binary at
// call time so users can install conftest while the server is running and see
// it light up on the next Evaluate.
func New() engines.PolicyEngine { return &conftestEngine{} }

func (c *conftestEngine) Name() string { return "conftest" }

func (c *conftestEngine) Available() bool {
	_, err := exec.LookPath(Binary)
	return err == nil
}

// conftestReport mirrors the JSON shape produced by `conftest test --output json`.
// We decode a minimal subset — enough to turn each message into a Finding.
type conftestReport struct {
	Filename  string                `json:"filename"`
	Namespace string                `json:"namespace"`
	Failures  []conftestRuleResult  `json:"failures"`
	Warnings  []conftestRuleResult  `json:"warnings"`
	Exceptions []conftestRuleResult `json:"exceptions"`
}

type conftestRuleResult struct {
	Msg      string         `json:"msg"`
	Metadata map[string]any `json:"metadata,omitempty"`
}

func (c *conftestEngine) Evaluate(ctx context.Context, in engines.EvalInput) (engines.Result, error) {
	res := engines.Result{Engine: c.Name()}

	if !c.Available() {
		res.Error = "conftest CLI not found on PATH — install from https://conftest.dev to enable this engine"
		return res, nil
	}
	res.Available = true

	if in.ProjectDir == "" {
		res.Error = "conftest engine requires a project directory"
		return res, nil
	}
	policiesPath := filepath.Join(in.ProjectDir, PoliciesDir)
	info, err := os.Stat(policiesPath)
	if err != nil {
		if os.IsNotExist(err) {
			// No policies/opa directory → quiet no-op, same as the embedded OPA.
			return res, nil
		}
		// Real IO/permission errors should surface rather than silently
		// disable the engine — otherwise a missing read permission looks
		// identical to a genuine "no policies" state.
		res.Error = fmt.Sprintf("conftest: stat %s: %v", policiesPath, err)
		return res, err
	}
	if !info.IsDir() {
		res.Error = fmt.Sprintf("conftest: %s is not a directory", policiesPath)
		return res, nil
	}
	if len(in.PlanJSON) == 0 {
		res.Error = "conftest engine requires Terraform plan JSON; run `terraform plan -out=tfplan && terraform show -json tfplan` first"
		return res, nil
	}

	// Write the plan JSON to a temp file because conftest reads from disk by
	// default. Using stdin via `-` works too, but the temp-file path keeps the
	// error messages from conftest more readable (they include the filename).
	tmp, err := os.CreateTemp("", "tfplan-*.json")
	if err != nil {
		res.Error = fmt.Sprintf("conftest tempfile: %v", err)
		return res, err
	}
	defer func() {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
	}()
	if _, err := tmp.Write(in.PlanJSON); err != nil {
		res.Error = fmt.Sprintf("conftest write tempfile: %v", err)
		return res, err
	}
	if err := tmp.Close(); err != nil {
		res.Error = fmt.Sprintf("conftest close tempfile: %v", err)
		return res, err
	}

	cmd := exec.CommandContext(ctx, Binary, "test",
		"--policy", policiesPath,
		"--output", "json",
		"--no-color",
		tmp.Name(),
	)
	// Explicitly capture stderr to our own buffer so real Conftest errors
	// (Rego compile failures, bad flags, missing subcommand) survive in
	// res.Error. cmd.Output() would also populate ExitError.Stderr when
	// cmd.Stderr is nil, but that path caps at a small excerpt and relies
	// on a subtle documented behaviour — the explicit buffer is plainer.
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	stdout, err := cmd.Output()
	// conftest exits with a non-zero code when findings exist. We only treat
	// exec errors without any JSON output as real failures; findings-present
	// is the expected success path.
	if err != nil && len(stdout) == 0 {
		res.Error = formatExecError(err, stderr.Bytes())
		return res, err
	}

	var reports []conftestReport
	if decodeErr := json.Unmarshal(stdout, &reports); decodeErr != nil {
		res.Error = fmt.Sprintf("conftest output not valid JSON: %v", decodeErr)
		return res, decodeErr
	}

	for _, rep := range reports {
		for _, f := range rep.Failures {
			res.Findings = append(res.Findings, engines.Finding{
				Engine:     c.Name(),
				PolicyID:   rep.Namespace + ".deny",
				PolicyName: rep.Namespace + "/deny",
				Severity:   engines.SeverityError,
				Category:   "compliance",
				Message:    f.Msg,
				PolicyFile: rep.Filename,
			})
		}
		for _, w := range rep.Warnings {
			res.Findings = append(res.Findings, engines.Finding{
				Engine:     c.Name(),
				PolicyID:   rep.Namespace + ".warn",
				PolicyName: rep.Namespace + "/warn",
				Severity:   engines.SeverityWarning,
				Category:   "compliance",
				Message:    w.Msg,
				PolicyFile: rep.Filename,
			})
		}
	}
	return res, nil
}

// formatExecError surfaces the explicit stderr buffer when available so the
// caller sees conftest's actual complaint rather than a bare exit code.
// ExitError.Stderr is consulted as a fallback for the edge case where the
// caller forgot to set cmd.Stderr before calling Output().
func formatExecError(err error, stderr []byte) string {
	if len(stderr) > 0 {
		return fmt.Sprintf("conftest: %s", string(stderr))
	}
	if ee, ok := err.(*exec.ExitError); ok && len(ee.Stderr) > 0 {
		return fmt.Sprintf("conftest: %s", string(ee.Stderr))
	}
	return fmt.Sprintf("conftest: %v", err)
}

