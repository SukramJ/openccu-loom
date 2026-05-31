// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// Package wire holds the TLV codecs for Matter application-cluster
// command payloads. The IM dispatcher calls these to decode inbound
// InvokeRequest payloads into typed Go structs before handing them
// off to the cluster-server implementations on Custom DPs.
//
// Layout: one file per cluster (onoff.go, levelcontrol.go,
// windowcovering.go, thermostat.go). Each file defines:
//
//   - Request / response structs with exported fields.
//   - DecodeXRequest(tlv.Element) (X, error) functions.
//   - EncodeXResponse(*tlv.Encoder, X) writers (for clusters that
//     emit response payloads, e.g. WindowCovering reports nothing,
//     OnOff reports a status).
//
// Reference: Matter Core Spec 1.5.1 §1.5 (OnOff), §1.6 (LevelControl),
// §5.3 (WindowCovering), §4.3 (Thermostat).
package wire
