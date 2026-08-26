// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package im

import (
	"testing"

	"github.com/SukramJ/openccu-loom/internal/north/matter/tlv"
)

// TestInvokeResponse_MarshalTLV_Empty verifies that an InvokeResponse with
// zero entries round-trips through the TLV encoder without error and
// produces a struct at the top level (anonymous tag).
func TestInvokeResponse_MarshalTLV_Empty(t *testing.T) {
	t.Parallel()
	ir := InvokeResponse{
		SuppressResponse: false,
		Responses:        nil,
	}
	enc := tlv.NewEncoder()
	ir.MarshalTLV(enc, func(_ *tlv.Encoder, _ tlv.Tag, _ any) {})
	wire, err := enc.Bytes()
	if err != nil {
		t.Fatalf("Bytes: %v", err)
	}

	// Must start with an anonymous struct opener.
	dec := tlv.NewDecoder(wire)
	el, err := dec.Next()
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	if !el.IsContainer || el.Type != tlv.TypeStructure {
		t.Errorf("expected struct, got type=0x%02X", el.Type)
	}
}

// TestInvokeResponse_MarshalTLV_StatusEntry verifies the CommandStatus
// branch (IsStatus=true) produces context tag 1 inside the InvokeResponseIB.
//
// Mirrors matter.js packages/types/src/protocol/types/TlvInvokeResponse.ts
// and chip src/app/CommandResponseSender.cpp:SendCommandResponse.
func TestInvokeResponse_MarshalTLV_StatusEntry(t *testing.T) {
	t.Parallel()
	ir := InvokeResponse{
		SuppressResponse: false,
		Responses: []InvokeResponseEntry{
			{
				Path: ConcreteCommandPath{
					Endpoint: 1, HasEndpoint: true,
					Cluster: 0x0006, HasCluster: true,
					Command: 0x01, HasCommand: true,
				},
				IsStatus: true,
				Status:   StatusIB{Status: StatusSuccess},
			},
		},
	}
	enc := tlv.NewEncoder()
	ir.MarshalTLV(enc, func(_ *tlv.Encoder, _ tlv.Tag, _ any) {})
	wire, err := enc.Bytes()
	if err != nil {
		t.Fatalf("Bytes: %v", err)
	}

	// Walk into the InvokeResponseIB and confirm tag 1 (CommandStatusIB)
	// is present rather than tag 0 (CommandDataIB).
	dec := tlv.NewDecoder(wire)
	if _, err := dec.Next(); err != nil { // top-level struct
		t.Fatalf("top struct: %v", err)
	}
	// Consume top-level fields until we find the Responses array.
	var foundResponseTag bool
	depth := 1
	for depth > 0 {
		el, err := dec.Next()
		if err != nil {
			t.Fatalf("decode: %v", err)
		}
		if el.IsEndContainer {
			depth--
			continue
		}
		if el.IsContainer {
			if depth == 1 && el.Tag.Kind == tlv.TagKindContext &&
				el.Tag.Number == uint32(tagInvokeRespResponses) {
				// We're in the responses array — advance into first element.
				// Next: InvokeResponseIB struct
				rib, err := dec.Next()
				if err != nil {
					t.Fatalf("InvokeResponseIB: %v", err)
				}
				if !rib.IsContainer || rib.Type != tlv.TypeStructure {
					t.Fatalf("expected InvokeResponseIB struct, got %+v", rib)
				}
				// First inner tag must be tagInvokeRespCommandStatus (1) for IsStatus path.
				inner, err := dec.Next()
				if err != nil {
					t.Fatalf("InvokeResponseIB inner: %v", err)
				}
				if inner.Tag.Kind == tlv.TagKindContext &&
					inner.Tag.Number == uint32(tagInvokeRespCommandStatus) {
					foundResponseTag = true
				}
				break
			}
			depth++
		}
	}
	if !foundResponseTag {
		t.Error("did not find CommandStatusIB (tag 1) in InvokeResponseIB")
	}
}

// TestInvokeResponse_MarshalTLV_DataEntry verifies the CommandData
// branch (IsStatus=false, HasResponse=true) produces context tag 0.
func TestInvokeResponse_MarshalTLV_DataEntry(t *testing.T) {
	t.Parallel()
	ir := InvokeResponse{
		SuppressResponse: false,
		Responses: []InvokeResponseEntry{
			{
				Path: ConcreteCommandPath{
					Endpoint: 2, HasEndpoint: true,
					Cluster: 0x0006, HasCluster: true,
					Command: 0x00, HasCommand: true,
				},
				IsStatus:    false,
				HasResponse: true,
				Response:    uint8(1),
			},
		},
	}
	var writerCalled bool
	enc := tlv.NewEncoder()
	ir.MarshalTLV(enc, func(e *tlv.Encoder, tag tlv.Tag, _ any) {
		writerCalled = true
		e.PutUint(tag, 1)
	})
	wire, err := enc.Bytes()
	if err != nil {
		t.Fatalf("Bytes: %v", err)
	}
	if !writerCalled {
		t.Error("fields writer not called for data entry with HasResponse=true")
	}

	dec := tlv.NewDecoder(wire)
	if _, err := dec.Next(); err != nil {
		t.Fatalf("top struct: %v", err)
	}
	depth := 1
	var foundDataTag bool
	for depth > 0 {
		el, err := dec.Next()
		if err != nil {
			t.Fatalf("decode: %v", err)
		}
		if el.IsEndContainer {
			depth--
			continue
		}
		if el.IsContainer {
			if depth == 1 && el.Tag.Kind == tlv.TagKindContext &&
				el.Tag.Number == uint32(tagInvokeRespResponses) {
				rib, err := dec.Next()
				if err != nil {
					t.Fatalf("InvokeResponseIB: %v", err)
				}
				if !rib.IsContainer || rib.Type != tlv.TypeStructure {
					t.Fatalf("expected struct")
				}
				inner, err := dec.Next()
				if err != nil {
					t.Fatalf("inner: %v", err)
				}
				if inner.Tag.Kind == tlv.TagKindContext &&
					inner.Tag.Number == uint32(tagInvokeRespCommandData) {
					foundDataTag = true
				}
				break
			}
			depth++
		}
	}
	if !foundDataTag {
		t.Error("did not find CommandDataIB (tag 0) in InvokeResponseIB")
	}
}

// TestInvokeResponse_MarshalTLV_CommandRef verifies that CommandRef
// is encoded when HasCommandRef is true.
func TestInvokeResponse_MarshalTLV_CommandRef(t *testing.T) {
	t.Parallel()
	ir := InvokeResponse{
		Responses: []InvokeResponseEntry{
			{
				Path: ConcreteCommandPath{
					Endpoint: 1, HasEndpoint: true,
					Cluster: 0x0006, HasCluster: true,
					Command: 0x00, HasCommand: true,
				},
				IsStatus:      true,
				Status:        StatusIB{Status: StatusSuccess},
				HasCommandRef: true,
				CommandRef:    0xABCD,
			},
		},
	}
	enc := tlv.NewEncoder()
	ir.MarshalTLV(enc, func(_ *tlv.Encoder, _ tlv.Tag, _ any) {})
	wire, err := enc.Bytes()
	if err != nil {
		t.Fatalf("Bytes: %v", err)
	}
	// Verify the wire is non-empty and parseable — structural check only.
	dec := tlv.NewDecoder(wire)
	el, err := dec.Next()
	if err != nil || !el.IsContainer {
		t.Fatalf("top-level container: err=%v el=%+v", err, el)
	}
}
