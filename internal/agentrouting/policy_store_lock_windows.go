//go:build windows

package agentrouting

import (
	"fmt"
	"os"
	"syscall"
	"unsafe"
)

var (
	policyProcLockFileEx   = policyKernel32.NewProc("LockFileEx")
	policyProcUnlockFileEx = policyKernel32.NewProc("UnlockFileEx")
)

type policyOverlapped struct {
	internal     uintptr
	internalHigh uintptr
	offset       uint32
	offsetHigh   uint32
	eventHandle  syscall.Handle
}

func lockPolicyStoreFile(handle *os.File) error {
	if _, err := handle.WriteAt([]byte{0}, 0); err != nil {
		return fmt.Errorf("prepare policy store lock: %w", err)
	}
	var overlapped policyOverlapped
	result, _, callErr := syscall.SyscallN(
		policyProcLockFileEx.Addr(),
		handle.Fd(),
		uintptr(0),
		uintptr(0),
		uintptr(1),
		uintptr(0),
		uintptr(unsafe.Pointer(&overlapped)),
	)
	if result == 0 {
		if callErr != syscall.Errno(0) {
			return fmt.Errorf("lock policy store: %w", callErr)
		}
		return fmt.Errorf("lock policy store: %w", syscall.EINVAL)
	}
	return nil
}

func unlockPolicyStoreFile(handle *os.File) error {
	var overlapped policyOverlapped
	result, _, callErr := syscall.SyscallN(
		policyProcUnlockFileEx.Addr(),
		handle.Fd(),
		uintptr(0),
		uintptr(1),
		uintptr(0),
		uintptr(unsafe.Pointer(&overlapped)),
	)
	if result == 0 {
		if callErr != syscall.Errno(0) {
			return fmt.Errorf("unlock policy store: %w", callErr)
		}
		return fmt.Errorf("unlock policy store: %w", syscall.EINVAL)
	}
	return nil
}
