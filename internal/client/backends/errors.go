// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package backends

import "errors"

// ErrUnsupported is returned from capability-gated methods when the
// backend does not advertise the feature. Callers check via
// [errors.Is].
var ErrUnsupported = errors.New("backend: operation unsupported")

// ErrNotWired is returned when a backend method is called without a
// concrete transport attached. Happens only in partial test setups.
var ErrNotWired = errors.New("backend: transport not wired")
