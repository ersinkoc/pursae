package plugins

import (
	"context"
	"testing"
)

// Server.Close releases the plugin runtime, and a test (or a failed start-up followed by the
// deferred shutdown) may close the same Server twice: the second Close must be a no-op.
func TestRuntimeCloseIdempotent(t *testing.T) {
	ctx := context.Background()
	rt, err := NewRuntime(ctx, NewRegistry(), Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := rt.Close(ctx); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	if err := rt.Close(ctx); err != nil {
		t.Fatalf("second Close: %v", err)
	}
}
