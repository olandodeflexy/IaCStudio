//go:build !windows

package mcpairlock

import "os"

func replaceFileAtomic(src, dst string) error {
	return os.Rename(src, dst)
}
