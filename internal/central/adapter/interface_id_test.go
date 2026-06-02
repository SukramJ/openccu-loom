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
			if got := WireInterfaceID(tc.instanceName, tc.centralName, tc.iface); got != tc.want {
				t.Errorf("WireInterfaceID(%q, %q, %q) = %q, want %q",
					tc.instanceName, tc.centralName, tc.iface, got, tc.want)
			}
		})
	}
}

// TestWireInterfaceIDIsClientUniqueAcrossDaemons pins the CCU-side
// uniqueness guarantee: two daemons (distinct instance names) against the
// SAME CCU (same central_name) must produce different interface_ids, or the
// CCU conflates their callback registrations. This is the case the bare
// `<central_name>-<interface>` form failed (ADR-0024).
func TestWireInterfaceIDIsClientUniqueAcrossDaemons(t *testing.T) {
	t.Parallel()
	a := WireInterfaceID("PrimaryDaemon", "GoOtto", hmenum.InterfaceHmIPRF)
	b := WireInterfaceID("BackupDaemon", "GoOtto", hmenum.InterfaceHmIPRF)
	if a == b {
		t.Fatalf("two daemons produced identical interface_id %q — the CCU would conflate their callback registrations", a)
	}
}

// TestWireInterfaceIDIsCcuUniqueWithinDaemon pins the daemon-internal
// uniqueness guarantee: one daemon (same instance name) against two CCUs
// must produce different interface_ids for the same interface, so devices
// with repeating addresses across CCUs do not collide on the InterfaceID.
func TestWireInterfaceIDIsCcuUniqueWithinDaemon(t *testing.T) {
	t.Parallel()
	a := WireInterfaceID("loomhost", "Wohnzimmer", hmenum.InterfaceHmIPRF)
	b := WireInterfaceID("loomhost", "Keller", hmenum.InterfaceHmIPRF)
	if a == b {
		t.Fatalf("two CCUs produced identical interface_id %q — internal DataPointKeys would collide", a)
	}
}
