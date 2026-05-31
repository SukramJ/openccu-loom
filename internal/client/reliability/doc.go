// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// Package reliability holds the cross-cutting resilience primitives
// the daemon wraps around every southbound call: circuit breaker,
// retry with jittered exponential backoff, duty-cycle throttle,
// request coalescer, and ping/pong tracker.
//
// Each primitive is an independent Go type with no transport
// dependencies; [InterfaceClient] composes them.
package reliability
