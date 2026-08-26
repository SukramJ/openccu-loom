// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

// Package-internal tests that exercise unexported helpers (hexDump,
// hexNibble, hexParse) used by the conformance vector runner.
package conformance

import (
	"testing"
)

func TestHexDump_Empty(t *testing.T) {
	t.Parallel()
	got := hexDump([]byte{})
	if got != "<empty>" {
		t.Errorf("hexDump(nil) = %q, want %q", got, "<empty>")
	}
}

func TestHexDump_SingleByte(t *testing.T) {
	t.Parallel()
	got := hexDump([]byte{0xAB})
	if got != "AB" {
		t.Errorf("hexDump([AB]) = %q, want %q", got, "AB")
	}
}

func TestHexDump_MultiBytes(t *testing.T) {
	t.Parallel()
	got := hexDump([]byte{0x00, 0xFF, 0x0A})
	const want = "00 FF 0A"
	if got != want {
		t.Errorf("hexDump([00 FF 0A]) = %q, want %q", got, want)
	}
}

func TestHexDump_AllNibbles(t *testing.T) {
	t.Parallel()
	// Check both digit (0-9) and letter (A-F) nibble paths.
	got := hexDump([]byte{0x19, 0xAF, 0xF0})
	const want = "19 AF F0"
	if got != want {
		t.Errorf("hexDump = %q, want %q", got, want)
	}
}

func TestHexNibble_Digits(t *testing.T) {
	t.Parallel()
	for n := range byte(10) {
		got := hexNibble(n)
		want := '0' + n
		if got != want {
			t.Errorf("hexNibble(%d) = %q, want %q", n, got, want)
		}
	}
}

func TestHexNibble_Letters(t *testing.T) {
	t.Parallel()
	for n := byte(10); n < 16; n++ {
		got := hexNibble(n)
		want := 'A' + (n - 10)
		if got != want {
			t.Errorf("hexNibble(%d) = %q, want %q", n, got, want)
		}
	}
}

func TestMustHex_Valid(t *testing.T) {
	t.Parallel()
	got := MustHex("DEADBEEF")
	want := []byte{0xDE, 0xAD, 0xBE, 0xEF}
	if len(got) != len(want) {
		t.Fatalf("MustHex length: got %d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("MustHex[%d] = 0x%02X, want 0x%02X", i, got[i], want[i])
		}
	}
}

func TestMustHex_WithSpaces(t *testing.T) {
	t.Parallel()
	got := MustHex("DE AD BE EF")
	want := []byte{0xDE, 0xAD, 0xBE, 0xEF}
	if len(got) != len(want) {
		t.Fatalf("MustHex with spaces: got len=%d, want %d", len(got), len(want))
	}
}

func TestMustHex_LowerCase(t *testing.T) {
	t.Parallel()
	got := MustHex("deadbeef")
	want := []byte{0xDE, 0xAD, 0xBE, 0xEF}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("MustHex lower[%d] = 0x%02X, want 0x%02X", i, got[i], want[i])
		}
	}
}

func TestMustHex_Panics_InvalidChar(t *testing.T) {
	t.Parallel()
	defer func() {
		if r := recover(); r == nil {
			t.Error("MustHex(invalid) should have panicked")
		}
	}()
	MustHex("GG")
}

func TestMustHex_Panics_OddNibble(t *testing.T) {
	t.Parallel()
	defer func() {
		if r := recover(); r == nil {
			t.Error("MustHex(odd nibble count) should have panicked")
		}
	}()
	MustHex("A")
}

func TestHexParse_AllValid(t *testing.T) {
	t.Parallel()
	cases := []struct {
		c    byte
		want byte
		ok   bool
	}{
		{'0', 0, true},
		{'9', 9, true},
		{'a', 10, true},
		{'f', 15, true},
		{'A', 10, true},
		{'F', 15, true},
		{'G', 0, false},
		{' ', 0, false},
		{'\n', 0, false},
	}
	for _, tc := range cases {
		v, ok := hexParse(tc.c)
		if ok != tc.ok {
			t.Errorf("hexParse(%q) ok=%v, want %v", tc.c, ok, tc.ok)
		}
		if ok && v != tc.want {
			t.Errorf("hexParse(%q) = %d, want %d", tc.c, v, tc.want)
		}
	}
}
