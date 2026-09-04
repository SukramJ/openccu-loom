// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

// Package endpoint walks the device model and produces the Matter
// endpoint topology the bridge advertises to commissioners.
//
// The assembler is non-reactive: it consumes a snapshot of devices
// and produces a topology, then is done. Re-running on model
// changes is the caller's responsibility (the bridge core subscribes
// to the event bus and triggers re-assembly when devices are added,
// removed, or change reachability).
//
// Endpoint identity is persisted to matter_endpoints (migration 007)
// keyed by the 5-tuple (central_name, device_address, channel_no,
// dp_kind, dp_key). The same source produces the same Matter endpoint
// ID across daemon restarts.
//
// Endpoint 0 is the root bridge endpoint and is never persisted —
// the assembler synthesises it from the bridge configuration on every
// run. Bridged endpoints occupy 1..65534.
//
// Source surface (see ADR 0012 §"Source surface"):
//
//   - Custom DPs implementing [mattercontract.EndpointSource] →
//     one endpoint per channel hosting a Matter-mappable Custom DP.
//   - Calculated / Combined DPs implementing
//     [mattercontract.EndpointSource] → one endpoint per DP.
//   - Calculated DPs implementing [mattercontract.MeasurementSource]
//     (and not MatterEndpointSource) → one standalone sensor
//     endpoint per measurement, gated by the IncludeMeasurements
//     config flag.
//   - Generic DPs implementing [mattercontract.EndpointSource] →
//     one endpoint per DP, but only on channels without a Custom-DP
//     wrapper (the wrapper owns the channel's Matter projection
//     otherwise, so this avoids double-publishing). Today only
//     [generic.Switch] on STATE → OnOffPlugInUnit qualifies.
//   - Generic DPs implementing [mattercontract.MeasurementSource] →
//     one standalone sensor endpoint per row (Button / Action
//     PRESS_*, BinarySensor, Sensor[float64]). The allowlist filter
//     gates each row.
package endpoint
