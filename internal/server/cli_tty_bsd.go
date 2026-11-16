//go:build darwin || dragonfly || freebsd || netbsd || openbsd

package server

import "golang.org/x/sys/unix"

// isatty reports whether fd is a terminal (a termios query succeeds; /dev/null fails it).
func isatty(fd uintptr) bool {
	_, err := unix.IoctlGetTermios(int(fd), unix.TIOCGETA)
	return err == nil
}
