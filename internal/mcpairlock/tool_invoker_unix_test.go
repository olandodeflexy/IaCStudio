//go:build !windows

package mcpairlock

import (
	"context"
	"io"
	"os"
	"os/exec"
	"testing"
	"time"
)

const toolTerminationHelperEnv = "IAC_STUDIO_TOOL_TERMINATION_HELPER"

func TestTerminateToolProcessTreeWithCanceledContext(t *testing.T) {
	cmd := exec.Command(os.Args[0], "-test.run=^TestToolTerminationHelperProcess$")
	cmd.Env = append(os.Environ(), toolTerminationHelperEnv+"=1")
	configureToolCommand(cmd)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("open helper stdout: %v", err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start helper process: %v", err)
	}
	if _, err := io.ReadFull(stdout, make([]byte, 1)); err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		t.Fatalf("wait for helper readiness: %v", err)
	}

	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
	}()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	terminateToolProcessTree(ctx, cmd)

	select {
	case <-done:
	case <-time.After(time.Second):
		_ = cmd.Process.Kill()
		t.Fatal("helper process was not reaped after termination")
	}
}

func TestToolTerminationHelperProcess(t *testing.T) {
	if os.Getenv(toolTerminationHelperEnv) != "1" {
		return
	}
	if _, err := os.Stdout.Write([]byte{1}); err != nil {
		os.Exit(2)
	}
	for {
		time.Sleep(time.Hour)
	}
}
