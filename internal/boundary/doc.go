// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// Package boundary wraps every public service call with consistent
// panic recovery, structured logging, and latency metrics. It is
// The Go counterpart
//
// Usage pattern — wrap the call-site, not each method:
//
//	err := boundary.Execute(ctx, boundary.Config{
//	    Logger: logger,
//	    Name:   "device.SetValue",
//	    Metrics: boundary.Metrics{
//	        Count:   counter,
//	        Latency: gauge,
//	    },
//	}, func(ctx context.Context) error { return doWork(ctx) })
//
// The MVP does not auto-wrap every service method; north-bound
// adapters use Execute for their inbound handlers, while lower
// layers rely on explicit error returns.
package boundary
