// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

// Package matteradapter maps the daemon's device model onto the Matter
// bridge's endpoint specs.
//
// It is the only place where the two vocabularies meet. The Matter side
// ([github.com/SukramJ/go-fabric/endpoint])
// knows nothing about devices, channels or data points: it consumes flat
// [endpoint.Spec] values and turns them into a topology, allocating and
// persisting endpoint ids. This package owns the other half — walking
// [device.Device] trees, deciding what deserves an endpoint, applying
// the operator's allowlist and visibility rules, and resolving the
// operator-facing labels through the model's own naming authority.
//
// Keeping the walk here is what lets the Matter side be reused by a host
// with a different model, and what keeps naming a daemon-wide decision
// rather than a Matter-side re-derivation.
//
// The walk is non-reactive: it consumes a snapshot of devices and
// describes their endpoints, then is done. Re-running on model changes
// is the caller's responsibility: the daemon subscribes to the event bus
// and syncs the described endpoints into the node when devices are
// added, removed, renamed or released, and when a central becomes
// ready; a reachability change is forwarded to the bridged endpoint as
// a ReachableChanged event without a sync.
//
// Source surface (see ADR 0012 §"Source surface"):
//
//   - Custom DPs implementing [contract.EndpointSource] →
//     one endpoint per channel hosting a Matter-mappable Custom DP.
//   - Calculated / Combined DPs implementing
//     [contract.EndpointSource] → one endpoint per DP.
//   - Calculated DPs implementing [contract.MeasurementSource]
//     (and not EndpointSource) → one standalone sensor endpoint per
//     measurement, gated by the IncludeMeasurements config flag.
//   - Generic DPs implementing [contract.EndpointSource] →
//     one endpoint per DP, but only on channels without a Custom-DP
//     wrapper (the wrapper owns the channel's Matter projection
//     otherwise, so this avoids double-publishing). Today only
//     [generic.Switch] on STATE → OnOffPlugInUnit qualifies.
//   - Generic DPs implementing [contract.MeasurementSource] →
//     one standalone sensor endpoint per row (Button / Action
//     PRESS_*, BinarySensor, Sensor[float64]). The allowlist filter
//     gates each row.
package matteradapter
