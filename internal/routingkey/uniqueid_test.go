// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package routingkey

import (
	"strings"
	"testing"
)

// TestPseudoAddressConstants verifies the wire values of the four exported
// pseudo-address constants and that PseudoAddresses lists them in the
// documented order.
func TestPseudoAddressConstants(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		got  string
		want string
	}{
		{"HubAddress", HubAddress, "hub"},
		{"InstallModeAddress", InstallModeAddress, "install_mode"},
		{"ProgramAddress", ProgramAddress, "program"},
		{"SysvarAddress", SysvarAddress, "sysvar"},
	}
	for _, tc := range cases {
		if tc.got != tc.want {
			t.Errorf("%s = %q, want %q", tc.name, tc.got, tc.want)
		}
	}
}

func TestPseudoAddresses_SliceOrderAndLength(t *testing.T) {
	t.Parallel()

	want := []string{HubAddress, InstallModeAddress, ProgramAddress, SysvarAddress}
	if len(PseudoAddresses) != len(want) {
		t.Fatalf("PseudoAddresses len = %d, want %d", len(PseudoAddresses), len(want))
	}
	for i, w := range want {
		if PseudoAddresses[i] != w {
			t.Errorf("PseudoAddresses[%d] = %q, want %q", i, PseudoAddresses[i], w)
		}
	}
}

// TestPseudoAddresses_GenerateUniqueID_NamespacedByCentral verifies that
// GenerateUniqueID produces a key that contains the central ID for every
// pseudo-address, confirming each one takes the central-prefix path.
func TestPseudoAddresses_GenerateUniqueID_NamespacedByCentral(t *testing.T) {
	t.Parallel()

	const central = "vccu0000000"
	cases := []struct {
		address string
		param   string
	}{
		{HubAddress, "foo"},
		{InstallModeAddress, "foo"},
		{ProgramAddress, "foo"},
		{SysvarAddress, "foo"},
	}
	for _, tc := range cases {
		uid := GenerateUniqueID(central, tc.address, tc.param, "")
		if !strings.Contains(uid, central) {
			t.Errorf("GenerateUniqueID(%q, %q, %q, %q) = %q, want central %q in result",
				central, tc.address, tc.param, "", uid, central)
		}
	}
}

// TestCUxDAddressesAreNamespacedByCentral pins the multi-CCU fix: CUxD hands out
// identical synthetic addresses on every CCU, so their unique_id must carry the
// central prefix. Without it two CCUs bridged into one Home Assistant declare
// colliding CUxD unique_ids and one CCU's entities are dropped — permanently,
// once the discovery payload is retained. A globally-unique HmIP serial keeps
// its bare, central-independent id so a single-CCU install is unchanged.
func TestCUxDAddressesAreNamespacedByCentral(t *testing.T) {
	t.Parallel()

	const paramName = "STATE"
	const cuxAddr = "CUX2801001:1"

	idA := GenerateUniqueID("ccu-a", cuxAddr, paramName, "")
	idB := GenerateUniqueID("ccu-b", cuxAddr, paramName, "")
	if idA == idB {
		t.Fatalf("two centrals produced identical CUxD unique_ids (%q); they collide in Home Assistant", idA)
	}
	if !strings.Contains(idA, "ccu-a") || !strings.Contains(idB, "ccu-b") {
		t.Fatalf("CUxD unique_ids are not namespaced by central: %q / %q", idA, idB)
	}
	if !NeedsCentralScope(cuxAddr) {
		t.Fatal("NeedsCentralScope reports a CUxD address as globally unique")
	}

	// A physical HmIP serial is globally unique and must stay central-independent.
	const hmipAddr = "00021BE9957782:4"
	if NeedsCentralScope(hmipAddr) {
		t.Fatal("NeedsCentralScope reports a globally-unique HmIP serial as needing central scope")
	}
	if a, b := GenerateUniqueID("ccu-a", hmipAddr, paramName, ""), GenerateUniqueID("ccu-b", hmipAddr, paramName, ""); a != b {
		t.Fatalf("HmIP serial unique_id changed with the central (%q vs %q); it must stay global", a, b)
	}
}
