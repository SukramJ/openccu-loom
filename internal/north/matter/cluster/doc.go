// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

// Package cluster contains Matter cluster server implementations that
// the bridge attaches to assembled endpoints. It is split into:
//
//   - core/ — bridge-system clusters the root endpoint or every bridged
//     endpoint mandates: AccessControl, AccessRestriction,
//     BasicInformation, Binding, BridgedDeviceBasicInformation,
//     Descriptor, DiagnosticLogs, GeneralCommissioning,
//     GeneralDiagnostics, GroupKeyManagement, IcdManagement, Identify,
//     NetworkCommissioning, OperationalCredentials,
//     OtaSoftwareUpdateRequestor, TimeSynchronization.
//
//   - application clusters, grouped by the device surface they serve
//     rather than one subpackage per cluster: closure/, cover/, light/,
//     lock/, measurement/, thermo/, wire/. PowerSource lives in
//     measurement/ with the other metering clusters, not in core/ — the
//     core-package variant was a duplicate and was removed (see
//     notes/parity/by_design.md, "Removed" table).
//
// All cluster implementations target Matter Core Specification 1.5.1.
// ClusterRevision constants in this package are authoritative; the
// rich-model `MatterClusterServer` projections under
// `internal/model/.../matter.go` mirror them.
//
// Each cluster type implements
// [github.com/SukramJ/openccu-loom/pkg/interfaces.MatterClusterServer]
// so the bridge dispatches reads / writes / invokes uniformly,
// regardless of whether the cluster is system or application.
package cluster
