//go:build !windows

package agentrouting

import (
	"fmt"
	"os"
	"syscall"
)

func lockPolicyStoreFile(handle *os.File) error {
	if err := syscall.Flock(int(handle.Fd()), syscall.LOCK_EX); err != nil {
		return fmt.Errorf("lock policy store: %w", err)
	}
	return nil
}

func unlockPolicyStoreFile(handle *os.File) error {
	if err := syscall.Flock(int(handle.Fd()), syscall.LOCK_UN); err != nil {
		return fmt.Errorf("unlock policy store: %w", err)
	}
	return nil
}
