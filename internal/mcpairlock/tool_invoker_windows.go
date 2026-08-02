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
	_ = exec.CommandContext(ctx, taskkillExecutable(), "/T", "/F", "/PID", strconv.Itoa(cmd.Process.Pid)).Run()
	_ = cmd.Process.Kill()
}

func taskkillExecutable() string {
	if systemDirectory, err := windows.GetSystemDirectory(); err == nil {
		candidate := filepath.Join(systemDirectory, "taskkill.exe")
		if info, statErr := os.Stat(candidate); statErr == nil && !info.IsDir() {
			return candidate
		}
	}
	if path, err := exec.LookPath("taskkill.exe"); err == nil {
		return path
	}
	return "taskkill.exe"
}
