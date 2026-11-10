//go:build windows

package server

import "golang.org/x/sys/windows"

// bmgrFreeBytes is the space available to this process on the volume holding dir; ok is false when it
// cannot be determined.
func bmgrFreeBytes(dir string) (free int64, ok bool) {
	p, err := windows.UTF16PtrFromString(dir)
	if err != nil {
		return 0, false
	}
	var avail, total, totalFree uint64
	if err := windows.GetDiskFreeSpaceEx(p, &avail, &total, &totalFree); err != nil {
		return 0, false
	}
	return int64(avail), true
}
