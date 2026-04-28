package runner

import (
	"bytes"
	"fmt"
	"os/exec"
	"strings"
)

// Runner executes IaC CLI commands.
type Runner struct{}

func New() *Runner {
	return &Runner{}
}

// ToolInfo describes an IaC tool found on the system.
type ToolInfo struct {
	Name      string `json:"name"`
	Binary    string `json:"binary"`
	Version   string `json:"version"`
	Available bool   `json:"available"`
}

// DetectTools checks which IaC tools are installed.
func (r *Runner) DetectTools() []ToolInfo {
	tools := []struct {
		name   string
		binary string
		args   []string
	}{
		{"Terraform", "terraform", []string{"version"}},
		{"OpenTofu", "tofu", []string{"version"}},
		{"Ansible", "ansible", []string{"--version"}},
		{"Pulumi", "pulumi", []string{"version"}},
	}

	var results []ToolInfo
	for _, t := range tools {
		info := ToolInfo{Name: t.name, Binary: t.binary}
		if path, err := exec.LookPath(t.binary); err == nil {
			info.Available = true
			info.Binary = path
			if out, err := exec.Command(t.binary, t.args...).Output(); err == nil {
				// Extract first line as version
				lines := strings.Split(string(out), "\n")
				if len(lines) > 0 {
					info.Version = strings.TrimSpace(lines[0])
				}
			}
		}
		results = append(results, info)
	}
	return results
}

// Execute runs an IaC command in the given project directory.
//
// env names the layered-v1 environment to target. For pulumi, env is
// passed through as `--stack <env>` so a request bound to dev cannot
// silently mutate the workspace-selected stack (which Pulumi resolves
// from local state and could be anything). Empty env runs in the
// project root with no explicit stack — fine for flat layouts.
func (r *Runner) Execute(projectDir, tool, command, env string) (string, error) {
	args := r.buildArgs(tool, command, env)
	if len(args) == 0 {
		return "", fmt.Errorf("unknown tool/command: %s %s", tool, command)
	}

	binary := args[0]
	cmdArgs := args[1:]

	cmd := exec.Command(binary, cmdArgs...)
	cmd.Dir = projectDir

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()

	output := stdout.String()
	if stderr.Len() > 0 {
		output += "\n" + stderr.String()
	}

	return output, err
}

func (r *Runner) buildArgs(tool, command, env string) []string {
	switch tool {
	case "terraform":
		return r.terraformArgs(command, "terraform")
	case "opentofu":
		return r.terraformArgs(command, "tofu")
	case "ansible":
		return r.ansibleArgs(command)
	case "pulumi":
		return r.pulumiArgs(command, env)
	}
	return nil
}

func (r *Runner) terraformArgs(command, binary string) []string {
	switch command {
	case "init":
		return []string{binary, "init"}
	case "plan":
		return []string{binary, "plan", "-no-color"}
	case "apply":
		return []string{binary, "apply", "-auto-approve", "-no-color"}
	case "destroy":
		return []string{binary, "destroy", "-auto-approve", "-no-color"}
	case "validate":
		return []string{binary, "validate", "-no-color"}
	case "fmt":
		return []string{binary, "fmt"}
	}
	return nil
}

// pulumiArgs maps the canvas's logical command names ("plan" / "apply"
// / "destroy") onto the Pulumi CLI's native verbs. We keep the same
// vocabulary the Terraform path uses so the UI doesn't need a per-tool
// branch — the translation happens here.
//
// "init" resolves to `npm install` rather than `pulumi stack init`
// because the scaffolded project has no stack state yet and stack
// creation requires an auth backend selection (local vs. Pulumi
// Cloud) that's outside the runner's scope. Users still run
// `pulumi login` + `pulumi stack init <env>` once per machine; the
// safe-runner path below only drives preview/up/destroy/refresh
// after that bootstrap is done.
//
// Every destructive verb carries --yes because the SafeRunner's
// approval gate fronts them — we rely on the server-side plan review
// rather than Pulumi's own interactive confirmation.
//
// Layout note: these args run in the caller's workdir. A single-env
// Pulumi project (Pulumi.yaml at the root) works as-is. Layered-
// pulumi projects (Pulumi.yaml under environments/<env>/) need the
// runner to cd into the env subdir first — that plumbing is a
// follow-up; for now, layered-pulumi users run through the scaffold's
// scripts/{init,plan,apply,destroy}.sh wrappers which handle the cd
// per-env themselves.
func (r *Runner) pulumiArgs(command, env string) []string {
	// withStack appends --stack <env> when env is provided. Pulumi
	// otherwise resolves the stack from local workspace state, so a
	// request bound to dev could silently mutate whatever stack
	// happens to be selected. Pinning explicitly removes that
	// ambiguity AND ensures the plan-gate (keyed by env-rebased
	// projectPath) actually corresponds to the stack apply will run
	// against.
	withStack := func(args []string) []string {
		if env == "" {
			return args
		}
		return append(args, "--stack", env)
	}
	switch command {
	case "init":
		// Prime the project: install node_modules so the TS program
		// compiles. Pulumi itself doesn't need this step, but without
		// it `pulumi preview` fails immediately on the missing
		// @pulumi/* SDKs.
		return []string{"npm", "install"}
	case "plan", "preview":
		return withStack([]string{"pulumi", "preview", "--non-interactive", "--color=never"})
	case "apply", "up":
		return withStack([]string{"pulumi", "up", "--yes", "--non-interactive", "--color=never"})
	case "destroy":
		return withStack([]string{"pulumi", "destroy", "--yes", "--non-interactive", "--color=never"})
	case "refresh":
		return withStack([]string{"pulumi", "refresh", "--yes", "--non-interactive", "--color=never"})
	case "validate":
		// The closest Pulumi equivalent of `terraform validate` is a
		// TypeScript compile with no emit — catches type errors
		// without invoking the engine.
		return []string{"npx", "tsc", "--noEmit"}
	}
	return nil
}

func (r *Runner) ansibleArgs(command string) []string {
	switch command {
	case "check":
		return []string{"ansible-playbook", "site.yml", "--check", "--diff"}
	case "playbook", "apply":
		return []string{"ansible-playbook", "site.yml"}
	case "syntax":
		return []string{"ansible-playbook", "site.yml", "--syntax-check"}
	case "inventory":
		return []string{"ansible-inventory", "--list"}
	}
	return nil
}
