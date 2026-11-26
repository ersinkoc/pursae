package server

import (
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"
)

// safeGoLog sends slog's default logger to a buffer for the rest of the test.
func safeGoLog(t *testing.T) *lockedBuffer {
	t.Helper()
	buf := &lockedBuffer{}
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(buf, nil)))
	t.Cleanup(func() { slog.SetDefault(old) })
	return buf
}

// waitForLog waits until the captured log holds every part.
func waitForLog(t *testing.T, buf *lockedBuffer, parts ...string) string {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		out := buf.String()
		all := true
		for _, p := range parts {
			all = all && strings.Contains(out, p)
		}
		if all {
			return out
		}
		if time.Now().After(deadline) {
			t.Fatalf("log never held %q:\n%s", parts, out)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// O10: a panic in a background goroutine is logged with its stack and does not end the process
// (without the recover the test binary itself would crash).
func TestSafeGoRecoversAndLogs(t *testing.T) {
	buf := safeGoLog(t)
	cleaned := make(chan struct{})
	safeGo("test.panics", func() {
		defer close(cleaned) // fn's deferred calls still run
		dims := []int{640}
		_ = dims[len(dims)] // index out of range: the runtime panic a malformed image or payload causes
	})
	select {
	case <-cleaned:
	case <-time.After(5 * time.Second):
		t.Fatal("the panicking goroutine's deferred call did not run")
	}
	out := waitForLog(t, buf, "background goroutine panicked", "goroutine=test.panics", "index out of range", "safego_test.go")
	if !strings.Contains(out, "stack=") {
		t.Errorf("no stack in the log:\n%s", out)
	}
	// The process goes on: another goroutine still runs.
	ran := make(chan struct{})
	safeGo("test.after", func() { close(ran) })
	<-ran
}

// goBackground recovers as well, and a loop that panicked still counts as returned for Close.
func TestGoBackgroundRecovers(t *testing.T) {
	buf := safeGoLog(t)
	s := &Server{}
	s.rt.startBackground(context.Background())
	s.goBackground(func(context.Context) { panic("loop failed") })
	waitForLog(t, buf, "background goroutine panicked", "loop failed", "TestGoBackgroundRecovers")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	s.stopBackground(ctx)
	if ctx.Err() != nil || strings.Contains(buf.String(), "still running at shutdown") {
		t.Errorf("Close waited for a loop that had panicked:\n%s", buf.String())
	}
}
