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
	t.Setenv("PURSAE_COREUPDATE_TEST_SETTING", "kept")
	var names []string
	mirrored := false
	execFn = func(_ string, _, env []string) error {
		for _, e := range env {
			if k, _, _ := strings.Cut(e, "="); k == listenEnv || k == legacyListenEnv {
				names = append(names, k)
			}
			mirrored = mirrored || e == "WPGO_COREUPDATE_TEST_SETTING=kept"
		}
		return os.ErrPermission
	}
	_ = Restart("/unused", time.Second)
	if len(names) != 2 {
		t.Errorf("Restart passed %v, want both %s and %s", names, listenEnv, legacyListenEnv)
	}
	// The binary Restart runs may be a release before 0.5.0 (a rollback): it gets every setting
	// under the WPGO_ name too.
	if !mirrored {
		t.Error("Restart did not pass PURSAE_COREUPDATE_TEST_SETTING as WPGO_COREUPDATE_TEST_SETTING")
	}
	restarting.Store(false)
	stopped.Store(false)
}

// An update that crossed 0.5.0 back and forth: the release before 0.5.0 inherited PURSAE_LISTEN_FD
// from a 0.5+ binary, cleared only WPGO_LISTEN_FD, and hands over its socket in WPGO_LISTEN_FD. The
// stale PURSAE_LISTEN_FD names another descriptor; Listen must take the WPGO_ one.
func TestListenStaleNewName(t *testing.T) {
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
	// The stale fd is a live listener on another port, so taking it would be visible.
	other, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	of, err := other.(*net.TCPListener).File()
	if err != nil {
		t.Fatal(err)
	}
	defer of.Close()
	t.Setenv(listenEnv, strconv.Itoa(int(of.Fd())))
	t.Setenv(legacyListenEnv, strconv.Itoa(int(f.Fd())))
	got, err := Listen("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer got.Close()
	if got.Addr().String() != ln.Addr().String() {
		t.Errorf("Listen took %s, want the socket in %s (%s)", got.Addr(), legacyListenEnv, ln.Addr())
	}
}

// Every exec path (Guard's rollback, Launch, the CLI's Exec) passes the WPGO_ names too.
func TestExecPassesLegacyNames(t *testing.T) {
	old := execFn
	defer func() { execFn = old }()
	t.Setenv("PURSAE_COREUPDATE_TEST_SETTING", "v")
	var env []string
	execFn = func(_ string, _, e []string) error { env = e; return nil }
	if err := Exec("/unused", []string{"x"}); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, e := range env {
		found = found || e == "WPGO_COREUPDATE_TEST_SETTING=v"
	}
	if !found {
		t.Error("Exec did not pass WPGO_COREUPDATE_TEST_SETTING")
	}
}
