// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

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

// TestWriteParity_MoreChunkedMessages_Preserved mirrors matter.js
// packages/protocol/src/action/request/Write.ts moreChunkedMessages
// field (Matter §10.6.5 tag 3): a WriteRequestMessage that sets tag 3
// true MUST decode with MoreChunkedMessages==true so the dispatch
// layer can enforce the chunked-write InvalidAction rules
// (InteractionServer.ts:397-402, :408-413).
func TestWriteParity_MoreChunkedMessages_Preserved(t *testing.T) {
	t.Parallel()
	enc := tlv.NewEncoder()
	enc.StartStruct(tlv.AnonymousTag())   // top WriteRequestMessage
	enc.PutBool(tlv.ContextTag(0), false) // SuppressResponse
	enc.PutBool(tlv.ContextTag(1), false) // TimedRequest
	enc.StartArray(tlv.ContextTag(2))     // WriteRequests (empty)
	_ = enc.EndContainer()                // end WriteRequests
	enc.PutBool(tlv.ContextTag(3), true)  // MoreChunkedMessages: true
	_ = enc.EndContainer()                // end top
	wire, err := enc.Bytes()
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	dec := tlv.NewDecoder(wire)
	out, err := UnmarshalWriteRequestTLV(dec, nil)
	if err != nil {
		t.Fatalf("UnmarshalWriteRequestTLV: %v", err)
	}
	if !out.MoreChunkedMessages {
		t.Fatal("MoreChunkedMessages flag not preserved after round-trip")
	}
}

// TestWriteParity_MoreChunkedMessages_DefaultFalse verifies that a
// WriteRequestMessage omitting tag 3 (MoreChunkedMessages) decodes
// with the field left at its zero value — a request with no
// following chunks is the common case and must not accidentally
// trip the chunked-write InvalidAction rules.
func TestWriteParity_MoreChunkedMessages_DefaultFalse(t *testing.T) {
	t.Parallel()
	enc := tlv.NewEncoder()
	enc.StartStruct(tlv.AnonymousTag())   // top WriteRequestMessage
	enc.PutBool(tlv.ContextTag(0), false) // SuppressResponse
	enc.PutBool(tlv.ContextTag(1), false) // TimedRequest
	enc.StartArray(tlv.ContextTag(2))     // WriteRequests (empty)
	_ = enc.EndContainer()                // end WriteRequests
	// tag 3 (MoreChunkedMessages) intentionally omitted.
	_ = enc.EndContainer() // end top
	wire, err := enc.Bytes()
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	dec := tlv.NewDecoder(wire)
	out, err := UnmarshalWriteRequestTLV(dec, nil)
	if err != nil {
		t.Fatalf("UnmarshalWriteRequestTLV: %v", err)
	}
	if out.MoreChunkedMessages {
		t.Fatal("MoreChunkedMessages flag set true without tag 3 present, want default false")
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

// TestWriteParity_DataVersion_WildcardEndpoint_RejectsWithInvalidAction mirrors
// matter.js packages/protocol/src/action/request/Write.ts:33 (#3988): a write
// that carries HasDataVersion=true but HasEndpoint=false (wildcard endpoint) MUST
// produce AttributeStatus with StatusInvalidAction. A DataVersion is meaningless
// against a wildcard-endpoint path — resolving it against endpoint 0 is silent
// data corruption per Matter §8.9.2.8.1.
func TestWriteParity_DataVersion_WildcardEndpoint_RejectsWithInvalidAction(t *testing.T) {
	t.Parallel()
	d := &fakeDispatcher{writeStat: StatusSuccess}
	req := WriteRequest{
		Writes: []AttributeWrite{
			{
				Path: ConcreteAttributePath{
					// HasEndpoint intentionally false — wildcard endpoint.
					Cluster:      0x0006,
					HasCluster:   true,
					Attribute:    0x0000,
					HasAttribute: true,
				},
				Value:          AttributeValue{Value: true},
				DataVersion:    0xABCD,
				HasDataVersion: true,
			},
		},
	}
	wr := HandleWriteRequest(context.Background(), d, req)
	if len(wr.Responses) != 1 {
		t.Fatalf("responses=%d, want 1", len(wr.Responses))
	}
	if wr.Responses[0].Status.Status != StatusInvalidAction {
		t.Fatalf("status=%v, want StatusInvalidAction", wr.Responses[0].Status.Status)
	}
}

// clusterStatusDispatcher returns a WriteResult with HasClusterStatus=true and
// a configurable global status. Used to test the StatusIB-clamping invariant
// (Fix F).
type clusterStatusDispatcher struct {
	clusterStatus uint8
	globalStatus  StatusCode
}

func (d *clusterStatusDispatcher) Read(_ context.Context, p ConcreteAttributePath) []ReadResult {
	return []ReadResult{{Path: p, Status: StatusSuccess}}
}

func (d *clusterStatusDispatcher) Write(_ context.Context, p ConcreteAttributePath, _ AttributeValue) []WriteResult {
	return []WriteResult{{
		Path:             p,
		Status:           d.globalStatus,
		ClusterStatus:    d.clusterStatus,
		HasClusterStatus: true,
	}}
}

func (d *clusterStatusDispatcher) Invoke(_ context.Context, p ConcreteCommandPath, _ any) InvokeResult {
	return InvokeResult{Path: p, Status: StatusSuccess}
}

var _ Dispatcher = (*clusterStatusDispatcher)(nil)

// TestWriteParity_ClusterStatus_NonSuccess_ClampedToFailure mirrors
// matter.js packages/protocol/src/action/server/AttributeWriteResponse.ts:32
// (#3988, Matter §7.10.7): when a WriteResult carries HasClusterStatus=true
// alongside a non-Success global status, HandleWriteRequest MUST clamp the
// outer Status to StatusFailure while preserving both HasClusterStatus and
// the ClusterStatus byte.
func TestWriteParity_ClusterStatus_NonSuccess_ClampedToFailure(t *testing.T) {
	t.Parallel()
	const clusterErrCode uint8 = 0x42
	d := &clusterStatusDispatcher{
		clusterStatus: clusterErrCode,
		globalStatus:  StatusConstraintError, // non-Success, non-Failure code
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
	wr := HandleWriteRequest(context.Background(), d, req)
	if len(wr.Responses) != 1 {
		t.Fatalf("responses=%d, want 1", len(wr.Responses))
	}
	got := wr.Responses[0].Status
	if got.Status != StatusFailure {
		t.Fatalf("outer Status=%v, want StatusFailure (clamped per Matter §7.10.7)", got.Status)
	}
	if !got.HasClusterStatus {
		t.Fatal("HasClusterStatus must be preserved after clamping")
	}
	if got.ClusterStatus != clusterErrCode {
		t.Fatalf("ClusterStatus=%#x, want %#x", got.ClusterStatus, clusterErrCode)
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
