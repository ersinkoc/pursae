//go:build windows

package server

// bmgrFreeBytes is not implemented on Windows; the free-space check is skipped there.
func bmgrFreeBytes(dir string) (free int64, ok bool) { return 0, false }
