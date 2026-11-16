//go:build !(darwin || dragonfly || freebsd || netbsd || openbsd || linux || windows)

package server

// isatty is false on platforms without a termios check: destructive commands then need --yes.
func isatty(uintptr) bool { return false }
