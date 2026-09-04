// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package textdisplay

import (
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// The two declarations the HmIP-WRCD makes for the channel this profile is
// registered on — internal/model/custom/profiles.go registers IPTextDisplay
// for DeviceType "hmip-wrcd", channel 3, and channel :3 is where both
// parameters live.
//
// Copied verbatim from the HmIP-WRCD's own captured paramset description
// in the device-descriptor corpus checked out beside this repo, address
// VCU4243444:3, paramset VALUES:
//
//	"DISPLAY_DATA_ID":     {"TYPE": "INTEGER", "MIN": 1, "MAX": 5, …}
//	"DISPLAY_DATA_STRING": {"TYPE": "STRING",  "MIN": "", "MAX": "[0x20-0x7E]{16}", …}
//
// Measured, not sampled: a walk over every file in that corpus finds
// DISPLAY_DATA_ID exactly once (MIN 1 / MAX 5) and DISPLAY_DATA_STRING
// exactly once (MAX "[0x20-0x7E]{16}"), both on HmIP-WRCD.
//
// The firmware side agrees and explains why the numbers must stay
// WRCD-scoped: HMIPServer de.eq3.cbcs.devicedescription.channelspecification.
// stateparameter.GeneralStateParameterFactory registers DISPLAY_DATA_ID in
// three variants — (1,2), (1,5) and (0,7) — and DISPLAY_DATA_STRING with four
// different max-value patterns, so neither number is a fleet-wide constant.
const (
	w2CstWRCDDisplayDataIDMin = 1
	w2CstWRCDDisplayDataIDMax = 5
	// A STRING parameter's declared MAX is a constraint pattern, not a
	// number — anything reading it numerically reads nothing.
	w2CstWRCDDisplayDataStringMax = "[0x20-0x7E]{16}"
)

// w2CstDeclaredRowLength pulls the character count out of the declared
// DISPLAY_DATA_STRING constraint pattern rather than restating it.
func w2CstDeclaredRowLength(t *testing.T, pattern string) int {
	t.Helper()
	m := regexp.MustCompile(`\{(\d+)\}$`).FindStringSubmatch(strings.TrimSpace(pattern))
	if m == nil {
		t.Fatalf("DISPLAY_DATA_STRING MAX %q does not end in a {N} repetition count — the length cannot be read out of it", pattern)
	}
	n, err := strconv.Atoi(m[1])
	if err != nil {
		t.Fatalf("DISPLAY_DATA_STRING MAX %q: %v", pattern, err)
	}
	return n
}

// TestW2CstRowGeometryMatchesTheDeviceDeclaration ties both display-geometry
// constants to what the one device this profile serves declares.
//
// The row length is the one that reaches an operator: [MaxRowLength] is
// republished into the Home Assistant discovery payload as the text field's
// `max`, so a value above the device's own limit invites the operator to type
// characters that cannot arrive, and [Row.Validate] lets them through to the
// wire.
func TestW2CstRowGeometryMatchesTheDeviceDeclaration(t *testing.T) {
	t.Parallel()

	declared := w2CstDeclaredRowLength(t, w2CstWRCDDisplayDataStringMax)
	if MaxRowLength != declared {
		t.Errorf("MaxRowLength = %d, but the HmIP-WRCD declares DISPLAY_DATA_STRING MAX %q — %d characters",
			MaxRowLength, w2CstWRCDDisplayDataStringMax, declared)
	}

	if maxDisplayID != w2CstWRCDDisplayDataIDMax {
		t.Errorf("maxDisplayID = %d, but the HmIP-WRCD declares DISPLAY_DATA_ID MAX %d", maxDisplayID, w2CstWRCDDisplayDataIDMax)
	}

	// A row exactly at the declared length is accepted; one character more
	// is refused before it can reach the wire.
	atLimit := Row{ID: w2CstWRCDDisplayDataIDMin, Text: strings.Repeat("A", declared)}
	if err := atLimit.Validate(); err != nil {
		t.Errorf("a %d-character row was rejected, but the device declares room for it: %v", declared, err)
	}
	overLimit := Row{ID: w2CstWRCDDisplayDataIDMin, Text: strings.Repeat("A", declared+1)}
	if err := overLimit.Validate(); err == nil {
		t.Errorf("a %d-character row passed validation, but the HmIP-WRCD declares DISPLAY_DATA_STRING MAX %q",
			declared+1, w2CstWRCDDisplayDataStringMax)
	}
}

// TestW2CstHADiscoveryMaxIsTheDeviceLimit checks the operator-facing half:
// whatever [MaxRowLength] says, the number Home Assistant is told must be the
// device's declared row length, because HA enforces it on the input field.
func TestW2CstHADiscoveryMaxIsTheDeviceLimit(t *testing.T) {
	t.Parallel()

	declared := w2CstDeclaredRowLength(t, w2CstWRCDDisplayDataStringMax)
	_, body := New("VCU4243444:3", &stubWriter{}).HADiscoveryPayload(discoveryCtx{})
	got, ok := body["max"]
	if !ok {
		t.Fatal("HA discovery payload carries no max — the text field would accept any length")
	}
	if got != declared {
		t.Errorf("HA discovery advertises max=%v, but the HmIP-WRCD declares %d characters per row — the operator is invited to type %v characters that cannot arrive",
			got, declared, got)
	}
}
