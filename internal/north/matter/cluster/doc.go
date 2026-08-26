// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

// Package cluster contains Matter cluster server implementations that
// the bridge attaches to assembled endpoints. It is split into:
//
//   - core/ — bridge-system clusters that every endpoint or the root
//     endpoint mandates: Descriptor, Binding, BasicInformation,
//     BridgedDeviceBasicInformation, GeneralDiagnostics,
//     DiagnosticLogs, OTASoftwareUpdateRequestor, PowerSource.
//
//   - application clusters (OnOff, LevelControl, ColorControl,
//     WindowCovering, Thermostat, DoorLock, BooleanState,
//     SmokeCOAlarm, …) live as further subpackages and are added
//     by the P0 cluster work.
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
