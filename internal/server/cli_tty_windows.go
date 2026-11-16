//go:build windows

package server

import "golang.org/x/sys/windows"

// isatty reports whether fd is a console (NUL and pipes are not).
func isatty(fd uintptr) bool {
	var mode uint32
	return windows.GetConsoleMode(windows.Handle(fd), &mode) == nil
}
