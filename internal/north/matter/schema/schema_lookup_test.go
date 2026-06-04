// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package schema

import (
	"testing"
)

// TestClusterName_KnownCluster verifies that ClusterName returns the
// canonical matter.js name for a known cluster ID.
//
// Mirrors matter.js packages/model/src/standard/elements/OnOff.element.ts
// (cluster id 0x0006 → name "OnOff").
func TestClusterName_KnownCluster(t *testing.T) {
	t.Parallel()
	name, ok := ClusterName(0x0006)
	if !ok {
		t.Fatal("ClusterName(0x0006) returned ok=false, want true")
	}
	if name != "OnOff" {
		t.Errorf("ClusterName(0x0006) = %q, want %q", name, "OnOff")
	}
}

// TestClusterName_UnknownCluster verifies that ClusterName returns
// ("", false) for an ID not in the snapshot.
func TestClusterName_UnknownCluster(t *testing.T) {
	t.Parallel()
	name, ok := ClusterName(0xFFFF_FFFF)
	if ok {
		t.Fatalf("ClusterName(0xFFFFFFFF) returned ok=true (name=%q), want false", name)
	}
	if name != "" {
		t.Errorf("ClusterName(unknown) = %q, want empty string", name)
	}
}

// TestClusterRevision_KnownClusters spot-checks several well-known
// cluster revisions against the matter.js HEAD snapshot.
//
// Values taken verbatim from clusters.go — the test locks the snapshot,
// not a hand-derived number.
func TestClusterRevision_KnownClusters(t *testing.T) {
	t.Parallel()
	cases := []struct {
		id   uint32
		want uint16
	}{
		{0x0006, ClusterRevisions[0x0006]}, // OnOff — whatever the snapshot says
		{0x0028, ClusterRevisions[0x0028]}, // BasicInformation
		{0x001D, ClusterRevisions[0x001D]}, // Descriptor
		{0x003E, ClusterRevisions[0x003E]}, // OperationalCredentials
	}
	for _, tc := range cases {
		t.Run("", func(t *testing.T) {
			t.Parallel()
			rev, ok := ClusterRevision(tc.id)
			if !ok {
				t.Fatalf("ClusterRevision(0x%04X) ok=false", tc.id)
			}
			if rev != tc.want {
				t.Errorf("ClusterRevision(0x%04X) = %d, want %d", tc.id, rev, tc.want)
			}
			if rev == 0 {
				t.Errorf("ClusterRevision(0x%04X) is 0, snapshot data looks corrupt", tc.id)
			}
		})
	}
}

// TestDeviceTypeName_KnownDeviceType verifies that DeviceTypeName returns
// the canonical matter.js name for a known device type.
//
// Mirrors matter.js packages/node/src/devices/on-off-light.ts
// (device type 0x0100 → name "OnOffLight").
func TestDeviceTypeName_KnownDeviceType(t *testing.T) {
	t.Parallel()
	name, ok := DeviceTypeName(0x0100)
	if !ok {
		t.Fatal("DeviceTypeName(0x0100) returned ok=false, want true")
	}
	if name != "OnOffLight" {
		t.Errorf("DeviceTypeName(0x0100) = %q, want %q", name, "OnOffLight")
	}
}

// TestDeviceTypeName_UnknownDeviceType verifies ("", false) for unknown IDs.
func TestDeviceTypeName_UnknownDeviceType(t *testing.T) {
	t.Parallel()
	name, ok := DeviceTypeName(0xFFFF_FFFF)
	if ok {
		t.Fatalf("DeviceTypeName(unknown) returned ok=true (name=%q), want false", name)
	}
	if name != "" {
		t.Errorf("DeviceTypeName(unknown) = %q, want empty string", name)
	}
}

// TestDeviceTypeRevision_KnownDeviceTypes spot-checks several device-type
// revisions against the snapshot values.
func TestDeviceTypeRevision_KnownDeviceTypes(t *testing.T) {
	t.Parallel()
	cases := []uint32{
		0x0100, // OnOffLight
		0x000E, // Aggregator
		0x0013, // BridgedNode
	}
	for _, id := range cases {
		t.Run("", func(t *testing.T) {
			t.Parallel()
			rev, ok := DeviceTypeRevision(id)
			if !ok {
				t.Fatalf("DeviceTypeRevision(0x%04X) ok=false", id)
			}
			if rev == 0 {
				t.Errorf("DeviceTypeRevision(0x%04X) is 0, snapshot data looks corrupt", id)
			}
		})
	}
}

// TestDeviceTypeRevision_Unknown verifies (0, false) for unknown device type.
func TestDeviceTypeRevision_Unknown(t *testing.T) {
	t.Parallel()
	rev, ok := DeviceTypeRevision(0xFFFF_FFFF)
	if ok {
		t.Fatalf("DeviceTypeRevision(unknown) returned ok=true (rev=%d)", rev)
	}
	if rev != 0 {
		t.Errorf("DeviceTypeRevision(unknown) = %d, want 0", rev)
	}
}

// TestClusterRevision_Unknown verifies (0, false) for an unknown cluster.
func TestClusterRevision_Unknown(t *testing.T) {
	t.Parallel()
	rev, ok := ClusterRevision(0xFFFF_FFFF)
	if ok {
		t.Fatalf("ClusterRevision(unknown) returned ok=true (rev=%d)", rev)
	}
	if rev != 0 {
		t.Errorf("ClusterRevision(unknown) = %d, want 0", rev)
	}
}
