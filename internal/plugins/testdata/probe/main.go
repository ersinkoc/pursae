// Command probe is a test plugin that exercises the runtime's failure modes
// and permission checks. It is built by the internal/plugins tests.
package main

import (
	"os"

	"wpgo/sdk/go/wpgo"
)

func init() {
	// Tries to write an option; returns "ok" or the error code.
	wpgo.AddFilter("probe_update_option", 10, func(_ wpgo.Context, v string, _ []any) string {
		if err := wpgo.UpdateOption("probe_option", v); err != nil {
			if wpgo.IsPermissionDenied(err) {
				return "permission_denied"
			}
			return "error: " + err.Error()
		}
		return "ok"
	})
	// Round-trips a value through plugin storage.
	wpgo.AddFilter("probe_storage", 10, func(_ wpgo.Context, v string, _ []any) string {
		if err := wpgo.Storage.Set("k", v); err != nil {
			return "error: " + err.Error()
		}
		got, _, err := wpgo.Storage.Get("k")
		if err != nil {
			return "error: " + err.Error()
		}
		return "stored:" + got
	})
	// Never returns; the runtime must stop it.
	wpgo.AddFilter("probe_loop", 10, func(_ wpgo.Context, v string, _ []any) string {
		n := 0
		for {
			n++
		}
	})
	// Panics; the SDK reports it as a callback failure.
	wpgo.AddFilter("probe_panic", 10, func(_ wpgo.Context, v string, _ []any) string {
		panic("probe panic")
	})
	// Terminates the module; the runtime must re-instantiate it.
	wpgo.AddFilter("probe_exit", 10, func(_ wpgo.Context, v string, _ []any) string {
		os.Exit(3)
		return v
	})
	// Echoes the value with a suffix; used to check recovery.
	wpgo.AddFilter("probe_echo", 10, func(_ wpgo.Context, v string, _ []any) string {
		println("echo called") // goes to the plugin log via stderr
		return v + "!"
	})
}

func main() {}
