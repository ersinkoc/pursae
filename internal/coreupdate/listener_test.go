package coreupdate

import (
	"context"
	"net"
	"os"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

// Restart hands over a duplicate of the listening socket: after the server's own listener is
// closed (drained), a connection made in between is still accepted through the inherited fd.
func TestRestartHandsOverTheSocket(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix only")
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	drained := false
	Serving(ln, func(context.Context) error { drained = true; return ln.Close() })
	var env []string
	old := execFn
	execFn = func(path string, argv, e []string) error { env = e; return nil }
	defer func() { execFn = old }()

	_ = Restart("/bin/true", time.Second)
	if !drained {
		t.Fatal("requests in flight were not drained")
	}
	if !Stopped() {
		t.Error("a failed exec after the drain does not report the server as stopped")
	}
	stopped.Store(false)
	var fdv string
	for _, e := range env {
		if v, ok := strings.CutPrefix(e, listenEnv+"="); ok {
			fdv = v
		}
	}
	if fdv == "" {
		t.Fatal("no " + listenEnv + " for the new binary")
	}
	// A visitor arrives while the new binary starts: the kernel queues the connection.
	conn, err := net.DialTimeout("tcp", addr, 2*time.Second)
	if err != nil {
		t.Fatalf("connection refused during the restart: %v", err)
	}
	defer conn.Close()
	// The new binary picks the socket up from the environment.
	t.Setenv(listenEnv, fdv)
	got, err := Listen("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer got.Close()
	if got.Addr().String() != addr {
		t.Fatalf("inherited %s, want %s", got.Addr(), addr)
	}
	got.(*net.TCPListener).SetDeadline(time.Now().Add(2 * time.Second))
	c, err := got.Accept()
	if err != nil {
		t.Fatalf("the queued connection was not accepted: %v", err)
	}
	c.Close()
	if os.Getenv(listenEnv) != "" {
		t.Error(listenEnv + " is left for the next exec")
	}
	if fd, _ := strconv.Atoi(fdv); fd <= 2 {
		t.Errorf("odd fd %d", fd)
	}
}

// Without a registered TCP listener nothing is shut down, so a failed exec leaves the server serving.
func TestRestartFailureWithoutListenerKeepsServing(t *testing.T) {
	Serving(nil, nil)
	stopped.Store(false)
	old := execFn
	execFn = func(string, []string, []string) error { return os.ErrPermission }
	defer func() { execFn = old }()
	if err := Restart("/bin/true", time.Second); err == nil {
		t.Fatal("no error")
	}
	if Stopped() || Restarting() {
		t.Error("server reported stopped although it never was")
	}
}
