package plugins

import (
	"context"
	"runtime"
	"testing"
)

func replacementAfterUnload(t *testing.T, overlap bool) bool {
	t.Helper()
	ctx := context.Background()
	rt := newTestRuntime(t, Config{})
	pkg := buildPackage(t, "internal/plugins/testdata/probe")
	old, err := rt.Load(ctx, pkg, pkg.Manifest.Permissions, NewMemoryHost())
	if err != nil {
		t.Fatal(err)
	}
	if overlap {
		// This semaphore is the barrier an active guest call would hold.
		old.lock()
		finished := make(chan error, 1)
		go func() { finished <- rt.Unload(ctx, pkg.Manifest.Slug) }()
		// Observe the first cleanup stage before starting the replacement. The old
		// shutdown cannot complete its second stage until this test unlocks it.
		for {
			_, exists := rt.Plugin(pkg.Manifest.Slug)
			if !exists && !rt.hooks.HasFilter("probe_echo") {
				break
			}
			runtime.Gosched()
		}
		replacement, loadErr := rt.Load(ctx, pkg, pkg.Manifest.Permissions, NewMemoryHost())
		old.unlock()
		if err := <-finished; err != nil {
			t.Fatal(err)
		}
		if loadErr != nil {
			t.Fatal(loadErr)
		}
		current, ok := rt.Plugin(pkg.Manifest.Slug)
		return ok && current == replacement && rt.hooks.ApplyFilters(ctx, "probe_echo", "ok") == "ok!"
	}
	if err := rt.Unload(ctx, pkg.Manifest.Slug); err != nil {
		t.Fatal(err)
	}
	_, err = rt.Load(ctx, pkg, pkg.Manifest.Permissions, NewMemoryHost())
	if err != nil {
		t.Fatal(err)
	}
	return rt.hooks.ApplyFilters(ctx, "probe_echo", "ok") == "ok!"
}

func TestUnloadKeepsReplacementHooks(t *testing.T) {
	if !replacementAfterUnload(t, false) {
		t.Fatal("CONTROL FAILED: ordinary unload/reload broke the callback")
	}
	got := replacementAfterUnload(t, true)
	if !got {
		t.Fatal("old unload removed the replacement plugin's callback")
	}
}
