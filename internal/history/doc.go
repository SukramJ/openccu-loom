// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

// Package history records a time-series of numeric measurement values
// into the dedicated history database so a daemon running without Home
// Assistant can still chart sensor history. The feature is opt-in and
// off by default. See ADR 0040.
//
// The Recorder subscribes to each central's EventBus for
// DataPointValueChangedEvent, applies the ADR 0040 provenance guard
// (only genuine live wire observations, filtered by the value's
// ValueSource — never the boot-time zero default, a cache replay, or a
// source-only flip), buffers accepted samples, and batch-flushes them to
// the MeasurementStore. A background ticker enforces the retention
// window. The capture point is non-blocking: the event handler never
// waits on disk I/O, and a full buffer drops the oldest sample rather
// than stalling the bus.
package history
