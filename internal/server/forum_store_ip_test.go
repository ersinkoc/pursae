package server

import (
	"bytes"
	"context"
	"log"
	"strings"
	"sync"
	"testing"
	"time"
)

// lockedBuffer is a log destination the test can read while other goroutines log.
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func (b *lockedBuffer) Reset() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.buf.Reset()
}

// forumIPOf stays quiet for a missing post or a cancelled request, but logs a real fault (a timeout)
// instead of silently returning the same "" as "no IP stored".
func TestForumIPOfErrors(t *testing.T) {
	_, _, srv := testServerBuilderOff(t)

	var buf lockedBuffer
	prev := log.Writer()
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(prev) })

	// Only forumIPOf's own lines count: the server's start-up work (the search stemming rebuild)
	// logs from its goroutines and may land in buf while the test runs.
	forumLog := func() bool { return strings.Contains(buf.String(), "forum: ip of post") }
	if ip := srv.forumIPOf(context.Background(), 987654321); ip != "" || forumLog() {
		t.Errorf("nonexistent post: ip=%q log=%q", ip, buf.String())
	}
	buf.Reset()

	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if ip := srv.forumIPOf(cancelled, 1); ip != "" || forumLog() {
		t.Errorf("cancelled context: ip=%q log=%q", ip, buf.String())
	}
	buf.Reset()

	expired, cancel2 := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel2()
	if ip := srv.forumIPOf(expired, 1); ip != "" || !strings.Contains(buf.String(), "forum: ip of post 1") {
		t.Errorf("expired deadline: ip=%q log=%q", ip, buf.String())
	}
}
