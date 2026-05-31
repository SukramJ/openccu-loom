// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// Package tlv — codec parity tests ported from matter.js HEAD ebe091744.
//
// These tests lock the TLV wire-byte output for every primitive type and
// tag combination against the matter.js reference encoder. Each test
// cites the source file + case name so drift can be traced back to the
// TypeScript reference. The test cases are drawn exhaustively from:
//
//   - packages/types/test/tlv/TlvBooleanTest.ts
//   - packages/types/test/tlv/TlvNumberTest.ts
//   - packages/types/test/tlv/TlvStringTest.ts
//   - packages/types/test/tlv/TlvNullableTest.ts
//   - packages/types/test/tlv/TlvAnyTest.ts
//
// matter.js HEAD: ebe091744

package tlv

import (
	"bytes"
	"encoding/hex"
	"errors"
	"io"
	"math"
	"testing"
)

// hexBytes is a test helper that decodes a hex string or calls t.Fatal.
func hexBytes(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(s)
	if err != nil {
		t.Fatalf("hexBytes: %v", err)
	}
	return b
}

// encodeOnly builds one element and returns the raw wire bytes.
func encodeOnly(t *testing.T, fn func(*Encoder)) []byte {
	t.Helper()
	enc := NewEncoder()
	fn(enc)
	wire, err := enc.Bytes()
	if err != nil {
		t.Fatalf("Bytes: %v", err)
	}
	return wire
}

// assertWire fails when got != want (both printed as hex).
func assertWire(t *testing.T, label string, got, want []byte) {
	t.Helper()
	if !bytes.Equal(got, want) {
		t.Errorf("%s: wire mismatch\n  got  %s\n  want %s",
			label, hex.EncodeToString(got), hex.EncodeToString(want))
	}
}

// --- TlvBoolean (TlvBooleanTest.ts lines 15-16) ---

// TestCodecParity_BoolTrue locks the 0x09 wire byte for true.
// Pins TlvBoolean wire encoding 0x08/0x09 against the matter.js TlvBoolean reference.
// Mirrors matter.js packages/types/test/tlv/TlvBooleanTest.ts:15 (case "true")
func TestCodecParity_BoolTrue(t *testing.T) {
	t.Parallel()
	got := encodeOnly(t, func(e *Encoder) { e.PutBool(AnonymousTag(), true) })
	assertWire(t, "bool-true", got, hexBytes(t, "09"))
}

// TestCodecParity_BoolFalse locks the 0x08 wire byte for false.
// Pins TlvBoolean wire encoding 0x08/0x09 against the matter.js TlvBoolean reference.
// Mirrors matter.js packages/types/test/tlv/TlvBooleanTest.ts:16 (case "false")
func TestCodecParity_BoolFalse(t *testing.T) {
	t.Parallel()
	got := encodeOnly(t, func(e *Encoder) { e.PutBool(AnonymousTag(), false) })
	assertWire(t, "bool-false", got, hexBytes(t, "08"))
}

// TestCodecParity_BoolDecodeTrue locks decoding of 0x09.
// Mirrors matter.js packages/types/test/tlv/TlvBooleanTest.ts:32 (decode loop for "true")
func TestCodecParity_BoolDecodeTrue(t *testing.T) {
	t.Parallel()
	dec := NewDecoder(hexBytes(t, "09"))
	el, err := dec.Next()
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if el.Type != TypeBoolTrue || !el.Bool {
		t.Errorf("got type=%02X bool=%v, want TypeBoolTrue+true", el.Type, el.Bool)
	}
}

// TestCodecParity_BoolDecodeFalse locks decoding of 0x08.
// Mirrors matter.js packages/types/test/tlv/TlvBooleanTest.ts:32 (decode loop for "false")
func TestCodecParity_BoolDecodeFalse(t *testing.T) {
	t.Parallel()
	dec := NewDecoder(hexBytes(t, "08"))
	el, err := dec.Next()
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if el.Type != TypeBoolFalse || el.Bool {
		t.Errorf("got type=%02X bool=%v, want TypeBoolFalse+false", el.Type, el.Bool)
	}
}

// --- TlvNumber — signed (TlvNumberTest.ts codecVectorNumeric) ---

// TestCodecParity_SignedInt1Byte locks encoding of −1 in 1 byte (0x00 ff).
// Pins TlvNumber width semantics 0x00..0x07 control octets against the matter.js reference.
// Mirrors matter.js packages/types/test/tlv/TlvNumberTest.ts:33 (case "an 1 byte signed int")
func TestCodecParity_SignedInt1Byte(t *testing.T) {
	t.Parallel()
	got := encodeOnly(t, func(e *Encoder) { e.PutInt(AnonymousTag(), -1) })
	assertWire(t, "int1", got, hexBytes(t, "00ff"))
}

// TestCodecParity_SignedInt2Byte locks encoding of 0x0100 in 2 bytes (01 0001).
// Mirrors matter.js packages/types/test/tlv/TlvNumberTest.ts:34 (case "a 2 bytes signed int")
func TestCodecParity_SignedInt2Byte(t *testing.T) {
	t.Parallel()
	got := encodeOnly(t, func(e *Encoder) { e.PutInt(AnonymousTag(), 0x0100) })
	assertWire(t, "int2", got, hexBytes(t, "010001"))
}

// TestCodecParity_SignedInt4Byte locks encoding of 0x01000000 in 4 bytes (02 00000001).
// Mirrors matter.js packages/types/test/tlv/TlvNumberTest.ts:35 (case "a 4 bytes signed int")
func TestCodecParity_SignedInt4Byte(t *testing.T) {
	t.Parallel()
	got := encodeOnly(t, func(e *Encoder) { e.PutInt(AnonymousTag(), 0x01000000) })
	assertWire(t, "int4", got, hexBytes(t, "0200000001"))
}

// TestCodecParity_SignedInt8Byte locks encoding of 0x01000000000000 in 8 bytes.
// Mirrors matter.js packages/types/test/tlv/TlvNumberTest.ts:36 (case "a 8 bytes signed int")
func TestCodecParity_SignedInt8Byte(t *testing.T) {
	t.Parallel()
	got := encodeOnly(t, func(e *Encoder) { e.PutInt(AnonymousTag(), 0x01000000000000) })
	assertWire(t, "int8", got, hexBytes(t, "030000000000000100"))
}

// --- TlvNumber — unsigned (TlvNumberTest.ts codecVectorNumeric) ---

// TestCodecParity_UnsignedInt1Byte locks encoding of 1 in 1 byte (04 01).
// Mirrors matter.js packages/types/test/tlv/TlvNumberTest.ts:37 (case "an 1 byte unsigned int")
func TestCodecParity_UnsignedInt1Byte(t *testing.T) {
	t.Parallel()
	got := encodeOnly(t, func(e *Encoder) { e.PutUint(AnonymousTag(), 1) })
	assertWire(t, "uint1", got, hexBytes(t, "0401"))
}

// TestCodecParity_UnsignedInt2Byte locks encoding of 0x0100 in 2 bytes.
// Mirrors matter.js packages/types/test/tlv/TlvNumberTest.ts:38 (case "a 2 bytes unsigned int")
func TestCodecParity_UnsignedInt2Byte(t *testing.T) {
	t.Parallel()
	got := encodeOnly(t, func(e *Encoder) { e.PutUint(AnonymousTag(), 0x0100) })
	assertWire(t, "uint2", got, hexBytes(t, "050001"))
}

// TestCodecParity_UnsignedInt4Byte locks encoding of 0x01000000 in 4 bytes.
// Mirrors matter.js packages/types/test/tlv/TlvNumberTest.ts:39 (case "a 4 bytes unsigned int")
func TestCodecParity_UnsignedInt4Byte(t *testing.T) {
	t.Parallel()
	got := encodeOnly(t, func(e *Encoder) { e.PutUint(AnonymousTag(), 0x01000000) })
	assertWire(t, "uint4", got, hexBytes(t, "0600000001"))
}

// TestCodecParity_UnsignedInt8Byte locks encoding of 0x01000000000000 in 8 bytes.
// Mirrors matter.js packages/types/test/tlv/TlvNumberTest.ts:40 (case "a 8 bytes unsigned int")
func TestCodecParity_UnsignedInt8Byte(t *testing.T) {
	t.Parallel()
	got := encodeOnly(t, func(e *Encoder) { e.PutUint(AnonymousTag(), 0x01000000000000) })
	assertWire(t, "uint8", got, hexBytes(t, "070000000000000100"))
}

// --- TlvNumber — floats (TlvNumberTest.ts codecVectorNumber) ---

// TestCodecParity_Float32 locks the 4-byte float wire shape for 6546.25390625.
// Mirrors matter.js packages/types/test/tlv/TlvNumberTest.ts:44 (case "a float")
func TestCodecParity_Float32(t *testing.T) {
	t.Parallel()
	// matter.js "a float": value 6546.25390625, encoded "0a0892cc45"
	got := encodeOnly(t, func(e *Encoder) { e.PutFloat32(AnonymousTag(), 6546.25390625) })
	assertWire(t, "float32", got, hexBytes(t, "0a0892cc45"))
}

// TestCodecParity_Float64 locks the 8-byte double wire shape for 6546.254.
// Mirrors matter.js packages/types/test/tlv/TlvNumberTest.ts:45 (case "a double")
func TestCodecParity_Float64(t *testing.T) {
	t.Parallel()
	// matter.js "a double": value 6546.254, encoded "0b2fdd24064192b940"
	got := encodeOnly(t, func(e *Encoder) { e.PutFloat64(AnonymousTag(), 6546.254) })
	assertWire(t, "float64", got, hexBytes(t, "0b2fdd24064192b940"))
}

// --- TlvString (TlvStringTest.ts) ---

// TestCodecParity_UTF8String_ASCII locks encoding of "test" → 0c 04 74657374.
// Mirrors matter.js packages/types/test/tlv/TlvStringTest.ts:29 (encode "test")
func TestCodecParity_UTF8String_ASCII(t *testing.T) {
	t.Parallel()
	got := encodeOnly(t, func(e *Encoder) { e.PutUTF8(AnonymousTag(), "test") })
	assertWire(t, "utf8-test", got, hexBytes(t, "0c0474657374"))
}

// TestCodecParity_UTF8String_UTF8Encoded locks encoding of "testè" (multi-byte rune).
// Mirrors matter.js packages/types/test/tlv/TlvStringTest.ts:34 (encode "testè")
func TestCodecParity_UTF8String_UTF8Encoded(t *testing.T) {
	t.Parallel()
	got := encodeOnly(t, func(e *Encoder) { e.PutUTF8(AnonymousTag(), "testè") })
	assertWire(t, "utf8-è", got, hexBytes(t, "0c0674657374c3a8"))
}

// TestCodecParity_UTF8String_Decode_ASCII locks decoding of 0c0474657374 → "test".
// Mirrors matter.js packages/types/test/tlv/TlvStringTest.ts:42 (decode "test")
func TestCodecParity_UTF8String_Decode_ASCII(t *testing.T) {
	t.Parallel()
	dec := NewDecoder(hexBytes(t, "0c0474657374"))
	el, err := dec.Next()
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if el.String != "test" {
		t.Errorf("got %q, want %q", el.String, "test")
	}
}

// TestCodecParity_UTF8String_Decode_UTF8 locks decoding of the multi-byte rune "è".
// Mirrors matter.js packages/types/test/tlv/TlvStringTest.ts:48 (decode "testè")
func TestCodecParity_UTF8String_Decode_UTF8(t *testing.T) {
	t.Parallel()
	dec := NewDecoder(hexBytes(t, "0c0674657374c3a8"))
	el, err := dec.Next()
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if el.String != "testè" {
		t.Errorf("got %q, want %q", el.String, "testè")
	}
}

// TestCodecParity_OctetString locks encoding of 0x0001 → 10 02 0001.
// Mirrors matter.js packages/types/test/tlv/TlvStringTest.ts:86 (TlvByteString encode)
func TestCodecParity_OctetString(t *testing.T) {
	t.Parallel()
	got := encodeOnly(t, func(e *Encoder) { e.PutOctets(AnonymousTag(), []byte{0x00, 0x01}) })
	assertWire(t, "octets-0001", got, hexBytes(t, "10020001"))
}

// TestCodecParity_OctetString_Decode locks decoding of 10 02 0001 → {0x00, 0x01}.
// Mirrors matter.js packages/types/test/tlv/TlvStringTest.ts:94 (TlvByteString decode)
func TestCodecParity_OctetString_Decode(t *testing.T) {
	t.Parallel()
	dec := NewDecoder(hexBytes(t, "10020001"))
	el, err := dec.Next()
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !bytes.Equal(el.Octets, []byte{0x00, 0x01}) {
		t.Errorf("got % X, want 00 01", el.Octets)
	}
}

// --- TlvNullable (TlvNullableTest.ts) ---

// TestCodecParity_NullableString_NonNull locks encoding of non-null string "a" → 0c 01 61.
// Pins TlvNullable's 0x14 null sentinel against the matter.js TlvNullable reference.
// Mirrors matter.js packages/types/test/tlv/TlvNullableTest.ts:17 (codecVector "a non-null value")
func TestCodecParity_NullableString_NonNull(t *testing.T) {
	t.Parallel()
	got := encodeOnly(t, func(e *Encoder) { e.PutUTF8(AnonymousTag(), "a") })
	assertWire(t, "nullable-string-nonnull", got, hexBytes(t, "0c0161"))
}

// TestCodecParity_NullableString_Null locks encoding of null → 0x14.
// Pins TlvNullable's 0x14 null sentinel against the matter.js TlvNullable reference.
// Mirrors matter.js packages/types/test/tlv/TlvNullableTest.ts:18 (codecVector "a null value")
func TestCodecParity_NullableString_Null(t *testing.T) {
	t.Parallel()
	got := encodeOnly(t, func(e *Encoder) { e.PutNull(AnonymousTag()) })
	assertWire(t, "nullable-null", got, hexBytes(t, "14"))
}

// TestCodecParity_NullableString_Decode_NonNull locks decoding of 0c0161 → string "a".
// Mirrors matter.js packages/types/test/tlv/TlvNullableTest.ts:48 (decode loop for "a non-null value")
func TestCodecParity_NullableString_Decode_NonNull(t *testing.T) {
	t.Parallel()
	dec := NewDecoder(hexBytes(t, "0c0161"))
	el, err := dec.Next()
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if el.String != "a" {
		t.Errorf("got %q, want %q", el.String, "a")
	}
}

// TestCodecParity_NullableString_Decode_Null locks decoding of 0x14 → null.
// Mirrors matter.js packages/types/test/tlv/TlvNullableTest.ts:48 (decode loop for "a null value")
func TestCodecParity_NullableString_Decode_Null(t *testing.T) {
	t.Parallel()
	dec := NewDecoder(hexBytes(t, "14"))
	el, err := dec.Next()
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if el.Type != TypeNull || !el.IsNull {
		t.Errorf("got type=%02X isNull=%v, want TypeNull+true", el.Type, el.IsNull)
	}
}

// --- TlvAny (TlvAnyTest.ts testVector) ---

// TestCodecParity_AnyNull locks encoding of null → 0x14.
// Mirrors matter.js packages/types/test/tlv/TlvAnyTest.ts:16 (testVector case "null")
func TestCodecParity_AnyNull(t *testing.T) {
	t.Parallel()
	got := encodeOnly(t, func(e *Encoder) { e.PutNull(AnonymousTag()) })
	assertWire(t, "any-null", got, hexBytes(t, "14"))
}

// TestCodecParity_AnyEmptyArray locks encoding of empty array → 16 18.
// Mirrors matter.js packages/types/test/tlv/TlvAnyTest.ts:17 (testVector case "array")
func TestCodecParity_AnyEmptyArray(t *testing.T) {
	t.Parallel()
	got := encodeOnly(t, func(e *Encoder) {
		e.StartArray(AnonymousTag())
		_ = e.EndContainer()
	})
	assertWire(t, "any-empty-array", got, hexBytes(t, "1618"))
}

// TestCodecParity_AnyEmptyArray_Decode locks decoding of 1618 → array open + end.
// Mirrors matter.js packages/types/test/tlv/TlvAnyTest.ts:55 (decode loop for "array")
func TestCodecParity_AnyEmptyArray_Decode(t *testing.T) {
	t.Parallel()
	dec := NewDecoder(hexBytes(t, "1618"))
	open, err := dec.Next()
	if err != nil || !open.IsContainer || open.Type != TypeArray {
		t.Fatalf("open: type=%02X isContainer=%v err=%v", open.Type, open.IsContainer, err)
	}
	end, err := dec.Next()
	if err != nil || !end.IsEndContainer {
		t.Fatalf("end: type=%02X isEnd=%v err=%v", end.Type, end.IsEndContainer, err)
	}
	_, err = dec.Next()
	if !errors.Is(err, io.EOF) {
		t.Errorf("expected EOF after empty array, got %v", err)
	}
}

// --- UInt32 decode from 8-byte representation (TlvNumberTest.ts:103) ---

// TestCodecParity_UInt32From8Bytes verifies that a small value encoded as
// uint64 (07 01 00000000000000) decodes to the correct integer value 1.
// Mirrors matter.js packages/types/test/tlv/TlvNumberTest.ts:103
// (decode "decodes a 8 bytes small value as a number")
func TestCodecParity_UInt32From8Bytes(t *testing.T) {
	t.Parallel()
	dec := NewDecoder(hexBytes(t, "070100000000000000"))
	el, err := dec.Next()
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if el.Uint != 1 {
		t.Errorf("got %d, want 1", el.Uint)
	}
}

// --- Control octet shape verification ---

// TestCodecParity_ControlOctet_Bool verifies that the control byte for
// anonymous + bool-true is exactly 0x09 (kind=0 << 5 | type=0x09).
// Derives from matter.js TlvBooleanTest.ts and TlvCodec.ts control-byte layout.
func TestCodecParity_ControlOctet_Bool(t *testing.T) {
	t.Parallel()
	wire := encodeOnly(t, func(e *Encoder) { e.PutBool(AnonymousTag(), true) })
	if len(wire) != 1 || wire[0] != 0x09 {
		t.Errorf("control byte = % X, want [09]", wire)
	}
}

// TestCodecParity_ControlOctet_ContextTagUint verifies that a context tag
// n=7 produces control = (1<<5)|TypeUnsignedInt1 = 0x24 followed by tag byte 0x07.
// Mirrors matter.js packages/types/test/tlv/TlvNumberTest.ts control octet layout.
func TestCodecParity_ControlOctet_ContextTagUint(t *testing.T) {
	t.Parallel()
	wire := encodeOnly(t, func(e *Encoder) { e.PutUint(ContextTag(7), 1) })
	// control=(1<<5)|0x04=0x24, tag=0x07, value=0x01
	if len(wire) < 3 || wire[0] != 0x24 || wire[1] != 0x07 {
		t.Errorf("wire=% X, want 24 07 01", wire)
	}
}

// TestCodecParity_ControlOctet_Null verifies the anonymous null control byte 0x14.
// Mirrors matter.js TlvAnyTest.ts null testVector.
func TestCodecParity_ControlOctet_Null(t *testing.T) {
	t.Parallel()
	wire := encodeOnly(t, func(e *Encoder) { e.PutNull(AnonymousTag()) })
	if len(wire) != 1 || wire[0] != 0x14 {
		t.Errorf("null control byte=% X, want [14]", wire)
	}
}

// TestCodecParity_ControlOctet_StructureOpen verifies the structure-open
// control byte 0x15 (anonymous + TypeStructure).
// Mirrors matter.js TlvObjectTest.ts encoded structure preamble.
func TestCodecParity_ControlOctet_StructureOpen(t *testing.T) {
	t.Parallel()
	wire := encodeOnly(t, func(e *Encoder) {
		e.StartStruct(AnonymousTag())
		_ = e.EndContainer()
	})
	// 0x15 = struct open; 0x18 = end-of-container
	if len(wire) != 2 || wire[0] != 0x15 || wire[1] != 0x18 {
		t.Errorf("struct wire=% X, want 15 18", wire)
	}
}

// TestCodecParity_ControlOctet_ArrayOpen verifies the array-open control
// byte 0x16 (anonymous + TypeArray).
// Mirrors matter.js TlvArrayTest.ts encoded array preamble.
func TestCodecParity_ControlOctet_ArrayOpen(t *testing.T) {
	t.Parallel()
	wire := encodeOnly(t, func(e *Encoder) {
		e.StartArray(AnonymousTag())
		_ = e.EndContainer()
	})
	// 0x16 = array open; 0x18 = end-of-container
	if len(wire) != 2 || wire[0] != 0x16 || wire[1] != 0x18 {
		t.Errorf("array wire=% X, want 16 18", wire)
	}
}

// TestCodecParity_ControlOctet_ListOpen verifies the list-open control
// byte 0x17 (anonymous + TypeList).
// Mirrors matter.js TlvObjectTest.ts TlvTaggedList preamble.
func TestCodecParity_ControlOctet_ListOpen(t *testing.T) {
	t.Parallel()
	wire := encodeOnly(t, func(e *Encoder) {
		e.StartList(AnonymousTag())
		_ = e.EndContainer()
	})
	// 0x17 = list open; 0x18 = end-of-container
	if len(wire) != 2 || wire[0] != 0x17 || wire[1] != 0x18 {
		t.Errorf("list wire=% X, want 17 18", wire)
	}
}

// --- Round-trip correctness for PutUint width-selection edges ---

// TestCodecParity_UintWidthBoundaries verifies the four width-selection
// thresholds for unsigned integers match the matter.js TlvCodec rule:
// use the smallest type that fits the value.
// Mirrors matter.js packages/types/test/tlv/TlvNumberTest.ts codecVectorNumeric.
func TestCodecParity_UintWidthBoundaries(t *testing.T) {
	t.Parallel()
	cases := []struct {
		v       uint64
		wantTyp ElementType
		label   string
	}{
		{0xFF, TypeUnsignedInt1, "max-uint8"},
		{0x100, TypeUnsignedInt2, "min-uint16"},
		{0xFFFF, TypeUnsignedInt2, "max-uint16"},
		{0x10000, TypeUnsignedInt4, "min-uint32"},
		{0xFFFFFFFF, TypeUnsignedInt4, "max-uint32"},
		{0x100000000, TypeUnsignedInt8, "min-uint64"},
		{math.MaxUint64, TypeUnsignedInt8, "max-uint64"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.label, func(t *testing.T) {
			t.Parallel()
			enc := NewEncoder()
			enc.PutUint(AnonymousTag(), tc.v)
			wire, _ := enc.Bytes()
			dec := NewDecoder(wire)
			el, err := dec.Next()
			if err != nil {
				t.Fatalf("decode: %v", err)
			}
			if el.Type != tc.wantTyp {
				t.Errorf("v=%d: type=0x%02X, want 0x%02X", tc.v, el.Type, tc.wantTyp)
			}
			if el.Uint != tc.v {
				t.Errorf("v=%d: decoded %d", tc.v, el.Uint)
			}
		})
	}
}

// TestCodecParity_BoolByteLength locks the encoded byte length for TlvBoolean
// as reported by the matter.js TlvAny.getEncodedByteLength path: both true and
// false serialize to exactly 1 byte (the control octet alone, no value payload).
// Mirrors matter.js packages/types/test/tlv/TlvBooleanTest.ts:41 (calculate byte length)
func TestCodecParity_BoolByteLength(t *testing.T) {
	t.Parallel()
	for _, v := range []bool{true, false} {
		v := v
		t.Run(func() string {
			if v {
				return "true"
			}
			return "false"
		}(), func(t *testing.T) {
			t.Parallel()
			wire := encodeOnly(t, func(e *Encoder) { e.PutBool(AnonymousTag(), v) })
			if len(wire) != 1 {
				t.Errorf("bool %v: encoded to %d bytes, want 1", v, len(wire))
			}
		})
	}
}

// TestCodecParity_NullableNullIs0x14 locks the canonical null-sentinel wire byte 0x14
// for TlvNullable. This is the critical regression guard: any encoder that emits the
// wrong control octet for null breaks the apple commissioner's attribute cache.
// Mirrors matter.js packages/types/test/tlv/TlvNullableTest.ts:18 (codecVector "a null value")
// and packages/types/src/tlv/TlvNullable.ts NullableSchema.encodeTlv.
func TestCodecParity_NullableNullIs0x14(t *testing.T) {
	t.Parallel()
	wire := encodeOnly(t, func(e *Encoder) { e.PutNull(AnonymousTag()) })
	if len(wire) != 1 || wire[0] != 0x14 {
		t.Errorf("null sentinel = % X, want [14]", wire)
	}
}

// TestCodecParity_NullableInt16_NullWire locks that a Nullable<int16> field emits
// 0x14 for null and a 2-byte signed int for non-null, matching the matter.js
// TlvNullable(TlvInt16) schema. This is the TempMeasurement.MeasuredValue null-path
// regression guard: incorrect wire type causes Apple commissioner to drop the
// attribute from its HAP cache.
// Source-Origin: derived invariant — openccu-loom-specific contract test
// based on matter.js packages/types/test/tlv/TlvNullableTest.ts and
// packages/types/src/tlv/TlvNumber.ts TlvInt16 + TlvNullable composition.
func TestCodecParity_NullableInt16_NullWire(t *testing.T) {
	t.Parallel()

	// null case: must be 0x14 (TypeNull, anonymous)
	nullWire := encodeOnly(t, func(e *Encoder) { e.PutNull(AnonymousTag()) })
	if len(nullWire) != 1 || nullWire[0] != 0x14 {
		t.Errorf("null: wire=% X, want [14]", nullWire)
	}

	// non-null case: PutInt16 with value 0 → TypeSignedInt2 (0x01) + 2 bytes LE
	nonNullWire := encodeOnly(t, func(e *Encoder) { e.PutInt16(AnonymousTag(), 0) })
	if len(nonNullWire) != 3 || nonNullWire[0] != 0x01 {
		t.Errorf("int16(0): wire=% X, want [01 00 00]", nonNullWire)
	}

	// non-null case: sentinel guard — int16 -32768 (0x8000) must NOT be used as
	// a regular value on a Nullable<int16> field; PutInt16Bounded rejects it.
	enc := NewEncoder()
	if err := enc.PutInt16Bounded(AnonymousTag(), -32768); err == nil {
		t.Error("PutInt16Bounded(-32768) should return sentinel error, got nil")
	}
}

// TestCodecParity_IntWidthBoundaries verifies the four width-selection
// thresholds for signed integers match matter.js's smallest-fit rule.
// Mirrors matter.js packages/types/test/tlv/TlvNumberTest.ts codecVectorNumeric.
func TestCodecParity_IntWidthBoundaries(t *testing.T) {
	t.Parallel()
	cases := []struct {
		v       int64
		wantTyp ElementType
		label   string
	}{
		{-128, TypeSignedInt1, "min-int8"},
		{127, TypeSignedInt1, "max-int8"},
		{-129, TypeSignedInt2, "below-int8"},
		{128, TypeSignedInt2, "above-int8"},
		{-32768, TypeSignedInt2, "min-int16"},
		{32767, TypeSignedInt2, "max-int16"},
		{-32769, TypeSignedInt4, "below-int16"},
		{32768, TypeSignedInt4, "above-int16"},
		{math.MinInt32, TypeSignedInt4, "min-int32"},
		{math.MaxInt32, TypeSignedInt4, "max-int32"},
		{int64(math.MinInt32) - 1, TypeSignedInt8, "below-int32"},
		{int64(math.MaxInt32) + 1, TypeSignedInt8, "above-int32"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.label, func(t *testing.T) {
			t.Parallel()
			enc := NewEncoder()
			enc.PutInt(AnonymousTag(), tc.v)
			wire, _ := enc.Bytes()
			dec := NewDecoder(wire)
			el, err := dec.Next()
			if err != nil {
				t.Fatalf("decode: %v", err)
			}
			if el.Type != tc.wantTyp {
				t.Errorf("v=%d: type=0x%02X, want 0x%02X", tc.v, el.Type, tc.wantTyp)
			}
			if el.Int != tc.v {
				t.Errorf("v=%d: decoded %d", tc.v, el.Int)
			}
		})
	}
}
