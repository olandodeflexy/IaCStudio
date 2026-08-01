package mcpairlock

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"
)

var (
	ErrToolServerUnavailable = errors.New("mcp tool server is unavailable")
	ErrToolSessionLaunch     = errors.New("mcp tool session launch failed")
	ErrToolSessionCleanup    = errors.New("mcp tool session cleanup failed")
)

// ToolSession is one isolated stdio MCP process. The session owns its streams
// and must stop and reap the process when the invocation ends.
type ToolSession interface {
	ServerInput() io.Writer
	ServerOutput() io.Reader
	Stop(context.Context) error
}

// ToolSessionLauncherFunc starts one isolated process for one MCP tool call.
type ToolSessionLauncherFunc func(context.Context, ServerDefinition) (ToolSession, error)

// WithToolSessionLauncher replaces the one-shot process launcher. It is
// intended for tests and future credential-brokered launch policies.
func WithToolSessionLauncher(launcher ToolSessionLauncherFunc) Option {
	return func(m *Manager) {
		if launcher != nil {
			m.toolLauncher = launcher
		}
	}
}

// InvokeTool runs one previously authorized MCP tool request in a fresh stdio
// process. It does not authorize the route or inject cloud credentials.
func (m *Manager) InvokeTool(ctx context.Context, request ToolCallRequest) (result ToolCallResult, returnErr error) {
	if m == nil || m.toolLauncher == nil {
		return ToolCallResult{}, ErrToolSessionLaunch
	}
	if err := request.Validate(); err != nil {
		return ToolCallResult{}, err
	}
	definition, ok := m.lookup(request.ServerID)
	if !ok {
		return ToolCallResult{}, ErrUnknownServer
	}
	status := m.passiveStatus(definition)
	if status.State != "available" {
		return ToolCallResult{}, fmt.Errorf("%w: %s", ErrToolServerUnavailable, status.State)
	}

	callCtx, cancel := context.WithTimeout(ctx, m.timeout)
	defer cancel()
	session, err := m.toolLauncher(callCtx, definition)
	if err != nil {
		if callErr := callCtx.Err(); callErr != nil {
			return ToolCallResult{}, callErr
		}
		return ToolCallResult{}, ErrToolSessionLaunch
	}
	if session == nil {
		return ToolCallResult{}, ErrToolSessionLaunch
	}
	defer func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), m.timeout)
		defer cleanupCancel()
		if err := session.Stop(cleanupCtx); err != nil {
			result = ToolCallResult{}
			returnErr = errors.Join(returnErr, ErrToolSessionCleanup)
		}
	}()

	serverInput := session.ServerInput()
	serverOutput := session.ServerOutput()
	if serverInput == nil || serverOutput == nil {
		return ToolCallResult{}, ErrToolSessionLaunch
	}
	result, returnErr = runToolCallSession(callCtx, serverOutput, serverInput, request)
	if callErr := callCtx.Err(); callErr != nil {
		return ToolCallResult{}, callErr
	}
	return result, returnErr
}

type stdioToolSession struct {
	cmd    *exec.Cmd
	cancel context.CancelFunc
	stdin  io.WriteCloser
	stdout io.ReadCloser
	dir    string
	wait   sync.Once
	done   chan error
}

func (s *stdioToolSession) ServerInput() io.Writer {
	return s.stdin
}

func (s *stdioToolSession) ServerOutput() io.Reader {
	return s.stdout
}

func (s *stdioToolSession) Stop(ctx context.Context) (returnErr error) {
	_ = s.stdin.Close()
	s.cancel()
	s.wait.Do(func() {
		go func() {
			s.done <- s.cmd.Wait()
			close(s.done)
		}()
	})
	defer func() {
		_ = s.stdout.Close()
		if err := os.RemoveAll(s.dir); err != nil {
			returnErr = errors.Join(returnErr, err)
		}
	}()
	select {
	case <-s.done:
		return nil
	case <-ctx.Done():
		if s.cmd.Process != nil {
			_ = s.cmd.Process.Kill()
		}
		select {
		case <-s.done:
			return nil
		case <-time.After(100 * time.Millisecond):
			return ctx.Err()
		}
	}
}

func defaultToolSessionLauncher(ctx context.Context, definition ServerDefinition) (ToolSession, error) {
	runCtx, cancel := context.WithCancel(ctx)
	workingDir, err := os.MkdirTemp("", "iac-studio-mcp-")
	if err != nil {
		cancel()
		return nil, err
	}
	cmd := exec.CommandContext(runCtx, definition.Command, definition.Args...)
	cmd.Dir = workingDir
	cmd.Env = isolatedToolEnv(workingDir)
	cmd.Stderr = io.Discard
	stdin, err := cmd.StdinPipe()
	if err != nil {
		cancel()
		return nil, errors.Join(err, os.RemoveAll(workingDir))
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		return nil, errors.Join(err, stdin.Close(), os.RemoveAll(workingDir))
	}
	if err := cmd.Start(); err != nil {
		cancel()
		return nil, errors.Join(err, stdin.Close(), stdout.Close(), os.RemoveAll(workingDir))
	}
	session := &stdioToolSession{
		cmd:    cmd,
		cancel: cancel,
		stdin:  stdin,
		stdout: stdout,
		dir:    workingDir,
		done:   make(chan error, 1),
	}
	return session, nil
}

func isolatedToolEnv(workingDir string) []string {
	return append(minimalEnv(),
		"HOME="+workingDir,
		"USERPROFILE="+workingDir,
		"XDG_CONFIG_HOME="+filepath.Join(workingDir, ".config"),
		"AWS_CONFIG_FILE="+filepath.Join(workingDir, ".aws", "config"),
		"AWS_SHARED_CREDENTIALS_FILE="+filepath.Join(workingDir, ".aws", "credentials"),
		"AWS_EC2_METADATA_DISABLED=true",
		"AZURE_CONFIG_DIR="+filepath.Join(workingDir, ".azure"),
		"CLOUDSDK_CONFIG="+filepath.Join(workingDir, ".config", "gcloud"),
		"TF_CLI_CONFIG_FILE="+filepath.Join(workingDir, ".terraformrc"),
	)
}
