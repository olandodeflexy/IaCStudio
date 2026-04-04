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
func (r *Runner) Execute(projectDir, tool, command string) (string, error) {
	args := r.buildArgs(tool, command)
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

func (r *Runner) buildArgs(tool, command string) []string {
	switch tool {
	case "terraform":
		return r.terraformArgs(command, "terraform")
	case "opentofu":
		return r.terraformArgs(command, "tofu")
	case "ansible":
		return r.ansibleArgs(command)
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
