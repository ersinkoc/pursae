//go:build !(wasip1 && wasm)

package wpgo

// invokeHost is the non-Wasm stub: there is no host to talk to, so every host
// function fails with ErrNotInHost. Hook registration and Dispatch still work,
// which lets plugin authors unit test callbacks with a normal toolchain.
func invokeHost(name string, body []byte) ([]byte, error) {
	if name == "add_hook" || name == "log" {
		// Registration and logging are best-effort outside the host.
		return []byte(`{"ok":true}`), nil
	}
	return nil, ErrNotInHost
}
