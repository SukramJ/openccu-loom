// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package tlv

import (
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"
)

// matter.js's TlvCodec is the wire-level reference. Drift between
// openccu-loom's TLV encoder and matter.js produces silent Apple Home
// pair aborts — the IM decoder reads bytes with strict-typed schemas
// and rejects an attribute whose width does not match the spec table.
// The fixtures here pin the exact wire-byte output for representative
// shapes (unsigned ints across width boundaries, signed ints, bools,
// nulls, strings, octets, context tags) so a regression in
// `Encoder.PutUint` / `PutInt` / etc. lights up at PR time, not
// after a 90-second pair attempt.
//
// Fixture regeneration: run
// `npx ts-node /tmp/matter-spike/tlv_fixtures.ts >
// docs/parity/matter/tlv-wire-fixtures.json` against an
// `@matter/types` install, then `cp` the file into `testdata/`.
// Producer script lives under
// `docs/parity/matter/generate-tlv-wire-fixtures.ts`.

//go:embed testdata/tlv-wire-fixtures.json
var tlvFixturesJSON []byte

type tlvFixture struct {
	Label       string `json:"label"`
	Description string `json:"description"`
	BytesHex    string `json:"bytesHex"`
}

func loadFixtures(t *testing.T) []tlvFixture {
	t.Helper()
	var f []tlvFixture
	if err := json.Unmarshal(tlvFixturesJSON, &f); err != nil {
		t.Fatalf("unmarshal tlv-wire-fixtures.json: %v", err)
	}
	if len(f) == 0 {
		t.Fatal("tlv-wire-fixtures.json is empty")
	}
	return f
}

func mustHex(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(s)
	if err != nil {
		t.Fatalf("decode hex %q: %v", s, err)
	}
	return b
}

// encode runs the openccu-loom encoder for a single label and returns
// the wire bytes. The labels are 1:1 with the matter.js fixtures.
func encode(t *testing.T, label string) []byte {
	t.Helper()
	enc := NewEncoder()
	switch label {
	// PutUint width selection: smallest type that fits.
	case "uint_anon_0":
		enc.PutUint(AnonymousTag(), 0)
	case "uint_anon_255":
		enc.PutUint(AnonymousTag(), 0xFF)
	case "uint_anon_256":
		enc.PutUint(AnonymousTag(), 0x0100)
	case "uint_anon_65535":
		enc.PutUint(AnonymousTag(), 0xFFFF)
	case "uint_anon_65536":
		enc.PutUint(AnonymousTag(), 0x10000)
	case "uint_anon_specversion":
		enc.PutUint(AnonymousTag(), 0x01050100)
	case "uint_anon_max32":
		enc.PutUint(AnonymousTag(), 0xFFFFFFFF)
	case "uint64_anon_2_to_32":
		enc.PutUint(AnonymousTag(), 1<<32)

	// Explicit-width writers — same value, different declared widths.
	case "uint16_explicit_1":
		enc.PutUint16(AnonymousTag(), 1)
	case "uint32_explicit_1":
		enc.PutUint32(AnonymousTag(), 1)
	case "uint64_explicit_1":
		enc.PutUint64(AnonymousTag(), 1)

	// Signed ints.
	case "int_anon_minus1":
		enc.PutInt(AnonymousTag(), -1)
	case "int_anon_minus128":
		enc.PutInt(AnonymousTag(), -128)
	case "int_anon_minus129":
		enc.PutInt(AnonymousTag(), -129)

	// Boolean / null.
	case "bool_anon_true":
		enc.PutBool(AnonymousTag(), true)
	case "bool_anon_false":
		enc.PutBool(AnonymousTag(), false)
	case "null_anon":
		enc.PutNull(AnonymousTag())

	// UTF-8 strings.
	case "utf8_anon_empty":
		enc.PutUTF8(AnonymousTag(), "")
	case "utf8_anon_openccu-loom":
		enc.PutUTF8(AnonymousTag(), "openccu-loom")

	// Octet strings.
	case "octets_anon_empty":
		enc.PutOctets(AnonymousTag(), []byte{})
	case "octets_anon_3bytes":
		enc.PutOctets(AnonymousTag(), []byte{0x01, 0x02, 0x03})

	// Context tags.
	case "uint_ctx0_1":
		enc.PutUint(ContextTag(0), 1)
	case "uint_ctx7_42":
		enc.PutUint(ContextTag(7), 42)

	// Explicit-width signed ints (L5-fixtures addition).
	case "int16_explicit_minus1":
		enc.PutInt16(AnonymousTag(), -1)
	case "int32_explicit_minus1":
		enc.PutInt32(AnonymousTag(), -1)
	case "int64_explicit_minus1":
		enc.PutInt64(AnonymousTag(), -1)

	// Floats (L5-fixtures addition).
	case "float32_anon_1":
		enc.PutFloat32(AnonymousTag(), 1.0)
	case "float64_anon_1":
		enc.PutFloat64(AnonymousTag(), 1.0)

	// Struct container (L5-fixtures addition).
	case "struct_empty":
		enc.StartStruct(AnonymousTag())
		_ = enc.EndContainer()

	// Array with one anonymous uint8 element (L5-fixtures addition).
	case "array_one_anon_uint8":
		enc.StartArray(AnonymousTag())
		enc.PutUint(AnonymousTag(), 42)
		_ = enc.EndContainer()

	// Empty list (L5-fixtures addition).
	case "list_empty":
		enc.StartList(AnonymousTag())
		_ = enc.EndContainer()

	// CommonTag (L5-fixtures addition).
	case "common_tag_0001_uint8_42":
		enc.PutUint(CommonTag(0x0001), 42)

	// Null with context tag (L5-fixtures addition).
	case "null_ctx0":
		enc.PutNull(ContextTag(0))

	// Decoder roundtrip fixtures (L5-fixtures addition).
	case "decoder_roundtrip_uint_ctx1":
		enc.PutUint(ContextTag(1), 0xFF)
	case "decoder_roundtrip_int16_ctx2":
		enc.PutInt16(ContextTag(2), -100)

	// FullyQualified tags — matter.js TlvCodec.writeTag
	// FullyQualified48/64 (control bytes 0xC0/0xE0). Profile is laid
	// out as a uint32 (LE), so a Tag.Vendor=low16,Tag.Profile=high16
	// split that back-fills `(Profile<<16)|Vendor` matches matter.js
	// byte-for-byte. The id field is uint16 (FQ48) or uint32 (FQ64).
	case "fq6_profile_12345678_id_abcd_uint8_42":
		enc.PutUint(FullyQualifiedTag(0x5678, 0x1234, 0xABCD), 42)
	case "fq8_profile_12345678_id_deadbeef_uint8_42":
		enc.PutUint(FullyQualifiedTag(0x5678, 0x1234, 0xDEADBEEF), 42)

	// ImplicitProfile tags — encoder paths matter.js does not own.
	// See `docs/parity/matter/tlv-wire-fixtures.json` for the
	// matter-spec derivation; matter.js's TlvCodec.writeTag intentionally
	// omits ImplicitProfile branches (use-case is "implicit profile is
	// the current message context" which the matter.js Behaviors layer
	// resolves before encoding). openccu-loom emits them so cluster code
	// can adopt the Matter 1.6 Implicit-Tag patterns without regressing
	// to a FullyQualified fallback.
	case "implicit2_id_1234_uint8_42":
		enc.PutUint(ImplicitTag(0x1234), 42)
	case "implicit4_id_12345678_uint8_42":
		enc.PutUint(ImplicitTag(0x12345678), 42)

	// 2-byte-length-prefix UTF-8 strings + nullable sentinels — locks
	// width-selection at the 256-byte boundary and the sentinel-value
	// encoding matter.js uses for "null" on nullable uint{8,16}
	// attributes (matter.js TlvType.UnsignedInt + nullable wrapper).
	case "utf8_anon_len300":
		enc.PutUTF8(AnonymousTag(), strings.Repeat("a", 300))
	case "uint8_ctx0_FF_nullable_sentinel":
		enc.PutUint(ContextTag(0), 0xFF)
	case "uint16_ctx0_FFFF_nullable_sentinel":
		// Force 2-byte width — natural-width PutUint would pick uint16
		// for 0xFFFF anyway, but PutUint16 makes the intent explicit.
		enc.PutUint16(ContextTag(0), 0xFFFF)

	// Float with context tag — locks the (TagKindContext<<5)|TypeFloat
	// control byte that matters.js TlvCodec emits for float cluster attrs.
	case "float32_ctx0_1":
		enc.PutFloat32(ContextTag(0), 1.0)
	case "float64_ctx1_1":
		enc.PutFloat64(ContextTag(1), 1.0)

	// Signed int8 auto-width at various positive magnitudes.
	case "int_anon_0":
		enc.PutInt(AnonymousTag(), 0)
	case "int_anon_42":
		enc.PutInt(AnonymousTag(), 42)
	case "int_anon_127":
		enc.PutInt(AnonymousTag(), 127)

	// Nullable signed-int sentinel values per Matter §6.6.4.5 Table 26.
	// These encode via the ordinary PutInt / PutInt16 / PutInt32 paths
	// (the fixture locks the wire bytes); PutInt8Bounded / PutInt16Bounded /
	// PutInt32Bounded intentionally REJECT these values — see
	// TestPutIntBounded_SentinelRejected in tlv_test.go.
	case "int8_ctx0_nullable_sentinel":
		enc.PutInt(ContextTag(0), -128)
	case "int16_ctx0_nullable_sentinel":
		enc.PutInt16(ContextTag(0), -32768)
	case "int32_ctx0_nullable_sentinel":
		enc.PutInt32(ContextTag(0), -2147483648)

	default:
		t.Fatalf("unknown fixture label %q — extend the encode switch", label)
	}
	got, err := enc.Bytes()
	if err != nil {
		t.Fatalf("encoder Bytes() returned error for %s: %v", label, err)
	}
	return got
}

// TestParityMatterJS_EncoderWireBytes verifies that openccu-loom's TLV
// encoder produces the exact same wire bytes as matter.js's TlvCodec
// for every fixture. Each fixture is a one-line shape (one element
// with one tag and one value); together they cover the full type +
// width matrix the bridge needs at the IM-encoding boundary.
func TestParityMatterJS_EncoderWireBytes(t *testing.T) {
	t.Parallel()
	fixtures := loadFixtures(t)
	for _, f := range fixtures {
		// capture
		t.Run(f.Label, func(t *testing.T) {
			t.Parallel()
			want := mustHex(t, f.BytesHex)
			got := encode(t, f.Label)
			if hex.EncodeToString(got) != hex.EncodeToString(want) {
				t.Errorf("encoder mismatch for %s\n  description: %s\n  want: %s\n  got:  %s",
					f.Label, f.Description, hex.EncodeToString(want), hex.EncodeToString(got))
			}
		})
	}
}

// TestParityMatterJS_EncoderFixturesCovered guards against a fixture
// being added in the JSON without a matching `case` arm in `encode`.
// Without this guard, a stale encoder switch would let new shapes pass
// silently because the test never asserts the bytes for them.
func TestParityMatterJS_EncoderFixturesCovered(t *testing.T) {
	t.Parallel()
	fixtures := loadFixtures(t)
	for _, f := range fixtures {
		t.Run(f.Label, func(t *testing.T) {
			t.Parallel()
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("encode panic for %s — switch arm missing: %v", f.Label, r)
				}
			}()
			_ = encode(t, f.Label)
		})
	}
}
