package mcpairlock

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestManagerInvokeToolUsesIsolatedSanitizedProcess(t *testing.T) {
	const inheritedSecret = "do-not-forward"
	t.Setenv("AWS_SECRET_ACCESS_KEY", inheritedSecret)
	manager := newToolInvokerManager(t, "mcp-tool-helper")

	result, err := manager.InvokeTool(context.Background(), testToolCallRequest(t))
	if err != nil {
		t.Fatalf("InvokeTool: %v", err)
	}
	message, workingDir, found := strings.Cut(result.Output, "\nworking_dir=")
	if !found || message != "safe helper output" || result.IsError || !result.UntrustedOutput {
		t.Fatalf("unexpected result: %+v", result)
	}
	if !strings.HasPrefix(filepath.Base(workingDir), "iac-studio-mcp-") {
		t.Fatalf("tool process used unexpected working directory: %q", workingDir)
	}
	if _, err := os.Stat(workingDir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("tool working directory was not removed: %q: %v", workingDir, err)
	}
	if strings.Contains(result.Output, inheritedSecret) {
		t.Fatalf("tool process inherited cloud credential: %+v", result)
	}
}

func TestManagerInvokeToolTimesOutAndReapsProcess(t *testing.T) {
	manager := newToolInvokerManager(t, "mcp-tool-helper-hang", WithTimeout(50*time.Millisecond))
	startedAt := time.Now()

	_, err := manager.InvokeTool(context.Background(), testToolCallRequest(t))
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("InvokeTool error = %v, want context deadline exceeded", err)
	}
	if elapsed := time.Since(startedAt); elapsed > time.Second {
		t.Fatalf("timed-out tool process was not reaped promptly: %s", elapsed)
	}
}

func TestManagerInvokeToolFailsClosedOnLaunchAndCleanupErrors(t *testing.T) {
	request := testToolCallRequest(t)
	t.Run("launch", func(t *testing.T) {
		manager := newToolInvokerManager(t, "unused", WithToolSessionLauncher(func(context.Context, ServerDefinition) (ToolSession, error) {
			return nil, errors.New("token=do-not-leak")
		}))
		_, err := manager.InvokeTool(context.Background(), request)
		if !errors.Is(err, ErrToolSessionLaunch) || strings.Contains(err.Error(), "do-not-leak") {
			t.Fatalf("InvokeTool error = %v, want sanitized launch failure", err)
		}
	})

	t.Run("cleanup", func(t *testing.T) {
		session := &fakeToolSession{
			output:  validToolCallResponses("safe"),
			stopErr: errors.New("secret_access_key=do-not-leak"),
		}
		manager := newToolInvokerManager(t, "unused", WithToolSessionLauncher(func(context.Context, ServerDefinition) (ToolSession, error) {
			return session, nil
		}))
		result, err := manager.InvokeTool(context.Background(), request)
		if !errors.Is(err, ErrToolSessionCleanup) || strings.Contains(err.Error(), "do-not-leak") {
			t.Fatalf("InvokeTool error = %v, want sanitized cleanup failure", err)
		}
		if result != (ToolCallResult{}) || !session.stopped {
			t.Fatalf("cleanup failure returned result or skipped stop: result=%+v stopped=%v", result, session.stopped)
		}
	})
}

func TestManagerInvokeToolRejectsUnavailableServerBeforeLaunch(t *testing.T) {
	calls := 0
	manager := NewManager(t.TempDir(),
		WithDefinitions([]ServerDefinition{{
			ID:        "terraform-official",
			Name:      "Terraform",
			Command:   testExecutable(t),
			Transport: "stdio",
			Trusted:   false,
		}}),
		WithToolSessionLauncher(func(context.Context, ServerDefinition) (ToolSession, error) {
			calls++
			return nil, nil
		}),
	)

	_, err := manager.InvokeTool(context.Background(), testToolCallRequest(t))
	if !errors.Is(err, ErrToolServerUnavailable) || calls != 0 {
		t.Fatalf("InvokeTool error = %v, launch calls = %d", err, calls)
	}
}

func TestToolInvokerHelperProcess(t *testing.T) {
	if len(os.Args) == 0 || !strings.HasPrefix(os.Args[len(os.Args)-1], "mcp-tool-helper") {
		return
	}
	mode := os.Args[len(os.Args)-1]
	scanner := bufio.NewScanner(os.Stdin)
	encoder := json.NewEncoder(os.Stdout)
	for scanner.Scan() {
		var request struct {
			ID     int    `json:"id"`
			Method string `json:"method"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &request); err != nil {
			os.Exit(2)
		}
		switch request.Method {
		case "initialize":
			if err := encoder.Encode(map[string]any{
				"jsonrpc": "2.0",
				"id":      request.ID,
				"result": map[string]any{
					"protocolVersion": toolCallProtocolVersion,
					"capabilities":    map[string]any{},
					"serverInfo":      map[string]any{"name": "test", "version": "1"},
				},
			}); err != nil {
				os.Exit(3)
			}
		case "tools/call":
			if mode == "mcp-tool-helper-hang" {
				time.Sleep(10 * time.Second)
			}
			workingDir, err := os.Getwd()
			if err != nil {
				os.Exit(4)
			}
			output := "safe helper output\nworking_dir=" + workingDir
			if secret := os.Getenv("AWS_SECRET_ACCESS_KEY"); secret != "" {
				output = "leaked " + secret + "\nworking_dir=" + workingDir
			}
			if !strings.HasPrefix(filepath.Base(os.Getenv("HOME")), "iac-studio-mcp-") ||
				os.Getenv("AWS_EC2_METADATA_DISABLED") != "true" ||
				!strings.HasPrefix(os.Getenv("AWS_SHARED_CREDENTIALS_FILE"), os.Getenv("HOME")) {
				output = "unsafe credential discovery environment\nworking_dir=" + workingDir
			}
			if err := encoder.Encode(map[string]any{
				"jsonrpc": "2.0",
				"id":      request.ID,
				"result": map[string]any{
					"content": []map[string]any{{"type": "text", "text": output}},
				},
			}); err != nil {
				os.Exit(5)
			}
			os.Exit(0)
		}
	}
	os.Exit(6)
}

func newToolInvokerManager(t *testing.T, helperMode string, opts ...Option) *Manager {
	t.Helper()
	base := []Option{
		WithDefinitions([]ServerDefinition{{
			ID:              "terraform-official",
			Name:            "Terraform",
			Command:         testExecutable(t),
			Args:            []string{"-test.run=TestToolInvokerHelperProcess", "--", helperMode},
			Transport:       "stdio",
			Trusted:         true,
			ReadOnlyDefault: true,
			CredentialMode:  "none",
		}}),
	}
	return NewManager(t.TempDir(), append(base, opts...)...)
}

func validToolCallResponses(output string) io.Reader {
	responses := toolCallInitializeResponse + "\n" + `{"jsonrpc":"2.0","id":2,"result":{"content":[{"type":"text","text":` + string(mustJSON(output)) + `}]}}`
	return strings.NewReader(responses)
}

func mustJSON(value string) []byte {
	encoded, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return encoded
}

type fakeToolSession struct {
	input   bytes.Buffer
	output  io.Reader
	stopErr error
	stopped bool
}

func (s *fakeToolSession) ServerInput() io.Writer {
	return &s.input
}

func (s *fakeToolSession) ServerOutput() io.Reader {
	return s.output
}

func (s *fakeToolSession) Stop(context.Context) error {
	s.stopped = true
	return s.stopErr
}
