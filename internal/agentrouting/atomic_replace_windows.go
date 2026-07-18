//go:build windows

package agentrouting

import (
	"syscall"
	"unsafe"
)

const (
	movePolicyFileReplaceExisting = 0x1
	movePolicyFileWriteThrough    = 0x8
)

var (
	policyKernel32        = syscall.NewLazyDLL("kernel32.dll")
	policyProcMoveFileExW = policyKernel32.NewProc("MoveFileExW")
)

func replacePolicyStoreFile(source, destination string) error {
	from, err := syscall.UTF16PtrFromString(source)
	if err != nil {
		return err
	}
	to, err := syscall.UTF16PtrFromString(destination)
	if err != nil {
		return err
	}
	result, _, callErr := syscall.SyscallN(
		policyProcMoveFileExW.Addr(),
		uintptr(unsafe.Pointer(from)),
		uintptr(unsafe.Pointer(to)),
		uintptr(movePolicyFileReplaceExisting|movePolicyFileWriteThrough),
	)
	if result == 0 {
		if callErr != syscall.Errno(0) {
			return callErr
		}
		return syscall.EINVAL
	}
	return nil
}
