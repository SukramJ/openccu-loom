// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package tlv

import (
	"strings"
	"testing"
)

// TestPutUTF8WithMax_WithinLimit verifies that a string shorter than maxBytes
// is written verbatim (no trimming, same output as PutUTF8).
func TestPutUTF8WithMax_WithinLimit(t *testing.T) {
	t.Parallel()
	s := "hello"
	enc := NewEncoder()
	enc.PutUTF8WithMax(AnonymousTag(), s, 32)
	wire, err := enc.Bytes()
	if err != nil {
		t.Fatalf("Bytes: %v", err)
	}

	dec := NewDecoder(wire)
	el, err := dec.Next()
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	got := el.String
	if el.Type != TypeUTF8Str1 && el.Type != TypeUTF8Str2 && el.Type != TypeUTF8Str4 && el.Type != TypeUTF8Str8 {
		t.Fatalf("element type %v is not a UTF-8 string type", el.Type)
	}
	if got != s {
		t.Errorf("got %q, want %q", got, s)
	}
}

// TestPutUTF8WithMax_ExceedsLimit verifies that an over-long string is trimmed
// to at most maxBytes and the result is still valid UTF-8.
func TestPutUTF8WithMax_ExceedsLimit(t *testing.T) {
	t.Parallel()
	s := strings.Repeat("a", 100)
	maxBytes := 10

	enc := NewEncoder()
	enc.PutUTF8WithMax(AnonymousTag(), s, maxBytes)
	wire, err := enc.Bytes()
	if err != nil {
		t.Fatalf("Bytes: %v", err)
	}

	dec := NewDecoder(wire)
	el, err := dec.Next()
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	got := el.String
	if el.Type != TypeUTF8Str1 && el.Type != TypeUTF8Str2 && el.Type != TypeUTF8Str4 && el.Type != TypeUTF8Str8 {
		t.Fatalf("element type %v is not a UTF-8 string type", el.Type)
	}
	if len(got) > maxBytes {
		t.Errorf("trimmed string length %d > maxBytes %d", len(got), maxBytes)
	}
}

// TestPutUTF8WithMax_MultibyteRune verifies rune-boundary safety: trimming a
// string containing multi-byte runes must not produce invalid UTF-8.
func TestPutUTF8WithMax_MultibyteRune(t *testing.T) {
	t.Parallel()
	// "€" is 3 bytes in UTF-8; build a string where naive byte-truncation
	// would land mid-rune.
	s := "ab€cd"
	maxBytes := 4 // cuts mid-€ at naive truncation

	enc := NewEncoder()
	enc.PutUTF8WithMax(AnonymousTag(), s, maxBytes)
	wire, err := enc.Bytes()
	if err != nil {
		t.Fatalf("Bytes: %v", err)
	}

	dec := NewDecoder(wire)
	el, err := dec.Next()
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	got := el.String
	if el.Type != TypeUTF8Str1 && el.Type != TypeUTF8Str2 && el.Type != TypeUTF8Str4 && el.Type != TypeUTF8Str8 {
		t.Fatalf("element type %v is not a UTF-8 string type", el.Type)
	}
	// Result must be valid UTF-8 and at most maxBytes bytes.
	if len(got) > maxBytes {
		t.Errorf("trimmed length %d > maxBytes %d", len(got), maxBytes)
	}
	for i, r := range got {
		if r == '�' {
			t.Errorf("replacement rune at position %d — invalid UTF-8 after trim", i)
		}
	}
}

// TestPutUTF8WithMax_ZeroMaxSkipsValidation verifies that maxBytes ≤ 0
// disables validation and the string is written as-is.
func TestPutUTF8WithMax_ZeroMaxSkipsValidation(t *testing.T) {
	t.Parallel()
	s := strings.Repeat("x", 500)

	enc := NewEncoder()
	enc.PutUTF8WithMax(AnonymousTag(), s, 0)
	wire, err := enc.Bytes()
	if err != nil {
		t.Fatalf("Bytes: %v", err)
	}

	dec := NewDecoder(wire)
	el, err := dec.Next()
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	got := el.String
	if el.Type != TypeUTF8Str1 && el.Type != TypeUTF8Str2 && el.Type != TypeUTF8Str4 && el.Type != TypeUTF8Str8 {
		t.Fatalf("element type %v is not a UTF-8 string type", el.Type)
	}
	if got != s {
		t.Errorf("got len %d, want len %d", len(got), len(s))
	}
}
