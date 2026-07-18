//go:build !windows

package agentrouting

import "os"

func securePolicyStoreFile(path string) error {
	return os.Chmod(path, 0o600)
}

func securePolicyStoreDirHandle(handle *os.File) error {
	return handle.Chmod(0o700)
}

func securePolicyStoreHandle(handle *os.File) error {
	return handle.Chmod(0o600)
}
