// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package mdns

import (
	"strings"
	"testing"
)

// TestGenerateRotatingID_ChipReferenceVector locks the binary +
// hex-string output against the canonical chip reference vector from
// `src/setup_payload/tests/TestAdditionalDataPayload.cpp` —
//
//	UniqueID         = {0x00..0xFF stepping 0x11}
//	LifetimeCounter  = 10
//	expected         = "0A00D00561E77A68A9FD975057375B9283A8"
//
// Matching this byte-exact guarantees openccu-loom's commissionable
// `RI` TXT key is interpretable by every chip-derived Matter
// controller (Apple Home, Google Home, chip-tool, …).
func TestGenerateRotatingID_ChipReferenceVector(t *testing.T) {
	t.Parallel()
	uniqueID := []byte{
		0x00, 0x11, 0x22, 0x33, 0x44, 0x55, 0x66, 0x77,
		0x88, 0x99, 0xaa, 0xbb, 0xcc, 0xdd, 0xee, 0xff,
	}
	got := GenerateRotatingID(uniqueID, 10)
	const want = "0A00D00561E77A68A9FD975057375B9283A8"
	if got != want {
		t.Fatalf("GenerateRotatingID:\n  got  = %s\n  want = %s", got, want)
	}
	if len(got) != 36 {
		t.Errorf("hex length = %d, want 36", len(got))
	}
	if strings.ToUpper(got) != got {
		t.Errorf("output not uppercase: %s", got)
	}
}

// TestGenerateRotatingID_ZeroCounter verifies the counter=0 corner —
// the 2-byte LE prefix is exactly "0000".
func TestGenerateRotatingID_ZeroCounter(t *testing.T) {
	t.Parallel()
	uniqueID := make([]byte, RotatingIDUniqueIDLength)
	for i := range uniqueID {
		uniqueID[i] = 0xAB
	}
	got := GenerateRotatingID(uniqueID, 0)
	if len(got) != 36 {
		t.Fatalf("hex length = %d, want 36", len(got))
	}
	if got[:4] != "0000" {
		t.Errorf("counter prefix = %q, want 0000", got[:4])
	}
}

// TestGenerateRotatingID_MaxCounter verifies the uint16 ceiling
// 0xFFFF emits "FFFF" as the prefix.
func TestGenerateRotatingID_MaxCounter(t *testing.T) {
	t.Parallel()
	uniqueID := make([]byte, RotatingIDUniqueIDLength)
	got := GenerateRotatingID(uniqueID, 0xFFFF)
	if got[:4] != "FFFF" {
		t.Errorf("counter prefix = %q, want FFFF", got[:4])
	}
}

// TestGenerateRotatingID_ShortUniqueIDReturnsEmpty verifies the
// pre-init guard: callers that pass a too-short UniqueID must see an
// empty string back so the mDNS layer can suppress the `RI` TXT key.
func TestGenerateRotatingID_ShortUniqueIDReturnsEmpty(t *testing.T) {
	t.Parallel()
	uniqueID := make([]byte, RotatingIDUniqueIDLength-1)
	if got := GenerateRotatingID(uniqueID, 7); got != "" {
		t.Fatalf("short UniqueID: got %q, want empty", got)
	}
}

// TestGenerateRotatingID_CounterAdvancesChangesValue verifies that
// distinct LifetimeCounter values produce distinct RIs (so the
// rotating-id surface actually rotates per the spec).
func TestGenerateRotatingID_CounterAdvancesChangesValue(t *testing.T) {
	t.Parallel()
	uniqueID := make([]byte, RotatingIDUniqueIDLength)
	a := GenerateRotatingID(uniqueID, 1)
	b := GenerateRotatingID(uniqueID, 2)
	if a == b {
		t.Fatalf("RI identical for counter 1 vs 2: %s", a)
	}
}

// TestMustGenerateRotatingID_PanicsOnShortUniqueID locks the
// fail-loud contract for the helper.
func TestMustGenerateRotatingID_PanicsOnShortUniqueID(t *testing.T) {
	t.Parallel()
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("MustGenerateRotatingID did not panic on short UniqueID")
		}
	}()
	MustGenerateRotatingID(make([]byte, 8), 0)
}
