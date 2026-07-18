//go:build !windows

package agentrouting

import (
	"errors"
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

func openPolicyStoreDirFile(path string) (*os.File, error) {
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW|unix.O_DIRECTORY, 0)
	if err != nil {
		return nil, err
	}
	handle := os.NewFile(uintptr(fd), path)
	if handle == nil {
		_ = unix.Close(fd)
		return nil, errors.New("create policy store directory handle")
	}
	info, err := handle.Stat()
	if err != nil {
		_ = handle.Close()
		return nil, fmt.Errorf("inspect policy store directory: %w", err)
	}
	if !info.IsDir() {
		_ = handle.Close()
		return nil, errors.New("policy store path is not a directory")
	}
	return handle, nil
}
