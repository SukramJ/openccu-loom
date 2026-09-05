// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

// Package endpoint turns flat endpoint descriptions into the Matter
// endpoint topology the bridge advertises to commissioners.
//
// Its input is [Snapshot] — a set of [Spec] values carrying no device
// model at all. Whoever owns a model walks it, decides what deserves an
// endpoint and resolves the operator-facing labels through its own
// naming authority ([NameResolver]); this package then allocates and
// persists endpoint ids, builds the three-tier root/aggregator
// scaffolding, materialises the cluster surface and dispatches
// Interaction Model requests into it.
//
// The assembler is non-reactive: it consumes a snapshot and produces a
// topology, then is done. Re-running on model changes is the caller's
// responsibility (the bridge core subscribes to the event bus and
// triggers re-assembly when devices are added, removed, or change
// reachability).
//
// Endpoint identity is persisted to matter_endpoints (migration 007)
// keyed by the 5-tuple (central_name, device_address, channel_no,
// dp_kind, dp_key) that [Spec.StableKey] carries. The same source
// produces the same Matter endpoint ID across daemon restarts.
//
// Endpoint 0 is the root bridge endpoint and is never persisted —
// the assembler synthesises it from the bridge configuration on every
// run. Bridged endpoints occupy 1..65534.
package endpoint
