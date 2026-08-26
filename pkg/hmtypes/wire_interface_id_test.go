// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package hmtypes_test

import (
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// TestNewWireInterfaceIDJoinsCentralAndInterface pins the wire format the CCU
// echoes back on every callback and every registry is keyed by. The empty
// central name is the tooling case and must stay the bare interface.
func TestNewWireInterfaceIDJoinsCentralAndInterface(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		central string
		iface   hmenum.Interface
		want    hmtypes.WireInterfaceID
	}{
		{"named central", "ccu1", hmenum.InterfaceHmIPRF, "ccu1-HmIP-RF"},
		{"interface with its own hyphen", "ccu1", hmenum.InterfaceBidCosWired, "ccu1-BidCos-Wired"},
		{"hyphenated central", "wohn-ccu", hmenum.InterfaceCUxD, "wohn-ccu-CUxD"},
		{"unnamed central stays bare", "", hmenum.InterfaceBidCosRF, "BidCos-RF"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := hmtypes.NewWireInterfaceID(tc.central, tc.iface)
			if got != tc.want {
				t.Fatalf("NewWireInterfaceID(%q, %q) = %q, want %q", tc.central, tc.iface, got, tc.want)
			}
			if got.String() != string(tc.want) {
				t.Fatalf("String() = %q, want %q", got.String(), tc.want)
			}
		})
	}
}

// TestWireInterfaceIDBareRoundTrips pins the inverse. The central name has to
// be supplied because the separator is an ordinary hyphen and two interface
// tokens carry one themselves — splitting on the last hyphen would turn
// `ccu1-BidCos-Wired` into `Wired`.
func TestWireInterfaceIDBareRoundTrips(t *testing.T) {
	t.Parallel()
	for _, iface := range []hmenum.Interface{
		hmenum.InterfaceHmIPRF,
		hmenum.InterfaceBidCosRF,
		hmenum.InterfaceBidCosWired,
		hmenum.InterfaceVirtualDevices,
		hmenum.InterfaceCUxD,
	} {
		const central = "wohn-ccu"
		if got := hmtypes.NewWireInterfaceID(central, iface).Bare(central); got != iface {
			t.Errorf("Bare(%q) after New = %q, want %q", central, got, iface)
		}
	}
}

// TestWireInterfaceIDBareLeavesAnUnprefixedIDAlone covers the id a tooling or
// single-central path produces: there is no prefix to strip, and inventing one
// would drop a real interface token.
func TestWireInterfaceIDBareLeavesAnUnprefixedIDAlone(t *testing.T) {
	t.Parallel()
	id := hmtypes.ParseWireInterfaceID("BidCos-Wired")
	if got := id.Bare("ccu1"); got != hmenum.InterfaceBidCosWired {
		t.Fatalf("Bare = %q, want %q", got, hmenum.InterfaceBidCosWired)
	}
	if got := id.Bare(""); got != hmenum.InterfaceBidCosWired {
		t.Fatalf("Bare with empty central = %q, want %q", got, hmenum.InterfaceBidCosWired)
	}
}
