// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

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
