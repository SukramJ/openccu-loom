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
		{"hmip rf with central", "GoOtto", hmenum.InterfaceHmIPRF, "GoOtto-HmIP-RF"},
		{"bidcos rf with central", "Backup", hmenum.InterfaceBidCosRF, "Backup-BidCos-RF"},
		{"cuxd with central", "Primary", hmenum.InterfaceCUxD, "Primary-CUxD"},
		{"empty central falls back to bare iface", "", hmenum.InterfaceHmIPRF, "HmIP-RF"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := WireInterfaceID(tc.centralName, tc.iface); got != tc.want {
				t.Errorf("WireInterfaceID(%q, %q) = %q, want %q", tc.centralName, tc.iface, got, tc.want)
			}
		})
	}
}

func TestWireInterfaceIDIsCcuUniqueAcrossCentrals(t *testing.T) {
	t.Parallel()
	// The whole point of the central-prefix is that two daemons sharing
	// a CCU produce different interface_ids for the same physical
	// interface. Pin that explicitly so a future refactor that drops
	// the prefix breaks loudly here.
	a := WireInterfaceID("PrimaryDaemon", hmenum.InterfaceHmIPRF)
	b := WireInterfaceID("BackupDaemon", hmenum.InterfaceHmIPRF)
	if a == b {
		t.Fatalf("two centrals produced identical interface_id %q — the CCU would conflate their callback registrations", a)
	}
}
