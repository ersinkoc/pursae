package server

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"
)

// Background work a request starts but does not wait for (404 log rows, redirect hit counters,
// "last used" stamps, CDN purges) goes through bounded queues instead of a goroutine per request:
// a burst of requests cannot pile up goroutines and pool connections, and shutdown can drain
// what is queued (Server.Close).

// runtimeState is the Server's background machinery; its zero value is ready to use.
type runtimeState struct {
	writes asyncWriter
	cdn    cdnPurgeQueue

	// The long-lived loops New starts (plugin sync, Pursae CDN housekeeping) run under bgCtx, a
	// child of New's ctx that Close cancels, and are tracked by bg: a Server owns their lifetime,
	// so closing it stops them even when the caller's ctx never ends (tests build one Server per
	// test in a single process; main's Shutdown path without a signal).
	bgCtx    context.Context
	bgCancel context.CancelFunc
	bg       sync.WaitGroup
}

// startBackground derives the context the Server's long-lived loops run under (see runtimeState).
func (r *runtimeState) startBackground(ctx context.Context) {
	r.bgCtx, r.bgCancel = context.WithCancel(ctx)
}

// goBackground runs fn on a goroutine Close waits for; fn must return once ctx ends.
func (s *Server) goBackground(fn func(ctx context.Context)) {
	ctx := s.rt.bgCtx
	if ctx == nil { // not started through New (a sandbox, a bare test Server): nothing owns it
		return
	}
	s.rt.bg.Add(1)
	go func() {
		defer s.rt.bg.Done()
		fn(ctx)
	}()
}

// stopBackground cancels the long-lived loops and waits for them to return, or until ctx ends.
func (s *Server) stopBackground(ctx context.Context) {
	if s.rt.bgCancel == nil {
		return
	}
	s.rt.bgCancel()
	done := make(chan struct{})
	go func() {
		s.rt.bg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-ctx.Done():
		slog.Warn("background loops still running at shutdown")
	}
}

// WatchSettings keeps the settings cache in step with the other instances sharing the database
// until ctx ends (settings.Store.Watch). main runs it for `wpgo serve`.
func (s *Server) WatchSettings(ctx context.Context) {
	s.settings.Watch(ctx)
}

// Close stops the long-lived loops, drains the background queues (or gives up when ctx ends) and
// releases the plugin runtime. main calls it after the HTTP server and the scheduler stopped,
// before closing the database pool. The loops stop first so the plugin sync loop cannot touch
// the runtime after it is released.
func (s *Server) Close(ctx context.Context) {
	s.stopBackground(ctx)
	s.rt.cdn.flush(ctx, s)
	s.pllCaptureFlush(ctx) // Multi Language strings still queued (golylang_capture.go)
	s.rt.writes.close(ctx)
	if s.plugins != nil && s.plugins.rt != nil {
		_ = s.plugins.rt.Close(ctx) // wazero runtime and its in-memory compilation cache
	}
}

// asyncExec runs a fire-and-forget statement on the bounded writer: when the queue is full the
// statement is dropped (and counted) rather than blocking the request.
func (s *Server) asyncExec(sql string, args ...any) {
	s.rt.writes.submit(func(ctx context.Context) {
		if _, err := s.store.Pool.Exec(ctx, sql, args...); err != nil && ctx.Err() == nil {
			slog.Warn("background write", "err", err)
		}
	})
}

const (
	asyncQueueSize = 1024
	asyncWorkers   = 2
	asyncTimeout   = 5 * time.Second
)

// asyncWriter is a buffered queue served by a few workers, started on first use.
type asyncWriter struct {
	once    sync.Once
	ch      chan func(context.Context)
	stop    chan struct{}
	wg      sync.WaitGroup
	closed  atomic.Bool
	dropped atomic.Int64
}

func (a *asyncWriter) start() {
	a.once.Do(func() {
		a.ch = make(chan func(context.Context), asyncQueueSize)
		a.stop = make(chan struct{})
		for range asyncWorkers {
			a.wg.Add(1)
			go a.work()
		}
	})
}

func (a *asyncWriter) work() {
	defer a.wg.Done()
	for {
		select {
		case fn := <-a.ch:
			a.run(fn)
		case <-a.stop:
			for { // drain what was queued before close
				select {
				case fn := <-a.ch:
					a.run(fn)
				default:
					return
				}
			}
		}
	}
}

func (a *asyncWriter) run(fn func(context.Context)) {
	defer func() {
		if r := recover(); r != nil {
			slog.Error("background write panicked", "panic", r)
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), asyncTimeout)
	defer cancel()
	fn(ctx)
}

// submit queues fn; it reports false when fn was dropped (queue full or writer closed).
func (a *asyncWriter) submit(fn func(context.Context)) bool {
	if a.closed.Load() {
		a.drop()
		return false
	}
	a.start()
	select {
	case a.ch <- fn:
		return true
	default:
		a.drop()
		return false
	}
}

func (a *asyncWriter) drop() {
	if n := a.dropped.Add(1); n == 1 || n%1000 == 0 {
		slog.Warn("background write queue full: dropping writes", "dropped", n)
	}
}

// Dropped is how many writes were dropped so far.
func (a *asyncWriter) Dropped() int64 { return a.dropped.Load() }

// close stops accepting work and waits (until ctx ends) for the queued writes.
func (a *asyncWriter) close(ctx context.Context) {
	if a.closed.Swap(true) {
		return
	}
	a.start()
	close(a.stop)
	done := make(chan struct{})
	go func() {
		a.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-ctx.Done():
		slog.Warn("shutdown: background writes still running")
	}
}
