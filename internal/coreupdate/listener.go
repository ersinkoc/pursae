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
// behind a proxy). The fd travels in PURSAE_LISTEN_FD, and also in WPGO_LISTEN_FD: a rollback execs an
// older release, which only reads the old name, and an older release hands over in WPGO_LISTEN_FD.

const listenEnv = "PURSAE_LISTEN_FD"

// legacyListenEnv is the name releases before 0.5.0 use (see listenEnv).
const legacyListenEnv = "WPGO_LISTEN_FD"

var (
	mu         sync.Mutex
	listener   net.Listener
	drain      func(context.Context) error
	restarting atomic.Bool
	stopped    atomic.Bool
)

// Listen returns the socket inherited from the previous binary, or a new one on addr.
//
// When both variables are set and differ, WPGO_LISTEN_FD is the live one: a 0.5+ binary always sets
// both to the same fd, while a release before 0.5.0 sets only WPGO_LISTEN_FD and clears only that
// name, so a PURSAE_LISTEN_FD it inherited from a 0.5+ binary (an update that crossed 0.5.0 back and
// forth) is stale and may name a closed or reused descriptor.
func Listen(addr string) (net.Listener, error) {
	v := os.Getenv(listenEnv)
	if legacy := os.Getenv(legacyListenEnv); legacy != "" {
		v = legacy
	}
	_ = os.Unsetenv(listenEnv)
	_ = os.Unsetenv(legacyListenEnv)
	if v != "" {
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

// Stopped reports whether a Restart shut the HTTP server down. It stays true when the exec then
// failed: the server cannot serve again, so the caller must exit and let the supervisor restart
// the process (main is parked waiting for an exec that never happened).
func Stopped() bool { return stopped.Load() }

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
			defer f.Close() // exec inherits it on success; a failed exec must release it
			if err := keepOnExec(f.Fd()); err == nil {
				fd := strconv.Itoa(int(f.Fd()))
				env = append(withoutListenEnv(env), listenEnv+"="+fd, legacyListenEnv+"="+fd)
				restarting.Store(true)
				if stop != nil {
					ctx, cancel := context.WithTimeout(context.Background(), drainFor)
					_ = stop(ctx)
					cancel()
					stopped.Store(true)
				}
			}
		}
	}
	err := execBinary(exe, os.Args, env)
	restarting.Store(false)
	if err == nil {
		err = errors.New("exec returned")
	}
	return err
}

func withoutListenEnv(env []string) []string {
	out := env[:0:0]
	for _, e := range env {
		if !strings.HasPrefix(e, listenEnv+"=") && !strings.HasPrefix(e, legacyListenEnv+"=") {
			out = append(out, e)
		}
	}
	return out
}
