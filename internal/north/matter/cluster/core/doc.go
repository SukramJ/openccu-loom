// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// Package core hosts the Matter cluster servers that every bridge
// endpoint (or the root endpoint specifically) mandates per Matter
// Core Specification 1.5.1. These clusters carry no DP-specific
// semantics — they describe the bridge itself or the bridged-device
// metadata.
//
// The endpoint assembler instantiates one of each as a fixed
// "boilerplate" set on top of the DP-specific cluster servers
// returned by [interfaces.MatterEndpointSource.MatterClusterServers].
//
// Cluster IDs:
//
//   - Descriptor                          0x001D — every endpoint
//   - Binding                             0x001E — every bridged endpoint
//   - BasicInformation                    0x0028 — root endpoint only
//   - BridgedDeviceBasicInformation       0x0039 — bridged endpoints
//   - GeneralCommissioning                0x0030 — root endpoint only (Stufe 5)
//   - NetworkCommissioning                0x0031 — root endpoint only (Stufe 5)
//   - GeneralDiagnostics                  0x0033 — root endpoint only
//   - DiagnosticLogs                      0x0032 — root endpoint only
//   - OTASoftwareUpdateRequestor          0x0029 — root endpoint (stub)
//
// PowerSource (0x002F) for bridged battery endpoints lives in the
// measurement package (measurement.PowerSourceServer), not here.
//
// All revisions match Matter 1.5.1. Updates require synchronised
// changes in the model-layer revision constants under
// `internal/model/custom/.../matter*.go`.
package core
