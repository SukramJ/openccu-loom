// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package tlv

import (
	"errors"
	"testing"
)

// TestValidate_ContextTagAtTopLevel pins the chip TLVReader.cpp:822-823
// invariant: a context tag emitted outside any container fails the
// strict check.
func TestValidate_ContextTagAtTopLevel(t *testing.T) {
	t.Parallel()
	enc := NewEncoder()
	enc.PutBool(ContextTag(7), true)
	wire, _ := enc.Bytes()
	if err := Validate(wire); !errors.Is(err, ErrStrictTagViolation) {
		t.Fatalf("Validate = %v, want ErrStrictTagViolation", err)
	}
}

// TestValidate_AnonymousTagInStructure pins chip TLVReader.cpp:826-827:
// anonymous tags inside a Structure are rejected (except the implicit
// EndOfContainer).
func TestValidate_AnonymousTagInStructure(t *testing.T) {
	t.Parallel()
	enc := NewEncoder()
	enc.StartStruct(AnonymousTag())
	enc.PutBool(AnonymousTag(), true) // illegal: anon tag inside structure
	if err := enc.EndContainer(); err != nil {
		t.Fatalf("end: %v", err)
	}
	wire, _ := enc.Bytes()
	if err := Validate(wire); !errors.Is(err, ErrStrictTagViolation) {
		t.Fatalf("Validate = %v, want ErrStrictTagViolation", err)
	}
}

// TestValidate_NonAnonymousTagInArray pins chip TLVReader.cpp:830-831:
// non-anonymous tags inside an Array are rejected.
func TestValidate_NonAnonymousTagInArray(t *testing.T) {
	t.Parallel()
	// The writer panics on context-tag-in-array (see encode.go), so we
	// hand-craft the wire bytes. Control byte 0x21 = (TagKind=1<<5) |
	// TypeBoolTrue=0x09 — wait, 0x09 is BoolTrue; for context we want
	// 0x25 = (1<<5) | 0x05 (UnsignedInt2). Frame:
	//   16            // StartArray, anonymous tag (TagKind=0, Type=0x16)
	//   25 07 00 00   // context tag 7, UnsignedInt2 value 0 — ILLEGAL inside Array
	//   18            // EndOfContainer
	wire := []byte{0x16, 0x25, 0x07, 0x00, 0x00, 0x18}
	if err := Validate(wire); !errors.Is(err, ErrStrictTagViolation) {
		t.Fatalf("Validate = %v, want ErrStrictTagViolation", err)
	}
}

// TestValidate_LegitStructPasses confirms the validator accepts a
// well-formed Structure containing context-tagged fields — the
// canonical IM-message shape.
func TestValidate_LegitStructPasses(t *testing.T) {
	t.Parallel()
	enc := NewEncoder()
	enc.StartStruct(AnonymousTag())
	enc.PutBool(ContextTag(1), true)
	enc.PutUint(ContextTag(2), 42)
	if err := enc.EndContainer(); err != nil {
		t.Fatalf("end: %v", err)
	}
	wire, _ := enc.Bytes()
	if err := Validate(wire); err != nil {
		t.Fatalf("Validate rejected legit struct: %v (wire=% X)", err, wire)
	}
}

// TestValidate_LegitArrayPasses confirms the validator accepts an
// Array filled with anonymous-tag scalar elements.
func TestValidate_LegitArrayPasses(t *testing.T) {
	t.Parallel()
	enc := NewEncoder()
	enc.StartArray(AnonymousTag())
	enc.PutUint(AnonymousTag(), 1)
	enc.PutUint(AnonymousTag(), 2)
	enc.PutUint(AnonymousTag(), 3)
	if err := enc.EndContainer(); err != nil {
		t.Fatalf("end: %v", err)
	}
	wire, _ := enc.Bytes()
	if err := Validate(wire); err != nil {
		t.Fatalf("Validate rejected legit array: %v (wire=% X)", err, wire)
	}
}

// TestValidate_UnclosedContainer ensures the validator catches
// truncated streams where a Structure opens but never closes.
func TestValidate_UnclosedContainer(t *testing.T) {
	t.Parallel()
	// 15 = StartStruct, anonymous tag — no EndOfContainer follows.
	wire := []byte{0x15}
	err := Validate(wire)
	if err == nil {
		t.Fatal("Validate accepted unclosed container")
	}
}
