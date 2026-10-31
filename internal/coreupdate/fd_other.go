//go:build !unix

package coreupdate

import "errors"

func keepOnExec(uintptr) error { return errors.New("not supported on this system") }
