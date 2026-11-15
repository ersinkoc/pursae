package plugins

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

func TestMemoryTransientGetReturnsIndependentBytes(t *testing.T) {
	h := NewMemoryHost()
	ctx := context.Background()
	if err := h.TransientSet(ctx, "p", "x", json.RawMessage(`{"value":1}`), time.Minute); err != nil {
		t.Fatal(err)
	}
	v, ok, err := h.TransientGet(ctx, "p", "x")
	if err != nil || !ok {
		t.Fatalf("get: %v %v", ok, err)
	}
	release, done := make(chan struct{}), make(chan struct{})
	go func() { <-release; v[9] = '9'; close(done) }()
	close(release)
	<-done
	got, ok, err := h.TransientGet(ctx, "p", "x")
	if err != nil || !ok || string(got) != `{"value":1}` {
		t.Fatalf("stored value=%s found=%v err=%v", got, ok, err)
	}
}
