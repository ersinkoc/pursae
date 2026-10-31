//go:build unix

package coreupdate

import "syscall"

// keepOnExec clears FD_CLOEXEC so the descriptor survives exec.
func keepOnExec(fd uintptr) error {
	_, _, errno := syscall.Syscall(syscall.SYS_FCNTL, fd, syscall.F_SETFD, 0)
	if errno != 0 {
		return errno
	}
	return nil
}
