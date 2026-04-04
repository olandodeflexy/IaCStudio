package workspace

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Manager handles Terraform workspaces as environment promotion lanes.
// This maps the DevOps mental model of dev → staging → prod to terraform workspaces
// so engineers can promote infrastructure changes through environments with confidence.
type Manager struct {
	projectsDir string
}

// Environment represents one deployment environment.
type Environment struct {
	Name         string            `json:"name"`          // dev, staging, prod
	Workspace    string            `json:"workspace"`     // terraform workspace name
	Status       string            `json:"status"`        // active | needs_plan | needs_apply | drifted
	LastApply    *time.Time        `json:"last_apply,omitempty"`
	ResourceCount int             `json:"resource_count"`
	VarFile      string            `json:"var_file"`      // path to env-specific .tfvars
	VarOverrides map[string]string `json:"var_overrides"` // env-specific variable overrides
	Locked       bool              `json:"locked"`        // prevent accidental applies
	LockReason   string            `json:"lock_reason,omitempty"`
}

// EnvironmentConfig is the project's environment setup stored in .iac-studio.json.
type EnvironmentConfig struct {
	Environments []Environment          `json:"environments"`
	PromotionOrder []string             `json:"promotion_order"` // ["dev", "staging", "prod"]
	RequireApproval map[string]bool     `json:"require_approval"` // env -> needs manual approval
}

// PromotionPlan describes what happens when promoting from one env to another.
type PromotionPlan struct {
	FromEnv      string   `json:"from_env"`
	ToEnv        string   `json:"to_env"`
	Changes      []string `json:"changes"`       // human-readable change descriptions
	RiskLevel    string   `json:"risk_level"`     // low | medium | high
	Warnings     []string `json:"warnings,omitempty"`
	Approved     bool     `json:"approved"`
}

// DriftResult shows differences between environments.
type DriftResult struct {
	Env1         string      `json:"env1"`
	Env2         string      `json:"env2"`
	Differences  []EnvDiff   `json:"differences"`
	InSync       bool        `json:"in_sync"`
}

// EnvDiff is a single difference between two environments.
type EnvDiff struct {
	Resource  string `json:"resource"`
	Field     string `json:"field"`
	Env1Value string `json:"env1_value"`
	Env2Value string `json:"env2_value"`
	Type      string `json:"type"` // added | removed | changed
}

func New(projectsDir string) *Manager {
	return &Manager{projectsDir: projectsDir}
}

// validName checks that a name is safe to pass as a CLI argument.
// Only allows alphanumeric, hyphens, and underscores.
func validName(name string) error {
	if name == "" {
		return fmt.Errorf("name cannot be empty")
	}
	for _, r := range name {
		if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9') || r == '-' || r == '_') {
			return fmt.Errorf("invalid name %q: only alphanumeric, hyphens, underscores allowed", name)
		}
	}
	return nil
}

// InitEnvironments sets up the standard dev/staging/prod workspace structure.
func (m *Manager) InitEnvironments(projectName string) (*EnvironmentConfig, error) {
	projectDir := filepath.Join(m.projectsDir, projectName)

	config := &EnvironmentConfig{
		PromotionOrder: []string{"dev", "staging", "prod"},
		RequireApproval: map[string]bool{
			"dev":     false,
			"staging": true,
			"prod":    true,
		},
	}

	envs := []struct {
		name   string
		locked bool
	}{
		{"dev", false},
		{"staging", false},
		{"prod", true}, // prod locked by default
	}

	for _, e := range envs {
		env := Environment{
			Name:         e.name,
			Workspace:    e.name,
			Status:       "needs_plan",
			VarFile:      fmt.Sprintf("env/%s.tfvars", e.name),
			VarOverrides: defaultVarsForEnv(e.name),
			Locked:       e.locked,
		}
		if e.locked {
			env.LockReason = "production environment — unlock explicitly before apply"
		}
		config.Environments = append(config.Environments, env)

		// Create the terraform workspace
		if err := m.createWorkspace(projectDir, e.name); err != nil {
			// Workspace might already exist, that's fine
			if !strings.Contains(err.Error(), "already exists") {
				return nil, fmt.Errorf("creating workspace %s: %w", e.name, err)
			}
		}

		// Create the env-specific tfvars file
		if err := m.writeVarFile(projectDir, env); err != nil {
			return nil, fmt.Errorf("writing var file for %s: %w", e.name, err)
		}
	}

	// Save config
	configPath := filepath.Join(projectDir, ".iac-studio-envs.json")
	data, _ := json.MarshalIndent(config, "", "  ")
	if err := os.WriteFile(configPath, data, 0644); err != nil {
		return nil, fmt.Errorf("saving env config: %w", err)
	}

	return config, nil
}

// ListEnvironments returns all configured environments for a project.
func (m *Manager) ListEnvironments(projectName string) (*EnvironmentConfig, error) {
	configPath := filepath.Join(m.projectsDir, projectName, ".iac-studio-envs.json")
	data, err := os.ReadFile(configPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("environments not initialized — run init first")
		}
		return nil, err
	}

	var config EnvironmentConfig
	if err := json.Unmarshal(data, &config); err != nil {
		return nil, err
	}
	return &config, nil
}

// SwitchEnvironment changes the active terraform workspace.
func (m *Manager) SwitchEnvironment(projectName, envName string) error {
	if err := validName(envName); err != nil {
		return fmt.Errorf("invalid environment name: %w", err)
	}
	projectDir := filepath.Join(m.projectsDir, projectName)
	cmd := exec.Command("terraform", "workspace", "select", envName)
	cmd.Dir = projectDir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("switching to workspace %s: %s", envName, string(out))
	}
	return nil
}

// CurrentEnvironment returns the currently active workspace.
func (m *Manager) CurrentEnvironment(projectName string) (string, error) {
	projectDir := filepath.Join(m.projectsDir, projectName)
	cmd := exec.Command("terraform", "workspace", "show")
	cmd.Dir = projectDir
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("getting current workspace: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

// PlanPromotion creates a promotion plan from one env to the next.
func (m *Manager) PlanPromotion(projectName, fromEnv, toEnv string) (*PromotionPlan, error) {
	config, err := m.ListEnvironments(projectName)
	if err != nil {
		return nil, err
	}

	// Validate promotion order
	fromIdx, toIdx := -1, -1
	for i, env := range config.PromotionOrder {
		if env == fromEnv {
			fromIdx = i
		}
		if env == toEnv {
			toIdx = i
		}
	}
	if fromIdx == -1 || toIdx == -1 {
		return nil, fmt.Errorf("invalid environment names")
	}
	if toIdx <= fromIdx {
		return nil, fmt.Errorf("can only promote forward: %v", config.PromotionOrder)
	}

	plan := &PromotionPlan{
		FromEnv:  fromEnv,
		ToEnv:    toEnv,
		Approved: false,
	}

	// Check if target is locked
	for _, env := range config.Environments {
		if env.Name == toEnv && env.Locked {
			plan.Warnings = append(plan.Warnings,
				fmt.Sprintf("environment '%s' is locked: %s", toEnv, env.LockReason))
			plan.RiskLevel = "high"
		}
	}

	// Check if approval is required
	if config.RequireApproval[toEnv] {
		plan.Warnings = append(plan.Warnings,
			fmt.Sprintf("environment '%s' requires manual approval before apply", toEnv))
	}

	// Risk assessment
	if plan.RiskLevel == "" {
		if toEnv == "prod" {
			plan.RiskLevel = "high"
		} else if toEnv == "staging" {
			plan.RiskLevel = "medium"
		} else {
			plan.RiskLevel = "low"
		}
	}

	plan.Changes = []string{
		fmt.Sprintf("switch terraform workspace from '%s' to '%s'", fromEnv, toEnv),
		fmt.Sprintf("apply using var file: env/%s.tfvars", toEnv),
	}

	return plan, nil
}

// LockEnvironment prevents applies to an environment.
func (m *Manager) LockEnvironment(projectName, envName, reason string) error {
	return m.updateEnv(projectName, envName, func(env *Environment) {
		env.Locked = true
		env.LockReason = reason
	})
}

// UnlockEnvironment allows applies to an environment.
func (m *Manager) UnlockEnvironment(projectName, envName string) error {
	return m.updateEnv(projectName, envName, func(env *Environment) {
		env.Locked = false
		env.LockReason = ""
	})
}

// CompareEnvironments shows drift between two environments.
func (m *Manager) CompareEnvironments(projectName, env1, env2 string) (*DriftResult, error) {
	projectDir := filepath.Join(m.projectsDir, projectName)
	result := &DriftResult{Env1: env1, Env2: env2}

	// Compare tfvars files
	vars1, err := readVarFile(filepath.Join(projectDir, "env", env1+".tfvars"))
	if err != nil {
		return nil, fmt.Errorf("reading %s vars: %w", env1, err)
	}
	vars2, err := readVarFile(filepath.Join(projectDir, "env", env2+".tfvars"))
	if err != nil {
		return nil, fmt.Errorf("reading %s vars: %w", env2, err)
	}

	seen := make(map[string]bool)
	for k, v1 := range vars1 {
		seen[k] = true
		v2, exists := vars2[k]
		if !exists {
			result.Differences = append(result.Differences, EnvDiff{
				Resource: "variable", Field: k, Env1Value: v1, Type: "removed",
			})
		} else if v1 != v2 {
			result.Differences = append(result.Differences, EnvDiff{
				Resource: "variable", Field: k, Env1Value: v1, Env2Value: v2, Type: "changed",
			})
		}
	}
	for k, v2 := range vars2 {
		if !seen[k] {
			result.Differences = append(result.Differences, EnvDiff{
				Resource: "variable", Field: k, Env2Value: v2, Type: "added",
			})
		}
	}

	result.InSync = len(result.Differences) == 0
	return result, nil
}

// --- internal helpers ---

func (m *Manager) createWorkspace(projectDir, name string) error {
	if err := validName(name); err != nil {
		return err
	}
	cmd := exec.Command("terraform", "workspace", "new", name)
	cmd.Dir = projectDir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s", strings.TrimSpace(string(out)))
	}
	return nil
}

func (m *Manager) writeVarFile(projectDir string, env Environment) error {
	envDir := filepath.Join(projectDir, "env")
	if err := os.MkdirAll(envDir, 0755); err != nil {
		return err
	}

	var lines []string
	lines = append(lines, fmt.Sprintf("# Environment: %s", env.Name))
	lines = append(lines, fmt.Sprintf("# Managed by IaC Studio — do not edit directly"))
	lines = append(lines, "")
	for k, v := range env.VarOverrides {
		lines = append(lines, fmt.Sprintf("%s = \"%s\"", k, v))
	}

	return os.WriteFile(filepath.Join(envDir, env.Name+".tfvars"), []byte(strings.Join(lines, "\n")+"\n"), 0644)
}

func (m *Manager) updateEnv(projectName, envName string, fn func(*Environment)) error {
	config, err := m.ListEnvironments(projectName)
	if err != nil {
		return err
	}

	found := false
	for i := range config.Environments {
		if config.Environments[i].Name == envName {
			fn(&config.Environments[i])
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("environment '%s' not found", envName)
	}

	configPath := filepath.Join(m.projectsDir, projectName, ".iac-studio-envs.json")
	data, _ := json.MarshalIndent(config, "", "  ")
	return os.WriteFile(configPath, data, 0644)
}

func defaultVarsForEnv(envName string) map[string]string {
	switch envName {
	case "dev":
		return map[string]string{
			"environment":   "dev",
			"instance_type": "t3.small",
			"min_capacity":  "1",
			"max_capacity":  "2",
		}
	case "staging":
		return map[string]string{
			"environment":   "staging",
			"instance_type": "t3.medium",
			"min_capacity":  "2",
			"max_capacity":  "4",
		}
	case "prod":
		return map[string]string{
			"environment":   "production",
			"instance_type": "t3.large",
			"min_capacity":  "3",
			"max_capacity":  "10",
		}
	default:
		return map[string]string{"environment": envName}
	}
}

func readVarFile(path string) (map[string]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	vars := make(map[string]string)
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) == 2 {
			key := strings.TrimSpace(parts[0])
			val := strings.TrimSpace(parts[1])
			val = strings.Trim(val, "\"")
			vars[key] = val
		}
	}
	return vars, nil
}
