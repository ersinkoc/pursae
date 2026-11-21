package coreupdate

import (
	"net"
	"os"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

// A release before 0.5.0 hands the socket over in WPGO_LISTEN_FD, and a rollback to one must find it
// there: Listen reads either name and clears both, Restart sets both.
func TestListenLegacyEnv(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix only")
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	f, err := ln.(*net.TCPListener).File()
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	t.Setenv(listenEnv, "")
	t.Setenv(legacyListenEnv, strconv.Itoa(int(f.Fd())))
	got, err := Listen("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer got.Close()
	if got.Addr().String() != ln.Addr().String() {
		t.Errorf("Listen took %s, want the inherited %s", got.Addr(), ln.Addr())
	}
	if os.Getenv(legacyListenEnv) != "" || os.Getenv(listenEnv) != "" {
		t.Error("the listener variables are left for the next exec")
	}

	Serving(ln, nil)
	t.Cleanup(func() { Serving(nil, nil) })
	old := execFn
	defer func() { execFn = old }()
	var names []string
	execFn = func(_ string, _, env []string) error {
		for _, e := range env {
			if k, _, _ := strings.Cut(e, "="); k == listenEnv || k == legacyListenEnv {
				names = append(names, k)
			}
		}
		return os.ErrPermission
	}
	_ = Restart("/unused", time.Second)
	if len(names) != 2 {
		t.Errorf("Restart passed %v, want both %s and %s", names, listenEnv, legacyListenEnv)
	}
	restarting.Store(false)
	stopped.Store(false)
}
