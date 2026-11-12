//go:build !(wasip1 && wasm)

package wpgo

import (
	"encoding/json"
	"sync"
)

var (
	testHostMu sync.RWMutex
	testHost   func(name string, body []byte) ([]byte, error)
)

// SetHostForTest routes every host function call of a non-Wasm build to fn, which receives the
// import name and the JSON request and returns the JSON response envelope
// ({"ok":true,"result":...} or {"ok":false,"code":...,"error":...}). It returns a function that
// removes the hook again. Use the wpgotest package instead of calling this directly.
func SetHostForTest(fn func(name string, body []byte) ([]byte, error)) (restore func()) {
	testHostMu.Lock()
	prev := testHost
	testHost = fn
	testHostMu.Unlock()
	return func() {
		testHostMu.Lock()
		testHost = prev
		testHostMu.Unlock()
	}
}

// invokeHost is the non-Wasm stub: there is no host to talk to, so every host
// function fails with ErrNotInHost. Hook registration and Dispatch still work,
// which lets plugin authors unit test callbacks with a normal toolchain.
func invokeHost(name string, body []byte) ([]byte, error) {
	testHostMu.RLock()
	th := testHost
	testHostMu.RUnlock()
	if th != nil {
		return th(name, body)
	}
	switch name {
	case "add_hook", "log", "register_shortcode", "register_rest_route", "enqueue_style", "enqueue_script",
		"register_admin_page", "register_dashboard_widget", "register_meta_box", "register_block", "register_builder_widget":
		// Registration and logging are best-effort outside the host.
		return []byte(`{"ok":true}`), nil
	case "translate":
		// Outside the host nothing is translated: the text comes back as it is.
		var r struct {
			Text string `json:"text"`
		}
		_ = json.Unmarshal(body, &r)
		out, _ := json.Marshal(map[string]any{"ok": true, "result": map[string]string{"text": r.Text}})
		return out, nil
	case "register_settings_page":
		return []byte(`{"ok":true,"result":{"option_prefix":"plugin_test_"}}`), nil
	}
	return nil, ErrNotInHost
}
