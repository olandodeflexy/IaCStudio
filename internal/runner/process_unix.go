//go:build !windows

package runner

import (
	"os/exec"
	"syscall"
)

func configureCommandCancel(cmd *exec.Cmd) {
	// Create a new process group so cancellation kills Terraform/OpenTofu and
	// child provider plugins together instead of orphaning state-lock holders.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
}
