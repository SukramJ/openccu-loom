// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package im

import (
	"testing"

	"github.com/SukramJ/openccu-loom/internal/north/matter/tlv"
)

// TestUnmarshalTimedRequestTLV_Basic verifies that a well-formed
// TimedRequestMessage is decoded correctly.
//
// Mirrors chip src/app/tests/TestTimedHandler.cpp.
func TestUnmarshalTimedRequestTLV_Basic(t *testing.T) {
	t.Parallel()
	enc := tlv.NewEncoder()
	enc.StartStruct(tlv.AnonymousTag())
	enc.PutUint(tlv.ContextTag(tagTimedReqTimeout), 5000) // 5 s
	_ = enc.EndContainer()
	wire, err := enc.Bytes()
	if err != nil {
		t.Fatalf("Bytes: %v", err)
	}

	dec := tlv.NewDecoder(wire)
	req, err := UnmarshalTimedRequestTLV(dec)
	if err != nil {
		t.Fatalf("UnmarshalTimedRequestTLV: %v", err)
	}
	if req.TimeoutMs != 5000 {
		t.Errorf("TimeoutMs = %d, want 5000", req.TimeoutMs)
	}
}

// TestUnmarshalTimedRequestTLV_WithIMRevision verifies that the IM
// revision field (tag 0xFF) does not cause parse failure.
func TestUnmarshalTimedRequestTLV_WithIMRevision(t *testing.T) {
	t.Parallel()
	enc := tlv.NewEncoder()
	enc.StartStruct(tlv.AnonymousTag())
	enc.PutUint(tlv.ContextTag(tagTimedReqTimeout), 1000)
	enc.PutUint(tlv.ContextTag(tagTimedReqInteractionModelRevision), 13)
	_ = enc.EndContainer()
	wire, _ := enc.Bytes()

	dec := tlv.NewDecoder(wire)
	req, err := UnmarshalTimedRequestTLV(dec)
	if err != nil {
		t.Fatalf("UnmarshalTimedRequestTLV: %v", err)
	}
	if req.TimeoutMs != 1000 {
		t.Errorf("TimeoutMs = %d, want 1000", req.TimeoutMs)
	}
}

// TestStatusResponse_MarshalTLV verifies that StatusResponse encodes
// a struct containing the status code and IM revision.
func TestStatusResponse_MarshalTLV_Success(t *testing.T) {
	t.Parallel()
	sr := StatusResponse{Status: StatusSuccess}
	enc := tlv.NewEncoder()
	sr.MarshalTLV(enc)
	wire, err := enc.Bytes()
	if err != nil {
		t.Fatalf("Bytes: %v", err)
	}

	dec := tlv.NewDecoder(wire)
	el, err := dec.Next()
	if err != nil || !el.IsContainer || el.Type != tlv.TypeStructure {
		t.Fatalf("expected struct: err=%v el=%+v", err, el)
	}

	// tag 0 = Status, tag 0xFF = IM revision
	var statusTag, revisionTag bool
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
		switch uint8(el.Tag.Number) { //nolint:gosec // Tag.Number is uint32 but Matter context tags fit in uint8; the conversion is intentional
		case tagStatusResponseStatus:
			statusTag = true
			if StatusCode(el.Uint) != StatusSuccess { //nolint:gosec // StatusCode is a uint8 alias; Matter status codes fit within the range
				t.Errorf("status = 0x%02X, want Success", el.Uint)
			}
		case tagStatusResponseInteractionModelRevision:
			revisionTag = true
		}
	}
	if !statusTag {
		t.Error("tag 0 (Status) missing from StatusResponse")
	}
	if !revisionTag {
		t.Error("tag 0xFF (IM revision) missing from StatusResponse")
	}
}

// TestStatusCode_String covers the String() cases not yet reached.
func TestStatusCode_String(t *testing.T) {
	t.Parallel()
	cases := []struct {
		code StatusCode
		want string
	}{
		{StatusSuccess, "Success"},
		{StatusFailure, "Failure"},
		{StatusUnsupportedAttribute, "UnsupportedAttribute"},
		{StatusUnsupportedCommand, "UnsupportedCommand"},
		{StatusUnsupportedCluster, "UnsupportedCluster"},
		{StatusUnsupportedEndpoint, "UnsupportedEndpoint"},
		{StatusUnsupportedWrite, "UnsupportedWrite"},
		{StatusUnsupportedRead, "UnsupportedRead"},
		{StatusInvalidAction, "InvalidAction"},
		{StatusInvalidCommand, "InvalidCommand"},
		{StatusConstraintError, "ConstraintError"},
		{StatusResourceExhausted, "ResourceExhausted"},
		{StatusBusy, "Busy"},
		{StatusTimeout, "Timeout"},
		{StatusCode(0xEE), "Status(0xEE)"}, // default branch
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.want, func(t *testing.T) {
			t.Parallel()
			got := tc.code.String()
			if got != tc.want {
				t.Errorf("StatusCode(0x%02X).String() = %q, want %q", uint8(tc.code), got, tc.want)
			}
		})
	}
}

// TestMinEventNumberFromFilters covers the helper.
func TestMinEventNumberFromFilters(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		filters []EventMinimumNumber
		want    uint64
	}{
		{"empty", nil, 0},
		{"single", []EventMinimumNumber{{EventMin: 7}}, 7},
		{"multiple_first_is_min", []EventMinimumNumber{{EventMin: 3}, {EventMin: 10}, {EventMin: 1}}, 1},
		{"all_equal", []EventMinimumNumber{{EventMin: 5}, {EventMin: 5}}, 5},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := minEventNumberFromFilters(tc.filters)
			if got != tc.want {
				t.Errorf("minEventNumberFromFilters = %d, want %d", got, tc.want)
			}
		})
	}
}

// TestSubscribeResponse_MarshalTLV verifies the SubscribeResponse wire shape.
// Mirrors matter.js packages/types/src/protocol/types/TlvSubscribeResponse.ts.
func TestSubscribeResponse_MarshalTLV(t *testing.T) {
	t.Parallel()
	sr := SubscribeResponse{
		SubscriptionID: 0x12345678,
		MaxInterval:    30,
	}
	enc := tlv.NewEncoder()
	sr.MarshalTLV(enc)
	wire, err := enc.Bytes()
	if err != nil {
		t.Fatalf("Bytes: %v", err)
	}

	dec := tlv.NewDecoder(wire)
	el, err := dec.Next()
	if err != nil || !el.IsContainer || el.Type != tlv.TypeStructure {
		t.Fatalf("expected struct: err=%v el=%+v", err, el)
	}
}
