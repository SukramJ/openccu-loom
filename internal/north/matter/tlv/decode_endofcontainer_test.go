// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package tlv

import (
	"errors"
	"testing"
)

// TestNext_EndOfContainerConsumesTagBytesBeforeClassifying pins the
// byte-consumption order matter.js TlvCodec.ts:155-158 readTagType uses:
// the tag bytes selected by the control byte's tag-control field are read
// unconditionally before the element is classified as EndOfContainer.
// A non-anonymous tag-control paired with the EndOfContainer element type
// is a malformed stream (no conformant encoder emits it -- see
// [Encoder.EndContainer]), so [Decoder.Next] rejects it instead of
// silently treating it as an anonymous close and leaving the tag bytes
// unconsumed. Mirrors chip TLVReader.cpp:852-856 VerifyElement
// (CHIP_ERROR_INVALID_TLV_TAG when mElemTag != AnonymousTag() for
// EndOfContainer).
func TestNext_EndOfContainerConsumesTagBytesBeforeClassifying(t *testing.T) {
	t.Parallel()

	// Control byte 0x38 = (TagKindContext=1)<<5 | TypeEndContainer(0x18),
	// followed by a 1-byte context-tag id (0x05). A well-formed
	// EndOfContainer control byte is the bare 0x18 (TagKindAnonymous);
	// this fixture pairs it with a context tag-control instead.
	buf := []byte{0x38, 0x05}
	dec := NewDecoder(buf)

	_, err := dec.Next()
	if !errors.Is(err, ErrStrictTagViolation) {
		t.Fatalf("Next() error = %v, want ErrStrictTagViolation", err)
	}
}

// TestNext_WellFormedEndOfContainerUnaffected is the regression guard for
// the fix above: a conformant anonymous-tag-control EndOfContainer (the
// only shape [Encoder.EndContainer] ever emits) still decodes cleanly and
// consumes exactly one byte.
func TestNext_WellFormedEndOfContainerUnaffected(t *testing.T) {
	t.Parallel()

	enc := NewEncoder()
	enc.StartStruct(AnonymousTag())
	enc.PutBool(ContextTag(0), true)
	if err := enc.EndContainer(); err != nil {
		t.Fatalf("EndContainer: %v", err)
	}
	wire, err := enc.Bytes()
	if err != nil {
		t.Fatalf("Bytes: %v", err)
	}

	dec := NewDecoder(wire)
	if _, err := dec.Next(); err != nil { // Structure open
		t.Fatalf("Next (struct): %v", err)
	}
	if _, err := dec.Next(); err != nil { // ContextTag(0) bool
		t.Fatalf("Next (bool): %v", err)
	}
	el, err := dec.Next() // EndOfContainer
	if err != nil {
		t.Fatalf("Next (end): %v", err)
	}
	if !el.IsEndContainer {
		t.Fatalf("expected IsEndContainer, got %+v", el)
	}
	if el.Tag.Kind != TagKindAnonymous {
		t.Fatalf("expected AnonymousTag, got %+v", el.Tag)
	}
	if dec.Remaining() != 0 {
		t.Fatalf("Remaining() = %d, want 0", dec.Remaining())
	}
}
