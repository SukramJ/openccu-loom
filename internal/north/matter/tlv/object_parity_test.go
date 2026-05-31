// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// Package tlv — struct / container parity tests ported from matter.js HEAD ebe091744.
//
// These tests lock TLV wire bytes for composite structures (TlvObject,
// TlvTaggedList, TlvArray) against the matter.js reference codec. Cases
// are drawn exhaustively from:
//
//   - packages/types/test/tlv/TlvObjectTest.ts
//   - packages/types/test/tlv/TlvArrayTest.ts
//   - packages/types/test/tlv/TlvComplexTest.ts
//
// Where openccu-loom does not expose a schema-layer equivalent
// (TlvObject.validate, injectField, removeField), the wire-byte invariant
// is tested directly against the encoder/decoder; higher-level schema
// semantics are noted as FixMe gaps.
//
// matter.js HEAD: ebe091744

package tlv

import (
	"bytes"
	"encoding/hex"
	"errors"
	"io"
	"testing"
)

// hexB is a test helper that hex-decodes or calls t.Fatal (local alias to
// avoid collision with the hexBytes helper in codec_parity_test.go).
func hexB(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(s)
	if err != nil {
		t.Fatalf("hexB: %v", err)
	}
	return b
}

// assertHex fails when got != want (both shown as hex strings).
func assertHex(t *testing.T, label string, got, want []byte) {
	t.Helper()
	if !bytes.Equal(got, want) {
		t.Errorf("%s:\n  got  %s\n  want %s",
			label, hex.EncodeToString(got), hex.EncodeToString(want))
	}
}

// buildWire runs fn against a fresh Encoder and returns the wire bytes.
func buildWire(t *testing.T, fn func(*Encoder)) []byte {
	t.Helper()
	enc := NewEncoder()
	fn(enc)
	wire, err := enc.Bytes()
	if err != nil {
		t.Fatalf("buildWire Bytes: %v", err)
	}
	return wire
}

// --- TlvObject encode cases (TlvObjectTest.ts codecVector) ---

// TestObjectParity_Encode_AllFields locks the wire bytes for a TlvObject with
// all fields present: { mandatoryField=1 (uint8 ctx1), optionalField="test" (utf8 ctx2) }.
// matter.js encoding: struct-open (15) + ctx1-uint8-1 (240101) + ctx2-utf8-"test" (2c020474657374) + end (18)
// Mirrors matter.js packages/types/test/tlv/TlvObjectTest.ts:90 (codecVector "an object with all fields")
func TestObjectParity_Encode_AllFields(t *testing.T) {
	t.Parallel()
	// Encode: struct { field1=ctx1/uint8=1, field2=ctx2/utf8="test" }
	got := buildWire(t, func(e *Encoder) {
		e.StartStruct(AnonymousTag())
		e.PutUint(ContextTag(1), 1)
		e.PutUTF8(ContextTag(2), "test")
		_ = e.EndContainer()
	})
	assertHex(t, "object-all-fields", got, hexB(t, "152401012c02047465737418"))
}

// TestObjectParity_Encode_OptionalOmitted locks the wire bytes for a TlvObject
// without the optional field: { mandatoryField=1 }.
// Mirrors matter.js packages/types/test/tlv/TlvObjectTest.ts:94 (codecVector "an object without optional fields")
func TestObjectParity_Encode_OptionalOmitted(t *testing.T) {
	t.Parallel()
	got := buildWire(t, func(e *Encoder) {
		e.StartStruct(AnonymousTag())
		e.PutUint(ContextTag(1), 1)
		_ = e.EndContainer()
	})
	assertHex(t, "object-optional-omitted", got, hexB(t, "1524010118"))
}

// TestObjectParity_Decode_AllFields locks decoding of the "all fields" payload
// back to the same two fields.
// Mirrors matter.js packages/types/test/tlv/TlvObjectTest.ts:109 (decode loop, "an object with all fields")
func TestObjectParity_Decode_AllFields(t *testing.T) {
	t.Parallel()
	dec := NewDecoder(hexB(t, "152401012c02047465737418"))

	open, err := dec.Next()
	if err != nil || !open.IsContainer || open.Type != TypeStructure {
		t.Fatalf("struct open: %+v err=%v", open, err)
	}
	f1, err := dec.Next()
	if err != nil || f1.Tag.Kind != TagKindContext || f1.Tag.Number != 1 || f1.Uint != 1 {
		t.Fatalf("field1: %+v err=%v", f1, err)
	}
	f2, err := dec.Next()
	if err != nil || f2.Tag.Kind != TagKindContext || f2.Tag.Number != 2 || f2.String != "test" {
		t.Fatalf("field2: %+v err=%v", f2, err)
	}
	end, err := dec.Next()
	if err != nil || !end.IsEndContainer {
		t.Fatalf("end: %+v err=%v", end, err)
	}
}

// TestObjectParity_Decode_OptionalOmitted locks decoding of the "no optional field"
// payload — only one field and then end-of-container.
// Mirrors matter.js packages/types/test/tlv/TlvObjectTest.ts:109 (decode loop, "an object without optional fields")
func TestObjectParity_Decode_OptionalOmitted(t *testing.T) {
	t.Parallel()
	dec := NewDecoder(hexB(t, "1524010118"))

	open, err := dec.Next()
	if err != nil || !open.IsContainer || open.Type != TypeStructure {
		t.Fatalf("struct open: %+v err=%v", open, err)
	}
	f1, err := dec.Next()
	if err != nil || f1.Tag.Kind != TagKindContext || f1.Tag.Number != 1 || f1.Uint != 1 {
		t.Fatalf("field1: %+v err=%v", f1, err)
	}
	end, err := dec.Next()
	if err != nil || !end.IsEndContainer {
		t.Fatalf("end: %+v err=%v", end, err)
	}
}

// --- TlvTaggedList (TlvObjectTest.ts "TlvTaggedList" describe block) ---

// TestObjectParity_TaggedList_Optional locks the wire bytes for a TlvTaggedList
// with one optional field: { optionalField="test" }.
// Mirrors matter.js packages/types/test/tlv/TlvObjectTest.ts:329-334
// (case "encode and decode list with optional fields", encoded "172c01047465737418")
func TestObjectParity_TaggedList_Optional(t *testing.T) {
	t.Parallel()
	got := buildWire(t, func(e *Encoder) {
		e.StartList(AnonymousTag())
		e.PutUTF8(ContextTag(1), "test")
		_ = e.EndContainer()
	})
	assertHex(t, "list-optional", got, hexB(t, "172c01047465737418"))
}

// TestObjectParity_TaggedList_OptionalAndRequired_InOrder locks the wire bytes
// for a list with both fields in definition order.
// Mirrors matter.js packages/types/test/tlv/TlvObjectTest.ts:336-341
// (case "encode and decode list with optional and required fields in list order",
// encoded "172c0104746573742c02077465737472657118")
func TestObjectParity_TaggedList_OptionalAndRequired_InOrder(t *testing.T) {
	t.Parallel()
	got := buildWire(t, func(e *Encoder) {
		e.StartList(AnonymousTag())
		e.PutUTF8(ContextTag(1), "test")
		e.PutUTF8(ContextTag(2), "testreq")
		_ = e.EndContainer()
	})
	assertHex(t, "list-opt+req-inorder", got, hexB(t, "172c0104746573742c02077465737472657118"))
}

// TestObjectParity_TaggedList_OptionalAndRequired_SwitchedOrder verifies that
// encoding in switched field order produces the expected bytes.
// Mirrors matter.js packages/types/test/tlv/TlvObjectTest.ts:343-348
// (case "encode and decode list with optional and required fields in switched order",
// encoded "172c0207746573747265712c01047465737418")
func TestObjectParity_TaggedList_OptionalAndRequired_SwitchedOrder(t *testing.T) {
	t.Parallel()
	got := buildWire(t, func(e *Encoder) {
		e.StartList(AnonymousTag())
		e.PutUTF8(ContextTag(2), "testreq")
		e.PutUTF8(ContextTag(1), "test")
		_ = e.EndContainer()
	})
	assertHex(t, "list-opt+req-switched", got, hexB(t, "172c0207746573747265712c01047465737418"))
}

// TestObjectParity_TaggedList_RequiredOnly locks the wire bytes for a list with
// only the required field.
// Mirrors matter.js packages/types/test/tlv/TlvObjectTest.ts:350-354
// (case "encode and decode list with optional and required fields without optional field",
// encoded "172c02077465737472657118")
func TestObjectParity_TaggedList_RequiredOnly(t *testing.T) {
	t.Parallel()
	got := buildWire(t, func(e *Encoder) {
		e.StartList(AnonymousTag())
		e.PutUTF8(ContextTag(2), "testreq")
		_ = e.EndContainer()
	})
	assertHex(t, "list-required-only", got, hexB(t, "172c02077465737472657118"))
}

// TestObjectParity_TaggedList_RepeatedFields locks the wire bytes for a list
// with a repeated field (same tag id appears multiple times).
// Mirrors matter.js packages/types/test/tlv/TlvObjectTest.ts:356-362
// (case "encode and decode list with optional repeated fields",
// encoded "172c0104746573742c020574657374312c0205746573743218")
func TestObjectParity_TaggedList_RepeatedFields(t *testing.T) {
	t.Parallel()
	got := buildWire(t, func(e *Encoder) {
		e.StartList(AnonymousTag())
		e.PutUTF8(ContextTag(1), "test")
		e.PutUTF8(ContextTag(2), "test1")
		e.PutUTF8(ContextTag(2), "test2")
		_ = e.EndContainer()
	})
	assertHex(t, "list-repeated", got, hexB(t, "172c0104746573742c020574657374312c0205746573743218"))
}

// TestObjectParity_TaggedList_Decode_Optional locks decoding of a list with
// one optional context-tagged field back to the correct tag / value.
// Mirrors matter.js packages/types/test/tlv/TlvObjectTest.ts:329-334 (decode half)
func TestObjectParity_TaggedList_Decode_Optional(t *testing.T) {
	t.Parallel()
	dec := NewDecoder(hexB(t, "172c01047465737418"))

	open, err := dec.Next()
	if err != nil || open.Type != TypeList {
		t.Fatalf("list open: %+v err=%v", open, err)
	}
	f1, err := dec.Next()
	if err != nil || f1.Tag.Kind != TagKindContext || f1.Tag.Number != 1 || f1.String != "test" {
		t.Fatalf("field1: %+v err=%v", f1, err)
	}
	end, err := dec.Next()
	if err != nil || !end.IsEndContainer {
		t.Fatalf("end: %+v err=%v", end, err)
	}
}

// --- TlvArray (TlvArrayTest.ts) ---

// TestObjectParity_Array_Encode locks the wire bytes for an array of three
// single-character strings: ["a","b","c"].
// Mirrors matter.js packages/types/test/tlv/TlvArrayTest.ts:28-32
// (case "encodes an array", encoded "160c01610c01620c016318")
func TestObjectParity_Array_Encode(t *testing.T) {
	t.Parallel()
	got := buildWire(t, func(e *Encoder) {
		e.StartArray(AnonymousTag())
		e.PutUTF8(AnonymousTag(), "a")
		e.PutUTF8(AnonymousTag(), "b")
		e.PutUTF8(AnonymousTag(), "c")
		_ = e.EndContainer()
	})
	assertHex(t, "array-abc", got, hexB(t, "160c01610c01620c016318"))
}

// TestObjectParity_Array_Decode locks decoding of the three-element string
// array "160c01610c01620c016318" back to ["a","b","c"].
// Mirrors matter.js packages/types/test/tlv/TlvArrayTest.ts:57-61
// (case "decodes an array")
func TestObjectParity_Array_Decode(t *testing.T) {
	t.Parallel()
	dec := NewDecoder(hexB(t, "160c01610c01620c016318"))

	open, err := dec.Next()
	if err != nil || open.Type != TypeArray {
		t.Fatalf("array open: %+v err=%v", open, err)
	}

	want := []string{"a", "b", "c"}
	for i, w := range want {
		el, err := dec.Next()
		if err != nil {
			t.Fatalf("[%d] Next: %v", i, err)
		}
		if el.String != w {
			t.Errorf("[%d] got %q, want %q", i, el.String, w)
		}
	}

	end, err := dec.Next()
	if err != nil || !end.IsEndContainer {
		t.Fatalf("end: %+v err=%v", end, err)
	}
	_, err = dec.Next()
	if !errors.Is(err, io.EOF) {
		t.Errorf("expected EOF, got %v", err)
	}
}

// TestObjectParity_Array_RoundTrip verifies that encoding and then decoding
// ["a","b"] produces the original slice.
// Mirrors matter.js packages/types/test/tlv/TlvArrayTest.ts:106-116
// (case "decodes an array" — self-encoded roundtrip)
func TestObjectParity_Array_RoundTrip(t *testing.T) {
	t.Parallel()
	input := []string{"a", "b"}
	wire := buildWire(t, func(e *Encoder) {
		e.StartArray(AnonymousTag())
		for _, s := range input {
			e.PutUTF8(AnonymousTag(), s)
		}
		_ = e.EndContainer()
	})

	dec := NewDecoder(wire)
	open, err := dec.Next()
	if err != nil || open.Type != TypeArray {
		t.Fatalf("array open: %v", err)
	}

	var got []string
	for {
		el, err := dec.Next()
		if err != nil {
			t.Fatalf("Next: %v", err)
		}
		if el.IsEndContainer {
			break
		}
		got = append(got, el.String)
	}

	if len(got) != len(input) {
		t.Fatalf("len(got)=%d, want %d", len(got), len(input))
	}
	for i := range input {
		if got[i] != input[i] {
			t.Errorf("[%d] got %q, want %q", i, got[i], input[i])
		}
	}
}

// --- TlvComplexTest.ts wire fixtures ---

// TestObjectParity_Complex_AllFields locks the wire bytes for the complex schema
// with all fields set, including optionalWrapperBigInt (TlvFabricId / TlvUInt64)
// with value BigInt(1). matter.js TlvCodec.ts:encodeUnsignedInt applies
// magnitude-driven width selection: BigInt(1) encodes as TypeUnsignedInt1 (1 byte),
// not TypeUnsignedInt8. PutUint64 mirrors this via PutUint (smallest-fit).
//
// Mirrors matter.js packages/types/test/tlv/TlvComplexTest.ts:55
// (codecVector "an object with all fields")
func TestObjectParity_Complex_AllFields(t *testing.T) {
	t.Parallel()
	got := buildWire(t, func(e *Encoder) {
		e.StartStruct(AnonymousTag()) // 0x15

		// arrayField ctx1: array of 2 structs
		e.StartArray(ContextTag(1))                          // 0x36 0x01
		e.StartStruct(AnonymousTag())                        // 0x15
		e.PutUint(ContextTag(1), 1)                          // 0x24 0x01 0x01 — mandatoryNumber=1
		e.PutOctets(ContextTag(2), []byte{0x00, 0x00, 0x00}) // 0x30 0x02 0x03 000000
		_ = e.EndContainer()                                 // 0x18
		e.StartStruct(AnonymousTag())                        // 0x15
		e.PutUint(ContextTag(1), 2)                          // 0x24 0x01 0x02 — mandatoryNumber=2
		e.PutOctets(ContextTag(2), []byte{0x99, 0x99, 0x99}) // 0x30 0x02 0x03 999999
		_ = e.EndContainer()                                 // 0x18
		_ = e.EndContainer()                                 // array close 0x18

		// optionalString ctx2: "test"
		e.PutUTF8(ContextTag(2), "test") // 0x2c 0x02 0x04 74657374

		// nullableBoolean ctx3: true
		e.PutBool(ContextTag(3), true) // 0x29 0x03

		// optionalWrapperBigInt ctx4: FabricId(BigInt(1)) — TlvUInt64 minimal-fit
		e.PutUint64(ContextTag(4), 1) // 0x24 0x04 0x01 — magnitude=1 → uint1

		// optionalWrapperNumber ctx5: FabricIndex(2) — TlvUInt8
		e.PutUint(ContextTag(5), 2) // 0x24 0x05 0x02

		_ = e.EndContainer() // 0x18
	})
	assertHex(
		t, "complex-all-fields",
		got,
		hexB(t, "15360115240101300203000000181524010230020399999918182c020474657374290324040124050218"),
	)
}

// TestObjectParity_Complex_MinFields locks the wire bytes for the complex
// schema with minimum fields: array with one item + nullable bool = null.
// Mirrors matter.js packages/types/test/tlv/TlvComplexTest.ts:68 (codecVector "an object with minimum fields")
func TestObjectParity_Complex_MinFields(t *testing.T) {
	t.Parallel()
	got := buildWire(t, func(e *Encoder) {
		e.StartStruct(AnonymousTag()) // 0x15

		// field 1: array ctx1 with one item
		e.StartArray(ContextTag(1))   // 0x36 0x01
		e.StartStruct(AnonymousTag()) // 0x15
		e.PutUint(ContextTag(1), 1)   // 0x24 0x01 0x01
		_ = e.EndContainer()          // 0x18
		_ = e.EndContainer()          // array close 0x18

		// field 3: nullable bool ctx3 = null
		e.PutNull(ContextTag(3)) // 0x34 0x03

		_ = e.EndContainer() // 0x18
	})
	assertHex(t, "complex-min-fields", got, hexB(t, "153601152401011818340318"))
}

// TestObjectParity_Complex_MinFields_Decode locks decoding of the minimum
// fields payload to verify struct/array depth traversal.
// Mirrors matter.js packages/types/test/tlv/TlvComplexTest.ts:68 (decode side, "an object with minimum fields")
func TestObjectParity_Complex_MinFields_Decode(t *testing.T) {
	t.Parallel()
	dec := NewDecoder(hexB(t, "153601152401011818340318"))
	depth := 0
	var elements []Element
	for {
		el, err := dec.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("Next: %v", err)
		}
		switch {
		case el.IsContainer:
			depth++
		case el.IsEndContainer:
			depth--
		}
		elements = append(elements, el)
	}
	if depth != 0 {
		t.Errorf("unbalanced depth=%d", depth)
	}
	// Expected: outer struct + array-ctx1 + inner-struct + uint8 + inner-end +
	//           array-end + null-ctx3 + outer-end = 8 elements
	if len(elements) != 8 {
		t.Errorf("element count=%d, want 8; elements: %+v", len(elements), elements)
	}
}

// --- Nested containers depth tracking ---

// TestObjectParity_NestedArrayOfStructs locks the depth-balanced traversal of
// an array containing two structs, matching the matter.js TlvAnyTest generic
// decoding pattern.
// Mirrors matter.js packages/types/test/tlv/TlvAnyTest.ts:130 ("decodes and array of structures")
func TestObjectParity_NestedArrayOfStructs(t *testing.T) {
	t.Parallel()
	wire := buildWire(t, func(e *Encoder) {
		e.StartArray(AnonymousTag()) // array
		e.StartStruct(AnonymousTag())
		e.PutUTF8(ContextTag(1), "a")
		e.PutUTF8(ContextTag(2), "b")
		_ = e.EndContainer()
		e.StartStruct(AnonymousTag())
		e.PutUTF8(ContextTag(3), "c")
		e.PutUTF8(ContextTag(4), "d")
		_ = e.EndContainer()
		_ = e.EndContainer()
	})

	dec := NewDecoder(wire)
	depth := 0
	structCount := 0
	for {
		el, err := dec.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("Next: %v", err)
		}
		switch {
		case el.IsContainer:
			depth++
			if el.Type == TypeStructure {
				structCount++
			}
		case el.IsEndContainer:
			depth--
		}
	}
	if depth != 0 {
		t.Errorf("unbalanced depth=%d", depth)
	}
	if structCount != 2 {
		t.Errorf("struct count=%d, want 2", structCount)
	}
}

// --- TlvSchema composite encode/decode (TlvSchemaTest.ts) ---

// TestObjectParity_Schema_UInt16AndBoolean locks the wire bytes for a struct
// containing TlvUInt16 and TlvBoolean fields. The encoded payload is
// 15 2402 0124 0300 2804 18 — note TlvUInt16 with value 1 uses TypeUnsignedInt1
// (smallest fit) and TlvBoolean false uses TypeBoolFalse (0x28 with ctx4).
// Mirrors matter.js packages/types/test/tlv/TlvSchemaTest.ts:42 (testTlvSchemaEncode,
// "TlvObject: TlvUInt16 and TlvBoolean fields")
func TestObjectParity_Schema_UInt16AndBoolean(t *testing.T) {
	t.Parallel()
	// schema: { field2: TlvUInt16 (ctx2), field3: TlvUInt16 (ctx3), field4: TlvBoolean (ctx4) }
	// jsObj: { field2: 1, field3: 0, field4: false }
	// expected TLV: 15240201240300280418
	got := buildWire(t, func(e *Encoder) {
		e.StartStruct(AnonymousTag())   // 0x15
		e.PutUint(ContextTag(2), 1)     // 0x24 0x02 0x01  (uint8, smallest fit)
		e.PutUint(ContextTag(3), 0)     // 0x24 0x03 0x00
		e.PutBool(ContextTag(4), false) // 0x28 0x04
		_ = e.EndContainer()            // 0x18
	})
	assertHex(t, "schema-uint16-bool", got, hexB(t, "15240201240300280418"))
}

// TestObjectParity_Schema_StringFields locks the wire bytes for a struct
// containing two TlvString fields.
// Mirrors matter.js packages/types/test/tlv/TlvSchemaTest.ts:52 (testTlvSchemaEncode,
// "TlvObject: TlvString fields")
func TestObjectParity_Schema_StringFields(t *testing.T) {
	t.Parallel()
	// schema: { field1: TlvString (ctx1), field2: TlvString (ctx2) }
	// jsObj: { field1: "Hello!", field2: "Hey there, how are you?" }
	// expected: 152c010648656c6c6f212c02174865792074686572652c20686f772061726520796f753f18
	got := buildWire(t, func(e *Encoder) {
		e.StartStruct(AnonymousTag())
		e.PutUTF8(ContextTag(1), "Hello!")
		e.PutUTF8(ContextTag(2), "Hey there, how are you?")
		_ = e.EndContainer()
	})
	assertHex(t, "schema-string-fields", got,
		hexB(t, "152c010648656c6c6f212c02174865792074686572652c20686f772061726520796f753f18"))
}

// TestObjectParity_Schema_ArrayField locks the wire bytes for a struct
// with an array-of-strings field.
// Mirrors matter.js packages/types/test/tlv/TlvSchemaTest.ts:58 (testTlvSchemaEncode,
// "TlvObject: TlvArray field")
func TestObjectParity_Schema_ArrayField(t *testing.T) {
	t.Parallel()
	// schema: { field1: TlvArray(TlvString) (ctx1) }
	// jsObj: { field1: ["a","b","c","zzz"] }
	// expected: 1536010c01610c01620c01630c037a7a7a1818
	got := buildWire(t, func(e *Encoder) {
		e.StartStruct(AnonymousTag())
		e.StartArray(ContextTag(1))
		e.PutUTF8(AnonymousTag(), "a")
		e.PutUTF8(AnonymousTag(), "b")
		e.PutUTF8(AnonymousTag(), "c")
		e.PutUTF8(AnonymousTag(), "zzz")
		_ = e.EndContainer() // array
		_ = e.EndContainer() // struct
	})
	assertHex(t, "schema-array-field", got,
		hexB(t, "1536010c01610c01620c01630c037a7a7a1818"))
}

// TestObjectParity_Schema_NumberFields locks the wire bytes for a struct
// containing all numeric types (float, double, int8/16/32/64, uint8/16/32/64).
// matter.js uses fixed-width schema types (TlvInt8, TlvUInt16, etc.) — each
// emits the declared width. For value -1: int8/16/32/64 all emit TypeSignedIntN
// with all-ones payload. For value 1: uint8/16/32/64 all emit TypeUnsignedInt1
// (smallest fit in matter.js).
// Mirrors matter.js packages/types/test/tlv/TlvSchemaTest.ts:63 (testTlvSchemaEncode,
// "TlvObject: TlvNumber fields")
func TestObjectParity_Schema_NumberFields(t *testing.T) {
	t.Parallel()
	// expected: 152a010892cc452b022fdd24064192b9402003ff2004ff2005ff2006ff240701240801240901240a0118
	// Breakdown:
	//   2a01 0892cc45   = ctx1 float32 6546.25390625
	//   2b02 2fdd240641 92b940 = ctx2 float64 6546.254
	//   2003 ff         = ctx3 int8 -1
	//   2004 ff         = ctx4 int8 -1 (matter.js TlvInt16 emits int8 for -1, smallest fit)
	//   2005 ff         = ctx5 int8 -1
	//   2006 ff         = ctx6 int8 -1
	//   2407 01         = ctx7 uint8 1
	//   2408 01         = ctx8 uint8 1
	//   2409 01         = ctx9 uint8 1
	//   240a 01         = ctx10 uint8 1
	//   18              = end
	got := buildWire(t, func(e *Encoder) {
		e.StartStruct(AnonymousTag())
		e.PutFloat32(ContextTag(1), 6546.25390625)
		e.PutFloat64(ContextTag(2), 6546.254)
		e.PutInt(ContextTag(3), -1)
		e.PutInt(ContextTag(4), -1)
		e.PutInt(ContextTag(5), -1)
		e.PutInt(ContextTag(6), -1)
		e.PutUint(ContextTag(7), 1)
		e.PutUint(ContextTag(8), 1)
		e.PutUint(ContextTag(9), 1)
		e.PutUint(ContextTag(10), 1)
		_ = e.EndContainer()
	})
	assertHex(t, "schema-number-fields", got,
		hexB(t, "152a010892cc452b022fdd24064192b9402003ff2004ff2005ff2006ff240701240801240901240a0118"))
}

// --- Container-type validation tests ---

// TestEncoder_ContextTagInArray_Panics verifies that writing a context-tagged
// element inside an Array container causes a panic.
// Mirrors chip src/lib/core/TLVWriter.cpp:WriteElementHead:705-710
// (CHIP_ERROR_INVALID_TLV_TAG for context tag inside Array).
func TestEncoder_ContextTagInArray_Panics(t *testing.T) {
	t.Parallel()
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic for context tag inside Array, got none")
		}
	}()
	enc := NewEncoder()
	enc.StartArray(AnonymousTag())
	// This must panic: context tag inside Array is invalid per chip + matter.js.
	enc.PutUint(ContextTag(1), 42)
}

// TestEncoder_ContextTagInArray_NestedStruct_OK verifies that a context tag
// inside a Struct that is itself inside an Array is allowed (the inner
// container resets the constraint).
func TestEncoder_ContextTagInArray_NestedStruct_OK(t *testing.T) {
	t.Parallel()
	enc := NewEncoder()
	enc.StartArray(AnonymousTag())
	enc.StartStruct(AnonymousTag()) // struct inside array — anonymous tag OK
	enc.PutUint(ContextTag(1), 42)  // context tag inside struct — valid
	if err := enc.EndContainer(); err != nil {
		t.Fatalf("EndContainer (struct): %v", err)
	}
	if err := enc.EndContainer(); err != nil {
		t.Fatalf("EndContainer (array): %v", err)
	}
	if _, err := enc.Bytes(); err != nil {
		t.Fatalf("Bytes: %v", err)
	}
}

// --- PutUintWidth tests ---

// TestPutUintWidth_FixedWidths verifies that PutUintWidth emits exactly the
// declared wire width regardless of value magnitude.
// Mirrors matter.js packages/types/src/tlv/TlvNumber.ts TlvUInt8/16/32/64 schema types
// that each emit a fixed declared width. Wire: PutUintWidth(AnonymousTag(), 1, 4) →
// 06 01000000 (TypeUnsignedInt4 + 4B LE).
func TestPutUintWidth_FixedWidths(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		v       uint64
		width   int
		wantHex string
	}{
		{"1byte-val1", 1, 1, "0401"},
		{"2byte-val1", 1, 2, "050100"},
		{"4byte-val1", 1, 4, "0601000000"},
		{"8byte-val1", 1, 8, "070100000000000000"},
		{"4byte-val0", 0, 4, "0600000000"},
		{"2byte-val255", 255, 2, "05ff00"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			enc := NewEncoder()
			enc.PutUintWidth(AnonymousTag(), tc.v, tc.width)
			got, err := enc.Bytes()
			if err != nil {
				t.Fatalf("Bytes: %v", err)
			}
			assertHex(t, tc.name, got, hexB(t, tc.wantHex))
		})
	}
}

// TestPutUintWidth_PanicOnBadWidth verifies that PutUintWidth panics for
// invalid widthBytes values.
func TestPutUintWidth_PanicOnBadWidth(t *testing.T) {
	t.Parallel()
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic for widthBytes=3, got none")
		}
	}()
	enc := NewEncoder()
	enc.PutUintWidth(AnonymousTag(), 1, 3)
}

// --- Schema-layer gaps (FixMe stubs) ---

// TestObjectParity_InjectField_NotImplemented documents that openccu-loom does
// not expose a schema-layer injectField API (TlvObject.injectField in matter.js).
// The wire bytes are correct; only the field-injection helper is missing.
// Mirrors matter.js packages/types/test/tlv/TlvObjectTest.ts:203-264
// (describe "inject Field value")
func TestObjectParity_InjectField_NotImplemented(t *testing.T) {
	t.Skip("FixMe: openccu-loom gap — no TlvObject.injectField equivalent; " +
		"wire-byte encoding is correct but field injection must be done by the caller. " +
		"Tracked as L3-D7-FUTURE.")
}

// TestObjectParity_RemoveField_NotImplemented documents that openccu-loom does
// not expose a schema-layer removeField API (TlvObject.removeField in matter.js).
// Mirrors matter.js packages/types/test/tlv/TlvObjectTest.ts:286-325
// (describe "remove Field value")
func TestObjectParity_RemoveField_NotImplemented(t *testing.T) {
	t.Skip("FixMe: openccu-loom gap — no TlvObject.removeField equivalent. " +
		"Tracked as L3-D8-FUTURE.")
}

// TestObjectParity_ValidationError_NotImplemented documents that openccu-loom's
// TLV layer does not have a schema-level validation layer equivalent to
// TlvObject.validate / ValidationError with fieldName.
// Mirrors matter.js packages/types/test/tlv/TlvObjectTest.ts:266-284
// (describe "ValidationError")
func TestObjectParity_ValidationError_NotImplemented(t *testing.T) {
	t.Skip("FixMe: openccu-loom gap — no TlvSchema validation layer; " +
		"validation is caller responsibility at cluster-server boundary. " +
		"Tracked as L3-D9-FUTURE.")
}

// TestObjectParity_TaggedList_ProtocolSpecificTags_NotImplemented documents
// that openccu-loom has no allowProtocolSpecificTags flag equivalent.
// When decoding a list with non-context-specific tags, the openccu-loom
// decoder surfaces them as TagKindImplicitProfile / TagKindFullyQualified
// rather than throwing or silently ignoring them.
// Mirrors matter.js packages/types/test/tlv/TlvObjectTest.ts:478-495
// (describe "Tlv Lists with protocol specific tags")
func TestObjectParity_TaggedList_ProtocolSpecificTags_NotImplemented(t *testing.T) {
	t.Skip("FixMe: openccu-loom gap — no allowProtocolSpecificTags validation; " +
		"decoder passes through non-context tags without error. " +
		"Tracked as L3-D10-FUTURE.")
}

// TestObjectParity_ChunkedArray_NotImplemented documents that openccu-loom has
// no encodeAsChunkedArray / decodeFromChunkedArray API equivalent.
// The full-array wire encoding is identical; chunking is an IM-layer concern.
// Mirrors matter.js packages/types/test/tlv/TlvArrayTest.ts:39-54 + 63-103
// (describe "encode/decode chunked array")
func TestObjectParity_ChunkedArray_NotImplemented(t *testing.T) {
	t.Skip("FixMe: openccu-loom gap — chunked-array encode/decode lives in the IM " +
		"layer (internal/north/matter/im/), not in the tlv package. " +
		"Tracked as L3-D11-FUTURE.")
}
