// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package adapter

import (
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// TestLegacyInitInterfaceIDReproducesThePrePrefixShape pins the identifier
// a pre-`loom-` release advertised, because that exact string is what a
// stale CUxD registration keeps delivering to after an upgrade.
//
// The doubled central name is the point, not an oversight: the old builder
// concatenated instance and central unconditionally, so an add-on running
// on the CCU it talks to produced `RM-Test-VM-96-RM-Test-VM-96-CUxD` —
// which is what showed up in an operator's callback log as an id nothing
// answered to.
func TestLegacyInitInterfaceIDReproducesThePrePrefixShape(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		instance string
		central  string
		iface    hmenum.Interface
		want     string
	}{
		{
			name:     "instance equals central — the doubled add-on shape",
			instance: "RM-Test-VM-96",
			central:  "RM-Test-VM-96",
			iface:    hmenum.InterfaceCUxD,
			want:     "RM-Test-VM-96-RM-Test-VM-96-CUxD",
		},
		{
			name:     "distinct instance",
			instance: "loom-host",
			central:  "ccu-01",
			iface:    hmenum.InterfaceCUxD,
			want:     "loom-host-ccu-01-CUxD",
		},
		{
			name:    "no instance falls back to the wire id",
			central: "ccu-01",
			iface:   hmenum.InterfaceCUxD,
			want:    "ccu-01-CUxD",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := LegacyInitInterfaceID(tc.instance, tc.central, tc.iface); got != tc.want {
				t.Fatalf("LegacyInitInterfaceID = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestLegacyInitInterfaceIDDiffersFromTheCurrentOne guards the reason the
// alias registration is conditional: once the two shapes agree there is
// nothing to alias, and registering twice under one key would be a silent
// overwrite rather than a second route.
func TestLegacyInitInterfaceIDDiffersFromTheCurrentOne(t *testing.T) {
	t.Parallel()

	legacy := LegacyInitInterfaceID("RM-Test-VM-96", "RM-Test-VM-96", hmenum.InterfaceCUxD)
	current := InitInterfaceID("RM-Test-VM-96", "RM-Test-VM-96", hmenum.InterfaceCUxD)
	if legacy == current {
		t.Fatal("legacy and current ids agree; the alias registration would be a no-op")
	}
	if current != "loom-RM-Test-VM-96-CUxD" {
		t.Fatalf("current id = %q, want the single-name loom shape", current)
	}
}
