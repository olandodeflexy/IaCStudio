//go:build windows

package agentrouting

import (
	"fmt"
	"os"

	"golang.org/x/sys/windows"
)

func lockPolicyStoreFile(handle *os.File) error {
	if _, err := handle.WriteAt([]byte{0}, 0); err != nil {
		return fmt.Errorf("prepare policy store lock: %w", err)
	}
	var overlapped windows.Overlapped
	if err := windows.LockFileEx(
		windows.Handle(handle.Fd()),
		windows.LOCKFILE_EXCLUSIVE_LOCK,
		0,
		1,
		0,
		&overlapped,
	); err != nil {
		return fmt.Errorf("lock policy store: %w", err)
	}
	return nil
}

func unlockPolicyStoreFile(handle *os.File) error {
	var overlapped windows.Overlapped
	if err := windows.UnlockFileEx(
		windows.Handle(handle.Fd()),
		0,
		1,
		0,
		&overlapped,
	); err != nil {
		return fmt.Errorf("unlock policy store: %w", err)
	}
	return nil
}
