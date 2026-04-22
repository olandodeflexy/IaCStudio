package runner

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"
)

// SafeRunner wraps Runner with timeouts, cancellation, approval gates, and kill switch.
type SafeRunner struct {
	runner    *Runner
	mu        sync.Mutex
	active    map[string]*Execution // projectDir -> running execution
	defaults  SafetyConfig
}

// SafetyConfig defines execution safety parameters.
type SafetyConfig struct {
	DefaultTimeout time.Duration `json:"default_timeout"` // max time for any command
	PlanTimeout    time.Duration `json:"plan_timeout"`
	ApplyTimeout   time.Duration `json:"apply_timeout"`
	InitTimeout    time.Duration `json:"init_timeout"`
	RequireApproval bool         `json:"require_approval"` // require plan review before apply
	MaxOutputBytes  int          `json:"max_output_bytes"` // prevent OOM from huge plans
}

// Execution tracks a running command with cancellation support.
type Execution struct {
	ID         string        `json:"id"`
	Project    string        `json:"project"`
	Tool       string        `json:"tool"`
	Command    string        `json:"command"`
	StartedAt  time.Time     `json:"started_at"`
	Status     string        `json:"status"` // running | completed | failed | cancelled | timed_out
	cancel     context.CancelFunc
}

// ExecutionResult is returned when a command completes.
type ExecutionResult struct {
	ID         string        `json:"id"`
	Output     string        `json:"output"`
	ExitCode   int           `json:"exit_code"`
	Duration   time.Duration `json:"duration"`
	Status     string        `json:"status"`
	Truncated  bool          `json:"truncated"` // true if output was cut
}

// PlanGate holds a pending plan that needs approval before apply.
type PlanGate struct {
	Project    string `json:"project"`
	Tool       string `json:"tool"`
	PlanOutput string `json:"plan_output"`
	Summary    string `json:"summary"` // "3 to add, 1 to change, 0 to destroy"
	ApprovedAt *time.Time `json:"approved_at,omitempty"`
}

func DefaultSafetyConfig() SafetyConfig {
	return SafetyConfig{
		DefaultTimeout:  5 * time.Minute,
		PlanTimeout:     10 * time.Minute,
		ApplyTimeout:    30 * time.Minute,
		InitTimeout:     5 * time.Minute,
		RequireApproval: true,
		MaxOutputBytes:  10 * 1024 * 1024, // 10MB
	}
}

func NewSafeRunner(config SafetyConfig) *SafeRunner {
	return &SafeRunner{
		runner:   New(),
		active:   make(map[string]*Execution),
		defaults: config,
	}
}

// Execute runs a command with timeout, cancellation, and output limiting.
func (sr *SafeRunner) Execute(ctx context.Context, projectDir, tool, command string) (*ExecutionResult, error) {
	// Project-level lock — prevent concurrent executions on the same project
	// which would cause terraform state lock contention and corruption.
	sr.mu.Lock()
	if existing, running := sr.active[projectDir]; running {
		sr.mu.Unlock()
		return nil, fmt.Errorf("project already has a running command: %s %s (started %s). Kill it first or wait for it to finish",
			existing.Tool, existing.Command, existing.StartedAt.Format("15:04:05"))
	}
	sr.mu.Unlock()

	timeout := sr.timeoutFor(command)
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	execID := fmt.Sprintf("%s-%s-%d", tool, command, time.Now().UnixNano())
	execution := &Execution{
		ID:        execID,
		Project:   projectDir,
		Tool:      tool,
		Command:   command,
		StartedAt: time.Now(),
		Status:    "running",
		cancel:    cancel,
	}

	sr.mu.Lock()
	sr.active[projectDir] = execution
	sr.mu.Unlock()

	defer func() {
		sr.mu.Lock()
		delete(sr.active, projectDir)
		sr.mu.Unlock()
	}()

	// Build command args
	args := sr.runner.buildArgs(tool, command)
	if len(args) == 0 {
		return nil, fmt.Errorf("unknown tool/command: %s %s", tool, command)
	}

	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
	cmd.Dir = projectDir

	// Create a new process group so we can kill Terraform AND its child
	// provider plugin processes (terraform-provider-aws, etc.) together.
	// Without this, context cancellation only kills the parent process,
	// orphaning provider plugins that hold state locks and leak memory.
	if runtime.GOOS != "windows" {
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		if runtime.GOOS == "windows" {
			return cmd.Process.Kill()
		}
		// Kill the entire process group (negative PID)
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}

	var stdout, stderr bytes.Buffer
	// Limit output to prevent OOM
	cmd.Stdout = &limitedWriter{buf: &stdout, limit: sr.defaults.MaxOutputBytes}
	cmd.Stderr = &limitedWriter{buf: &stderr, limit: sr.defaults.MaxOutputBytes}

	start := time.Now()
	err := cmd.Run()
	duration := time.Since(start)

	result := &ExecutionResult{
		ID:       execID,
		Duration: duration,
	}

	// Combine output
	output := stdout.String()
	if stderr.Len() > 0 {
		output += "\n" + stderr.String()
	}
	result.Output = output

	// Check if output was truncated
	if lw, ok := cmd.Stdout.(*limitedWriter); ok && lw.truncated {
		result.Truncated = true
		result.Output += "\n\n--- OUTPUT TRUNCATED (exceeded limit) ---"
	}

	// Determine status
	switch {
	case ctx.Err() == context.DeadlineExceeded:
		result.Status = "timed_out"
		execution.Status = "timed_out"
		return result, fmt.Errorf("command timed out after %v", timeout)
	case ctx.Err() == context.Canceled:
		result.Status = "cancelled"
		execution.Status = "cancelled"
		return result, fmt.Errorf("command was cancelled")
	case err != nil:
		result.Status = "failed"
		execution.Status = "failed"
		if exitErr, ok := err.(*exec.ExitError); ok {
			result.ExitCode = exitErr.ExitCode()
		}
		return result, err
	default:
		result.Status = "completed"
		execution.Status = "completed"
		result.ExitCode = 0
		return result, nil
	}
}

// Kill cancels a running execution for a project (the kill switch).
func (sr *SafeRunner) Kill(projectDir string) error {
	sr.mu.Lock()
	defer sr.mu.Unlock()

	exec, ok := sr.active[projectDir]
	if !ok {
		return fmt.Errorf("no active execution for project")
	}
	exec.cancel()
	exec.Status = "cancelled"
	return nil
}

// ActiveExecutions returns all currently running commands.
func (sr *SafeRunner) ActiveExecutions() []*Execution {
	sr.mu.Lock()
	defer sr.mu.Unlock()

	var execs []*Execution
	for _, e := range sr.active {
		execs = append(execs, e)
	}
	return execs
}

// DetectTools delegates to the underlying runner.
func (sr *SafeRunner) DetectTools() []ToolInfo {
	return sr.runner.DetectTools()
}

// ExecutePlanJSON runs terraform plan with JSON output for structured parsing.
func (sr *SafeRunner) ExecutePlanJSON(ctx context.Context, projectDir, tool string) (*ExecutionResult, error) {
	timeout := sr.defaults.PlanTimeout
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	binary := "terraform"
	if tool == "opentofu" {
		binary = "tofu"
	}

	cmd := exec.CommandContext(ctx, binary, "plan", "-json", "-no-color")
	cmd.Dir = projectDir
	if runtime.GOOS != "windows" {
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		if runtime.GOOS == "windows" {
			return cmd.Process.Kill()
		}
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &limitedWriter{buf: &stdout, limit: sr.defaults.MaxOutputBytes}
	cmd.Stderr = &limitedWriter{buf: &stderr, limit: sr.defaults.MaxOutputBytes}

	start := time.Now()
	err := cmd.Run()

	result := &ExecutionResult{
		ID:       fmt.Sprintf("plan-json-%d", time.Now().UnixNano()),
		Output:   stdout.String(),
		Duration: time.Since(start),
	}

	if err != nil {
		result.Status = "failed"
		if exitErr, ok := err.(*exec.ExitError); ok {
			result.ExitCode = exitErr.ExitCode()
		}
		// Still return output — plan -json outputs errors as JSON too
		return result, nil
	}

	result.Status = "completed"
	return result, nil
}

// RequiresApproval returns true if the command needs plan review first.
// Covers both the Terraform/Ansible vocabulary ("apply", "destroy")
// and Pulumi's native verbs ("up", "refresh") so every state-mutating
// action lands on the approval gate regardless of which tool the user
// picked. refresh is gated because Pulumi's refresh re-reads and
// overwrites the stack state file with whatever it observes in the
// cloud — a surprise refresh can hide drift an operator wanted to
// investigate.
func (sr *SafeRunner) RequiresApproval(command string) bool {
	if !sr.defaults.RequireApproval {
		return false
	}
	switch command {
	case "apply", "up", "destroy", "refresh":
		return true
	}
	return false
}

// ParsePlanSummary extracts the summary line from terraform plan output.
func ParsePlanSummary(output string) string {
	for _, line := range strings.Split(output, "\n") {
		if strings.Contains(line, "to add") && strings.Contains(line, "to change") {
			return strings.TrimSpace(line)
		}
		if strings.Contains(line, "No changes") || strings.Contains(line, "no changes") {
			return "No changes. Infrastructure is up-to-date."
		}
	}
	return "Unable to parse plan summary"
}

func (sr *SafeRunner) timeoutFor(command string) time.Duration {
	switch command {
	case "init":
		return sr.defaults.InitTimeout
	case "plan":
		return sr.defaults.PlanTimeout
	case "apply", "destroy":
		return sr.defaults.ApplyTimeout
	default:
		return sr.defaults.DefaultTimeout
	}
}

// limitedWriter caps output size to prevent OOM on huge terraform plans.
type limitedWriter struct {
	buf       *bytes.Buffer
	limit     int
	truncated bool
}

func (lw *limitedWriter) Write(p []byte) (n int, err error) {
	if lw.buf.Len()+len(p) > lw.limit {
		remaining := lw.limit - lw.buf.Len()
		if remaining > 0 {
			lw.buf.Write(p[:remaining])
		}
		lw.truncated = true
		return len(p), nil // pretend we wrote it all so the command doesn't error
	}
	return lw.buf.Write(p)
}
