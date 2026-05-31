// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package cluster

// Global cluster attribute IDs per Matter Core Spec §7.13. Every
// cluster server must expose these in addition to its own attributes.
const (
	// AttrGlobalClusterRevision is the revision attribute every
	// cluster server returns. The value is cluster-specific.
	AttrGlobalClusterRevision uint32 = 0xFFFD
	// AttrGlobalFeatureMap is the bitmask of optional features the
	// cluster server reports as enabled.
	AttrGlobalFeatureMap uint32 = 0xFFFC
	// AttrGlobalAttributeList is the list of attribute IDs the
	// cluster server implements.
	AttrGlobalAttributeList uint32 = 0xFFFB
	// AttrGlobalAcceptedCommandList is the list of cluster-command
	// IDs the server accepts on Invoke.
	AttrGlobalAcceptedCommandList uint32 = 0xFFF9
	// AttrGlobalGeneratedCommandList is the list of cluster-command
	// IDs the server may emit in InvokeResponse.
	AttrGlobalGeneratedCommandList uint32 = 0xFFF8
	// AttrGlobalEventList is the list of event IDs the server
	// emits. Required since Matter 1.0.
	AttrGlobalEventList uint32 = 0xFFFA
)

// SpecificationVersion is the Matter Specification Version advertised
// in BasicInformation.SpecificationVersion (Matter §11.1.5.16). The
// value encodes major / minor / patch / reserved as four bytes:
//
//	0x01 05 01 00 → 1.5.1.0
//
// Tracks matter.js HEAD's `Specification.SPECIFICATION_VERSION` so the
// bridge rides on the same wire baseline as the reference
// implementation. Matter §1.4 is explicit that revisions are strictly
// superset-compatible — a 1.3 / 1.4 commissioner MUST tolerate a 1.5
// bridge by ignoring unknown attributes, not by rejecting the
// connection. Apple Home's HAP-service-mapper rejection
// (`MTRErrorDomain Code=12 "No known schema for decoding attribute
// value"`) lives one layer above the spec-compliant decoder; pinning
// SpecificationVersion lower does not affect it.
const SpecificationVersion uint32 = 0x01050100
