// Package crossguard shells out to Pulumi CrossGuard policy packs.
//
// Pulumi runs local policy packs as part of preview/up via the
// `--policy-pack` flag. This adapter uses preview so Policy Studio and the
// apply gate can surface the same CrossGuard findings without mutating stack
// state. Policy packs are discovered under policies/crossguard or
// policies/pulumi, either in the current Pulumi project directory or in the
// layered project root that owns it.
package crossguard

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/iac-studio/iac-studio/internal/policy/engines"
)

// Binary is the Pulumi CLI name or full path. Tests override it with a fake
// script, matching the pattern used by the other shell-out policy engines.
var Binary = "pulumi"

// PolicyPackRoots are project-relative directories where IaC Studio looks for
// local Pulumi policy packs. Each directory can either be a policy pack itself
// (contains PulumiPolicy.yaml) or a parent containing one or more policy-pack
// subdirectories.
var PolicyPackRoots = []string{
	filepath.Join("policies", "crossguard"),
	filepath.Join("policies", "pulumi"),
}

type crossguardEngine struct{}

// New returns the Pulumi CrossGuard PolicyEngine.
func New() engines.PolicyEngine { return &crossguardEngine{} }

func (c *crossguardEngine) Name() string { return "crossguard" }

func (c *crossguardEngine) Available() bool {
	_, err := exec.LookPath(Binary)
	return err == nil
}

func (c *crossguardEngine) Evaluate(ctx context.Context, in engines.EvalInput) (engines.Result, error) {
	res := engines.Result{Engine: c.Name()}

	if !c.Available() {
		res.Error = "pulumi CLI not found on PATH - install Pulumi to enable CrossGuard policies"
		return res, nil
	}
	res.Available = true

	if in.ProjectDir == "" {
		res.Error = "crossguard engine requires a project directory"
		return res, nil
	}
	packs, err := discoverPolicyPacks(in.ProjectDir)
	if err != nil {
		res.Error = err.Error()
		return res, err
	}
	if len(packs) == 0 {
		return res, nil
	}
	if !hasPulumiProject(in.ProjectDir) {
		res.Error = "crossguard engine requires a Pulumi project directory containing Pulumi.yaml"
		return res, nil
	}

	args := []string{"preview", "--non-interactive", "--color=never"}
	if stack := inferStackName(in.ProjectDir); stack != "" {
		args = append(args, "--stack", stack)
	}
	for _, pack := range packs {
		args = append(args, "--policy-pack", pack)
	}

	cmd := exec.CommandContext(ctx, Binary, args...)
	cmd.Dir = in.ProjectDir
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	runErr := cmd.Run()

	res.Findings = parsePolicyViolations(out.String(), packs)
	if len(res.Findings) > 0 {
		return res, nil
	}
	if runErr != nil {
		res.Error = formatExecError(runErr, out.String())
		return res, runErr
	}
	return res, nil
}

func hasPulumiProject(projectDir string) bool {
	info, err := os.Stat(filepath.Join(projectDir, "Pulumi.yaml"))
	return err == nil && !info.IsDir()
}

func discoverPolicyPacks(projectDir string) ([]string, error) {
	abs, err := filepath.Abs(projectDir)
	if err != nil {
		return nil, err
	}
	roots := []string{abs}
	for dir := filepath.Dir(abs); dir != abs; dir = filepath.Dir(dir) {
		if _, err := os.Stat(filepath.Join(dir, ".iac-studio.json")); err == nil {
			roots = append(roots, dir)
			break
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
	}

	seen := map[string]struct{}{}
	var packs []string
	for _, root := range roots {
		for _, rel := range PolicyPackRoots {
			found, err := discoverPacksIn(filepath.Join(root, rel))
			if err != nil {
				return nil, err
			}
			for _, pack := range found {
				if _, ok := seen[pack]; ok {
					continue
				}
				seen[pack] = struct{}{}
				packs = append(packs, pack)
			}
		}
	}
	sort.Strings(packs)
	return packs, nil
}

func discoverPacksIn(root string) ([]string, error) {
	info, err := os.Stat(root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("crossguard: stat %s: %w", root, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("crossguard: %s is not a directory", root)
	}
	if isPolicyPack(root) {
		return []string{root}, nil
	}

	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, fmt.Errorf("crossguard: read %s: %w", root, err)
	}
	var packs []string
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		candidate := filepath.Join(root, entry.Name())
		if isPolicyPack(candidate) {
			packs = append(packs, candidate)
		}
	}
	sort.Strings(packs)
	return packs, nil
}

func isPolicyPack(dir string) bool {
	info, err := os.Stat(filepath.Join(dir, "PulumiPolicy.yaml"))
	return err == nil && !info.IsDir()
}

func inferStackName(projectDir string) string {
	matches, err := filepath.Glob(filepath.Join(projectDir, "Pulumi.*.yaml"))
	if err != nil || len(matches) != 1 {
		return ""
	}
	base := filepath.Base(matches[0])
	if base == "Pulumi.yaml" || base == "PulumiPolicy.yaml" {
		return ""
	}
	return strings.TrimSuffix(strings.TrimPrefix(base, "Pulumi."), ".yaml")
}

var violationHeaderRE = regexp.MustCompile(`^\s*\[(mandatory|advisory|remediate|disabled)\]\s+(.+?)\s*$`)
var resourceSuffixRE = regexp.MustCompile(`\s+\(([^)]*)\)\s*$`)
var multiSpaceRE = regexp.MustCompile(`\s{2,}`)

type violationBlock struct {
	enforcement string
	header      string
	lines       []string
}

func parsePolicyViolations(output string, packs []string) []engines.Finding {
	var blocks []violationBlock
	inSection := false
	var current *violationBlock

	for _, raw := range strings.Split(output, "\n") {
		line := strings.TrimRight(raw, "\r")
		if strings.TrimSpace(line) == "Policy Violations:" {
			inSection = true
			continue
		}
		if !inSection {
			continue
		}
		if match := violationHeaderRE.FindStringSubmatch(line); match != nil {
			if current != nil {
				blocks = append(blocks, *current)
			}
			current = &violationBlock{enforcement: match[1], header: strings.TrimSpace(match[2])}
			continue
		}
		if current == nil {
			continue
		}
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		current.lines = append(current.lines, trimmed)
	}
	if current != nil {
		blocks = append(blocks, *current)
	}

	defaultPack := ""
	if len(packs) == 1 {
		defaultPack = packs[0]
	}
	findings := make([]engines.Finding, 0, len(blocks))
	for _, block := range blocks {
		findings = append(findings, block.toFinding(defaultPack))
	}
	return findings
}

func (b violationBlock) toFinding(policyFile string) engines.Finding {
	header := b.header
	resource := ""
	if match := resourceSuffixRE.FindStringSubmatch(header); match != nil {
		resource = strings.TrimSpace(match[1])
		header = strings.TrimSpace(strings.TrimSuffix(header, match[0]))
	}

	policyID := header
	parts := multiSpaceRE.Split(header, -1)
	if len(parts) > 1 {
		policyID = strings.TrimSpace(parts[len(parts)-1])
	} else if fields := strings.Fields(header); len(fields) > 0 {
		policyID = fields[len(fields)-1]
	}
	if policyID == "" {
		policyID = "pulumi-policy"
	}

	message := strings.Join(b.lines, "\n")
	if message == "" {
		message = strings.TrimSpace(b.header)
	}
	return engines.Finding{
		Engine:     "crossguard",
		PolicyID:   policyID,
		PolicyName: policyID,
		Severity:   severityForEnforcement(b.enforcement),
		Category:   "compliance",
		Resource:   resource,
		Message:    message,
		PolicyFile: policyFile,
	}
}

func severityForEnforcement(enforcement string) engines.Severity {
	switch strings.ToLower(enforcement) {
	case "mandatory", "remediate":
		return engines.SeverityError
	case "advisory":
		return engines.SeverityWarning
	default:
		return engines.SeverityInfo
	}
}

func formatExecError(err error, output string) string {
	msg := strings.TrimSpace(output)
	if msg != "" {
		return fmt.Sprintf("crossguard: %s", msg)
	}
	return fmt.Sprintf("crossguard: %v", err)
}
