// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package adapter

import (
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

func TestWireInterfaceID(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		centralName string
		iface       hmenum.Interface
		want        string
	}{
		{"ccu-iface", "GoOtto", hmenum.InterfaceHmIPRF, "GoOtto-HmIP-RF"},
		{"bidcos", "Backup", hmenum.InterfaceBidCosRF, "Backup-BidCos-RF"},
		{"cuxd", "Primary", hmenum.InterfaceCUxD, "Primary-CUxD"},
		{"empty ccu yields bare iface", "", hmenum.InterfaceHmIPRF, "HmIP-RF"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := WireInterfaceID(tc.centralName, tc.iface); got != tc.want {
				t.Errorf("WireInterfaceID(%q, %q) = %q, want %q",
					tc.centralName, tc.iface, got, tc.want)
			}
		})
	}
}

func TestInitInterfaceID(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name         string
		instanceName string
		centralName  string
		iface        hmenum.Interface
		want         string
	}{
		{"full form", "loomhost", "GoOtto", hmenum.InterfaceHmIPRF, "loom-loomhost-GoOtto-HmIP-RF"},
		{"full form bidcos", "loomhost", "Backup", hmenum.InterfaceBidCosRF, "loom-loomhost-Backup-BidCos-RF"},
		{"full form cuxd", "loomhost", "Primary", hmenum.InterfaceCUxD, "loom-loomhost-Primary-CUxD"},
		{"empty instance", "", "GoOtto", hmenum.InterfaceHmIPRF, "loom-GoOtto-HmIP-RF"},
		{"empty ccu", "loomhost", "", hmenum.InterfaceHmIPRF, "loom-loomhost-HmIP-RF"},
		{"both empty yields bare iface", "", "", hmenum.InterfaceHmIPRF, "loom-HmIP-RF"},
		// Running as the CCU's own add-on: instance and central both default to
		// a host-derived name, and repeating it adds no uniqueness.
		{"instance equals central collapses", "RM-Test-VM-96", "RM-Test-VM-96", hmenum.InterfaceBidCosRF, "loom-RM-Test-VM-96-BidCos-RF"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := InitInterfaceID(tc.instanceName, tc.centralName, tc.iface); got != tc.want {
				t.Errorf("InitInterfaceID(%q, %q, %q) = %q, want %q",
					tc.instanceName, tc.centralName, tc.iface, got, tc.want)
			}
		})
	}
}

// TestInitInterfaceIDIsClientUniqueAcrossDaemons pins the CCU-side
// uniqueness guarantee: two daemons (distinct instance names) against the
// SAME CCU (same central_name) must produce different wire interface_ids, or
// the CCU conflates their callback registrations. This is the case the bare
// `<central_name>-<interface>` form would fail (ADR-0024).
func TestInitInterfaceIDIsClientUniqueAcrossDaemons(t *testing.T) {
	t.Parallel()
	a := InitInterfaceID("PrimaryDaemon", "GoOtto", hmenum.InterfaceHmIPRF)
	b := InitInterfaceID("BackupDaemon", "GoOtto", hmenum.InterfaceHmIPRF)
	if a == b {
		t.Fatalf("two daemons produced identical interface_id %q — the CCU would conflate their callback registrations", a)
	}
}

// TestWireInterfaceIDIsCcuUniqueWithinDaemon pins the daemon-internal
// uniqueness guarantee: one daemon against two CCUs must produce different
// interface_ids for the same interface, so devices with repeating addresses
// across CCUs do not collide on the InterfaceID.
func TestWireInterfaceIDIsCcuUniqueWithinDaemon(t *testing.T) {
	t.Parallel()
	a := WireInterfaceID("Wohnzimmer", hmenum.InterfaceHmIPRF)
	b := WireInterfaceID("Keller", hmenum.InterfaceHmIPRF)
	if a == b {
		t.Fatalf("two CCUs produced identical interface_id %q — internal DataPointKeys would collide", a)
	}
}

// TestCanonicalInterfaceIDRoundtrip pins the inbound-callback contract: the
// CCU echoes the [InitInterfaceID] form, and [CanonicalInterfaceID] must map
// it back to the [WireInterfaceID] that stamps the devices + registries. A
// mismatch drops every callback on the floor — the echoed id resolves to no
// known client.
func TestCanonicalInterfaceIDRoundtrip(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name         string
		instanceName string
		centralName  string
		iface        hmenum.Interface
	}{
		{"with instance", "loomhost", "GoOtto", hmenum.InterfaceHmIPRF},
		{"empty instance", "", "GoOtto", hmenum.InterfaceBidCosRF},
		{"cuxd", "loomhost", "Primary", hmenum.InterfaceCUxD},
		{"instance equals central", "RM-Test-VM-96", "RM-Test-VM-96", hmenum.InterfaceBidCosRF},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			initID := InitInterfaceID(tc.instanceName, tc.centralName, tc.iface)
			wireID := WireInterfaceID(tc.centralName, tc.iface)
			if got := CanonicalInterfaceID(tc.instanceName, tc.centralName, initID); got != wireID {
				t.Errorf("CanonicalInterfaceID(%q, %q, %q) = %q, want %q (= WireInterfaceID)",
					tc.instanceName, tc.centralName, initID, got, wireID)
			}
		})
	}
}

// TestCanonicalInterfaceIDAcceptsPrePrefixIDs covers the upgrade window: a
// callback registered by an earlier release carries the un-prefixed
// `<instance>-<central>-<iface>` form and is still in flight when the new
// binary takes over. It must resolve to the same canonical id rather than
// arriving under one no registry knows.
func TestCanonicalInterfaceIDAcceptsPrePrefixIDs(t *testing.T) {
	t.Parallel()
	legacy := "loomhost-GoOtto-HmIP-RF"
	if got := CanonicalInterfaceID("loomhost", "GoOtto", legacy); got != "GoOtto-HmIP-RF" {
		t.Errorf("pre-prefix id = %q, want %q", got, "GoOtto-HmIP-RF")
	}
}

// TestCanonicalInterfaceIDNoPrefix verifies the mapping is a no-op for an id
// that carries neither shape (defensive: a bare two-part id arriving on the
// inbound path must pass through unchanged).
func TestCanonicalInterfaceIDNoPrefix(t *testing.T) {
	t.Parallel()
	if got := CanonicalInterfaceID("", "GoOtto", "GoOtto-HmIP-RF"); got != "GoOtto-HmIP-RF" {
		t.Errorf("no-prefix id = %q, want unchanged", got)
	}
}
