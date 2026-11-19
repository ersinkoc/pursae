package plugins

import (
	"context"
	"errors"
	"testing"

	"github.com/tetratelabs/wazero"
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

type pendingPluginCompiler struct {
	wazero.Runtime
	entered chan struct{}
	release chan struct{}
}

func (c *pendingPluginCompiler) CompileModule(ctx context.Context, wasm []byte) (wazero.CompiledModule, error) {
	close(c.entered)
	<-c.release
	return c.Runtime.CompileModule(ctx, wasm)
}

func TestUnloadAbandonsPendingPluginLoad(t *testing.T) {
	rt := newTestRuntime(t, Config{})
	pkg := buildPackage(t, "internal/plugins/testdata/probe")
	ctx := t.Context()
	gate := &pendingPluginCompiler{Runtime: rt.wz, entered: make(chan struct{}), release: make(chan struct{})}
	rt.wz = gate
	loaded := make(chan error, 1)
	go func() {
		_, err := rt.Load(ctx, pkg, pkg.Manifest.Permissions, NewMemoryHost())
		loaded <- err
	}()
	<-gate.entered
	unloadErr := rt.Unload(ctx, pkg.Manifest.Slug)
	close(gate.release)
	if err := <-loaded; !errors.Is(err, ErrNotLoaded) || unloadErr != nil {
		t.Fatalf("load error: %v, unload error: %v", err, unloadErr)
	}
	if len(rt.Plugins()) != 0 || rt.Hooks().HasFilter("probe_echo") {
		t.Fatal("the unloaded plugin left a reservation or hook")
	}
}
