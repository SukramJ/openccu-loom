// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// Package bridge defines the uniform lifecycle contract for north-bound
// adapters (MQTT, Matter, MCP, REST, webhook, …). Today each bridge is
// hand-wired in cmd/openccu-loom with a bespoke constructor and Start/Stop
// call; the Service interface + Registry give them one start/stop shape so
// new bridges register uniformly and shutdown is ordered and best-effort.
//
// This is a daemon-internal wiring contract, not a cross-package wire
// protocol, so it lives in the consumer-side package rather than
// pkg/interfaces (per CLAUDE.md "Interfaces in the consumer package").
package bridge

import "context"

// Service is a north-bound adapter with a uniform lifecycle.
//
//   - Start must be non-blocking: spawn whatever goroutines the adapter
//     needs and return. A Service that blocks in Start stalls StartAll and
//     every Service registered after it.
//   - Stop must be idempotent and must unblock any background goroutine the
//     Service started. Calling Stop on a never-started or already-stopped
//     Service is a no-op, not an error.
//   - Name is a stable identifier used in logs and health surfaces; it must
//     be constant for the lifetime of the Service.
type Service interface {
	Name() string
	Start(ctx context.Context) error
	Stop(ctx context.Context) error
}

// HealthReporter is an optional capability a Service may implement so the
// Registry can roll its liveness into the daemon health surface. A Service
// that does not implement it is treated as always-healthy by the Registry.
type HealthReporter interface {
	// Healthy reports whether the Service is currently operating. detail is
	// a short human-readable reason, surfaced when ok is false.
	Healthy() (ok bool, detail string)
}
