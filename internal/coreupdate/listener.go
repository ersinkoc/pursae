package coreupdate

import (
	"context"
	"errors"
	"net"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Seamless restarts: the listening socket is handed to the new binary across exec, so the kernel
// queues the connections that arrive while it starts instead of refusing them (no "Bad Gateway"
// behind a proxy). The fd travels in WPGO_LISTEN_FD.

const listenEnv = "WPGO_LISTEN_FD"

var (
	mu         sync.Mutex
	listener   net.Listener
	drain      func(context.Context) error
	restarting atomic.Bool
)

// Listen returns the socket inherited from the previous binary, or a new one on addr.
func Listen(addr string) (net.Listener, error) {
	if v := os.Getenv(listenEnv); v != "" {
		_ = os.Unsetenv(listenEnv)
		if fd, err := strconv.Atoi(v); err == nil && fd > 2 {
			f := os.NewFile(uintptr(fd), "listener")
			ln, err := net.FileListener(f)
			f.Close() // FileListener dups it
			if err == nil {
				return ln, nil
			}
		}
	}
	return net.Listen("tcp", addr)
}

// Serving registers the server's listener and a function that stops it gracefully (http.Server
// Shutdown): a restart hands the socket over and lets requests in flight finish first.
func Serving(ln net.Listener, shutdown func(context.Context) error) {
	mu.Lock()
	listener, drain = ln, shutdown
	mu.Unlock()
}

// Restarting reports whether the server stopped because the process is about to re-exec; main
// then waits instead of exiting.
func Restarting() bool { return restarting.Load() }

// Restart re-executes exe with the listening socket inherited (when Serving registered one): it
// drains requests in flight for up to drainFor, then execs. It returns only on error.
func Restart(exe string, drainFor time.Duration) error {
	env := os.Environ()
	mu.Lock()
	ln, stop := listener, drain
	mu.Unlock()
	if tl, ok := ln.(*net.TCPListener); ok {
		f, err := tl.File() // a duplicate: closing the server's listener leaves the socket open
		if err == nil {
			if err := keepOnExec(f.Fd()); err == nil {
				env = append(withoutListenEnv(env), listenEnv+"="+strconv.Itoa(int(f.Fd())))
				restarting.Store(true)
				if stop != nil {
					ctx, cancel := context.WithTimeout(context.Background(), drainFor)
					_ = stop(ctx)
					cancel()
				}
			}
		}
	}
	err := execFn(exe, os.Args, env)
	restarting.Store(false)
	if err == nil {
		err = errors.New("exec returned")
	}
	return err
}

func withoutListenEnv(env []string) []string {
	out := env[:0:0]
	for _, e := range env {
		if !strings.HasPrefix(e, listenEnv+"=") {
			out = append(out, e)
		}
	}
	return out
}
