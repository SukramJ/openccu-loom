// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package eligibility

import (
	"strings"
	"testing"
)

// TestVendorNameReadsAsAControllerNotAnId pins what an operator sees in
// the fabric list.
//
// A fabric's vendor id is the only thing a controller declares about
// itself at commissioning. Rendered raw it is `0x1349`, which tells an
// operator nothing about which of their apps holds that fabric — and the
// fabric list exists precisely to answer "who is paired with this
// bridge, and can I remove this one".
func TestVendorNameReadsAsAControllerNotAnId(t *testing.T) {
	t.Parallel()

	// Every id below is the one the CSA Distributed Compliance Ledger
	// carries for that vendor. They are not guesses: two entries this
	// table replaced were wrong — 0x1037 is NXP Semiconductors, not
	// Aqara, and 0x125D is Tuya, not Home Assistant.
	cases := []struct {
		vendorID uint16
		want     string
	}{
		{0x1349, "Apple Home"},
		{0x1384, "Apple Keychain"},
		{0x6006, "Google"},
		{0x1217, "Amazon Alexa"},
		{0x110A, "Samsung SmartThings"},
		{0x115F, "Aqara"},
		{0x134B, "Home Assistant"},
	}
	for _, tc := range cases {
		if got := VendorName(tc.vendorID); got != tc.want {
			t.Errorf("VendorName(%#04x) = %q, want %q", tc.vendorID, got, tc.want)
		}
	}
}

// TestAnUnknownVendorStillIdentifiesItself keeps the unknown case
// useful.
//
// A vendor the table does not carry must still render as something an
// operator can act on: the id in the form every Matter tool prints it,
// so it can be searched. An empty string would make the row look like a
// bug in the bridge rather than a controller nobody recognises.
func TestAnUnknownVendorStillIdentifiesItself(t *testing.T) {
	t.Parallel()

	got := VendorName(0xABCD)
	if !strings.Contains(got, "ABCD") {
		t.Errorf("VendorName(0xABCD) = %q, want it to carry the id so it can be looked up", got)
	}
	if got == "" {
		t.Error("an unknown vendor rendered as the empty string")
	}
}

// TestEveryClassifiedEcosystemHasAVendorName keeps the two tables from
// drifting apart.
//
// The ecosystem classifier and the display name are fed by the same
// vendor ids. A vendor that classifies into an ecosystem but renders as
// a bare hex id means one table was extended and the other was not, and
// the symptom — a compatibility warning naming an ecosystem beside a
// fabric row that names nobody — reads as two different controllers.
func TestEveryClassifiedEcosystemHasAVendorName(t *testing.T) {
	t.Parallel()

	for vendorID, eco := range vendorEcosystems {
		name := VendorName(vendorID)
		if strings.Contains(name, "0x") {
			t.Errorf("vendor %#04x classifies as %q but has no display name", vendorID, eco)
		}
	}
}

// TestEcosystemClassificationUsesTheRealVendorIds is the regression
// guard for two ids that were wrong in a shipped release.
//
// The classifier decides whether an operator sees a compatibility
// warning at all — "Google will not show this device type" is only
// emitted for a fabric whose vendor id classifies into an ecosystem. An
// id that belongs to somebody else therefore does two things at once:
// the real ecosystem's fabric falls through to unknown and is never
// warned about, and a fabric belonging to the vendor who actually owns
// that id is labelled as an ecosystem it has nothing to do with.
//
// 0x1037 is NXP Semiconductors and was mapped to Aqara; 0x125D is Tuya
// and was mapped to Home Assistant. Both per the CSA ledger.
func TestEcosystemClassificationUsesTheRealVendorIds(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		vendorID uint16
		want     Ecosystem
	}{
		{0x1349, EcosystemApple},
		{0x1384, EcosystemApple}, // Apple adds a second management fabric
		{0x6006, EcosystemGoogle},
		{0x1217, EcosystemAmazon},
		{0x110A, EcosystemSmartThings},
		{0x115F, EcosystemAqara},
		{0x134B, EcosystemHomeAssistant},
		{0x1037, EcosystemUnknown}, // NXP Semiconductors
		{0x125D, EcosystemUnknown}, // Tuya
	} {
		if got := EcosystemForVendor(tc.vendorID); got != tc.want {
			t.Errorf("EcosystemForVendor(%#04x) = %q, want %q", tc.vendorID, got, tc.want)
		}
	}
}
