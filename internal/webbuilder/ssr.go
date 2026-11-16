package webbuilder

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"sync"
	"time"

	"github.com/dop251/goja"
	"golang.org/x/net/html"
)

// Server render: the editor's own renderStatic (upstream live/renderStatic, patch 0002), built by
// `make builder` into web/web-builder/render/render.js (builder/host/ssr.ts, one IIFE with React and a
// small prelude of the globals React's server build touches), runs here in an embedded JS engine. A
// page can then be published without a browser — Pursae MCP, REST documents/{id}/publish with
// "render": "server" — and the snapshot is the one the editor would have posted: same code, same
// RendererVersion. Remote SVGs whose colors the editor recolors (svgTint) keep their own colors, as
// renderStatic does wherever there is no DOM.

// RenderFile is the server render bundle under EmbedDir.
const RenderFile = "render/render.js"

// ErrNoServerRender means the build has no server render bundle (a dist from before it existed).
var ErrNoServerRender = errors.New("webbuilder: the server render bundle is missing (run make builder)")

// ServerRenderOptions are the host options of one render (contract RenderOptions).
type ServerRenderOptions struct {
	PostID int64 `json:"postId"`
	// SiteURL is the home URL without a trailing slash.
	SiteURL string `json:"siteUrl"`
	// FormActionPrefix + url-escaped section id is a form section's action.
	FormActionPrefix string `json:"formActionPrefix"`
	// Sample is the DynContext a template preview fills dynamic widgets with (nil = placeholders).
	Sample any `json:"sample,omitempty"`
}

// ServerRenderer runs the render bundle. Runtimes are pooled (a goja runtime serves one goroutine at a
// time); the compiled program is shared.
type ServerRenderer struct {
	prog    *goja.Program
	pool    chan *goja.Runtime
	timeout time.Duration

	catOnce sync.Once
	cat     json.RawMessage
	catErr  error

	lastUse atomicTime
}

// atomicTime is a time read and written from several goroutines.
type atomicTime struct {
	mu sync.Mutex
	t  time.Time
}

func (a *atomicTime) set(t time.Time) { a.mu.Lock(); a.t = t; a.mu.Unlock() }
func (a *atomicTime) get() time.Time  { a.mu.Lock(); defer a.mu.Unlock(); return a.t }

// reapIdle drops the JS runtimes (about 7.5 MB each) after idle without a render; the next render
// creates one again (a few ms, the program stays compiled).
func (r *ServerRenderer) reapIdle(every, idle time.Duration) {
	for range time.Tick(every) {
		if last := r.lastUse.get(); last.IsZero() || time.Since(last) < idle {
			continue
		}
		n := cap(r.pool)
		for i := 0; i < n; i++ {
			select {
			case <-r.pool:
				r.pool <- nil
			default:
			}
		}
	}
}

var (
	ssrOnce sync.Once
	ssrInst *ServerRenderer
	ssrErr  error
)

// SharedServerRenderer loads the bundle from webFS once per process.
func SharedServerRenderer(webFS fs.FS) (*ServerRenderer, error) {
	ssrOnce.Do(func() {
		b, err := fs.ReadFile(webFS, EmbedDir+"/"+RenderFile)
		if err != nil {
			ssrErr = ErrNoServerRender
			return
		}
		ssrInst, ssrErr = NewServerRenderer(b, 2)
		if ssrErr == nil {
			go ssrInst.reapIdle(time.Minute, 5*time.Minute)
		}
	})
	return ssrInst, ssrErr
}

// NewServerRenderer compiles a render bundle; size is the number of pooled runtimes.
func NewServerRenderer(bundle []byte, size int) (*ServerRenderer, error) {
	prog, err := goja.Compile(RenderFile, string(bundle), false)
	if err != nil {
		return nil, fmt.Errorf("webbuilder: compile server render bundle: %w", err)
	}
	if size < 1 {
		size = 1
	}
	r := &ServerRenderer{prog: prog, pool: make(chan *goja.Runtime, size), timeout: 20 * time.Second}
	for i := 0; i < size; i++ {
		r.pool <- nil // runtimes are created lazily
	}
	return r, nil
}

func (r *ServerRenderer) runtime() (*goja.Runtime, error) {
	vm := goja.New()
	// rich.ts sanitizes with the DOM; without one the bundle asks the host (builder/scripts/live.mjs).
	_ = vm.Set("__wbHostRich", func(src string, lists bool) string { return SanitizeRichLists(src, lists) })
	_ = vm.Set("__wbHostDecode", html.UnescapeString)
	if _, err := vm.RunProgram(r.prog); err != nil {
		return nil, fmt.Errorf("webbuilder: load server render bundle: %w", err)
	}
	if _, ok := goja.AssertFunction(vm.Get("__wbRender")); !ok {
		return nil, errors.New("webbuilder: the server render bundle defines no __wbRender")
	}
	return vm, nil
}

// Render draws doc (the DocState JSON) like the editor's publish does.
func (r *ServerRenderer) Render(doc []byte, opts ServerRenderOptions) (*RenderResult, error) {
	optsJSON, _ := json.Marshal(opts)
	out, err := r.invoke("__wbRender", string(doc), string(optsJSON))
	if err != nil {
		return nil, err
	}
	var rr RenderResult
	if err := json.Unmarshal([]byte(out), &rr); err != nil {
		return nil, fmt.Errorf("webbuilder: server render result: %w", err)
	}
	return &rr, nil
}

// Catalog is what the builder can draw (bundle __wbCatalog): section kinds with fields, item lists,
// options, designs and defaults, plus palettes, font pairs, corners, icons and layout structures.
func (r *ServerRenderer) Catalog() (json.RawMessage, error) {
	r.catOnce.Do(func() {
		out, err := r.invoke("__wbCatalog")
		r.cat, r.catErr = json.RawMessage(out), err
	})
	return r.cat, r.catErr
}

// CreateSection builds one complete section from a short spec (bundle __wbCreate): the editor's own
// defaults for the kind and design, the spec's content, items and style on top, ids filled in.
func (r *ServerRenderer) CreateSection(spec []byte) (json.RawMessage, error) {
	out, err := r.invoke("__wbCreate", string(spec))
	if err != nil {
		return nil, err
	}
	return json.RawMessage(out), nil
}

// ApplyDesign switches a section to another design of its kind (bundle __wbDesign, upstream
// applyVariant): content and items are kept, the design's presets replace the old design's.
func (r *ServerRenderer) ApplyDesign(section []byte, design string) (json.RawMessage, error) {
	out, err := r.invoke("__wbDesign", string(section), design)
	if err != nil {
		return nil, err
	}
	return json.RawMessage(out), nil
}

// invoke calls a global function of the bundle with string arguments on a pooled runtime.
func (r *ServerRenderer) invoke(name string, args ...string) (string, error) {
	r.lastUse.set(time.Now())
	vm := <-r.pool
	if vm == nil {
		var err error
		if vm, err = r.runtime(); err != nil {
			r.pool <- nil
			return "", err
		}
	}
	timer := time.AfterFunc(r.timeout, func() { vm.Interrupt("timeout") })
	out, err := r.call(vm, name, args)
	if !timer.Stop() {
		vm = nil // interrupted: never reuse a runtime stopped mid-run
	}
	if err != nil {
		var ex *goja.Exception
		if errors.As(err, &ex) {
			vm = nil // a throw can leave module state half-updated; start the next render clean
		}
	}
	r.pool <- vm
	return out, err
}

func (r *ServerRenderer) call(vm *goja.Runtime, name string, args []string) (out string, err error) {
	defer func() {
		if p := recover(); p != nil {
			err = fmt.Errorf("webbuilder: server render panicked: %v", p)
		}
	}()
	fn, ok := goja.AssertFunction(vm.Get(name))
	if !ok {
		return "", fmt.Errorf("webbuilder: the server bundle has no %s (run make builder)", name)
	}
	vals := make([]goja.Value, len(args))
	for i, a := range args {
		vals[i] = vm.ToValue(a)
	}
	v, err := fn(goja.Undefined(), vals...)
	if err != nil {
		var ie *goja.InterruptedError
		if errors.As(err, &ie) {
			return "", errors.New("webbuilder: server render timed out")
		}
		var ex *goja.Exception
		if errors.As(err, &ex) {
			return "", fmt.Errorf("webbuilder: %s", ex.Value().String())
		}
		return "", fmt.Errorf("webbuilder: server render: %w", err)
	}
	return v.String(), nil
}
