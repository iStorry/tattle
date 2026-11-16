//go:build unix

package api

import (
	"path/filepath"
	"syscall"
)

// diskFree returns the space available on the disk holding path.
func diskFree(path string) (uint64, bool) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(filepath.Dir(path), &st); err != nil {
		return 0, false
	}
	return uint64(st.Bavail) * uint64(st.Bsize), true
}
