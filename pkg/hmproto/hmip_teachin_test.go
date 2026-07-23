// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// hmip_teachin_test.go — tests for NormalizeSGTIN and NormalizeHmIPKey: the
// keyserver-less HmIP LOCAL teach-in input normalisation (operator-entered
// SGTIN and device key, including the CCU WebUI's Base32 label form).

package hmproto_test

import (
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmproto"
)

// ---------------------------------------------------------------------------
// NormalizeHmIPKey — Base32 label-form decoding
// ---------------------------------------------------------------------------

// TestNormalizeHmIPKeyLabelFormVectors pins the exact byte-for-byte output of
// the Base32-to-hex decode against vectors verified against the CCU WebUI's
// convertHmIPKeyBase32ToBase16 algorithm (right-to-left 5-bit accumulation,
// flushed into a 16-byte buffer from the rightmost byte, no final flush).
func TestNormalizeHmIPKeyLabelFormVectors(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		label string
		want  string
	}{
		{
			name:  "sequential alphabet start",
			label: "0123456789ABCEFGHJKLMNPQRS",
			want:  "0110C8531D0952D8D73E1194E95B5F19",
		},
		{
			name:  "rotated alphabet start",
			label: "TUWXYZ0123456789ABCEFGHJKL",
			want:  "5BE77DF00443214C74254B635CF84653",
		},
		{
			name:  "repeated first symbol",
			label: "AAAAAAAAAAAAAAAAAAAAAAAAAA",
			want:  "4A5294A5294A5294A5294A5294A5294A",
		},
		{
			name:  "24-char label leaves 0x00 lead-in bytes",
			label: "0123456789ABCEFGHJKLMNPQ",
			want:  "0000443214C74254B635CF84653A56D7",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := hmproto.NormalizeHmIPKey(tc.label)
			if err != nil {
				t.Fatalf("NormalizeHmIPKey(%q) unexpected error: %v", tc.label, err)
			}
			if got != tc.want {
				t.Fatalf("NormalizeHmIPKey(%q) = %q, want %q", tc.label, got, tc.want)
			}
		})
	}
}

// TestNormalizeHmIPKeyLabelFormAcceptsSeparatorsAndLowercase verifies that
// dashes/spaces in a label-form key are stripped and mixed case is folded
// to uppercase before decoding, mirroring how an operator copies the key
// straight off the device label.
func TestNormalizeHmIPKeyLabelFormAcceptsSeparatorsAndLowercase(t *testing.T) {
	t.Parallel()
	const want = "0110C8531D0952D8D73E1194E95B5F19"
	got, err := hmproto.NormalizeHmIPKey("0123-4567 89ab-cefg-hjkl-mnpq-rs")
	if err != nil {
		t.Fatalf("NormalizeHmIPKey with separators: unexpected error: %v", err)
	}
	if got != want {
		t.Fatalf("NormalizeHmIPKey with separators = %q, want %q", got, want)
	}
}

// ---------------------------------------------------------------------------
// NormalizeHmIPKey — 32-hex passthrough
// ---------------------------------------------------------------------------

// TestNormalizeHmIPKey32HexPassesThroughUppercased verifies that a
// full-length 32-hex key round-trips unchanged except for uppercasing and
// separator stripping — it must not be misinterpreted as a Base32 label.
func TestNormalizeHmIPKey32HexPassesThroughUppercased(t *testing.T) {
	t.Parallel()
	got, err := hmproto.NormalizeHmIPKey("0110c853-1d09-52d8-d73e-1194-e95b-5f19")
	if err != nil {
		t.Fatalf("NormalizeHmIPKey 32-hex passthrough: unexpected error: %v", err)
	}
	const want = "0110C8531D0952D8D73E1194E95B5F19"
	if got != want {
		t.Fatalf("NormalizeHmIPKey 32-hex passthrough = %q, want %q", got, want)
	}
}

// ---------------------------------------------------------------------------
// NormalizeHmIPKey — rejections
// ---------------------------------------------------------------------------

// TestNormalizeHmIPKeyRejectsForbiddenLabelCharacters verifies that D, I, O
// and V — omitted from the CCU's 32-character label alphabet to avoid
// misreadings — are rejected rather than silently decoded.
func TestNormalizeHmIPKeyRejectsForbiddenLabelCharacters(t *testing.T) {
	t.Parallel()
	for _, ch := range []string{"D", "I", "O", "V"} {
		t.Run(ch, func(t *testing.T) {
			t.Parallel()
			// A short label containing the forbidden character.
			_, err := hmproto.NormalizeHmIPKey("ABC" + ch + "EFG")
			if err == nil {
				t.Fatalf("NormalizeHmIPKey with forbidden character %q: expected error, got nil", ch)
			}
		})
	}
}

// TestNormalizeHmIPKeyRejectsOverlongInput verifies that an input of 33 or
// more characters is rejected outright — it is too long for a 32-hex key and
// too long for the Base32 label form.
func TestNormalizeHmIPKeyRejectsOverlongInput(t *testing.T) {
	t.Parallel()
	// 33 chars: one hex digit past the 32-hex boundary.
	_, err := hmproto.NormalizeHmIPKey("0110C8531D0952D8D73E1194E95B5F190")
	if err == nil {
		t.Fatal("NormalizeHmIPKey with 33 characters: expected error, got nil")
	}
}

// TestNormalizeHmIPKeyRejectsEmpty verifies the empty-input guard.
func TestNormalizeHmIPKeyRejectsEmpty(t *testing.T) {
	t.Parallel()
	_, err := hmproto.NormalizeHmIPKey("")
	if err == nil {
		t.Fatal("NormalizeHmIPKey(\"\"): expected error, got nil")
	}
}

// TestNormalizeHmIPKeyRejectsEmptyAfterSeparatorStrip verifies that an input
// consisting solely of dashes/spaces (which strips down to empty) is
// rejected the same way as a literal empty string.
func TestNormalizeHmIPKeyRejectsEmptyAfterSeparatorStrip(t *testing.T) {
	t.Parallel()
	_, err := hmproto.NormalizeHmIPKey("- - -")
	if err == nil {
		t.Fatal("NormalizeHmIPKey(\"- - -\"): expected error, got nil")
	}
}

// TestNormalizeHmIPKeyRejects32CharNonHex verifies that an input of exactly
// 32 characters that is not pure hex (e.g. drawn from the label alphabet but
// containing letters past F) is rejected rather than silently treated as a
// label — the length-32 boundary only ever means "hex", never "label". The
// full 32-character label alphabet itself is a convenient fixture: it has
// the right length but contains G/H/J/K/... which are not hex digits.
func TestNormalizeHmIPKeyRejects32CharNonHex(t *testing.T) {
	t.Parallel()
	const fullLabelAlphabet = "0123456789ABCEFGHJKLMNPQRSTUWXYZ"
	if len(fullLabelAlphabet) != 32 {
		t.Fatalf("test fixture length = %d, want 32", len(fullLabelAlphabet))
	}
	_, err := hmproto.NormalizeHmIPKey(fullLabelAlphabet)
	if err == nil {
		t.Fatal("NormalizeHmIPKey with 32 non-hex characters: expected error, got nil")
	}
}

// ---------------------------------------------------------------------------
// NormalizeSGTIN
// ---------------------------------------------------------------------------

// TestNormalizeSGTINStripsDashesAndUppercases verifies the happy path: an
// operator-pasted, dash-separated SGTIN normalises to 24 uppercase hex
// characters.
func TestNormalizeSGTINStripsDashesAndUppercases(t *testing.T) {
	t.Parallel()
	got, err := hmproto.NormalizeSGTIN("3014-f711-a061-a7d5-6989-2a67")
	if err != nil {
		t.Fatalf("NormalizeSGTIN: unexpected error: %v", err)
	}
	const want = "3014F711A061A7D569892A67"
	if got != want {
		t.Fatalf("NormalizeSGTIN = %q, want %q", got, want)
	}
	if len(got) != 24 {
		t.Fatalf("NormalizeSGTIN result length = %d, want 24", len(got))
	}
}

// TestNormalizeSGTINRejectsWrongLength verifies that an SGTIN shorter or
// longer than 24 hex characters (after separator stripping) is rejected.
func TestNormalizeSGTINRejectsWrongLength(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		in   string
	}{
		{"too short", "3014-F711-A061-A7D5"},
		{"too long", "3014-F711-A061-A7D5-6989-2A67-FF"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if _, err := hmproto.NormalizeSGTIN(tc.in); err == nil {
				t.Fatalf("NormalizeSGTIN(%q): expected error, got nil", tc.in)
			}
		})
	}
}

// TestNormalizeSGTINRejectsNonHex verifies that non-hex characters (beyond
// the stripped dash/space separators) are rejected.
func TestNormalizeSGTINRejectsNonHex(t *testing.T) {
	t.Parallel()
	// 24 characters after stripping, but "G" and "Z" are not hex digits.
	_, err := hmproto.NormalizeSGTIN("3014-G711-A061-A7D5-6989-2AZ7")
	if err == nil {
		t.Fatal("NormalizeSGTIN with non-hex characters: expected error, got nil")
	}
}
