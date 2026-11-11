// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

// Package measurements forwards numeric data-point values to an external
// time-series store. The daemon keeps no measurement history of its own:
// a value that should be charted or archived belongs in a store built for
// it, and this package is the north-bound plane that feeds one. The
// feature is opt-in and off by default.
//
// The Forwarder subscribes to each central's EventBus for
// DataPointValueChangedEvent and applies the provenance guard: only
// genuine live wire observations pass, filtered by the value's
// ValueSource — never the boot-time zero default, a cache replay, or a
// source-only flip. A sample that also passes the parameter-name policy
// and the per-data-point recording overrides is handed to the Exporter.
// The capture point is non-blocking: the event handler never waits on
// I/O, and the exporter drops its oldest buffered sample rather than
// stalling the bus.
package measurements
