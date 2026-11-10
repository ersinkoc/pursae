//go:build !windows

package server

import "syscall"

// bmgrFreeBytes is the space available to this process on the filesystem holding dir; ok is false
// when it cannot be determined.
func bmgrFreeBytes(dir string) (free int64, ok bool) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(dir, &st); err != nil {
		return 0, false
	}
	return int64(st.Bavail) * int64(st.Bsize), true
}
