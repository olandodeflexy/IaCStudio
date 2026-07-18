//go:build !windows

package agentrouting

import (
	"os"

	"golang.org/x/sys/unix"
)

func openPolicyStoreDataFile(path string) (*os.File, error) {
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
	if err != nil {
		if err == unix.ELOOP {
			return nil, ErrInvalidPolicyStore
		}
		return nil, err
	}
	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil {
		_ = unix.Close(fd)
		return nil, err
	}
	if stat.Nlink != 1 {
		_ = unix.Close(fd)
		return nil, ErrInvalidPolicyStore
	}
	handle := os.NewFile(uintptr(fd), path)
	if handle == nil {
		_ = unix.Close(fd)
		return nil, ErrInvalidPolicyStore
	}
	return handle, nil
}
