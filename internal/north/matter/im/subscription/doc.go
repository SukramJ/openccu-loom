// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// Package subscription implements the Matter subscription state
// machine layered on top of the [im] message codecs.
//
// Matter Core Spec §8.5 governs subscriptions:
//
//   - A commissioner sends SubscribeRequest with a path list, a
//     MinIntervalFloor (lower-bound seconds between reports) and a
//     MaxIntervalCeiling (upper-bound; the bridge MUST report at
//     least this often as a keep-alive).
//   - The bridge accepts or rejects the subscription. On accept it
//     allocates a SubscriptionID and emits an initial ReportData
//     containing the current values for every requested path.
//   - Subsequent reports fire when an attribute changes (gated by
//     MinInterval) or after MaxInterval expires (keep-alive).
//   - Each subscription is fabric-scoped and counts against the
//     per-fabric MaxSubscriptions quota.
//
// openccu-loom v1.1 ships a baseline implementation: subscriptions
// live in RAM, reports are produced by a single Tick goroutine that
// drives every Subscription's MaxInterval timer, and attribute
// changes flow in via [Manager.OnAttributeChanged] (called by the
// bridge core when the source DP fires).
//
// Replay buffering across transient network drops is **not** in
// v1.1 — the spec leaves it implementation-defined and our
// commissioner targets (Apple Home, Google Home) re-issue
// SubscribeRequest after disconnect rather than relying on
// resumable subscriptions.
package subscription
