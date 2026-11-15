package server

import (
	"bytes"
	"context"
	"log"
	"strings"
	"testing"
	"time"
)

// forumIPOf stays quiet for a missing post or a cancelled request, but logs a real fault (a timeout)
// instead of silently returning the same "" as "no IP stored".
func TestForumIPOfErrors(t *testing.T) {
	_, _, srv := testServerBuilderOff(t)

	var buf bytes.Buffer
	prev := log.Writer()
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(prev) })

	if ip := srv.forumIPOf(context.Background(), 987654321); ip != "" || buf.Len() != 0 {
		t.Errorf("nonexistent post: ip=%q log=%q", ip, buf.String())
	}
	buf.Reset()

	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if ip := srv.forumIPOf(cancelled, 1); ip != "" || buf.Len() != 0 {
		t.Errorf("cancelled context: ip=%q log=%q", ip, buf.String())
	}
	buf.Reset()

	expired, cancel2 := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel2()
	if ip := srv.forumIPOf(expired, 1); ip != "" || !strings.Contains(buf.String(), "forum: ip of post 1") {
		t.Errorf("expired deadline: ip=%q log=%q", ip, buf.String())
	}
}
