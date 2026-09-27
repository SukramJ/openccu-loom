// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package backends

import "errors"

// ErrUnsupported is returned from capability-gated methods when the
// backend does not advertise the feature. Callers check via
// [errors.Is].
var ErrUnsupported = errors.New("backend: operation unsupported")

// ErrNotWired is returned when a backend method is called without a
// concrete transport attached. Happens only in partial test setups.
var ErrNotWired = errors.New("backend: transport not wired")

// ErrLiteInitRefused is returned when an openccu-lite box refuses an
// `init` call. Loom never sends init to a box (the event stream replaces
// the callback registration), so this error always marks a Loom bug and
// callers log it at error level rather than treating it as a transport
// condition.
var ErrLiteInitRefused = errors.New("backend: openccu-lite refused init (a Loom bug: init must never reach a box)")
