package plugins

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/tetratelabs/wazero"
)

type gatedLoadCompiler struct {
	wazero.Runtime
	calls   atomic.Int32
	entered chan struct{}
	release chan struct{}
}

func (r *gatedLoadCompiler) CompileModule(ctx context.Context, wasm []byte) (wazero.CompiledModule, error) {
	if r.calls.Add(1) == 1 {
		close(r.entered)
		<-r.release
		return nil, errors.New("injected first compilation failure")
	}
	return r.Runtime.CompileModule(ctx, wasm)
}

func TestFailedLoadKeepsReplacement(t *testing.T) {
	ctx := context.Background()
	rt := newTestRuntime(t, Config{})
	pkg := buildPackage(t, "internal/plugins/testdata/probe")
	// Nearby control: a failed load with no replacement cleans its own reservation.
	bad := *pkg
	bad.Wasm = []byte("invalid module")
	if _, err := rt.Load(ctx, &bad, pkg.Manifest.Permissions, NewMemoryHost()); err == nil {
		t.Fatal("CONTROL FAILED: invalid module loaded")
	}
	if _, ok := rt.Plugin(pkg.Manifest.Slug); ok {
		t.Fatal("CONTROL FAILED: failed load left a reservation")
	}

	compiler := &gatedLoadCompiler{Runtime: rt.wz, entered: make(chan struct{}), release: make(chan struct{})}
	rt.wz = compiler
	stale := make(chan error, 1)
	completed := make(chan struct{})
	go func() {
		defer close(completed)
		_, err := rt.Load(ctx, pkg, pkg.Manifest.Permissions, NewMemoryHost())
		stale <- err
	}()
	defer func() {
		select {
		case <-compiler.release:
		default:
			close(compiler.release)
		}
		<-completed
	}()
	<-compiler.entered
	if err := rt.Unload(ctx, pkg.Manifest.Slug); err != nil {
		close(compiler.release)
		t.Fatal(err)
	}
	replacement, err := rt.Load(ctx, pkg, pkg.Manifest.Permissions, NewMemoryHost())
	if err != nil {
		close(compiler.release)
		<-stale
		t.Fatal(err)
	}
	if !rt.hooks.HasFilter("probe_echo") {
		t.Fatal("CONTROL FAILED: replacement registered no echo hook")
	}
	close(compiler.release)
	if err := <-stale; err == nil {
		t.Fatal("CONTROL FAILED: injected compilation failure was lost")
	}
	current, ok := rt.Plugin(pkg.Manifest.Slug)
	kept := ok && current == replacement && rt.hooks.HasFilter("probe_echo")
	if !kept {
		replacement.shutdown(ctx, "proof cleanup")
		if replacement.compiled != nil {
			_ = replacement.compiled.Close(ctx)
		}
		t.Fatal("stale load cleanup removed the replacement plugin or its hooks")
	}
	if got := rt.hooks.ApplyFilters(ctx, "probe_echo", "ok"); got != "ok!" {
		t.Fatalf("replacement callback = %v, want ok!", got)
	}
}
