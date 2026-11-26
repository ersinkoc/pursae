package server

import (
	"log/slog"
	"runtime/debug"
)

// safeGo runs fn on a new goroutine that cannot take the process down. A panic in a goroutine
// is not caught by gin's recovery or the scheduler's: it ends the whole server, and background
// work handles visitors' and plugins' data (images to optimize, webhook payloads, imports,
// remote responses). The panic is logged with its stack under name and the goroutine ends;
// fn's deferred calls still run. Every go statement in this package goes through safeGo or
// goBackground, except the few that only wait on a WaitGroup.
func safeGo(name string, fn func()) {
	go func() {
		defer recoverGoroutine(name)
		fn()
	}()
}

// recoverGoroutine is the deferred recover of safeGo and goBackground: it must be the deferred
// function itself for recover to stop the panic.
func recoverGoroutine(name string) {
	if r := recover(); r != nil {
		slog.Error("background goroutine panicked", "goroutine", name, "panic", r, "stack", string(debug.Stack()))
	}
}
