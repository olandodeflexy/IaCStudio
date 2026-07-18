//go:build !windows

package agentrouting

import "os"

func replacePolicyStoreFile(source, destination string) error {
	return os.Rename(source, destination)
}
