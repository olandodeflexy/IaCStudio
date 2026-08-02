//go:build windows

package mcpairlock

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"

	"golang.org/x/sys/windows"
)

func configureToolCommand(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP}
}

func terminateToolProcessTree(ctx context.Context, cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	if executable, ok := taskkillExecutable(); ok {
		_ = exec.CommandContext(ctx, executable, "/T", "/F", "/PID", strconv.Itoa(cmd.Process.Pid)).Run()
	}
	_ = cmd.Process.Kill()
}

func taskkillExecutable() (string, bool) {
	systemDirectory, err := windows.GetSystemDirectory()
	if err != nil {
		return "", false
	}
	candidate := filepath.Join(systemDirectory, "taskkill.exe")
	info, err := os.Stat(candidate)
	if err != nil || info.IsDir() {
		return "", false
	}
	return candidate, true
}
