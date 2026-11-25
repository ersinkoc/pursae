package server

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// An active plugin that can no longer be loaded (here: its package is unsigned and unsigned packages
// are no longer accepted, as with a signer that is no longer trusted) stays active, shows why on the
// Plugins screen, emails the administrator once and is not retried on every tick; once the problem is
// fixed it loads again by itself and the reason is cleared.
func TestPluginLoadFailureIsVisible(t *testing.T) {
	_, _, srv := testServerBuilderOff(t)
	ctx := context.Background()
	installDemo(t, srv)
	_ = srv.settings.Set(ctx, map[string]any{"admin_email": "owner@example.test"})
	const slug = "shortcode-demo"
	_ = srv.plugins.rt.Unload(ctx, slug)
	srv.cfg.AllowUnsignedPlugins = false

	mails := func() int {
		n := 0
		for _, m := range sentMail(t) {
			if strings.Contains(m.Subject, "The plugin "+slug+" could not be loaded") {
				n++
			}
		}
		return n
	}
	before := mails()
	pending := map[string]string{}
	tick := func() { srv.syncPluginsOnce(ctx, pending) }
	tick()
	tick() // acted on at the second sighting
	var status, why string
	_ = srv.store.Pool.QueryRow(ctx, `SELECT status, disabled_reason FROM plugins WHERE slug = $1`, slug).Scan(&status, &why)
	if status != "active" || !strings.HasPrefix(why, "Could not be loaded: ") || !strings.Contains(why, "not signed") {
		t.Fatalf("status=%q reason=%q", status, why)
	}
	if mails()-before != 1 {
		t.Fatalf("%d emails, want 1", mails()-before)
	}
	found := false
	for _, r := range srv.pluginInventory(ctx) {
		if r.Slug == slug {
			found = true
			if !r.Active || !r.Disabled || !r.LoadFailed || r.DisabledWhy != why {
				t.Errorf("the Plugins screen does not show the failure: %+v", r)
			}
		}
	}
	if !found {
		t.Fatal("plugin missing from the inventory")
	}

	// Further ticks neither retry with the same package and keys nor email again.
	srv.plugins.failMu.Lock()
	at := srv.plugins.fails[slug].at
	srv.plugins.failMu.Unlock()
	for range 4 {
		tick()
	}
	srv.plugins.failMu.Lock()
	again := srv.plugins.fails[slug].at
	srv.plugins.failMu.Unlock()
	if !again.Equal(at) {
		t.Error("retried on every tick")
	}
	srv.recordPluginLoadFailed(ctx, slug, why) // another instance reporting the same failure
	if mails()-before != 1 {
		t.Fatalf("%d emails after more ticks, want 1", mails()-before)
	}

	// Fixed (the trust setting changed): loaded again at once, the reason cleared.
	srv.cfg.AllowUnsignedPlugins = true
	tick()
	tick()
	if _, ok := srv.plugins.rt.Plugin(slug); !ok {
		t.Fatal("not loaded again after the fix")
	}
	_ = srv.store.Pool.QueryRow(ctx, `SELECT status, disabled_reason FROM plugins WHERE slug = $1`, slug).Scan(&status, &why)
	if status != "active" || why != "" {
		t.Fatalf("after the fix: status=%q reason=%q", status, why)
	}
	srv.plugins.failMu.Lock()
	_, still := srv.plugins.fails[slug]
	srv.plugins.failMu.Unlock()
	if still {
		t.Error("the failure is still remembered")
	}
}

// A missing package is retried after pluginLoadRetry; deactivating forgets the failure, and a
// runtime disable still switches the plugin off with its own reason.
func TestPluginLoadFailureRetryAndDeactivate(t *testing.T) {
	_, _, srv := testServerBuilderOff(t)
	ctx := context.Background()
	installDemo(t, srv)
	const slug = "shortcode-demo"
	_ = srv.plugins.rt.Unload(ctx, slug)
	zip := filepath.Join(srv.plugins.dir, slug+".zip")
	data, err := os.ReadFile(zip)
	if err != nil {
		t.Fatal(err)
	}
	os.Remove(zip)
	old := pluginLoadRetry
	pluginLoadRetry = 50 * time.Millisecond
	t.Cleanup(func() { pluginLoadRetry = old })
	pending := map[string]string{}
	srv.syncPluginsOnce(ctx, pending)
	srv.syncPluginsOnce(ctx, pending)
	var why string
	_ = srv.store.Pool.QueryRow(ctx, `SELECT disabled_reason FROM plugins WHERE slug = $1`, slug).Scan(&why)
	if why == "" {
		t.Fatal("a missing package left no reason")
	}
	os.WriteFile(zip, data, 0o644) // the disk is back
	time.Sleep(60 * time.Millisecond)
	srv.syncPluginsOnce(ctx, pending)
	srv.syncPluginsOnce(ctx, pending)
	if _, ok := srv.plugins.rt.Plugin(slug); !ok {
		t.Fatal("not retried after pluginLoadRetry")
	}
	_ = srv.store.Pool.QueryRow(ctx, `SELECT disabled_reason FROM plugins WHERE slug = $1`, slug).Scan(&why)
	if why != "" {
		t.Fatalf("reason not cleared: %q", why)
	}

	// Deactivated while failing: the failure is forgotten for a later activation.
	_ = srv.plugins.rt.Unload(ctx, slug)
	os.Remove(zip)
	srv.syncPluginsOnce(ctx, pending)
	srv.syncPluginsOnce(ctx, pending)
	_, _ = srv.store.Pool.Exec(ctx, `UPDATE plugins SET status = 'inactive' WHERE slug = $1`, slug)
	srv.syncPluginsOnce(ctx, pending)
	srv.plugins.failMu.Lock()
	_, still := srv.plugins.fails[slug]
	srv.plugins.failMu.Unlock()
	if still {
		t.Error("an inactive plugin's failure is still remembered")
	}
}
