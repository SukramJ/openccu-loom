// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package tlv

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
)

// Decoder reads TLV elements from a byte slice. The decoder is
// stateless beyond the read position; callers walking nested
// containers track depth themselves by counting Structure / Array /
// List opens against End-of-Container closes.
type Decoder struct {
	buf []byte
	pos int
}

// NewDecoder wraps a byte slice for sequential element reading. The
// slice is not copied.
func NewDecoder(buf []byte) *Decoder { return &Decoder{buf: buf} }

// Pos returns the current read position. Useful for diagnostics when
// a malformed payload triggers an error.
func (d *Decoder) Pos() int { return d.pos }

// Remaining reports the byte count not yet consumed.
func (d *Decoder) Remaining() int { return len(d.buf) - d.pos }

// Next decodes and returns the next element. Returns [io.EOF] when the
// buffer is exhausted at an element boundary; otherwise the typed
// errors from [tlv].
//
// End-of-container markers (0x18) surface as Element with
// [Element.IsEndContainer] = true so callers can drive a depth counter
// over a flat element stream.
func (d *Decoder) Next() (Element, error) {
	if d.pos >= len(d.buf) {
		return Element{}, io.EOF
	}
	control := d.buf[d.pos]
	d.pos++

	kind := TagKind(control >> 5)
	etype := ElementType(control & 0x1F)

	if kind > TagKindFullyQualified8 {
		return Element{}, fmt.Errorf("%w: control=0x%02X at pos %d", ErrInvalidTagKind, control, d.pos-1)
	}

	// End-of-container is always anonymous.
	if etype == TypeEndContainer {
		return Element{Type: TypeEndContainer, IsEndContainer: true, Tag: AnonymousTag()}, nil
	}

	tag, err := d.readTag(kind)
	if err != nil {
		return Element{}, err
	}

	el := Element{Tag: tag, Type: etype}
	if err := d.readValue(&el); err != nil {
		return Element{}, err
	}
	return el, nil
}

// readTag consumes the tag bytes following the control byte.
func (d *Decoder) readTag(kind TagKind) (Tag, error) {
	tag := Tag{Kind: kind}
	switch kind {
	case TagKindAnonymous:
		return tag, nil
	case TagKindContext:
		if err := d.need(1); err != nil {
			return tag, err
		}
		tag.Number = uint32(d.buf[d.pos])
		d.pos++
	case TagKindCommonProfile2, TagKindImplicitProfile2:
		// Note: ImplicitProfile tags are decoded as raw
		// Tag{Kind: TagKindImplicitProfile2, Number: n} without resolving
		// the implicit profile context — by design. matter.js TlvCodec.ts:170
		// throws NotImplementedError for these; chip's TLVReader resolves them
		// against ImplicitProfileId when set. openccu-loom acts solely as a
		// responder (never initiates ImplicitProfile-tagged requests) so
		// no resolution is needed. If future code paths require it, implement
		// chip's ImplicitProfileId pattern.
		if err := d.need(2); err != nil {
			return tag, err
		}
		tag.Number = uint32(binary.LittleEndian.Uint16(d.buf[d.pos:]))
		d.pos += 2
	case TagKindCommonProfile4, TagKindImplicitProfile4:
		if err := d.need(4); err != nil {
			return tag, err
		}
		tag.Number = binary.LittleEndian.Uint32(d.buf[d.pos:])
		d.pos += 4
	case TagKindFullyQualified6:
		if err := d.need(6); err != nil {
			return tag, err
		}
		tag.Vendor = binary.LittleEndian.Uint16(d.buf[d.pos:])
		tag.Profile = binary.LittleEndian.Uint16(d.buf[d.pos+2:])
		tag.Number = uint32(binary.LittleEndian.Uint16(d.buf[d.pos+4:]))
		d.pos += 6
	case TagKindFullyQualified8:
		if err := d.need(8); err != nil {
			return tag, err
		}
		tag.Vendor = binary.LittleEndian.Uint16(d.buf[d.pos:])
		tag.Profile = binary.LittleEndian.Uint16(d.buf[d.pos+2:])
		tag.Number = binary.LittleEndian.Uint32(d.buf[d.pos+4:])
		d.pos += 8
	}
	return tag, nil
}

// readValue dispatches on element type and fills the value field of
// el.
func (d *Decoder) readValue(el *Element) error {
	switch el.Type {
	case TypeBoolFalse:
		el.Bool = false
	case TypeBoolTrue:
		el.Bool = true

	case TypeNull:
		el.IsNull = true

	case TypeSignedInt1:
		v, err := d.readU(1)
		if err != nil {
			return err
		}
		// readU(1) returns at most 0xFF; the int8() narrowing reverses
		// the encoder's two's-complement sign-extension.
		el.Int = int64(int8(v)) //nolint:gosec // G115: width matches encoder
	case TypeSignedInt2:
		v, err := d.readU(2)
		if err != nil {
			return err
		}
		el.Int = int64(int16(v)) //nolint:gosec // G115: width matches encoder
	case TypeSignedInt4:
		v, err := d.readU(4)
		if err != nil {
			return err
		}
		el.Int = int64(int32(v)) //nolint:gosec // G115: width matches encoder
	case TypeSignedInt8:
		v, err := d.readU(8)
		if err != nil {
			return err
		}
		el.Int = int64(v) //nolint:gosec // G115: bit-pattern preserved, two's-complement is the wire form

	case TypeUnsignedInt1, TypeUnsignedInt2, TypeUnsignedInt4, TypeUnsignedInt8:
		w := []int{1, 2, 4, 8}[int(el.Type)-int(TypeUnsignedInt1)]
		v, err := d.readU(w)
		if err != nil {
			return err
		}
		el.Uint = v

	case TypeFloat4:
		v, err := d.readU(4)
		if err != nil {
			return err
		}
		// readU(4) returns ≤ 0xFFFFFFFF.
		el.Float = float64(math.Float32frombits(uint32(v))) //nolint:gosec // G115: width matches readU(4)
	case TypeFloat8:
		v, err := d.readU(8)
		if err != nil {
			return err
		}
		el.Float = math.Float64frombits(v)

	case TypeUTF8Str1, TypeUTF8Str2, TypeUTF8Str4, TypeUTF8Str8:
		w := []int{1, 2, 4, 8}[int(el.Type)-int(TypeUTF8Str1)]
		body, err := d.readStringLike(w)
		if err != nil {
			return err
		}
		el.String = string(body)
	case TypeOctetStr1, TypeOctetStr2, TypeOctetStr4, TypeOctetStr8:
		w := []int{1, 2, 4, 8}[int(el.Type)-int(TypeOctetStr1)]
		body, err := d.readStringLike(w)
		if err != nil {
			return err
		}
		// Decoder copies the body so the caller can safely retain it
		// after a subsequent Next() advances past the slice.
		out := make([]byte, len(body))
		copy(out, body)
		el.Octets = out

	case TypeStructure, TypeArray, TypeList:
		el.IsContainer = true

	default:
		return fmt.Errorf("%w: type=0x%02X at pos %d", ErrInvalidElementType, el.Type, d.pos-1)
	}
	return nil
}

// readU reads an unsigned little-endian integer of width [w] bytes
// (1, 2, 4 or 8).
func (d *Decoder) readU(w int) (uint64, error) {
	if err := d.need(w); err != nil {
		return 0, err
	}
	var v uint64
	switch w {
	case 1:
		v = uint64(d.buf[d.pos])
	case 2:
		v = uint64(binary.LittleEndian.Uint16(d.buf[d.pos:]))
	case 4:
		v = uint64(binary.LittleEndian.Uint32(d.buf[d.pos:]))
	case 8:
		v = binary.LittleEndian.Uint64(d.buf[d.pos:])
	}
	d.pos += w
	return v, nil
}

// readStringLike reads a [w]-byte length prefix followed by the body
// and returns a slice into the underlying buffer.
func (d *Decoder) readStringLike(w int) ([]byte, error) {
	n, err := d.readU(w)
	if err != nil {
		return nil, err
	}
	remaining := len(d.buf) - d.pos
	// remaining is guaranteed ≥ 0 by the d.pos invariants in [need].
	if n > uint64(remaining) { //nolint:gosec // G115: remaining is non-negative by readU's bound check
		return nil, fmt.Errorf("%w: declared %d, have %d", ErrLengthOverflow, n, remaining)
	}
	body := d.buf[d.pos : d.pos+int(n)] //nolint:gosec // G115: n bounded by remaining (int) above
	d.pos += int(n)                     //nolint:gosec // G115: n bounded by remaining (int) above
	return body, nil
}

// need verifies n bytes remain in the buffer.
func (d *Decoder) need(n int) error {
	if d.pos+n > len(d.buf) {
		return fmt.Errorf("%w: need %d, have %d", ErrTruncated, n, len(d.buf)-d.pos)
	}
	return nil
}

// Validate walks buf with chip-strict tag/container rules and
// returns the first violation. Strict rules enforced (mirroring chip
// TLVReader.cpp:806-839 + TLVWriter.cpp:WriteElementHead):
//
//   - Context tag at top level (no enclosing container) →
//     CHIP_ERROR_INVALID_TLV_TAG (chip TLVReader.cpp:822-823).
//   - Anonymous tag inside Structure (except EndOfContainer) →
//     CHIP_ERROR_INVALID_TLV_TAG (chip TLVReader.cpp:826-827).
//   - Non-anonymous tag inside Array →
//     CHIP_ERROR_INVALID_TLV_TAG (chip TLVReader.cpp:830-831).
//
// Returns nil when buf parses cleanly under the strict rules. Used
// by [im.WireRoundTripGuard]-style tests + by north-bound code that
// wants to refuse self-produced bytes Apple would silently drop.
func Validate(buf []byte) error {
	d := NewDecoder(buf)
	containers := make([]ElementType, 0, 8)
	for d.Remaining() != 0 {

		// Snapshot the current container BEFORE reading the next
		// element so the rules apply to the element's PARENT.
		var parent ElementType
		if n := len(containers); n > 0 {
			parent = containers[n-1]
		}
		el, err := d.Next()
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return err
		}
		if el.IsEndContainer {
			if len(containers) == 0 {
				return fmt.Errorf("%w: EndOfContainer outside any container at pos %d",
					ErrStrictTagViolation, d.Pos())
			}
			containers = containers[:len(containers)-1]
			continue
		}
		switch {
		case len(containers) == 0 && el.Tag.Kind == TagKindContext:
			return fmt.Errorf("%w: context tag at top level (tag=%d, pos=%d)",
				ErrStrictTagViolation, el.Tag.Number, d.Pos())
		case parent == TypeStructure && el.Tag.Kind == TagKindAnonymous:
			return fmt.Errorf("%w: anonymous tag inside Structure (element type=0x%02X, pos=%d)",
				ErrStrictTagViolation, byte(el.Type), d.Pos())
		case parent == TypeArray && el.Tag.Kind != TagKindAnonymous:
			return fmt.Errorf("%w: non-anonymous tag inside Array (tag-kind=%d, pos=%d)",
				ErrStrictTagViolation, el.Tag.Kind, d.Pos())
		}
		if el.Type == TypeStructure || el.Type == TypeArray || el.Type == TypeList {
			containers = append(containers, el.Type)
		}
	}
	if len(containers) > 0 {
		return fmt.Errorf("%w: %d unclosed container(s) at end of buffer",
			ErrUnbalancedContainer, len(containers))
	}
	return nil
}
