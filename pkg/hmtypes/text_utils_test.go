// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package hmtypes

import (
	"testing"
)

// TestFixXMLRPCEncoding_MojibakeUmlaut verifies that a string that was
// incorrectly decoded as ISO-8859-1 (the 'ü' → "Ã¼" case) is repaired.
// The CCU stores user-defined strings as raw UTF-8 bytes inside its
// ISO-8859-1 XML-RPC stream; the charset decoder maps each byte as a
// single ISO-8859-1 code point, turning ü (0xC3 0xBC) into Ã¼.
func TestFixXMLRPCEncoding_MojibakeUmlaut(t *testing.T) {
	// "ü" in UTF-8 is bytes 0xC3 0xBC.
	// Decoded naively as ISO-8859-1 those bytes become U+00C3 (Ã) and
	// U+00BC (¼).
	input := "Ã¼" // the mis-decoded form
	want := "ü"
	got := FixXMLRPCEncoding(input)
	if got != want {
		t.Fatalf("FixXMLRPCEncoding(%q) = %q, want %q", input, got, want)
	}
}

// TestFixXMLRPCEncoding_MojibakeUmlautOe verifies the ö case.
// ö is UTF-8 bytes 0xC3 0xB6; decoded as ISO-8859-1 → Ã¶.
func TestFixXMLRPCEncoding_MojibakeUmlautOe(t *testing.T) {
	input := "Ã¶" // mis-decoded ö
	want := "ö"
	got := FixXMLRPCEncoding(input)
	if got != want {
		t.Fatalf("FixXMLRPCEncoding(%q) = %q, want %q", input, got, want)
	}
}

// TestFixXMLRPCEncoding_PureASCII verifies that an already-correct ASCII
// string is returned unchanged. Pure ASCII bytes are valid both as
// ISO-8859-1 and UTF-8; the round-trip is a no-op.
func TestFixXMLRPCEncoding_PureASCII(t *testing.T) {
	input := "Living Room"
	got := FixXMLRPCEncoding(input)
	if got != input {
		t.Fatalf("FixXMLRPCEncoding(%q) = %q, want unchanged %q", input, got, input)
	}
}

// TestFixXMLRPCEncoding_AlreadyCorrectUTF8 verifies that a string that is
// already valid UTF-8 with non-ASCII runes — but cannot have come from an
// ISO-8859-1 mis-decode (it contains code points outside U+0000–U+00FF) —
// is returned unchanged. The re-encode to ISO-8859-1 will fail for such
// code points, triggering the early-return path.
func TestFixXMLRPCEncoding_AlreadyCorrectUTF8(t *testing.T) {
	// U+1F600 (😀) is outside the ISO-8859-1 range.
	input := "Hello 😀"
	got := FixXMLRPCEncoding(input)
	if got != input {
		t.Fatalf("FixXMLRPCEncoding(%q) = %q, want unchanged %q", input, got, input)
	}
}

// TestFixXMLRPCEncoding_EmptyString verifies that an empty string is
// handled without panic and returns empty.
func TestFixXMLRPCEncoding_EmptyString(t *testing.T) {
	got := FixXMLRPCEncoding("")
	if got != "" {
		t.Fatalf("FixXMLRPCEncoding(%q) = %q, want %q", "", got, "")
	}
}

// TestFixXMLRPCEncoding_MixedMojibakeAndASCII verifies that a string
// containing both ASCII and mis-decoded UTF-8 segments is fully repaired.
func TestFixXMLRPCEncoding_MixedMojibakeAndASCII(t *testing.T) {
	// "Küche" stored as UTF-8 (K=0x4B, ü=0xC3 0xBC, che=0x63 0x68 0x65)
	// decoded naively as ISO-8859-1 → "KÃ¼che"
	input := "KÃ¼che"
	want := "Küche"
	got := FixXMLRPCEncoding(input)
	if got != want {
		t.Fatalf("FixXMLRPCEncoding(%q) = %q, want %q", input, got, want)
	}
}

// TestFixXMLRPCEncoding_GenuineISO8859 verifies that a string with genuine
// ISO-8859-1 characters (e.g. U+00E9 é, which is a single byte 0xE9 in
// ISO-8859-1) is returned unchanged because the recovered bytes are not
// valid UTF-8 (0xE9 alone is not a valid UTF-8 leading byte without
// continuation bytes).
func TestFixXMLRPCEncoding_GenuineISO8859(t *testing.T) {
	// é is U+00E9; as ISO-8859-1 it is the single byte 0xE9.
	// Re-encoding "é" → []byte{0xE9}, which is NOT valid UTF-8.
	// So the function must return the original "é".
	input := "café" // "café"
	got := FixXMLRPCEncoding(input)
	if got != input {
		t.Fatalf("FixXMLRPCEncoding(%q) = %q, want unchanged %q", input, got, input)
	}
}
