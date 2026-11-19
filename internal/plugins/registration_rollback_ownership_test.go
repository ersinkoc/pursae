package plugins

import (
	"context"
	"errors"
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
)

type gatedRollbackStarter struct {
	wazero.Runtime
	old     *Plugin
	stage   RegisteredHook
	calls   atomic.Int32
	entered chan struct{}
	release chan struct{}
}

func (r *gatedRollbackStarter) InstantiateModule(ctx context.Context, c wazero.CompiledModule, cfg wazero.ModuleConfig) (api.Module, error) {
	if r.calls.Add(1) == 1 {
		// Model a partial initializer registering a hook before failing.
		if err := r.old.registerHook(r.stage); err != nil {
			return nil, err
		}
		close(r.entered)
		<-r.release
		return nil, errors.New("injected initializer failure")
	}
	return r.Runtime.InstantiateModule(ctx, c, cfg)
}

func TestRollbackKeepsReplacementHooks(t *testing.T) {
	ctx := context.Background()
	control := newTestRuntime(t, Config{})
	host := NewMemoryHost()
	p := loadPlugin(t, control, "internal/plugins/testdata/probe", host)
	host.SetOption("probe_init_fail", true)
	p.lock()
	err := p.instantiate(ctx)
	p.unlock()
	if err == nil || !control.hooks.HasFilter("probe_echo") {
		t.Fatal("CONTROL FAILED: ordinary failed initialization lost registrations")
	}
	host.SetOption("probe_init_fail", false)
	if got := control.hooks.ApplyFilters(ctx, "probe_echo", "ok"); got != "ok!" {
		t.Fatalf("CONTROL FAILED: retry = %v", got)
	}

	rt := newTestRuntime(t, Config{InitTimeout: time.Minute})
	pkg := buildPackage(t, "internal/plugins/testdata/probe")
	old, err := rt.Load(ctx, pkg, pkg.Manifest.Permissions, NewMemoryHost())
	if err != nil {
		t.Fatal(err)
	}
	var stage RegisteredHook
	for _, h := range old.Hooks() {
		if h.Hook == "probe_echo" {
			stage = h
		}
	}
	if stage.Hook == "" {
		t.Fatal("CONTROL FAILED: no initial echo hook")
	}
	starter := &gatedRollbackStarter{Runtime: rt.wz, old: old, stage: stage, entered: make(chan struct{}), release: make(chan struct{})}
	rt.wz = starter
	old.lock()
	failed := make(chan error, 1)
	go func() { failed <- old.instantiate(ctx) }()
	<-starter.entered
	unloaded := make(chan error, 1)
	go func() { unloaded <- rt.Unload(ctx, pkg.Manifest.Slug) }()
	// The first unload stage removes the partial initializer's hook; the final
	// stage cannot run while the instance semaphore is held.
	for {
		_, exists := rt.Plugin(pkg.Manifest.Slug)
		if !exists && !rt.hooks.HasFilter("probe_echo") {
			break
		}
		runtime.Gosched()
	}
	replacement, loadErr := rt.Load(ctx, pkg, pkg.Manifest.Permissions, NewMemoryHost())
	before := loadErr == nil && rt.hooks.ApplyFilters(ctx, "probe_echo", "ok") == "ok!"
	close(starter.release)
	initErr := <-failed
	current, ok := rt.Plugin(pkg.Manifest.Slug)
	kept := ok && current == replacement && rt.hooks.ApplyFilters(ctx, "probe_echo", "ok") == "ok!"
	// Measure before allowing final unload cleanup, isolating initializer rollback.
	old.unlock()
	if err := <-unloaded; err != nil {
		t.Fatal(err)
	}
	if loadErr != nil || !before || initErr == nil {
		t.Fatalf("CONTROL FAILED: replacement=%v before=%v initializer=%v", loadErr, before, initErr)
	}
	if !kept {
		t.Fatal("old initializer rollback removed the replacement's callback")
	}
}
