// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package tlv

import "errors"

// ElementType identifies the lower 5 bits of a TLV control byte.
// Values match Matter Core Spec §A.7.2 Table 73 verbatim.
type ElementType uint8

// ElementType values.
const (
	TypeSignedInt1   ElementType = 0x00
	TypeSignedInt2   ElementType = 0x01
	TypeSignedInt4   ElementType = 0x02
	TypeSignedInt8   ElementType = 0x03
	TypeUnsignedInt1 ElementType = 0x04
	TypeUnsignedInt2 ElementType = 0x05
	TypeUnsignedInt4 ElementType = 0x06
	TypeUnsignedInt8 ElementType = 0x07
	TypeBoolFalse    ElementType = 0x08
	TypeBoolTrue     ElementType = 0x09
	TypeFloat4       ElementType = 0x0A
	TypeFloat8       ElementType = 0x0B
	TypeUTF8Str1     ElementType = 0x0C
	TypeUTF8Str2     ElementType = 0x0D
	TypeUTF8Str4     ElementType = 0x0E
	TypeUTF8Str8     ElementType = 0x0F
	TypeOctetStr1    ElementType = 0x10
	TypeOctetStr2    ElementType = 0x11
	TypeOctetStr4    ElementType = 0x12
	TypeOctetStr8    ElementType = 0x13
	TypeNull         ElementType = 0x14
	TypeStructure    ElementType = 0x15
	TypeArray        ElementType = 0x16
	TypeList         ElementType = 0x17
	TypeEndContainer ElementType = 0x18
)

// TagKind identifies a TLV tag class (control-byte bits 5..7) per
// Matter Core Spec §A.7.3 Table 74.
type TagKind uint8

// TagKind values.
const (
	TagKindAnonymous        TagKind = 0
	TagKindContext          TagKind = 1
	TagKindCommonProfile2   TagKind = 2
	TagKindCommonProfile4   TagKind = 3
	TagKindImplicitProfile2 TagKind = 4
	TagKindImplicitProfile4 TagKind = 5
	TagKindFullyQualified6  TagKind = 6
	TagKindFullyQualified8  TagKind = 7
)

// Tag identifies a TLV element. The interpretation of Vendor /
// Profile / Number depends on the [TagKind].
type Tag struct {
	Kind    TagKind
	Vendor  uint16
	Profile uint16
	Number  uint32
}

// AnonymousTag returns the unique tag used for anonymous elements
// (typical for elements inside an Array container).
func AnonymousTag() Tag { return Tag{Kind: TagKindAnonymous} }

// ContextTag returns a 1-byte context-specific tag. n is the field
// index within the enclosing structure / list (0..255).
func ContextTag(n uint8) Tag { return Tag{Kind: TagKindContext, Number: uint32(n)} }

// CommonTag returns a Common-Profile tag. The width (2 or 4 bytes) is
// chosen automatically based on the magnitude of n.
func CommonTag(n uint32) Tag {
	if n <= 0xFFFF {
		return Tag{Kind: TagKindCommonProfile2, Number: n}
	}
	return Tag{Kind: TagKindCommonProfile4, Number: n}
}

// ImplicitTag returns an Implicit-Profile tag. Width (2 or 4 bytes)
// chosen by magnitude of n.
func ImplicitTag(n uint32) Tag {
	if n <= 0xFFFF {
		return Tag{Kind: TagKindImplicitProfile2, Number: n}
	}
	return Tag{Kind: TagKindImplicitProfile4, Number: n}
}

// FullyQualifiedTag returns a Fully-Qualified tag carrying explicit
// vendor + profile + number. Width (6 or 8 bytes) chosen by
// magnitude of number.
func FullyQualifiedTag(vendor, profile uint16, number uint32) Tag {
	kind := TagKindFullyQualified6
	if number > 0xFFFF {
		kind = TagKindFullyQualified8
	}
	return Tag{Kind: kind, Vendor: vendor, Profile: profile, Number: number}
}

// BoundedString is a UTF-8 string paired with a maximum byte length.
// Cluster servers that return a BoundedString from [interfaces.MatterClusterServer.MatterRead]
// signal to the bridge encoder that [Encoder.PutUTF8Bounded] should be
// used instead of the generic [Encoder.PutUTF8].
//
// Example: BasicInformation returns BoundedString{Value: nodeLabel, MaxBytes: 32}
// for attribute 0x0005 (NodeLabel) per Matter §11.1.5.7.
type BoundedString struct {
	// Value is the UTF-8 string payload.
	Value string

	// MaxBytes is the maximum byte length. See [Encoder.PutUTF8Bounded].
	MaxBytes int
}

// Element is the decoded view of a single TLV entry. Exactly one of
// Bool / Uint / Int / Float / String / Octets / IsNull / IsContainer
// is valid; the dispatch field is [Element.Type].
type Element struct {
	Tag    Tag
	Type   ElementType
	Bool   bool
	Uint   uint64
	Int    int64
	Float  float64
	String string
	Octets []byte
	IsNull bool

	// IsContainer is true for Structure / Array / List markers; the
	// caller drives the inner-element loop with successive Next()
	// calls until the matching End-of-Container.
	IsContainer bool

	// IsEndContainer is true for the 0x18 End-of-Container marker.
	IsEndContainer bool
}

// Errors returned by encoder / decoder.
var (
	// ErrTruncated is returned by [Decoder.Next] when the wire payload
	// ends mid-element.
	ErrTruncated = errors.New("tlv: truncated element")

	// ErrInvalidElementType is returned for control bytes whose lower
	// 5 bits do not match a known ElementType.
	ErrInvalidElementType = errors.New("tlv: invalid element type")

	// ErrInvalidTagKind is returned for control bytes whose upper 3
	// bits do not match a known TagKind.
	ErrInvalidTagKind = errors.New("tlv: invalid tag kind")

	// ErrStrictTagViolation is returned by [Validate] when a TLV
	// stream violates one of the chip TLVReader.cpp:806-839 strictness
	// rules — context tag at top level, anonymous tag inside Structure
	// (except EndOfContainer), or non-anonymous tag inside Array.
	// Apple's verifier drops the whole IM message on these patterns,
	// so a pre-flight Validate() lets callers fail fast before
	// emitting the bytes.
	ErrStrictTagViolation = errors.New("tlv: strict tag/container rule violation")

	// ErrLengthOverflow is returned when a length prefix exceeds the
	// remaining buffer.
	ErrLengthOverflow = errors.New("tlv: length exceeds buffer")

	// ErrUnbalancedContainer is returned by [Encoder.EndContainer]
	// when there is no open container, or by [Encoder.Bytes] when
	// containers remain open.
	ErrUnbalancedContainer = errors.New("tlv: unbalanced container")

	// ErrInt8NullableSentinel is returned by [Encoder.PutInt8Bounded]
	// when the caller passes −128, the Matter §6.6.4.5 Table 26
	// nullable-int8 sentinel. Encode null via [Encoder.PutNull] instead.
	ErrInt8NullableSentinel = errors.New("tlv: int8 nullable sentinel value")

	// ErrInt16NullableSentinel is returned by [Encoder.PutInt16Bounded]
	// when the caller passes −32768, the Matter §6.6.4.5 Table 26
	// nullable-int16 sentinel.
	ErrInt16NullableSentinel = errors.New("tlv: int16 nullable sentinel value")

	// ErrInt32NullableSentinel is returned by [Encoder.PutInt32Bounded]
	// when the caller passes −2147483648, the Matter §6.6.4.5 Table 26
	// nullable-int32 sentinel.
	ErrInt32NullableSentinel = errors.New("tlv: int32 nullable sentinel value")

	// ErrUint8NullableSentinel is returned by [Encoder.PutUint8Bounded]
	// when the caller passes 0xFF, the Matter §6.6.4.5 Table 26
	// nullable-uint8 sentinel. Encode null via [Encoder.PutNull] instead.
	//
	// Mirrors matter.js packages/types/src/tlv/TlvNullable.ts:28-31 —
	// NullableSchema shrinks the max of UInt8 from 0xFF to 0xFE so that
	// 0xFF is reserved as the null-sentinel wire value.
	ErrUint8NullableSentinel = errors.New("tlv: uint8 nullable sentinel value")

	// ErrUint16NullableSentinel is returned by [Encoder.PutUint16Bounded]
	// when the caller passes 0xFFFF, the Matter §6.6.4.5 Table 26
	// nullable-uint16 sentinel.
	ErrUint16NullableSentinel = errors.New("tlv: uint16 nullable sentinel value")

	// ErrUint32NullableSentinel is returned by [Encoder.PutUint32Bounded]
	// when the caller passes 0xFFFFFFFF, the Matter §6.6.4.5 Table 26
	// nullable-uint32 sentinel.
	ErrUint32NullableSentinel = errors.New("tlv: uint32 nullable sentinel value")

	// ErrUint64NullableSentinel is returned by [Encoder.PutUint64Bounded]
	// when the caller passes 0xFFFFFFFFFFFFFFFF, the Matter §6.6.4.5
	// Table 26 nullable-uint64 sentinel.
	ErrUint64NullableSentinel = errors.New("tlv: uint64 nullable sentinel value")
)
