//go:build !windows

package agentrouting

func securePolicyStoreDir(string) error {
	return nil
}

func securePolicyStoreFile(string) error {
	return nil
}
