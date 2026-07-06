// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package tlv

import (
	"encoding/binary"
	"fmt"
	"log/slog"
	"math"
	"unicode/utf8"
)

// Encoder builds a TLV byte stream. Use [NewEncoder], emit elements,
// then call [Encoder.Bytes] to retrieve the wire payload. The encoder
// tracks open containers so [Encoder.Bytes] can refuse a partial
// stream.
//
// Container-type validation: the encoder maintains a stack of open container
// types. [Encoder.writeControlAndTag] rejects context-specific tags inside an
// Array container, mirroring chip TLVWriter.cpp:WriteElementHead
// (CHIP_ERROR_INVALID_TLV_TAG) and matter.js TlvArray / TlvObject
// schema-layer enforcement.
type Encoder struct {
	buf            []byte
	openCont       int
	containerStack []ElementType
}

// NewEncoder returns a fresh encoder.
func NewEncoder() *Encoder { return &Encoder{} }

// Bytes returns the accumulated wire payload. Returns
// [ErrUnbalancedContainer] when one or more containers are still open.
func (e *Encoder) Bytes() ([]byte, error) {
	if e.openCont != 0 {
		return nil, fmt.Errorf("%w: %d container(s) still open", ErrUnbalancedContainer, e.openCont)
	}
	out := make([]byte, len(e.buf))
	copy(out, e.buf)
	return out, nil
}

// PutBool writes a boolean element.
func (e *Encoder) PutBool(tag Tag, v bool) {
	t := TypeBoolFalse
	if v {
		t = TypeBoolTrue
	}
	e.writeControlAndTag(t, tag)
}

// PutNull writes a null element.
func (e *Encoder) PutNull(tag Tag) {
	e.writeControlAndTag(TypeNull, tag)
}

// PutUint16 writes an explicit 2-byte unsigned-int element regardless
// of v's magnitude. Use this for spec-typed TLV fields (e.g. Matter
// `TlvUInt16` slots) where the consumer's decoder is strict on the
// element width — the magnitude-driven [Encoder.PutUint] would emit
// the smallest type that fits (UInt1 for v ≤ 0xFF) and the receiver
// would surface that as a parse error.
func (e *Encoder) PutUint16(tag Tag, v uint16) {
	e.writeControlAndTag(TypeUnsignedInt2, tag)
	e.buf = binary.LittleEndian.AppendUint16(e.buf, v)
}

// PutUint32 writes an explicit 4-byte unsigned-int element regardless
// of v's magnitude. Use for Matter `TlvUInt32` fields (e.g. DataVersion
// in AttributeDataIB §10.6.1.4, SubscriptionId in ReportData §10.6.4).
// Apple Home's MTRDevice IM-decoder is strict on element width and
// rejects the entire AttributeDataIB when DataVersion arrives as
// `TypeUnsignedInt1` — even though the magnitude (always 1 in v1.x
// without per-cluster version tracking) fits in one byte.
func (e *Encoder) PutUint32(tag Tag, v uint32) {
	e.writeControlAndTag(TypeUnsignedInt4, tag)
	e.buf = binary.LittleEndian.AppendUint32(e.buf, v)
}

// PutUint64 writes the smallest unsigned-int element that fits v,
// choosing width 1, 2, 4, or 8 bytes based on magnitude. This mirrors
// the wire behavior of matter.js TlvNumber.ts:TlvUInt64 / TlvFabricId
// (encodeUnsignedInt), where BigInt values are encoded at minimum fit.
// chip TLVReader is width-tolerant for unsigned types.
//
// For fields where the receiver enforces a fixed 8-byte width (e.g.
// EventNumber, EpochTimestamp — chip-tool rejects narrower encodings
// with CHIP Error 0x26), use [Encoder.PutUintWidth] with widthBytes=8.
func (e *Encoder) PutUint64(tag Tag, v uint64) {
	e.PutUint(tag, v)
}

// PutUint writes the smallest unsigned-int element that fits v. The
// width (1/2/4/8 bytes) is chosen automatically. The switch arms
// range-check before each width-narrowing conversion, so the
// uint64→uint8/16/32 conversions are safe.
func (e *Encoder) PutUint(tag Tag, v uint64) {
	switch {
	case v <= 0xFF:
		e.writeControlAndTag(TypeUnsignedInt1, tag)
		e.buf = append(e.buf, byte(v&0xFF))
	case v <= 0xFFFF:
		e.writeControlAndTag(TypeUnsignedInt2, tag)
		e.buf = binary.LittleEndian.AppendUint16(e.buf, uint16(v&0xFFFF))
	case v <= 0xFFFFFFFF:
		e.writeControlAndTag(TypeUnsignedInt4, tag)
		e.buf = binary.LittleEndian.AppendUint32(e.buf, uint32(v&0xFFFFFFFF))
	default:
		e.writeControlAndTag(TypeUnsignedInt8, tag)
		e.buf = binary.LittleEndian.AppendUint64(e.buf, v)
	}
}

// PutUintWidth writes an unsigned-int element using the exact wire width
// specified by widthBytes (1, 2, 4, or 8). Values are truncated to the
// given width. Panics for any other widthBytes value.
//
// Mirrors matter.js TlvNumber.ts:TlvUInt8/16/32/64 schema types that each
// emit a fixed declared width regardless of magnitude. Unlike
// [Encoder.PutUint] and [Encoder.PutUint64] (both smallest-fit),
// this helper lets callers express an exact schema-declared width — useful
// when a spec field is nominally declared uint32 but the caller holds a
// uint64 value whose upper bits are guaranteed zero. chip tolerates narrower
// or equal widths on read (TLVReader.cpp:GetValue template specialization);
// Apple Home also tolerates width mismatch for unsigned types. Wire example:
// PutUintWidth(AnonymousTag(), 1, 4) emits 06 01000000 (TypeUnsignedInt4 + 4-byte LE).
// Mirrors matter.js packages/types/src/tlv/TlvNumber.ts:TlvUInt32 et al.
func (e *Encoder) PutUintWidth(tag Tag, v uint64, widthBytes int) {
	switch widthBytes {
	case 1:
		e.writeControlAndTag(TypeUnsignedInt1, tag)
		e.buf = append(e.buf, byte(v&0xFF)) // caller-declared width: explicit truncation
	case 2:
		e.writeControlAndTag(TypeUnsignedInt2, tag)
		e.buf = binary.LittleEndian.AppendUint16(e.buf, uint16(v&0xFFFF)) // caller-declared width: explicit truncation
	case 4:
		e.writeControlAndTag(TypeUnsignedInt4, tag)
		e.buf = binary.LittleEndian.AppendUint32(e.buf, uint32(v&0xFFFFFFFF)) // caller-declared width: explicit truncation
	case 8:
		e.writeControlAndTag(TypeUnsignedInt8, tag)
		e.buf = binary.LittleEndian.AppendUint64(e.buf, v)
	default:
		// invariant: widthBytes is always a literal constant chosen by
		// the caller at the call site (every Encoder Put* method in
		// this package is void-returning and encodes wire-side
		// schema knowledge, not peer-supplied data) — an unsupported
		// value can only reach here through a new call site passing
		// the wrong constant, which is a build-time-discoverable coding
		// error, not a value that flows in from a remote message.
		panic(fmt.Sprintf("tlv: PutUintWidth: unsupported widthBytes %d (must be 1, 2, 4, or 8)", widthBytes))
	}
}

// PutInt16 writes an explicit 2-byte signed-int element regardless of
// v's magnitude. Counterpart to [Encoder.PutUint16] for spec-typed
// `int16` slots — Apple Home's HAP service mapper rejects an attribute
// whose declared type is `int16` if the wire shape is `TypeSignedInt1`.
func (e *Encoder) PutInt16(tag Tag, v int16) {
	e.writeControlAndTag(TypeSignedInt2, tag)
	e.buf = binary.LittleEndian.AppendUint16(e.buf, uint16(v)) //nolint:gosec // G115: bit-pattern preserved, two's-complement is the wire form; see #20
}

// PutInt32 writes an explicit 4-byte signed-int element regardless of
// v's magnitude. Counterpart to [Encoder.PutUint32] for spec-typed
// `int32` slots.
func (e *Encoder) PutInt32(tag Tag, v int32) {
	e.writeControlAndTag(TypeSignedInt4, tag)
	e.buf = binary.LittleEndian.AppendUint32(e.buf, uint32(v)) //nolint:gosec // G115: bit-pattern preserved, two's-complement is the wire form; see #20
}

// PutInt64 writes an explicit 8-byte signed-int element regardless of
// v's magnitude. For spec-typed `int64` slots (e.g. EpochS /
// SystemTimeUs / time-of-day fields) where the schema declares a
// fixed 64-bit width.
func (e *Encoder) PutInt64(tag Tag, v int64) {
	e.writeControlAndTag(TypeSignedInt8, tag)
	e.buf = binary.LittleEndian.AppendUint64(e.buf, uint64(v)) //nolint:gosec // G115: bit-pattern preserved, two's-complement is the wire form; see #20
}

// PutInt writes the smallest signed-int element that fits v. The
// switch arms guard the range before each width-narrowing conversion,
// so the gosec G115 warnings on int64→int8/16/32 below are
// intentional and bit-exact reversed by the matching int8/16/32
// sign-extending reads in [Decoder.readValue].
func (e *Encoder) PutInt(tag Tag, v int64) {
	switch {
	case v >= -0x80 && v <= 0x7F:
		e.writeControlAndTag(TypeSignedInt1, tag)
		e.buf = append(e.buf, byte(int8(v))) //nolint:gosec // G115: range checked above; see #20
	case v >= -0x8000 && v <= 0x7FFF:
		e.writeControlAndTag(TypeSignedInt2, tag)
		e.buf = binary.LittleEndian.AppendUint16(e.buf, uint16(int16(v))) //nolint:gosec // G115: range checked above; see #20
	case v >= -0x80000000 && v <= 0x7FFFFFFF:
		e.writeControlAndTag(TypeSignedInt4, tag)
		e.buf = binary.LittleEndian.AppendUint32(e.buf, uint32(int32(v))) //nolint:gosec // G115: range checked above; see #20
	default:
		e.writeControlAndTag(TypeSignedInt8, tag)
		e.buf = binary.LittleEndian.AppendUint64(e.buf, uint64(v)) //nolint:gosec // G115: bit-pattern preserved, two's-complement is the wire form; see #20
	}
}

// PutFloat32 writes a single-precision float element.
func (e *Encoder) PutFloat32(tag Tag, v float32) {
	e.writeControlAndTag(TypeFloat4, tag)
	e.buf = binary.LittleEndian.AppendUint32(e.buf, math.Float32bits(v))
}

// PutFloat64 writes a double-precision float element.
func (e *Encoder) PutFloat64(tag Tag, v float64) {
	e.writeControlAndTag(TypeFloat8, tag)
	e.buf = binary.LittleEndian.AppendUint64(e.buf, math.Float64bits(v))
}

// PutUTF8 writes a UTF-8 string element. The length-prefix width is
// chosen by string length.
func (e *Encoder) PutUTF8(tag Tag, s string) {
	e.writeStringLike(tag, []byte(s),
		TypeUTF8Str1, TypeUTF8Str2, TypeUTF8Str4, TypeUTF8Str8)
}

// PutUTF8WithMax writes a UTF-8 string element, enforcing a caller-supplied
// byte-length ceiling. When the encoded byte length of s exceeds maxBytes,
// the string is trimmed to fit (trimming preserves valid UTF-8 rune boundaries)
// and a warning is logged once so operators can diagnose over-length strings
// without losing the write. maxBytes ≤ 0 skips validation (equivalent to
// calling PutUTF8 directly).
func (e *Encoder) PutUTF8WithMax(tag Tag, s string, maxBytes int) {
	if maxBytes > 0 && len(s) > maxBytes {
		// Trim to a valid UTF-8 boundary at or below maxBytes.
		trimmed := s[:maxBytes]
		for trimmed != "" && !utf8.ValidString(trimmed) {
			trimmed = trimmed[:len(trimmed)-1]
		}
		slog.Warn(
			"tlv: UTF-8 string exceeds max length; trimming",
			slog.Int("original_bytes", len(s)),
			slog.Int("max_bytes", maxBytes),
			slog.Int("trimmed_bytes", len(trimmed)),
		)
		s = trimmed
	}
	e.writeStringLike(tag, []byte(s),
		TypeUTF8Str1, TypeUTF8Str2, TypeUTF8Str4, TypeUTF8Str8)
}

// PutOctets writes an octet-string element.
func (e *Encoder) PutOctets(tag Tag, b []byte) {
	e.writeStringLike(tag, b,
		TypeOctetStr1, TypeOctetStr2, TypeOctetStr4, TypeOctetStr8)
}

// StartStruct opens a Structure container.
func (e *Encoder) StartStruct(tag Tag) {
	e.writeControlAndTag(TypeStructure, tag)
	e.openCont++
	e.containerStack = append(e.containerStack, TypeStructure)
}

// StartArray opens an Array container. Inner elements must be
// anonymous-tagged; context-tagged writes inside this container are
// rejected by [Encoder.writeControlAndTag].
func (e *Encoder) StartArray(tag Tag) {
	e.writeControlAndTag(TypeArray, tag)
	e.openCont++
	e.containerStack = append(e.containerStack, TypeArray)
}

// StartList opens a List container.
func (e *Encoder) StartList(tag Tag) {
	e.writeControlAndTag(TypeList, tag)
	e.openCont++
	e.containerStack = append(e.containerStack, TypeList)
}

// EndContainer closes the most recently opened container. Returns
// [ErrUnbalancedContainer] when no container is open.
func (e *Encoder) EndContainer() error {
	if e.openCont == 0 {
		return ErrUnbalancedContainer
	}
	e.openCont--
	if len(e.containerStack) > 0 {
		e.containerStack = e.containerStack[:len(e.containerStack)-1]
	}
	// End-of-container has no tag (always anonymous).
	e.buf = append(e.buf, byte(TypeEndContainer))
	return nil
}

// PutUTF8Bounded writes a UTF-8 string element whose byte length must
// not exceed maxBytes. If len([]byte(s)) <= maxBytes the string is
// emitted verbatim via [Encoder.PutUTF8]. If the string is too long
// the encoder trims it to the largest valid UTF-8 boundary that fits
// within maxBytes, logs a warning, and emits the trimmed value.
//
// This mirrors the behaviour of matter.js StringSchema.validate in
// packages/types/src/tlv/TlvString.ts — matter.js throws on overflow;
// we trim-and-log instead of returning an error so cluster servers
// that use this helper do not need to handle an encoding error on every
// attribute write path.
//
// Consumers: BasicInformation (VendorName/ProductName/NodeLabel max=32,
// HardwareVersionString/SoftwareVersionString max=64, Location exact=2).
// For Location (exact=2) callers validate length separately and should
// not rely on trimming.
func (e *Encoder) PutUTF8Bounded(tag Tag, s string, maxBytes int) {
	b := []byte(s)
	if len(b) <= maxBytes {
		e.writeStringLike(tag, b,
			TypeUTF8Str1, TypeUTF8Str2, TypeUTF8Str4, TypeUTF8Str8)
		return
	}
	// Trim to the largest valid UTF-8 boundary that fits within maxBytes.
	// Work backwards from maxBytes until we land on a rune boundary.
	trimmed := b[:maxBytes]
	for !utf8.Valid(trimmed) {
		trimmed = trimmed[:len(trimmed)-1]
	}
	slog.Warn("tlv: PutUTF8Bounded: string truncated",
		"original_bytes", len(b),
		"max_bytes", maxBytes,
		"trimmed_bytes", len(trimmed))
	e.writeStringLike(tag, trimmed,
		TypeUTF8Str1, TypeUTF8Str2, TypeUTF8Str4, TypeUTF8Str8)
}

// PutInt8Bounded writes a 1-byte signed-int element. Returns
// [ErrInt8NullableSentinel] when v equals the Matter §6.6.4.5 Table 26
// nullable-int8 sentinel (−128 / 0x80). Callers on a nullable int8
// attribute must encode null via [Encoder.PutNull] when the intended
// semantic is "no value"; passing the sentinel as a regular value would
// cause the commissioner to interpret the field as null on read.
//
// Mirrors the boundary value documented in matter.js
// packages/types/src/tlv/TlvNumber.ts (TlvInt8 nullable min sentinel).
func (e *Encoder) PutInt8Bounded(tag Tag, v int8) error {
	if v == -128 {
		return fmt.Errorf("%w: int8 sentinel −128 must be encoded as null, not as a value", ErrInt8NullableSentinel)
	}
	e.writeControlAndTag(TypeSignedInt1, tag)
	e.buf = append(e.buf, byte(v)) //nolint:gosec // G115: range is int8, sentinel excluded above; see #20
	return nil
}

// PutInt16Bounded writes a 2-byte signed-int element. Returns
// [ErrInt16NullableSentinel] when v equals the Matter §6.6.4.5 Table 26
// nullable-int16 sentinel (−32768 / 0x8000). See [Encoder.PutInt8Bounded]
// for the encoding rationale.
//
// Mirrors matter.js packages/types/src/tlv/TlvNumber.ts TlvInt16
// nullable sentinel.
func (e *Encoder) PutInt16Bounded(tag Tag, v int16) error {
	if v == -32768 {
		return fmt.Errorf("%w: int16 sentinel −32768 must be encoded as null", ErrInt16NullableSentinel)
	}
	e.writeControlAndTag(TypeSignedInt2, tag)
	e.buf = binary.LittleEndian.AppendUint16(e.buf, uint16(v)) //nolint:gosec // G115: bit-pattern preserved, two's-complement is the wire form; see #20
	return nil
}

// PutInt32Bounded writes a 4-byte signed-int element. Returns
// [ErrInt32NullableSentinel] when v equals the Matter §6.6.4.5 Table 26
// nullable-int32 sentinel (−2147483648 / 0x80000000). See
// [Encoder.PutInt8Bounded] for the encoding rationale.
//
// Mirrors matter.js packages/types/src/tlv/TlvNumber.ts TlvInt32
// nullable sentinel.
func (e *Encoder) PutInt32Bounded(tag Tag, v int32) error {
	if v == -2147483648 {
		return fmt.Errorf("%w: int32 sentinel −2147483648 must be encoded as null", ErrInt32NullableSentinel)
	}
	e.writeControlAndTag(TypeSignedInt4, tag)
	e.buf = binary.LittleEndian.AppendUint32(e.buf, uint32(v)) //nolint:gosec // G115: bit-pattern preserved, two's-complement is the wire form; see #20
	return nil
}

// PutUint8Bounded writes a 1-byte unsigned-int element. Returns
// [ErrUint8NullableSentinel] when v equals 0xFF, the Matter §6.6.4.5
// Table 26 nullable-uint8 sentinel. Callers on a nullable uint8
// attribute (e.g. NullableEnum8, occupancy percentage) must encode
// null via [Encoder.PutNull] when the intended semantic is "no value";
// passing 0xFF as a regular value would cause the commissioner to
// interpret the field as null on read.
//
// Mirrors matter.js packages/types/src/tlv/TlvNullable.ts:28-31 —
// NullableSchema shrinks max from baseTypeMax to baseTypeMax-1 for
// nullable unsigned types.
func (e *Encoder) PutUint8Bounded(tag Tag, v uint8) error {
	if v == 0xFF {
		return fmt.Errorf("%w: uint8 sentinel 0xFF must be encoded as null, not as a value", ErrUint8NullableSentinel)
	}
	e.writeControlAndTag(TypeUnsignedInt1, tag)
	e.buf = append(e.buf, v)
	return nil
}

// PutUint16Bounded writes a 2-byte unsigned-int element. Returns
// [ErrUint16NullableSentinel] when v equals 0xFFFF, the Matter
// §6.6.4.5 Table 26 nullable-uint16 sentinel. See
// [Encoder.PutUint8Bounded] for the encoding rationale.
//
// Mirrors matter.js packages/types/src/tlv/TlvNullable.ts TlvUInt16
// nullable sentinel.
func (e *Encoder) PutUint16Bounded(tag Tag, v uint16) error {
	if v == 0xFFFF {
		return fmt.Errorf("%w: uint16 sentinel 0xFFFF must be encoded as null", ErrUint16NullableSentinel)
	}
	e.writeControlAndTag(TypeUnsignedInt2, tag)
	e.buf = binary.LittleEndian.AppendUint16(e.buf, v)
	return nil
}

// PutUint32Bounded writes a 4-byte unsigned-int element. Returns
// [ErrUint32NullableSentinel] when v equals 0xFFFFFFFF, the Matter
// §6.6.4.5 Table 26 nullable-uint32 sentinel. See
// [Encoder.PutUint8Bounded] for the encoding rationale.
//
// Mirrors matter.js packages/types/src/tlv/TlvNullable.ts TlvUInt32
// nullable sentinel.
func (e *Encoder) PutUint32Bounded(tag Tag, v uint32) error {
	if v == 0xFFFFFFFF {
		return fmt.Errorf("%w: uint32 sentinel 0xFFFFFFFF must be encoded as null", ErrUint32NullableSentinel)
	}
	e.writeControlAndTag(TypeUnsignedInt4, tag)
	e.buf = binary.LittleEndian.AppendUint32(e.buf, v)
	return nil
}

// PutUint64Bounded writes an 8-byte unsigned-int element. Returns
// [ErrUint64NullableSentinel] when v equals 0xFFFFFFFFFFFFFFFF, the
// Matter §6.6.4.5 Table 26 nullable-uint64 sentinel. See
// [Encoder.PutUint8Bounded] for the encoding rationale.
//
// Mirrors matter.js packages/types/src/tlv/TlvNullable.ts TlvUInt64
// nullable sentinel.
func (e *Encoder) PutUint64Bounded(tag Tag, v uint64) error {
	if v == 0xFFFFFFFFFFFFFFFF {
		return fmt.Errorf("%w: uint64 sentinel 0xFFFFFFFFFFFFFFFF must be encoded as null", ErrUint64NullableSentinel)
	}
	e.writeControlAndTag(TypeUnsignedInt8, tag)
	e.buf = binary.LittleEndian.AppendUint64(e.buf, v)
	return nil
}

// writeStringLike emits the control byte, tag, length-prefix (width
// chosen by [len]), and the body bytes. Range checks gate every
// width-narrowing conversion below.
func (e *Encoder) writeStringLike(tag Tag, b []byte, t1, t2, t4, t8 ElementType) {
	n := uint64(len(b))
	switch {
	case n <= 0xFF:
		e.writeControlAndTag(t1, tag)
		e.buf = append(e.buf, byte(n)) //nolint:gosec // G115: range checked above; see #20
	case n <= 0xFFFF:
		e.writeControlAndTag(t2, tag)
		e.buf = binary.LittleEndian.AppendUint16(e.buf, uint16(n)) //nolint:gosec // G115: range checked above; see #20
	case n <= 0xFFFFFFFF:
		e.writeControlAndTag(t4, tag)
		e.buf = binary.LittleEndian.AppendUint32(e.buf, uint32(n)) //nolint:gosec // G115: range checked above; see #20
	default:
		e.writeControlAndTag(t8, tag)
		e.buf = binary.LittleEndian.AppendUint64(e.buf, n)
	}
	e.buf = append(e.buf, b...)
}

// writeControlAndTag emits the control byte (tag-kind << 5 | type)
// followed by the tag bytes for that kind.
//
// Mirrors chip TLVWriter.cpp:WriteElementHead (CHIP_ERROR_INVALID_TLV_TAG) —
// context-specific tags are rejected inside an Array container. All other
// container/tag combinations are allowed (Structure accepts context tags;
// List accepts any tag).
func (e *Encoder) writeControlAndTag(t ElementType, tag Tag) {
	// Container-type check: reject context tags inside an Array.
	// Mirrors chip src/lib/core/TLVWriter.cpp:WriteElementHead:705-710.
	if len(e.containerStack) > 0 &&
		e.containerStack[len(e.containerStack)-1] == TypeArray &&
		tag.Kind == TagKindContext {
		// invariant: both the container nesting (StartArray/EndContainer
		// calls) and the tag kind passed to every Put* method are chosen
		// by our own cluster-server encoding code, never by re-emitting
		// a tag lifted from a decoded remote message — so this only
		// fires when a caller in this codebase picks ContextTag() for
		// an element it is writing into an Array, a coding mistake
		// caught by the parity/encode tests, not a wire-triggerable path.
		panic(fmt.Sprintf("tlv: context tag inside Array container (tag=%d); use AnonymousTag()", tag.Number))
	}
	// Note: chip TLVReader.cpp:822-827 enforces two strictness rules
	// (no context tag at top level; no anonymous tag inside a
	// Structure except EndOfContainer). Both are READ-side checks in
	// chip's verifier — Apple's MTRDevice drops the whole IM message
	// when either rule fires. OpenCCU-Loom's writer stays permissive
	// so codec-level tests can construct malformed fixtures to
	// validate the decoder rejection path; the matching decoder
	// enforcement lives in decode.go (StrictCheck).
	control := byte(tag.Kind)<<5 | byte(t)
	e.buf = append(e.buf, control)
	switch tag.Kind {
	case TagKindAnonymous:
		// no tag bytes
	case TagKindContext:
		// ContextTag(uint8) constructor guarantees Number ≤ 255.
		e.buf = append(e.buf, byte(tag.Number)) //nolint:gosec // G115: ContextTag bounds Number to uint8; see #20
	case TagKindCommonProfile2, TagKindImplicitProfile2:
		// CommonTag/ImplicitTag pick the *2 kind only when Number ≤ 0xFFFF.
		e.buf = binary.LittleEndian.AppendUint16(e.buf, uint16(tag.Number)) //nolint:gosec // G115: kind selection enforces Number ≤ 0xFFFF; see #20
	case TagKindCommonProfile4, TagKindImplicitProfile4:
		e.buf = binary.LittleEndian.AppendUint32(e.buf, tag.Number)
	case TagKindFullyQualified6:
		e.buf = binary.LittleEndian.AppendUint16(e.buf, tag.Vendor)
		e.buf = binary.LittleEndian.AppendUint16(e.buf, tag.Profile)
		// FullyQualifiedTag picks the *6 kind only when number ≤ 0xFFFF.
		e.buf = binary.LittleEndian.AppendUint16(e.buf, uint16(tag.Number)) //nolint:gosec // G115: kind selection enforces Number ≤ 0xFFFF; see #20
	case TagKindFullyQualified8:
		e.buf = binary.LittleEndian.AppendUint16(e.buf, tag.Vendor)
		e.buf = binary.LittleEndian.AppendUint16(e.buf, tag.Profile)
		e.buf = binary.LittleEndian.AppendUint32(e.buf, tag.Number)
	}
}
