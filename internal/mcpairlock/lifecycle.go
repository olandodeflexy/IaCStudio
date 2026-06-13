package mcpairlock

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"time"
)

const startupGrace = 250 * time.Millisecond

// ProcessHandle is a running external MCP server process.
type ProcessHandle interface {
	Done() <-chan error
	Stop(context.Context) error
}

// LauncherFunc starts a configured external MCP server process.
type LauncherFunc func(ctx context.Context, definition ServerDefinition, timeout time.Duration) (ProcessHandle, error)

type lifecycleStore struct {
	mu      sync.Mutex
	running map[string]*processRecord
	exits   map[string]exitRecord
}

type processRecord struct {
	handle    ProcessHandle
	startedAt time.Time
}

type exitRecord struct {
	at     time.Time
	reason string
}

func newLifecycleStore() *lifecycleStore {
	return &lifecycleStore{
		running: make(map[string]*processRecord),
		exits:   make(map[string]exitRecord),
	}
}

// WithLauncher replaces the process launcher. It is intended for tests and
// future launcher policies.
func WithLauncher(launcher LauncherFunc) Option {
	return func(m *Manager) {
		if launcher != nil {
			m.launcher = launcher
		}
	}
}

// Start launches one configured stdio MCP server with a sanitized environment.
func (m *Manager) Start(ctx context.Context, id string) (ServerStatus, error) {
	definition, ok := m.lookup(id)
	if !ok {
		return ServerStatus{}, ErrUnknownServer
	}
	status := m.passiveStatus(definition)
	if status.State != "available" {
		return m.withLifecycleStatus(status), nil
	}

	now := time.Now().UTC()
	m.lifecycle.mu.Lock()
	defer m.lifecycle.mu.Unlock()
	m.reapLocked(id, now)
	if record, ok := m.lifecycle.running[id]; ok {
		status = m.withLifecycleStatusLocked(status, record)
		status.Checks = append(status.Checks, Check{Name: "lifecycle", Status: "pass", Message: "server is already running"})
		return status, nil
	}

	handle, err := m.launcher(ctx, definition, m.timeout)
	if err != nil {
		status.State = "launch_failed"
		status.Summary = "Airlock could not start the MCP server process."
		status.Checks = append(status.Checks, Check{Name: "lifecycle", Status: "error", Message: redactOutput(err.Error())})
		return status, nil
	}
	record := &processRecord{handle: handle, startedAt: now}
	m.lifecycle.running[id] = record
	delete(m.lifecycle.exits, id)
	status = m.withLifecycleStatusLocked(status, record)
	status.Checks = append(status.Checks, Check{Name: "lifecycle", Status: "pass", Message: "server process started with a sanitized environment"})
	return status, nil
}

// Stop terminates one running MCP server process. It is safe to call when the
// server is already stopped.
func (m *Manager) Stop(ctx context.Context, id string) (ServerStatus, error) {
	definition, ok := m.lookup(id)
	if !ok {
		return ServerStatus{}, ErrUnknownServer
	}
	status := m.passiveStatus(definition)
	now := time.Now().UTC()

	m.lifecycle.mu.Lock()
	m.reapLocked(id, now)
	record, ok := m.lifecycle.running[id]
	if !ok {
		status = m.withLifecycleStatusLocked(status, nil)
		status.Checks = append(status.Checks, Check{Name: "lifecycle", Status: "warn", Message: "server is not running"})
		m.lifecycle.mu.Unlock()
		return status, nil
	}
	m.lifecycle.mu.Unlock()

	stopCtx, cancel := context.WithTimeout(ctx, m.timeout)
	defer cancel()
	if err := record.handle.Stop(stopCtx); err != nil {
		m.lifecycle.mu.Lock()
		status = m.withLifecycleStatusLocked(status, record)
		status.State = "stop_failed"
		status.Summary = "Airlock could not stop the MCP server process before the timeout."
		status.Checks = append(status.Checks, Check{Name: "lifecycle", Status: "error", Message: redactOutput(err.Error())})
		m.lifecycle.mu.Unlock()
		return status, nil
	}

	m.lifecycle.mu.Lock()
	delete(m.lifecycle.running, id)
	m.lifecycle.exits[id] = exitRecord{at: time.Now().UTC(), reason: "stopped by user"}
	status = m.withLifecycleStatusLocked(status, nil)
	status.State = "stopped"
	status.Summary = "MCP server process stopped."
	status.Checks = append(status.Checks, Check{Name: "lifecycle", Status: "pass", Message: "server process stopped"})
	m.lifecycle.mu.Unlock()
	return status, nil
}

// Close stops all running MCP server processes. It is best-effort cleanup for
// application shutdown.
func (m *Manager) Close() error {
	ctx, cancel := context.WithTimeout(context.Background(), m.timeout)
	defer cancel()
	return m.StopAll(ctx)
}

// StopAll terminates all running MCP server processes.
func (m *Manager) StopAll(ctx context.Context) error {
	m.lifecycle.mu.Lock()
	ids := make([]string, 0, len(m.lifecycle.running))
	for id := range m.lifecycle.running {
		ids = append(ids, id)
	}
	m.lifecycle.mu.Unlock()

	var joined error
	for _, id := range ids {
		if _, err := m.Stop(ctx, id); err != nil {
			joined = errors.Join(joined, err)
		}
	}
	return joined
}

func (m *Manager) withLifecycleStatus(status ServerStatus) ServerStatus {
	m.lifecycle.mu.Lock()
	defer m.lifecycle.mu.Unlock()
	m.reapLocked(status.Server.ID, time.Now().UTC())
	return m.withLifecycleStatusLocked(status, nil)
}

func (m *Manager) withLifecycleStatusLocked(status ServerStatus, record *processRecord) ServerStatus {
	if record == nil {
		record = m.lifecycle.running[status.Server.ID]
	}
	if record != nil {
		status.Running = true
		status.Ready = true
		status.State = "running"
		status.StartedAt = record.startedAt.Format(time.RFC3339)
		status.Summary = "MCP server process is running with cloud credentials withheld."
		status.Checks = append(status.Checks, Check{Name: "lifecycle", Status: "pass", Message: "server process is running"})
		return status
	}
	if exit, ok := m.lifecycle.exits[status.Server.ID]; ok {
		status.LastExitAt = exit.at.Format(time.RFC3339)
		status.LastExitReason = exit.reason
		if status.State == "available" {
			status.State = "exited"
			status.Summary = "MCP server process is not running."
			status.Checks = append(status.Checks, Check{Name: "lifecycle", Status: "warn", Message: exit.reason})
		}
	}
	return status
}

func (m *Manager) reapLocked(id string, now time.Time) {
	record := m.lifecycle.running[id]
	if record == nil {
		return
	}
	select {
	case err := <-record.handle.Done():
		delete(m.lifecycle.running, id)
		m.lifecycle.exits[id] = exitRecord{at: now, reason: exitReason(err)}
	default:
	}
}

func exitReason(err error) string {
	if err == nil {
		return "process exited"
	}
	return redactOutput(err.Error())
}

type osProcessHandle struct {
	cmd    *exec.Cmd
	cancel context.CancelFunc
	stdin  io.Closer
	done   chan error
}

func (p *osProcessHandle) Done() <-chan error {
	return p.done
}

func (p *osProcessHandle) Stop(ctx context.Context) error {
	if p.stdin != nil {
		_ = p.stdin.Close()
	}
	grace := startupGrace
	timer := time.NewTimer(grace)
	defer timer.Stop()
	select {
	case <-p.done:
		p.cancel()
		return nil
	case <-timer.C:
	}

	p.cancel()
	select {
	case <-p.done:
		return nil
	case <-ctx.Done():
		if p.cmd.Process != nil {
			_ = p.cmd.Process.Kill()
		}
		select {
		case <-p.done:
			return nil
		case <-time.After(100 * time.Millisecond):
			return ctx.Err()
		}
	}
}

func defaultLauncher(ctx context.Context, definition ServerDefinition, timeout time.Duration) (ProcessHandle, error) {
	runCtx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(runCtx, definition.Command, definition.Args...)
	cmd.Dir = os.TempDir()
	cmd.Env = minimalEnv()
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	stdin, err := cmd.StdinPipe()
	if err != nil {
		cancel()
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		_ = stdin.Close()
		cancel()
		return nil, err
	}
	handle := &osProcessHandle{
		cmd:    cmd,
		cancel: cancel,
		stdin:  stdin,
		done:   make(chan error, 1),
	}
	go func() {
		handle.done <- cmd.Wait()
		close(handle.done)
	}()

	grace := startupGrace
	if timeout > 0 && timeout < grace {
		grace = timeout
	}
	timer := time.NewTimer(grace)
	defer timer.Stop()
	select {
	case err := <-handle.done:
		cancel()
		if err == nil {
			return nil, errors.New("process exited during startup")
		}
		return nil, fmt.Errorf("process exited during startup: %w", err)
	case <-ctx.Done():
		stopCtx, stopCancel := context.WithTimeout(context.Background(), timeout)
		defer stopCancel()
		_ = handle.Stop(stopCtx)
		return nil, ctx.Err()
	case <-timer.C:
		return handle, nil
	}
}
