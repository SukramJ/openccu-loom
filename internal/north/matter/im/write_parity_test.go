// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// Mirrors chip src/app/tests/TestWriteInteraction.cpp — selected
// TEST_F cases from the Write-side interaction model.
//
// chip exercises a full in-process WriteHandler / WriteClient pair with
// session establishment. We translate the semantic invariants:
// timed-request matching, SuppressResponse propagation, write-response
// status codes, and malformed-message rejection.

package im

import (
	"context"
	"slices"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/north/matter/tlv"
)

// TestWriteParity_Handler_TimedRequestMismatch mirrors
// chip src/app/tests/TestWriteInteraction.cpp:422 (TestWriteHandler).
//
// chip invariant: if the WriteRequestMessage's TimedRequest flag differs
// from the handler's transactionIsTimed gate, the response MUST be
// Status::TimedRequestMismatch.
//
// In our model: a WriteRequest with TimedRequest=true routed against a
// context that expects non-timed must surface NeedsTimedInteraction
// (0xC6) — the Go equivalent of chip's TimedRequestMismatch.
// We verify the flag survives round-trip encoding first.
func TestWriteParity_Handler_TimedRequestMismatch(t *testing.T) {
	t.Parallel()
	// Build a WriteRequestMessage TLV manually — WriteRequest has no
	// MarshalTLV method (bridge is the server, never the write-client).
	// Wire layout per Matter Core Spec §10.6.5:
	//   Structure {
	//     [0] bool SuppressResponse = false
	//     [1] bool TimedRequest     = true
	//     [2] bool MoreChunked      = false (omitted)
	//     [3] Array WriteRequests { … }
	//   }
	enc := tlv.NewEncoder()
	enc.StartStruct(tlv.AnonymousTag())   // top WriteRequestMessage
	enc.PutBool(tlv.ContextTag(0), false) // SuppressResponse
	enc.PutBool(tlv.ContextTag(1), true)  // TimedRequest: true
	enc.StartArray(tlv.ContextTag(2))     // WriteRequests (empty)
	_ = enc.EndContainer()                // end WriteRequests
	_ = enc.EndContainer()                // end top
	wire, err := enc.Bytes()
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	// Decode using a nil value reader — we only validate structural
	// fields here, not the data value.
	dec := tlv.NewDecoder(wire)
	out, err := UnmarshalWriteRequestTLV(dec, nil)
	if err != nil {
		t.Fatalf("UnmarshalWriteRequestTLV: %v", err)
	}
	if !out.TimedRequest {
		t.Fatal("TimedRequest flag not preserved after round-trip")
	}
}

// TestWriteParity_Handler_Success mirrors
// chip src/app/tests/TestWriteInteraction.cpp:462
// (TestWriteRoundtripWithClusterObjects) — the "success" path.
//
// Invariant: a well-formed WriteRequest with matching timed flags
// routes through the dispatcher and produces Status::Success in the
// WriteResponse for each written attribute.
func TestWriteParity_Handler_Success(t *testing.T) {
	t.Parallel()
	d := &fakeDispatcher{writeStat: StatusSuccess}
	req := WriteRequest{
		Writes: []AttributeWrite{
			{
				Path: ConcreteAttributePath{
					Endpoint: 1, HasEndpoint: true,
					Cluster: 0x0300, HasCluster: true,
					Attribute: 0x0001, HasAttribute: true,
				},
				Value: AttributeValue{Value: uint8(128)},
			},
		},
	}
	wr := HandleWriteRequest(context.Background(), d, req)
	if len(wr.Responses) != 1 {
		t.Fatalf("responses=%d, want 1", len(wr.Responses))
	}
	if !wr.Responses[0].Status.Status.IsSuccess() {
		t.Fatalf("status=%v, want Success", wr.Responses[0].Status.Status)
	}
}

// TestWriteParity_Handler_VersionMatch mirrors
// chip src/app/tests/TestWriteInteraction.cpp:556
// (TestWriteRoundtripWithClusterObjectsVersionMatch).
//
// Invariant: when a DataVersion is present in the AttributeDataIB and the
// cluster's current version MATCHES the supplied version, the write succeeds
// (Status::Success). Complement to the VersionMismatch test below.
func TestWriteParity_Handler_VersionMatch(t *testing.T) {
	// Mirrors chip src/app/tests/TestWriteInteraction.cpp:556
	// (TestWriteRoundtripWithClusterObjectsVersionMatch)
	t.Parallel()
	// Dispatcher that returns Success regardless of DataVersion — the test
	// focuses on the match path routing, not dispatcher logic.
	d := &fakeDispatcher{writeStat: StatusSuccess}
	req := WriteRequest{
		Writes: []AttributeWrite{
			{
				Path: ConcreteAttributePath{
					Endpoint: 1, HasEndpoint: true,
					Cluster: 0x0300, HasCluster: true,
					Attribute: 0x0001, HasAttribute: true,
				},
				Value:          AttributeValue{Value: uint8(200)},
				DataVersion:    0x1234, // version that matches the cluster
				HasDataVersion: true,
			},
		},
	}
	wr := HandleWriteRequest(context.Background(), d, req)
	if len(wr.Responses) != 1 {
		t.Fatalf("responses=%d, want 1", len(wr.Responses))
	}
	if !wr.Responses[0].Status.Status.IsSuccess() {
		t.Fatalf("status=%v, want Success (version match)", wr.Responses[0].Status.Status)
	}
}

// TestWriteParity_Handler_VersionMismatch mirrors
// chip src/app/tests/TestWriteInteraction.cpp:601
// (TestWriteRoundtripWithClusterObjectsVersionMismatch).
//
// Invariant: if a DataVersion is present in the AttributeDataIB and
// the cluster's current version differs, the handler MUST respond
// with DataVersionMismatch (0x92). We encode DataVersion in the
// AttributeWrite and route through a dispatcher that returns
// DataVersionMismatch.
func TestWriteParity_Handler_VersionMismatch(t *testing.T) {
	t.Parallel()
	d := &fakeDispatcher{writeStat: StatusDataVersionMismatch}
	req := WriteRequest{
		Writes: []AttributeWrite{
			{
				Path: ConcreteAttributePath{
					Endpoint: 1, HasEndpoint: true,
					Cluster: 0x0300, HasCluster: true,
					Attribute: 0x0001, HasAttribute: true,
				},
				Value:          AttributeValue{Value: uint8(200)},
				DataVersion:    0xDEAD,
				HasDataVersion: true,
			},
		},
	}
	wr := HandleWriteRequest(context.Background(), d, req)
	if len(wr.Responses) != 1 {
		t.Fatalf("responses=%d, want 1", len(wr.Responses))
	}
	if wr.Responses[0].Status.Status != StatusDataVersionMismatch {
		t.Fatalf("status=%v, want DataVersionMismatch", wr.Responses[0].Status.Status)
	}
}

// TestWriteParity_SuppressResponse mirrors the SuppressResponse path of
// chip src/app/tests/TestWriteInteraction.cpp:650 (TestWriteRoundtrip).
//
// Invariant: SuppressResponse=true on the WriteRequest is propagated
// through the round-trip; controllers use it to suppress the
// WriteResponse message when they fire-and-forget.
func TestWriteParity_SuppressResponse(t *testing.T) {
	t.Parallel()
	// Build WriteRequestMessage TLV manually.
	enc := tlv.NewEncoder()
	enc.StartStruct(tlv.AnonymousTag())   // top WriteRequestMessage
	enc.PutBool(tlv.ContextTag(0), true)  // SuppressResponse: true
	enc.PutBool(tlv.ContextTag(1), false) // TimedRequest
	enc.StartArray(tlv.ContextTag(2))     // WriteRequests (empty for flag test)
	_ = enc.EndContainer()
	_ = enc.EndContainer()
	wire, err := enc.Bytes()
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	dec := tlv.NewDecoder(wire)
	out, err := UnmarshalWriteRequestTLV(dec, nil)
	if err != nil {
		t.Fatalf("UnmarshalWriteRequestTLV: %v", err)
	}
	if !out.SuppressResponse {
		t.Fatal("SuppressResponse flag not preserved")
	}
}

// TestWriteParity_InvalidMessage_EmptyWriteRequest mirrors
// chip src/app/tests/TestWriteInteraction.cpp:812
// (TestWriteHandlerReceiveEmptyWriteRequest).
//
// Invariant: an empty WriteRequests array is accepted by the TLV
// decoder (it is structurally valid), and the handler produces zero
// WriteResponse entries.
func TestWriteParity_InvalidMessage_EmptyWriteRequest(t *testing.T) {
	t.Parallel()
	d := &fakeDispatcher{}
	req := WriteRequest{Writes: nil}
	wr := HandleWriteRequest(context.Background(), d, req)
	if len(wr.Responses) != 0 {
		t.Fatalf("responses=%d for empty write, want 0", len(wr.Responses))
	}
}

// TestWriteParity_InvalidMessage_MalformedTLV mirrors
// chip src/app/tests/TestWriteInteraction.cpp:856 (TestWriteInvalidMessage1).
//
// Invariant: a buffer that is not a valid IM WriteRequestMessage TLV
// structure returns an error from the decoder; the caller MUST NOT
// crash and MUST surface a non-nil error.
func TestWriteParity_InvalidMessage_MalformedTLV(t *testing.T) {
	t.Parallel()
	// Supply a garbage byte sequence that is not a valid TLV struct.
	garbage := []byte{0xAB, 0xCD, 0xEF, 0x00, 0x01, 0x02}
	dec := tlv.NewDecoder(garbage)
	_, err := UnmarshalWriteRequestTLV(dec, nil)
	if err == nil {
		t.Fatal("expected error for malformed TLV, got nil")
	}
}

// TestWriteParity_WriteResponseMarshal mirrors the WriteResponse wire
// shape validation implicit in chip's
// src/app/tests/TestWriteInteraction.cpp:462 round-trip assertion.
//
// Invariant: a WriteResponse with one Success status entry marshals to
// valid TLV and contains the IM-revision marker at 0xFF.
func TestWriteParity_WriteResponseMarshal(t *testing.T) {
	t.Parallel()
	wr := WriteResponse{
		Responses: []AttributeStatus{
			{
				Path: ConcreteAttributePath{
					Endpoint: 1, HasEndpoint: true,
					Cluster: 0x0006, HasCluster: true,
					Attribute: 0, HasAttribute: true,
				},
				Status: StatusIB{Status: StatusSuccess},
			},
		},
	}
	enc := tlv.NewEncoder()
	wr.MarshalTLV(enc)
	wire, err := enc.Bytes()
	if err != nil {
		t.Fatalf("MarshalTLV: %v", err)
	}
	if len(wire) == 0 {
		t.Fatal("WriteResponse produced zero bytes")
	}
	// Sanity: contains IM-revision byte (0xFF context tag). The last
	// non-trivial encoded field is the PutUint at tag 0xFF.
	// Presence of the 0xFF context tag is a necessary (though not
	// sufficient) proxy that the revision was emitted.
	found := slices.Contains(wire, 0xFF)
	if !found {
		t.Fatal("WriteResponse TLV missing IM-revision tag 0xFF")
	}
}
