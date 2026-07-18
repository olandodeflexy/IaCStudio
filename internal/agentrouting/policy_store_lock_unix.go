//go:build !windows

package agentrouting

import (
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

func openPolicyStoreLockFile(path string) (*os.File, error) {
	fd, err := unix.Open(path, unix.O_CREAT|unix.O_RDWR|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0o600)
	if err != nil {
		return nil, err
	}
	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil {
		_ = unix.Close(fd)
		return nil, fmt.Errorf("inspect policy store lock: %w", err)
	}
	if stat.Nlink != 1 {
		_ = unix.Close(fd)
		return nil, fmt.Errorf("policy store lock must have exactly one hard link")
	}
	handle := os.NewFile(uintptr(fd), path)
	if handle == nil {
		_ = unix.Close(fd)
		return nil, fmt.Errorf("create policy store lock handle")
	}
	info, err := handle.Stat()
	if err != nil {
		_ = handle.Close()
		return nil, fmt.Errorf("inspect policy store lock: %w", err)
	}
	if !info.Mode().IsRegular() {
		_ = handle.Close()
		return nil, fmt.Errorf("policy store lock is not a regular file")
	}
	return handle, nil
}

func lockPolicyStoreFile(handle *os.File) error {
	if err := unix.Flock(int(handle.Fd()), unix.LOCK_EX); err != nil {
		return fmt.Errorf("lock policy store: %w", err)
	}
	return nil
}

func unlockPolicyStoreFile(handle *os.File) error {
	if err := unix.Flock(int(handle.Fd()), unix.LOCK_UN); err != nil {
		return fmt.Errorf("unlock policy store: %w", err)
	}
	return nil
}
