// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package core

// AccessRestrictionList (ARL) is a Matter 1.4 fabric-scoped mechanism
// that lets a fabric administrator restrict which subjects can access
// specific clusters or endpoints on a node. Cluster ID 0x002B,
// introduced alongside the Matter 1.4 Managed Aggregator use-case.
//
// OpenCCU-Loom does not currently implement the Managed Aggregator
// use-case, so this cluster is NOT mounted on any endpoint. The
// skeleton is present to document the integration point for a future
// implementation.
//
// When the Managed Aggregator use-case is implemented, mount this
// cluster on the Root endpoint and wire the CommitRestrictionEntries /
// ReviewFabricRestrictions commands to the daemon's fabric-store.
//
// By-design entry: see docs/parity/by_design.md §ARL (C-P2-3).
//
// TODO: implement full ARL cluster when Managed Aggregator is in scope.
// At minimum: Cluster 0x002B, attrs CommissioningARLEntries (0x0000),
// ARLEntries (0x0001), commands CommitRestrictionEntries (0x01) +
// ReviewFabricRestrictions (0x02) + RestrictedAccessEvent (event 0x00).
const (
	// ARLClusterID is the Matter 1.4 Access Restriction List cluster ID.
	ARLClusterID uint32 = 0x002B
)
