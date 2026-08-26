// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package tlv

import (
	"bytes"
	"encoding/hex"
	"errors"
	"io"
	"math"
	"strings"
	"testing"
)

// roundTrip encodes a single element and decodes it back, asserting
// the decoded form matches the input. Helper for the type-by-type
// tests.
func roundTrip(t *testing.T, name string, encode func(*Encoder), check func(*testing.T, Element)) {
	t.Helper()
	enc := NewEncoder()
	encode(enc)
	wire, err := enc.Bytes()
	if err != nil {
		t.Fatalf("%s: Bytes err: %v", name, err)
	}
	dec := NewDecoder(wire)
	el, err := dec.Next()
	if err != nil {
		t.Fatalf("%s: Next err: %v", name, err)
	}
	check(t, el)
	if dec.Remaining() != 0 {
		t.Errorf("%s: %d trailing bytes", name, dec.Remaining())
	}
}

// TestRoundTripBoolFalse round-trips a false element. The control
// byte must encode the literal 0x08 type per Matter spec.
func TestRoundTripBoolFalse(t *testing.T) {
	roundTrip(t, "bool-false",
		func(e *Encoder) { e.PutBool(AnonymousTag(), false) },
		func(t *testing.T, el Element) {
			t.Helper()
			if el.Type != TypeBoolFalse || el.Bool {
				t.Fatalf("got type=0x%02X bool=%v", el.Type, el.Bool)
			}
		})
}

// TestRoundTripBoolTrue is the symmetric companion of
// [TestRoundTripBoolFalse] — control byte must encode 0x09.
func TestRoundTripBoolTrue(t *testing.T) {
	roundTrip(t, "bool-true",
		func(e *Encoder) { e.PutBool(AnonymousTag(), true) },
		func(t *testing.T, el Element) {
			t.Helper()
			if el.Type != TypeBoolTrue || !el.Bool {
				t.Fatalf("got type=0x%02X bool=%v", el.Type, el.Bool)
			}
		})
}

// TestRoundTripNull surfaces the null marker.
func TestRoundTripNull(t *testing.T) {
	roundTrip(t, "null",
		func(e *Encoder) { e.PutNull(AnonymousTag()) },
		func(t *testing.T, el Element) {
			t.Helper()
			if el.Type != TypeNull || !el.IsNull {
				t.Fatalf("got type=0x%02X isNull=%v", el.Type, el.IsNull)
			}
		})
}

// TestUnsignedIntWidthSelection verifies the width-by-magnitude
// selection rules.
func TestUnsignedIntWidthSelection(t *testing.T) {
	cases := []struct {
		v       uint64
		wantTyp ElementType
	}{
		{0, TypeUnsignedInt1},
		{0xFF, TypeUnsignedInt1},
		{0x100, TypeUnsignedInt2},
		{0xFFFF, TypeUnsignedInt2},
		{0x10000, TypeUnsignedInt4},
		{0xFFFFFFFF, TypeUnsignedInt4},
		{0x100000000, TypeUnsignedInt8},
		{math.MaxUint64, TypeUnsignedInt8},
	}
	for _, tc := range cases {
		enc := NewEncoder()
		enc.PutUint(AnonymousTag(), tc.v)
		wire, _ := enc.Bytes()
		dec := NewDecoder(wire)
		el, err := dec.Next()
		if err != nil {
			t.Fatalf("v=%d: Next err: %v", tc.v, err)
		}
		if el.Type != tc.wantTyp {
			t.Errorf("v=%d: type=0x%02X, want 0x%02X", tc.v, el.Type, tc.wantTyp)
		}
		if el.Uint != tc.v {
			t.Errorf("v=%d: decoded %d", tc.v, el.Uint)
		}
	}
}

// TestSignedIntWidthSelection covers signed positive, signed negative,
// and full-range edges.
func TestSignedIntWidthSelection(t *testing.T) {
	cases := []struct {
		v       int64
		wantTyp ElementType
	}{
		{0, TypeSignedInt1},
		{127, TypeSignedInt1},
		{-128, TypeSignedInt1},
		{128, TypeSignedInt2},
		{-129, TypeSignedInt2},
		{32767, TypeSignedInt2},
		{-32768, TypeSignedInt2},
		{32768, TypeSignedInt4},
		{-32769, TypeSignedInt4},
		{math.MaxInt32, TypeSignedInt4},
		{math.MinInt32, TypeSignedInt4},
		{int64(math.MaxInt32) + 1, TypeSignedInt8},
		{int64(math.MinInt32) - 1, TypeSignedInt8},
		{math.MaxInt64, TypeSignedInt8},
		{math.MinInt64, TypeSignedInt8},
	}
	for _, tc := range cases {
		enc := NewEncoder()
		enc.PutInt(AnonymousTag(), tc.v)
		wire, _ := enc.Bytes()
		dec := NewDecoder(wire)
		el, err := dec.Next()
		if err != nil {
			t.Fatalf("v=%d: Next err: %v", tc.v, err)
		}
		if el.Type != tc.wantTyp {
			t.Errorf("v=%d: type=0x%02X, want 0x%02X", tc.v, el.Type, tc.wantTyp)
		}
		if el.Int != tc.v {
			t.Errorf("v=%d: decoded %d", tc.v, el.Int)
		}
	}
}

// TestFloat32RoundTrip — IEEE 754 single-precision.
func TestFloat32RoundTrip(t *testing.T) {
	roundTrip(t, "f32",
		func(e *Encoder) { e.PutFloat32(AnonymousTag(), 3.5) },
		func(t *testing.T, el Element) {
			t.Helper()
			if el.Type != TypeFloat4 || float32(el.Float) != 3.5 {
				t.Fatalf("got type=0x%02X float=%v", el.Type, el.Float)
			}
		})
}

// TestFloat64RoundTrip — IEEE 754 double-precision; the test value
// chosen so the round-trip is exact.
func TestFloat64RoundTrip(t *testing.T) {
	roundTrip(t, "f64",
		func(e *Encoder) { e.PutFloat64(AnonymousTag(), 1.234567890123) },
		func(t *testing.T, el Element) {
			t.Helper()
			if el.Type != TypeFloat8 || el.Float != 1.234567890123 {
				t.Fatalf("got type=0x%02X float=%v", el.Type, el.Float)
			}
		})
}

// TestUTF8RoundTripShort uses a sub-256-byte length so the encoder
// picks the 1-byte length prefix.
func TestUTF8RoundTripShort(t *testing.T) {
	roundTrip(t, "utf8-1",
		func(e *Encoder) { e.PutUTF8(AnonymousTag(), "hello matter") },
		func(t *testing.T, el Element) {
			t.Helper()
			if el.Type != TypeUTF8Str1 || el.String != "hello matter" {
				t.Fatalf("got type=0x%02X str=%q", el.Type, el.String)
			}
		})
}

// TestUTF8WidthEscalates256B forces the 2-byte length prefix.
func TestUTF8WidthEscalates256B(t *testing.T) {
	body := bytes.Repeat([]byte("a"), 300)
	roundTrip(t, "utf8-2",
		func(e *Encoder) { e.PutUTF8(AnonymousTag(), string(body)) },
		func(t *testing.T, el Element) {
			t.Helper()
			if el.Type != TypeUTF8Str2 || el.String != string(body) {
				t.Fatalf("got type=0x%02X len=%d", el.Type, len(el.String))
			}
		})
}

// TestOctetsRoundTrip — octet-string body retained as a copy so a
// later Next() call cannot invalidate the caller's slice.
func TestOctetsRoundTrip(t *testing.T) {
	body := []byte{0xDE, 0xAD, 0xBE, 0xEF}
	roundTrip(t, "octets",
		func(e *Encoder) { e.PutOctets(AnonymousTag(), body) },
		func(t *testing.T, el Element) {
			t.Helper()
			if el.Type != TypeOctetStr1 || !bytes.Equal(el.Octets, body) {
				t.Fatalf("got type=0x%02X octets=% X", el.Type, el.Octets)
			}
		})
}

// --- Tag classes ---

// TestContextTagRoundTrip locks the 1-byte context-tag wire shape.
func TestContextTagRoundTrip(t *testing.T) {
	enc := NewEncoder()
	enc.PutBool(ContextTag(7), true)
	wire, _ := enc.Bytes()
	// control byte: kind=1<<5 | type=0x09 = 0x29; tag byte: 0x07.
	if wire[0] != 0x29 || wire[1] != 0x07 {
		t.Fatalf("wire=% X, want 29 07", wire)
	}
	dec := NewDecoder(wire)
	el, _ := dec.Next()
	if el.Tag.Kind != TagKindContext || el.Tag.Number != 7 {
		t.Fatalf("tag=%+v, want context/7", el.Tag)
	}
}

// TestCommonTagWidthSelection — small numbers use 2 bytes, large use 4.
func TestCommonTagWidthSelection(t *testing.T) {
	enc := NewEncoder()
	enc.PutBool(CommonTag(0xFFFF), true)
	enc.PutBool(CommonTag(0x10000), true)
	wire, _ := enc.Bytes()
	dec := NewDecoder(wire)
	el1, _ := dec.Next()
	if el1.Tag.Kind != TagKindCommonProfile2 || el1.Tag.Number != 0xFFFF {
		t.Fatalf("el1 tag=%+v", el1.Tag)
	}
	el2, _ := dec.Next()
	if el2.Tag.Kind != TagKindCommonProfile4 || el2.Tag.Number != 0x10000 {
		t.Fatalf("el2 tag=%+v", el2.Tag)
	}
}

// TestFullyQualifiedTagRoundTrip locks the 6-byte wire shape and the
// vendor / profile / number split.
func TestFullyQualifiedTagRoundTrip(t *testing.T) {
	tag := FullyQualifiedTag(0xFFF1, 0x1234, 0xABCD)
	enc := NewEncoder()
	enc.PutNull(tag)
	wire, _ := enc.Bytes()
	if got := len(wire); got != 1+6 {
		t.Fatalf("len(wire)=%d, want 7", got)
	}
	dec := NewDecoder(wire)
	el, _ := dec.Next()
	if el.Tag != tag {
		t.Fatalf("tag=%+v, want %+v", el.Tag, tag)
	}
}

// --- Containers ---

// TestStructureRoundTrip exercises a small structure with two
// context-tagged fields.
func TestStructureRoundTrip(t *testing.T) {
	enc := NewEncoder()
	enc.StartStruct(AnonymousTag())
	enc.PutBool(ContextTag(0), true)
	enc.PutUint(ContextTag(1), 42)
	if err := enc.EndContainer(); err != nil {
		t.Fatalf("EndContainer err: %v", err)
	}
	wire, err := enc.Bytes()
	if err != nil {
		t.Fatalf("Bytes err: %v", err)
	}

	dec := NewDecoder(wire)
	open, err := dec.Next()
	if err != nil || !open.IsContainer || open.Type != TypeStructure {
		t.Fatalf("open=%+v err=%v", open, err)
	}
	field0, err := dec.Next()
	if err != nil || field0.Tag.Number != 0 || !field0.Bool {
		t.Fatalf("field0=%+v err=%v", field0, err)
	}
	field1, err := dec.Next()
	if err != nil || field1.Tag.Number != 1 || field1.Uint != 42 {
		t.Fatalf("field1=%+v err=%v", field1, err)
	}
	end, err := dec.Next()
	if err != nil || !end.IsEndContainer {
		t.Fatalf("end=%+v err=%v", end, err)
	}
	if dec.Remaining() != 0 {
		t.Errorf("trailing bytes: %d", dec.Remaining())
	}
}

// TestNestedContainers exercises a Structure-in-Array.
func TestNestedContainers(t *testing.T) {
	enc := NewEncoder()
	enc.StartArray(ContextTag(0))
	enc.StartStruct(AnonymousTag())
	enc.PutUint(ContextTag(0), 1)
	_ = enc.EndContainer()
	enc.StartStruct(AnonymousTag())
	enc.PutUint(ContextTag(0), 2)
	_ = enc.EndContainer()
	_ = enc.EndContainer()
	wire, err := enc.Bytes()
	if err != nil {
		t.Fatalf("Bytes err: %v", err)
	}

	// Walk the decoded element stream and verify the depth-balanced
	// sequence of opens and closes.
	dec := NewDecoder(wire)
	depth := 0
	count := 0
	for {
		el, err := dec.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("Next err: %v at pos %d", err, dec.Pos())
		}
		switch {
		case el.IsContainer:
			depth++
		case el.IsEndContainer:
			depth--
		}
		count++
	}
	if depth != 0 {
		t.Errorf("unbalanced depth: %d", depth)
	}
	// 1 array open + 2 × (struct open + uint + struct close) + 1 array close = 8.
	if count != 8 {
		t.Errorf("element count = %d, want 8", count)
	}
}

// TestUnbalancedEncoderRefusesBytes catches the partial-stream case.
func TestUnbalancedEncoderRefusesBytes(t *testing.T) {
	enc := NewEncoder()
	enc.StartStruct(AnonymousTag())
	enc.PutBool(ContextTag(0), true)
	// missing EndContainer
	_, err := enc.Bytes()
	if !errors.Is(err, ErrUnbalancedContainer) {
		t.Fatalf("err = %v, want ErrUnbalancedContainer", err)
	}
}

// TestEndContainerWithoutOpenFails refuses a stray close.
func TestEndContainerWithoutOpenFails(t *testing.T) {
	enc := NewEncoder()
	if err := enc.EndContainer(); !errors.Is(err, ErrUnbalancedContainer) {
		t.Fatalf("err = %v, want ErrUnbalancedContainer", err)
	}
}

// --- Error paths ---

// TestDecoderTruncatedTag surfaces ErrTruncated when the tag bytes
// are missing.
func TestDecoderTruncatedTag(t *testing.T) {
	// control byte advertises a 6-byte fully-qualified tag, but the
	// payload truncates after the control byte.
	dec := NewDecoder([]byte{0xC9}) // FQ6 | bool-true
	_, err := dec.Next()
	if !errors.Is(err, ErrTruncated) {
		t.Fatalf("err = %v, want ErrTruncated", err)
	}
}

// TestDecoderTruncatedValue surfaces ErrTruncated when the value
// payload runs short.
func TestDecoderTruncatedValue(t *testing.T) {
	// 0x05 = anonymous + uint16; only one byte of value present.
	dec := NewDecoder([]byte{0x05, 0xFF})
	_, err := dec.Next()
	if !errors.Is(err, ErrTruncated) {
		t.Fatalf("err = %v, want ErrTruncated", err)
	}
}

// TestDecoderLengthOverflow rejects a string whose declared length
// exceeds the buffer.
func TestDecoderLengthOverflow(t *testing.T) {
	// 0x0C = anonymous UTF-8 string (1-byte length); declared length
	// 0xFF but only 2 body bytes follow.
	dec := NewDecoder([]byte{0x0C, 0xFF, 'a', 'b'})
	_, err := dec.Next()
	if !errors.Is(err, ErrLengthOverflow) {
		t.Fatalf("err = %v, want ErrLengthOverflow", err)
	}
}

// TestDecoderUnknownElementType rejects a control byte whose lower 5
// bits do not name a known type.
func TestDecoderUnknownElementType(t *testing.T) {
	dec := NewDecoder([]byte{0x1F}) // anonymous + reserved type 0x1F
	_, err := dec.Next()
	if !errors.Is(err, ErrInvalidElementType) {
		t.Fatalf("err = %v, want ErrInvalidElementType", err)
	}
}

// TestDecoderEOFAtElementBoundary returns io.EOF after the last
// element (clean stream).
func TestDecoderEOFAtElementBoundary(t *testing.T) {
	enc := NewEncoder()
	enc.PutBool(AnonymousTag(), true)
	wire, _ := enc.Bytes()
	dec := NewDecoder(wire)
	if _, err := dec.Next(); err != nil {
		t.Fatalf("first Next err: %v", err)
	}
	if _, err := dec.Next(); !errors.Is(err, io.EOF) {
		t.Fatalf("second Next err = %v, want io.EOF", err)
	}
}

// TestOctetsAreCopiedFromBuffer guarantees the caller's slice is
// independent of the underlying decoder buffer — important for any
// caller that retains the slice across Next() calls.
func TestOctetsAreCopiedFromBuffer(t *testing.T) {
	enc := NewEncoder()
	enc.PutOctets(AnonymousTag(), []byte{1, 2, 3, 4})
	wire, _ := enc.Bytes()
	dec := NewDecoder(wire)
	el, _ := dec.Next()
	// Mutate the underlying buffer and confirm the decoded slice is
	// unaffected.
	for i := range wire {
		wire[i] = 0
	}
	if !bytes.Equal(el.Octets, []byte{1, 2, 3, 4}) {
		t.Fatalf("decoded octets aliased the buffer: % X", el.Octets)
	}
}

// TestSignedNegativeRoundTripIsExact catches sign-extension bugs.
func TestSignedNegativeRoundTripIsExact(t *testing.T) {
	for _, v := range []int64{-1, -42, -1000, math.MinInt32, math.MinInt64} {
		enc := NewEncoder()
		enc.PutInt(AnonymousTag(), v)
		wire, _ := enc.Bytes()
		dec := NewDecoder(wire)
		el, err := dec.Next()
		if err != nil {
			t.Fatalf("v=%d: err %v", v, err)
		}
		if el.Int != v {
			t.Errorf("v=%d: decoded %d", v, el.Int)
		}
	}
}

// --- PutInt*Bounded sentinel rejection ---

// TestPutInt8Bounded_SentinelRejected verifies that PutInt8Bounded
// rejects −128, the Matter §6.6.4.5 Table 26 nullable-int8 sentinel.
// Callers must encode null via PutNull; sending −128 as a value would
// cause commissioners to read the attribute as null.
// Mirrors matter.js packages/types/src/tlv/TlvNumber.ts TlvInt8 sentinel.
func TestPutInt8Bounded_SentinelRejected(t *testing.T) {
	t.Parallel()
	enc := NewEncoder()
	err := enc.PutInt8Bounded(AnonymousTag(), -128)
	if !errors.Is(err, ErrInt8NullableSentinel) {
		t.Fatalf("err = %v, want ErrInt8NullableSentinel", err)
	}
}

// TestPutInt8Bounded_ValidValue confirms non-sentinel values are
// accepted and encode correctly.
func TestPutInt8Bounded_ValidValue(t *testing.T) {
	t.Parallel()
	for _, v := range []int8{-127, -1, 0, 42, 127} {
		enc := NewEncoder()
		if err := enc.PutInt8Bounded(AnonymousTag(), v); err != nil {
			t.Fatalf("v=%d: unexpected error: %v", v, err)
		}
		wire, _ := enc.Bytes()
		dec := NewDecoder(wire)
		el, err := dec.Next()
		if err != nil {
			t.Fatalf("v=%d: decode err: %v", v, err)
		}
		if el.Int != int64(v) {
			t.Errorf("v=%d: decoded %d", v, el.Int)
		}
	}
}

// TestPutInt16Bounded_SentinelRejected verifies that PutInt16Bounded
// rejects −32768, the Matter §6.6.4.5 Table 26 nullable-int16 sentinel.
// Mirrors matter.js packages/types/src/tlv/TlvNumber.ts TlvInt16 sentinel.
func TestPutInt16Bounded_SentinelRejected(t *testing.T) {
	t.Parallel()
	enc := NewEncoder()
	err := enc.PutInt16Bounded(AnonymousTag(), -32768)
	if !errors.Is(err, ErrInt16NullableSentinel) {
		t.Fatalf("err = %v, want ErrInt16NullableSentinel", err)
	}
}

// TestPutInt16Bounded_ValidValue confirms non-sentinel values encode correctly.
func TestPutInt16Bounded_ValidValue(t *testing.T) {
	t.Parallel()
	for _, v := range []int16{-32767, -1, 0, 1000, 32767} {
		enc := NewEncoder()
		if err := enc.PutInt16Bounded(AnonymousTag(), v); err != nil {
			t.Fatalf("v=%d: unexpected error: %v", v, err)
		}
		wire, _ := enc.Bytes()
		dec := NewDecoder(wire)
		el, err := dec.Next()
		if err != nil {
			t.Fatalf("v=%d: decode err: %v", v, err)
		}
		if el.Int != int64(v) {
			t.Errorf("v=%d: decoded %d", v, el.Int)
		}
	}
}

// TestPutInt32Bounded_SentinelRejected verifies that PutInt32Bounded
// rejects −2147483648, the Matter §6.6.4.5 Table 26 nullable-int32 sentinel.
// Mirrors matter.js packages/types/src/tlv/TlvNumber.ts TlvInt32 sentinel.
func TestPutInt32Bounded_SentinelRejected(t *testing.T) {
	t.Parallel()
	enc := NewEncoder()
	err := enc.PutInt32Bounded(AnonymousTag(), -2147483648)
	if !errors.Is(err, ErrInt32NullableSentinel) {
		t.Fatalf("err = %v, want ErrInt32NullableSentinel", err)
	}
}

// TestPutInt32Bounded_ValidValue confirms non-sentinel values encode correctly.
func TestPutInt32Bounded_ValidValue(t *testing.T) {
	t.Parallel()
	for _, v := range []int32{-2147483647, -1, 0, 1000000, 2147483647} {
		enc := NewEncoder()
		if err := enc.PutInt32Bounded(AnonymousTag(), v); err != nil {
			t.Fatalf("v=%d: unexpected error: %v", v, err)
		}
		wire, _ := enc.Bytes()
		dec := NewDecoder(wire)
		el, err := dec.Next()
		if err != nil {
			t.Fatalf("v=%d: decode err: %v", v, err)
		}
		if el.Int != int64(v) {
			t.Errorf("v=%d: decoded %d", v, el.Int)
		}
	}
}

// --- PutUTF8Bounded ---

// TestPutUTF8Bounded_WithinLimit verifies that a string within the
// byte limit is encoded verbatim.
func TestPutUTF8Bounded_WithinLimit(t *testing.T) {
	t.Parallel()
	s := "openccu-loom"
	enc := NewEncoder()
	enc.PutUTF8Bounded(AnonymousTag(), s, 32)
	wire, err := enc.Bytes()
	if err != nil {
		t.Fatalf("Bytes err: %v", err)
	}
	dec := NewDecoder(wire)
	el, err := dec.Next()
	if err != nil {
		t.Fatalf("decode err: %v", err)
	}
	if el.String != s {
		t.Fatalf("decoded %q, want %q", el.String, s)
	}
}

// TestPutUTF8Bounded_ExactLimit verifies that a string at exactly
// maxBytes is accepted without truncation.
func TestPutUTF8Bounded_ExactLimit(t *testing.T) {
	t.Parallel()
	s := strings.Repeat("a", 32)
	enc := NewEncoder()
	enc.PutUTF8Bounded(AnonymousTag(), s, 32)
	wire, _ := enc.Bytes()
	dec := NewDecoder(wire)
	el, _ := dec.Next()
	if len(el.String) != 32 {
		t.Fatalf("len=%d, want 32", len(el.String))
	}
}

// TestPutUTF8Bounded_OverflowAsciiTrims verifies that a pure-ASCII
// string exceeding maxBytes is trimmed to exactly maxBytes.
func TestPutUTF8Bounded_OverflowAsciiTrims(t *testing.T) {
	t.Parallel()
	s := strings.Repeat("x", 40) // 40 bytes, limit=32
	enc := NewEncoder()
	enc.PutUTF8Bounded(AnonymousTag(), s, 32)
	wire, _ := enc.Bytes()
	dec := NewDecoder(wire)
	el, _ := dec.Next()
	if len(el.String) != 32 {
		t.Fatalf("trimmed len=%d, want 32", len(el.String))
	}
	if el.String != strings.Repeat("x", 32) {
		t.Fatalf("trimmed content mismatch")
	}
}

// TestPutUTF8Bounded_OverflowMultibyteTrimsAtRuneBoundary verifies
// that trimming a multi-byte UTF-8 string does not split a rune. A
// 3-byte Japanese character (ほ = U+307B, 3 bytes in UTF-8) repeated
// so that the 3n+1 byte falls inside a character — the trim must land
// on a rune boundary, not mid-character.
func TestPutUTF8Bounded_OverflowMultibyteTrimsAtRuneBoundary(t *testing.T) {
	t.Parallel()
	// "ほ" = 0xE3 0x81 0xBB (3 UTF-8 bytes). Repeat 11 times = 33 bytes;
	// limit = 32. A naive byte-slice at 32 would split the 11th rune
	// (byte 30-32 for the 11th char, which starts at byte 30). The trim
	// must stop at byte 30 (10 complete runes = 30 bytes).
	s := strings.Repeat("ほ", 11) // 33 bytes
	enc := NewEncoder()
	enc.PutUTF8Bounded(AnonymousTag(), s, 32)
	wire, _ := enc.Bytes()
	dec := NewDecoder(wire)
	el, err := dec.Next()
	if err != nil {
		t.Fatalf("decode err: %v", err)
	}
	// Must be 10 complete runes = 30 bytes.
	if len(el.String) != 30 {
		t.Fatalf("trimmed len=%d, want 30 (10 runes × 3 bytes)", len(el.String))
	}
	if el.String != strings.Repeat("ほ", 10) {
		t.Fatalf("trimmed rune boundary mismatch: %q", el.String)
	}
}

// --- PutUint*Bounded sentinel rejection ---
// These tests mirror the signed-int sentinel tests above for the
// unsigned side. See Matter §6.6.4.5 Table 26; matter.js
// packages/types/src/tlv/TlvNullable.ts:28-31 (NullableSchema shrinks
// max from baseTypeMax to baseTypeMax-1 for nullable unsigned types).

// TestPutUint8Bounded_SentinelRejected verifies that 0xFF is rejected,
// as it is the wire sentinel for nullable uint8 fields.
func TestPutUint8Bounded_SentinelRejected(t *testing.T) {
	t.Parallel()
	enc := NewEncoder()
	err := enc.PutUint8Bounded(AnonymousTag(), 0xFF)
	if !errors.Is(err, ErrUint8NullableSentinel) {
		t.Fatalf("err = %v, want ErrUint8NullableSentinel", err)
	}
}

// TestPutUint8Bounded_ValidValues confirms non-sentinel values round-trip
// correctly and are encoded as TypeUnsignedInt1.
func TestPutUint8Bounded_ValidValues(t *testing.T) {
	t.Parallel()
	for _, v := range []uint8{0, 1, 127, 0xFE} {
		enc := NewEncoder()
		if err := enc.PutUint8Bounded(AnonymousTag(), v); err != nil {
			t.Fatalf("v=%d: unexpected error: %v", v, err)
		}
		wire, _ := enc.Bytes()
		if wire[0] != byte(TypeUnsignedInt1) {
			t.Errorf("v=%d: control byte 0x%02X, want TypeUnsignedInt1 0x%02X", v, wire[0], TypeUnsignedInt1)
		}
		dec := NewDecoder(wire)
		el, err := dec.Next()
		if err != nil {
			t.Fatalf("v=%d: decode err: %v", v, err)
		}
		if el.Uint != uint64(v) {
			t.Errorf("v=%d: decoded %d", v, el.Uint)
		}
	}
}

// TestPutUint16Bounded_SentinelRejected verifies that 0xFFFF is rejected,
// as it is the wire sentinel for nullable uint16 fields.
func TestPutUint16Bounded_SentinelRejected(t *testing.T) {
	t.Parallel()
	enc := NewEncoder()
	err := enc.PutUint16Bounded(AnonymousTag(), 0xFFFF)
	if !errors.Is(err, ErrUint16NullableSentinel) {
		t.Fatalf("err = %v, want ErrUint16NullableSentinel", err)
	}
}

// TestPutUint16Bounded_ValidValues confirms non-sentinel values round-trip
// correctly and are encoded as TypeUnsignedInt2.
func TestPutUint16Bounded_ValidValues(t *testing.T) {
	t.Parallel()
	for _, v := range []uint16{0, 1, 1000, 0xFFFE} {
		enc := NewEncoder()
		if err := enc.PutUint16Bounded(AnonymousTag(), v); err != nil {
			t.Fatalf("v=%d: unexpected error: %v", v, err)
		}
		wire, _ := enc.Bytes()
		if wire[0] != byte(TypeUnsignedInt2) {
			t.Errorf("v=%d: control byte 0x%02X, want TypeUnsignedInt2 0x%02X", v, wire[0], TypeUnsignedInt2)
		}
		dec := NewDecoder(wire)
		el, err := dec.Next()
		if err != nil {
			t.Fatalf("v=%d: decode err: %v", v, err)
		}
		if el.Uint != uint64(v) {
			t.Errorf("v=%d: decoded %d", v, el.Uint)
		}
	}
}

// TestPutUint32Bounded_SentinelRejected verifies that 0xFFFFFFFF is
// rejected, as it is the wire sentinel for nullable uint32 fields.
func TestPutUint32Bounded_SentinelRejected(t *testing.T) {
	t.Parallel()
	enc := NewEncoder()
	err := enc.PutUint32Bounded(AnonymousTag(), 0xFFFFFFFF)
	if !errors.Is(err, ErrUint32NullableSentinel) {
		t.Fatalf("err = %v, want ErrUint32NullableSentinel", err)
	}
}

// TestPutUint32Bounded_ValidValues confirms non-sentinel values round-trip
// correctly and are encoded as TypeUnsignedInt4.
func TestPutUint32Bounded_ValidValues(t *testing.T) {
	t.Parallel()
	for _, v := range []uint32{0, 1, 1000000, 0xFFFFFFFE} {
		enc := NewEncoder()
		if err := enc.PutUint32Bounded(AnonymousTag(), v); err != nil {
			t.Fatalf("v=%d: unexpected error: %v", v, err)
		}
		wire, _ := enc.Bytes()
		if wire[0] != byte(TypeUnsignedInt4) {
			t.Errorf("v=%d: control byte 0x%02X, want TypeUnsignedInt4 0x%02X", v, wire[0], TypeUnsignedInt4)
		}
		dec := NewDecoder(wire)
		el, err := dec.Next()
		if err != nil {
			t.Fatalf("v=%d: decode err: %v", v, err)
		}
		if el.Uint != uint64(v) {
			t.Errorf("v=%d: decoded %d", v, el.Uint)
		}
	}
}

// TestPutUint64Bounded_SentinelRejected verifies that 0xFFFFFFFFFFFFFFFF
// is rejected, as it is the wire sentinel for nullable uint64 fields.
func TestPutUint64Bounded_SentinelRejected(t *testing.T) {
	t.Parallel()
	enc := NewEncoder()
	err := enc.PutUint64Bounded(AnonymousTag(), 0xFFFFFFFFFFFFFFFF)
	if !errors.Is(err, ErrUint64NullableSentinel) {
		t.Fatalf("err = %v, want ErrUint64NullableSentinel", err)
	}
}

// TestPutUint64Bounded_ValidValues confirms non-sentinel values round-trip
// correctly and are encoded as TypeUnsignedInt8.
func TestPutUint64Bounded_ValidValues(t *testing.T) {
	t.Parallel()
	for _, v := range []uint64{0, 1, 1_000_000_000_000, 0xFFFFFFFFFFFFFFFE} {
		enc := NewEncoder()
		if err := enc.PutUint64Bounded(AnonymousTag(), v); err != nil {
			t.Fatalf("v=%d: unexpected error: %v", v, err)
		}
		wire, _ := enc.Bytes()
		if wire[0] != byte(TypeUnsignedInt8) {
			t.Errorf("v=%d: control byte 0x%02X, want TypeUnsignedInt8 0x%02X", v, wire[0], TypeUnsignedInt8)
		}
		dec := NewDecoder(wire)
		el, err := dec.Next()
		if err != nil {
			t.Fatalf("v=%d: decode err: %v", v, err)
		}
		if el.Uint != v {
			t.Errorf("v=%d: decoded %d", v, el.Uint)
		}
	}
}

// --- Decoder diagnostic tests ---

// TestDecodeAppleSigma1Bytes parses the first 32 bytes of an iPhone
// Sigma1 capture so we can see the decoder's view of each element.
// Diagnostic only — the assertions here are informational.
func TestDecodeAppleSigma1Bytes(t *testing.T) {
	b, _ := hex.DecodeString("15300120051c20660838df2ffc4996e8c28918a4f93a82e2a9e66fac50edf22a")
	dec := NewDecoder(b)
	for i := range 6 {
		el, err := dec.Next()
		if err != nil {
			t.Logf("el%d err: %v", i, err)
			return
		}
		t.Logf("el%d: pos=%d type=0x%02X tag=%+v container=%v end=%v octets_len=%d uint=%d",
			i, dec.Pos(), el.Type, el.Tag, el.IsContainer, el.IsEndContainer, len(el.Octets), el.Uint)
	}
}
