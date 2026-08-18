// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package adapter

import (
	"log/slog"
	"runtime/debug"
)

// SafeGo starts fn in a new goroutine and ensures that any panic does not
// tear down the whole program: the stack trace is logged at slog.LevelError
// and the goroutine exits cleanly.
//
// Used by the adapter's background tasks — the auto-refresh loop, the
// central bring-up and the event bridge — which would otherwise run as
// bare `go func()` calls with no panic recovery.
// The wrapper centralises goroutine lifetime hygiene.
//
// `name` identifies the goroutine in the log and should be a static
// identifier (e.g. the function name or a job tag).
// fn may be nil — in that case nothing happens.
//
// loom:reachable:reason="utility wrapper for panic-safe goroutines; called from central_bringup.go, auto_refresh.go and eventbridge.go's background tasks"
func SafeGo(name string, fn func()) {
	if fn == nil {
		return
	}
	go func() {
		defer func() {
			if r := recover(); r != nil {
				slog.Default().Error(
					"adapter goroutine panicked",
					slog.String("name", name),
					slog.Any("panic", r),
					slog.String("stack", string(debug.Stack())),
				)
			}
		}()
		fn()
	}()
}
