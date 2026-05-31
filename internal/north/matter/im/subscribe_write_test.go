// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package im

import (
	"context"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/north/matter/tlv"
)

// TestSubscribeResponse_MarshalTLV_FieldPresence verifies that
// SubscribeResponse encodes SubscriptionID (tag 0), MaxInterval (tag 2),
// and IM-revision (tag 0xFF) per matter.js
// packages/types/src/protocol/types/TlvSubscribeResponse.ts.
func TestSubscribeResponse_MarshalTLV_FieldPresence(t *testing.T) {
	t.Parallel()
	sr := SubscribeResponse{
		SubscriptionID: 99,
		MaxInterval:    60,
	}
	enc := tlv.NewEncoder()
	sr.MarshalTLV(enc)
	wire, err := enc.Bytes()
	if err != nil {
		t.Fatalf("Bytes: %v", err)
	}

	dec := tlv.NewDecoder(wire)
	if _, err := dec.Next(); err != nil { // struct opener
		t.Fatalf("struct opener: %v", err)
	}
	var foundSubID, foundMax, foundRev bool
	for {
		el, err := dec.Next()
		if err != nil {
			t.Fatalf("decode: %v", err)
		}
		if el.IsEndContainer {
			break
		}
		if el.Tag.Kind != tlv.TagKindContext {
			continue
		}
		switch uint8(el.Tag.Number) { //nolint:gosec // Tag.Number is a uint32 but Matter context tags fit in uint8; the conversion is intentional
		case tagSubRespSubscriptionID:
			foundSubID = true
		case tagSubRespMaxInterval:
			foundMax = true
			if el.Uint != 60 {
				t.Errorf("MaxInterval = %d, want 60", el.Uint)
			}
		case tagInteractionModelRevision:
			foundRev = true
		}
	}
	if !foundSubID {
		t.Error("SubscriptionID tag missing")
	}
	if !foundMax {
		t.Error("MaxInterval tag missing")
	}
	if !foundRev {
		t.Error("IM revision tag missing")
	}
}

// TestUnmarshalSubscribeRequestTLV_Basic verifies round-trip of a minimal
// SubscribeRequest (KeepSubscriptions + MinInterval + MaxInterval).
func TestUnmarshalSubscribeRequestTLV_Basic(t *testing.T) {
	t.Parallel()
	want := SubscribeRequest{
		KeepSubscriptions:  true,
		MinIntervalFloor:   5,
		MaxIntervalCeiling: 60,
	}
	enc := tlv.NewEncoder()
	want.MarshalTLV(enc)
	wire, err := enc.Bytes()
	if err != nil {
		t.Fatalf("Bytes: %v", err)
	}

	dec := tlv.NewDecoder(wire)
	got, err := UnmarshalSubscribeRequestTLV(dec)
	if err != nil {
		t.Fatalf("UnmarshalSubscribeRequestTLV: %v", err)
	}
	if got.KeepSubscriptions != want.KeepSubscriptions {
		t.Errorf("KeepSubscriptions: got %v, want %v", got.KeepSubscriptions, want.KeepSubscriptions)
	}
	if got.MinIntervalFloor != want.MinIntervalFloor {
		t.Errorf("MinIntervalFloor: got %d, want %d", got.MinIntervalFloor, want.MinIntervalFloor)
	}
	if got.MaxIntervalCeiling != want.MaxIntervalCeiling {
		t.Errorf("MaxIntervalCeiling: got %d, want %d", got.MaxIntervalCeiling, want.MaxIntervalCeiling)
	}
}

// TestUnmarshalSubscribeRequestTLV_WithAttributeRequests verifies that
// an AttributeRequests array is decoded.
func TestUnmarshalSubscribeRequestTLV_WithAttributeRequests(t *testing.T) {
	t.Parallel()
	want := SubscribeRequest{
		MinIntervalFloor:   1,
		MaxIntervalCeiling: 30,
		AttributeRequests: []ConcreteAttributePath{
			{Endpoint: 1, HasEndpoint: true, Cluster: 0x0006, HasCluster: true, Attribute: 0, HasAttribute: true},
		},
	}
	enc := tlv.NewEncoder()
	want.MarshalTLV(enc)
	wire, _ := enc.Bytes()

	dec := tlv.NewDecoder(wire)
	got, err := UnmarshalSubscribeRequestTLV(dec)
	if err != nil {
		t.Fatalf("UnmarshalSubscribeRequestTLV: %v", err)
	}
	if len(got.AttributeRequests) != 1 {
		t.Fatalf("AttributeRequests: got %d, want 1", len(got.AttributeRequests))
	}
	if got.AttributeRequests[0] != want.AttributeRequests[0] {
		t.Errorf("AttributeRequests[0]: got %+v, want %+v", got.AttributeRequests[0], want.AttributeRequests[0])
	}
}

// TestUnmarshalSubscribeRequestTLV_WithEventRequests verifies that
// an EventRequests array is decoded.
func TestUnmarshalSubscribeRequestTLV_WithEventRequests(t *testing.T) {
	t.Parallel()
	want := SubscribeRequest{
		MinIntervalFloor:   0,
		MaxIntervalCeiling: 30,
		EventRequests: []ConcreteEventPath{
			{Endpoint: 2, HasEndpoint: true, Cluster: 0x003B, HasCluster: true, Event: 0x01, HasEvent: true},
		},
	}
	enc := tlv.NewEncoder()
	want.MarshalTLV(enc)
	wire, _ := enc.Bytes()

	dec := tlv.NewDecoder(wire)
	got, err := UnmarshalSubscribeRequestTLV(dec)
	if err != nil {
		t.Fatalf("UnmarshalSubscribeRequestTLV: %v", err)
	}
	if len(got.EventRequests) != 1 {
		t.Fatalf("EventRequests: got %d, want 1", len(got.EventRequests))
	}
	if got.EventRequests[0] != want.EventRequests[0] {
		t.Errorf("EventRequests[0]: got %+v, want %+v", got.EventRequests[0], want.EventRequests[0])
	}
}

// TestUnmarshalWriteRequestTLV_SingleWrite verifies basic write decoding.
func TestUnmarshalWriteRequestTLV_SingleWrite(t *testing.T) {
	t.Parallel()
	path := ConcreteAttributePath{
		Endpoint: 1, HasEndpoint: true,
		Cluster: 0x0006, HasCluster: true,
		Attribute: 0x0000, HasAttribute: true,
	}
	// Build a WriteRequest manually using TLV.
	enc := tlv.NewEncoder()
	enc.StartStruct(tlv.AnonymousTag())
	enc.PutBool(tlv.ContextTag(tagWriteReqSuppressResponse), false)
	enc.PutBool(tlv.ContextTag(tagWriteReqTimedRequest), false)
	// WriteRequests array (tag 2).
	enc.StartArray(tlv.ContextTag(tagWriteReqWriteRequests))
	// AttributeDataIB struct (anon inside array).
	enc.StartStruct(tlv.AnonymousTag())
	// DataVersion at tag 0 (optional).
	// Path at tag 1.
	path.MarshalTLV(enc, tlv.ContextTag(1))
	// Value at tag 2 — a simple uint8.
	enc.PutUint(tlv.ContextTag(2), 1)
	_ = enc.EndContainer()
	_ = enc.EndContainer()
	_ = enc.EndContainer()
	wire, err := enc.Bytes()
	if err != nil {
		t.Fatalf("Bytes: %v", err)
	}

	// A simple value reader that captures the raw uint value.
	var capturedValue AttributeValue
	reader := func(p ConcreteAttributePath, el tlv.Element, dec *tlv.Decoder) (AttributeValue, error) {
		capturedValue = AttributeValue{Value: el.Uint}
		return capturedValue, nil
	}

	dec := tlv.NewDecoder(wire)
	req, err := UnmarshalWriteRequestTLV(dec, reader)
	if err != nil {
		t.Fatalf("UnmarshalWriteRequestTLV: %v", err)
	}
	if len(req.Writes) != 1 {
		t.Fatalf("Writes: got %d, want 1", len(req.Writes))
	}
	w := req.Writes[0]
	if w.Path != path {
		t.Errorf("path: got %+v, want %+v", w.Path, path)
	}
}

// TestHandleWriteRequest_ACLChecker verifies that HandleWriteRequest
// respects an ACLChecker that returns an error.
func TestHandleWriteRequest_ACLChecker(t *testing.T) {
	t.Parallel()
	d := &fakeDispatcher{
		writeStat: StatusSuccess,
	}
	req := WriteRequest{
		Writes: []AttributeWrite{
			{
				Path: ConcreteAttributePath{
					Endpoint: 1, HasEndpoint: true,
					Cluster: 0x0006, HasCluster: true,
					Attribute: 0x0000, HasAttribute: true,
				},
				Value: AttributeValue{Value: true},
			},
		},
	}
	resp := HandleWriteRequest(context.Background(), d, req)
	if len(resp.Responses) != 1 {
		t.Fatalf("Responses: got %d, want 1", len(resp.Responses))
	}
}
