package plugins

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"wpgo/internal/wp"
)

// Without content:html a shortcode's output and what a content filter adds go through KSES.
func TestOutputIsSanitizedWithoutContentHTML(t *testing.T) {
	rt := newTestRuntime(t, Config{})
	pkg := buildPackage(t, "internal/plugins/testdata/probe")
	var perms []Permission
	for _, p := range pkg.Manifest.Permissions {
		if p != PermContentHTML {
			perms = append(perms, p)
		}
	}
	p, err := rt.Load(context.Background(), pkg, perms, NewMemoryHost())
	if err != nil {
		t.Fatal(err)
	}
	got := rt.Hooks().ApplyFilters(context.Background(), "the_excerpt", `<form action="/x"><input name="q"></form><p>core</p>`).(string)
	if !strings.HasPrefix(got, `<form action="/x"><input name="q"></form><p>core</p>`) {
		t.Errorf("the core's markup before the change was touched: %q", got)
	}
	if strings.Contains(got, "<script") || strings.Contains(got, "onclick") || !strings.Contains(got, "<b>bold</b>") {
		t.Errorf("the plugin's markup was not sanitised: %q", got)
	}
	out, _ := rt.DoShortcode(context.Background(), "probe", map[string]string{"a": "1"}, "inner")
	if out != "" { // "<probe a=1|inner>" is one unknown tag: KSES drops it whole
		t.Errorf("shortcode output not sanitised: %q", out)
	}
	// Front-end scripts need content:html.
	if s := rt.EnqueuedAssets("front", "script"); len(s) != 0 {
		t.Errorf("front script enqueued without content:html: %+v", s)
	}
	if !logged(p, `"content:html"`) {
		t.Error("refused front script not logged")
	}

	// With content:html the output is the plugin's own.
	rt2 := newTestRuntime(t, Config{})
	loadPlugin(t, rt2, "internal/plugins/testdata/probe", NewMemoryHost())
	if got := rt2.Hooks().ApplyFilters(context.Background(), "the_excerpt", "x").(string); !strings.Contains(got, "<script>") {
		t.Errorf("content:html output was sanitised: %q", got)
	}
}

func TestAdminScriptNeedsAdminScripts(t *testing.T) {
	rt := newTestRuntime(t, Config{})
	p := loadPlugin(t, rt, "internal/plugins/testdata/probe", NewMemoryHost())
	if s := rt.EnqueuedAssets("admin", "script"); len(s) != 0 {
		t.Fatalf("admin script enqueued without admin:scripts: %+v", s)
	}
	if !logged(p, `"admin:scripts"`) {
		dumpLogs(t, p)
		t.Error("refused admin script not logged")
	}
	if !PermAdminScripts.Valid() || !PermAdminScripts.Dangerous() || !strings.Contains(PermAdminScripts.Description(), "full control") {
		t.Error("admin:scripts must be a dangerous permission with a full-control warning")
	}
}

func TestSanitizeChange(t *testing.T) {
	for _, c := range []struct{ in, out, want string }{
		{`<p>a</p>`, `<p>a</p><img src=x onerror=alert(1)>`, `<p>a</p><img src="x">`},
		{`<form><input></form>`, `<form><input></form><p>ok</p>`, `<form><input></form><p>ok</p>`},
		{`<a href="/x">t</a>`, `<a href="/x" onclick="e()">t</a>`, `<a href="/x" rel="nofollow">t</a>`},
		{`<p>one</p><p>two</p>`, `<p>one</p><iframe src=//e></iframe><p>two</p>`, `<p>one</p><p>two</p>`},
	} {
		got, ok := sanitizeChange(c.in, c.out)
		if !ok || got != c.want {
			t.Errorf("sanitizeChange(%q, %q) = %q, %v; want %q", c.in, c.out, got, ok, c.want)
		}
	}
	// A change inside a script cannot be sanitised as HTML: it is dropped.
	in := `<script>var a = 1;</script>`
	if got, ok := sanitizeChange(in, `<script>var a = 1;alert(2)</script>`); ok || got != in {
		t.Errorf("change inside a script kept: %q", got)
	}
	if got, ok := sanitizeChange(`<!-- x --><p>a</p>`, `<!-- x --><p>a</p><b>b</b>`); !ok || got != `<!-- x --><p>a</p><b>b</b>` {
		t.Errorf("closed comment before the change: %q %v", got, ok)
	}
	_ = wp.KsesPost
}

// A call that succeeds but uses most of its budget counts as a failure; a request that cannot get
// the instance in time gives up instead of queueing behind it.
func TestSlowCallsCountAndWaitersGiveUp(t *testing.T) {
	rt := newTestRuntime(t, Config{CallTimeout: 500 * time.Millisecond, MaxFailures: 2})
	p := loadPlugin(t, rt, "internal/plugins/testdata/probe", NewMemoryHost())
	hooks := rt.Hooks()

	done := make(chan any, 1)
	go func() { done <- hooks.ApplyFilters(context.Background(), "probe_slow", "v") }()
	time.Sleep(50 * time.Millisecond)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	start := time.Now()
	if got := hooks.ApplyFilters(ctx, "probe_echo", "w"); got != "w" {
		t.Errorf("a waiter whose deadline passed got %q", got)
	}
	cancel()
	if waited := time.Since(start); waited > 300*time.Millisecond {
		t.Errorf("waiter blocked %v despite its deadline", waited)
	}
	if got := <-done; got != "v slow" {
		t.Fatalf("slow call result %q", got)
	}
	if d, _ := p.Disabled(); d {
		t.Fatal("disabled after one slow call")
	}
	if got := hooks.ApplyFilters(context.Background(), "probe_slow", "v"); got != "v slow" {
		t.Fatalf("second slow call %q", got)
	}
	if d, reason := p.Disabled(); !d || !strings.Contains(reason, "budget") {
		dumpLogs(t, p)
		t.Fatalf("two slow calls in a row did not disable the plugin: %q", reason)
	}
}

func TestLockCtxBusy(t *testing.T) {
	rt := newTestRuntime(t, Config{CallTimeout: 30 * time.Millisecond})
	p := loadPlugin(t, rt, "internal/plugins/testdata/probe", NewMemoryHost())
	p.lock()
	defer p.unlock()
	if err := p.lockCtx(context.Background()); !errors.Is(err, errBusy) {
		t.Fatalf("lockCtx on a busy plugin: %v", err)
	}
}

// A re-instantiated guest registers its hooks again; hooks the new instance no longer registers
// must not stay behind.
func TestReinstantiationDropsStaleHooks(t *testing.T) {
	rt := newTestRuntime(t, Config{})
	host := NewMemoryHost()
	host.SetOption("probe_conditional", true)
	loadPlugin(t, rt, "internal/plugins/testdata/probe", host)
	hooks := rt.Hooks()
	if got := hooks.ApplyFilters(context.Background(), "probe_conditional", "v"); got != "v conditional" {
		t.Fatalf("conditional hook: %q", got)
	}
	host.SetOption("probe_conditional", false)
	hooks.ApplyFilters(context.Background(), "probe_exit", "v") // kills the instance
	hooks.ApplyFilters(context.Background(), "probe_echo", "v") // re-instantiates
	if hooks.HasFilter("probe_conditional") {
		t.Fatal("a hook the new instance did not register is still wired")
	}
	if !hooks.HasFilter("probe_echo") {
		t.Fatal("hooks lost after re-instantiation")
	}
}
