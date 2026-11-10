//go:build unix

package server

import "syscall"

// diskUsedPercent is how full the file system holding dir is.
func diskUsedPercent(dir string) (float64, bool) {
	var st syscall.Statfs_t
	if syscall.Statfs(dir, &st) != nil || st.Blocks == 0 {
		return 0, false
	}
	return float64(st.Blocks-st.Bavail) / float64(st.Blocks) * 100, true
}
