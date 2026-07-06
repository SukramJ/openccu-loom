// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package sigma

import (
	"errors"
	"fmt"

	"github.com/SukramJ/openccu-loom/internal/north/matter/tlv"
)

// Thin wrappers around `internal/north/matter/tlv` tailored to the
// Sigma{1,2,3} message shape. The wrappers exist so the Sigma encoders
// stay readable — every field is "context-tagged primitive in an
// anonymous struct" — without dragging the full TLV package surface
// into every call site.

type sigmaEncoder struct {
	enc *tlv.Encoder
}

func sigmaTLVEncoder() *sigmaEncoder {
	return &sigmaEncoder{enc: tlv.NewEncoder()}
}

func (e *sigmaEncoder) startStruct() {
	e.enc.StartStruct(tlv.AnonymousTag())
}

// startStructTag opens a nested structure tagged with `tag`. Used by
// the Sigma2 responderSessionParams (context-tag 5) sub-struct per
// Matter §4.13.2.1 SessionParameters TLV.
func (e *sigmaEncoder) startStructTag(tag uint8) {
	e.enc.StartStruct(tlv.ContextTag(tag))
}

func (e *sigmaEncoder) endContainer() {
	_ = e.enc.EndContainer()
}

func (e *sigmaEncoder) putOctets(tag uint8, b []byte) {
	e.enc.PutOctets(tlv.ContextTag(tag), b)
}

func (e *sigmaEncoder) putUint(tag uint8, v uint64) {
	e.enc.PutUint(tlv.ContextTag(tag), v)
}

// putUint16 writes an explicit 2-byte unsigned integer regardless of
// the value's magnitude. Required for spec-typed TLV fields such as
// `responderSessionId` in Sigma2 — matter.js's TlvUInt16 decoder
// rejects a UInt1 element even when the value fits, surfacing as
// SecureChannel INVALID_PARAMETER on the wire.
func (e *sigmaEncoder) putUint16(tag uint8, v uint16) {
	e.enc.PutUint16(tlv.ContextTag(tag), v)
}

// putUint32 writes an explicit 4-byte unsigned integer regardless of
// the value's magnitude. SessionParameters.SessionIdleInterval and
// SessionActiveInterval are TlvUInt32 per matter.js
// SessionParameters.ts; magnitude-variable encoding would surface as
// SecureChannel INVALID_PARAMETER on chip and matter.js.
func (e *sigmaEncoder) putUint32(tag uint8, v uint32) {
	e.enc.PutUint32(tlv.ContextTag(tag), v)
}

func (e *sigmaEncoder) bytes() []byte {
	out, err := e.enc.Bytes()
	if err != nil {
		// invariant: Bytes only returns an error on encoder mis-use
		// (unbalanced containers) — every open/close call in this file
		// is authored locally, so an unbalanced container is a caller
		// bug, not something a remote peer's input can trigger; the
		// caller would be shipping malformed wire bytes anyway.
		panic(fmt.Sprintf("sigma: encoder.Bytes: %v", err))
	}
	return out
}

type sigmaDecoder struct {
	dec   *tlv.Decoder
	depth int
}

func sigmaTLVDecoder(b []byte) *sigmaDecoder {
	return &sigmaDecoder{dec: tlv.NewDecoder(b)}
}

// openStruct reads the leading anonymous-structure container marker.
// Returns an error when the wire bytes do not start with a struct.
func (d *sigmaDecoder) openStruct() error {
	el, err := d.dec.Next()
	if err != nil {
		return fmt.Errorf("read struct: %w", err)
	}
	if !el.IsContainer {
		return errors.New("expected struct container")
	}
	d.depth = 1
	return nil
}

// sigmaValue is the union of decoded element payloads the Sigma
// messages care about. `tag` is the context-tag number (1..255).
type sigmaValue struct {
	octets    []byte
	u         uint64
	elemType  tlv.ElementType
	container bool // true for Structure/Array/List headers — caller must skip via skipContainer.
}

// next returns the next field of the current container. `end=true`
// signals the end of the struct opened by openStruct (the matching
// `EndContainer` element).
func (d *sigmaDecoder) next() (tag uint8, val sigmaValue, end bool, err error) {
	el, err := d.dec.Next()
	if err != nil {
		return 0, sigmaValue{}, false, fmt.Errorf("decode: %w", err)
	}
	if el.IsEndContainer {
		return 0, sigmaValue{}, true, nil
	}
	if el.Tag.Kind != tlv.TagKindContext {
		return 0, sigmaValue{}, false, fmt.Errorf("expected context tag, got kind=%d", el.Tag.Kind)
	}
	if el.Tag.Number > 255 {
		return 0, sigmaValue{}, false, fmt.Errorf("context tag out of range: %d", el.Tag.Number)
	}
	return uint8(el.Tag.Number & 0xFF), sigmaValue{
		octets:    el.Octets,
		u:         el.Uint,
		elemType:  el.Type,
		container: el.IsContainer,
	}, false, nil
}

// skipContainer drains elements until the matching End-of-Container.
// Required after [next] returns a container value the caller does not
// want to descend into (e.g. the Sigma1 SessionParameters struct).
func (d *sigmaDecoder) skipContainer() error {
	depth := 1
	for depth > 0 {
		el, err := d.dec.Next()
		if err != nil {
			return fmt.Errorf("skip container: %w", err)
		}
		if el.IsContainer {
			depth++
		}
		if el.IsEndContainer {
			depth--
		}
	}
	return nil
}
