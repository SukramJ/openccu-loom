// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

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
		{"triple", "loomhost", "GoOtto", hmenum.InterfaceHmIPRF, "loomhost-GoOtto-HmIP-RF"},
		{"triple bidcos", "loomhost", "Backup", hmenum.InterfaceBidCosRF, "loomhost-Backup-BidCos-RF"},
		{"triple cuxd", "loomhost", "Primary", hmenum.InterfaceCUxD, "loomhost-Primary-CUxD"},
		{"empty instance falls back to ccu-iface", "", "GoOtto", hmenum.InterfaceHmIPRF, "GoOtto-HmIP-RF"},
		{"empty ccu yields instance-iface", "loomhost", "", hmenum.InterfaceHmIPRF, "loomhost-HmIP-RF"},
		{"both empty falls back to bare iface", "", "", hmenum.InterfaceHmIPRF, "HmIP-RF"},
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

// TestStripInstanceRoundtrip pins the inbound-callback contract: the CCU
// echoes the [InitInterfaceID] triple, and [StripInstance] must map it back to
// the canonical [WireInterfaceID] that stamps the devices + registries.
func TestStripInstanceRoundtrip(t *testing.T) {
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
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			initID := InitInterfaceID(tc.instanceName, tc.centralName, tc.iface)
			wireID := WireInterfaceID(tc.centralName, tc.iface)
			if got := StripInstance(tc.instanceName, initID); got != wireID {
				t.Errorf("StripInstance(%q, %q) = %q, want %q (= WireInterfaceID)",
					tc.instanceName, initID, got, wireID)
			}
		})
	}
}

// TestStripInstanceNoPrefix verifies StripInstance is a no-op when the id does
// not carry the instance prefix (defensive: a two-part id arriving on the
// inbound path must pass through unchanged).
func TestStripInstanceNoPrefix(t *testing.T) {
	t.Parallel()
	if got := StripInstance("loomhost", "GoOtto-HmIP-RF"); got != "GoOtto-HmIP-RF" {
		t.Errorf("StripInstance no-prefix = %q, want unchanged", got)
	}
	if got := StripInstance("", "GoOtto-HmIP-RF"); got != "GoOtto-HmIP-RF" {
		t.Errorf("StripInstance empty-instance = %q, want unchanged", got)
	}
}
