// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package im

import (
	"testing"

	"github.com/SukramJ/openccu-loom/internal/north/matter/tlv"
)

// buildReadRequestWithEventFilters builds a minimal ReadRequest TLV that
// contains only an EventFilters array at context tag 2. Each filter is
// encoded as a struct (anon tag inside array) with tag 0=NodeID and tag
// 1=EventMin, matching matter.js
// packages/types/src/protocol/types/TlvEventFilter.ts:16-17.
func buildReadRequestWithEventFilters(t *testing.T, filters []EventMinimumNumber) []byte {
	t.Helper()
	enc := tlv.NewEncoder()
	enc.StartStruct(tlv.AnonymousTag())
	enc.StartArray(tlv.ContextTag(tagReadReqEventFilters))
	for _, f := range filters {
		enc.StartStruct(tlv.AnonymousTag()) // struct inside array → anonymous
		if f.HasNodeID {
			enc.PutUint(tlv.ContextTag(0), f.NodeID)
		}
		enc.PutUint(tlv.ContextTag(1), f.EventMin)
		if err := enc.EndContainer(); err != nil {
			t.Fatalf("EndContainer filter struct: %v", err)
		}
	}
	if err := enc.EndContainer(); err != nil {
		t.Fatalf("EndContainer filters array: %v", err)
	}
	if err := enc.EndContainer(); err != nil {
		t.Fatalf("EndContainer ReadRequest: %v", err)
	}
	wire, err := enc.Bytes()
	if err != nil {
		t.Fatalf("buildReadRequestWithEventFilters Bytes: %v", err)
	}
	return wire
}

// TestReadEventFilterArray_SingleFilter exercises readEventFilterArray via
// UnmarshalReadRequestTLV's EventFilters path with a single filter that
// carries a NodeID.
//
// Mirrors chip src/app/ReadHandler.cpp ProcessEventFilters.
func TestReadEventFilterArray_SingleFilter(t *testing.T) {
	t.Parallel()
	want := EventMinimumNumber{
		NodeID:    0xDEAD,
		HasNodeID: true,
		EventMin:  42,
	}

	wire := buildReadRequestWithEventFilters(t, []EventMinimumNumber{want})
	dec := tlv.NewDecoder(wire)
	req, err := UnmarshalReadRequestTLV(dec)
	if err != nil {
		t.Fatalf("UnmarshalReadRequestTLV: %v", err)
	}
	if len(req.EventFilters) != 1 {
		t.Fatalf("EventFilters: got %d, want 1", len(req.EventFilters))
	}
	got := req.EventFilters[0]
	if got != want {
		t.Errorf("EventFilter: got %+v, want %+v", got, want)
	}
}

// TestReadEventFilterArray_NoNodeID exercises the filter path when
// NodeID is absent.
func TestReadEventFilterArray_NoNodeID(t *testing.T) {
	t.Parallel()
	want := EventMinimumNumber{
		HasNodeID: false,
		EventMin:  7,
	}

	wire := buildReadRequestWithEventFilters(t, []EventMinimumNumber{want})
	dec := tlv.NewDecoder(wire)
	req, err := UnmarshalReadRequestTLV(dec)
	if err != nil {
		t.Fatalf("UnmarshalReadRequestTLV: %v", err)
	}
	if len(req.EventFilters) != 1 {
		t.Fatalf("EventFilters: got %d, want 1", len(req.EventFilters))
	}
	got := req.EventFilters[0]
	if got != want {
		t.Errorf("EventFilter: got %+v, want %+v", got, want)
	}
}

// TestReadEventFilterArray_MultipleFilters verifies that multiple
// filter entries are all decoded.
func TestReadEventFilterArray_MultipleFilters(t *testing.T) {
	t.Parallel()
	filters := []EventMinimumNumber{
		{EventMin: 1},
		{EventMin: 2},
		{EventMin: 3},
	}
	wire := buildReadRequestWithEventFilters(t, filters)
	dec := tlv.NewDecoder(wire)
	req, err := UnmarshalReadRequestTLV(dec)
	if err != nil {
		t.Fatalf("UnmarshalReadRequestTLV: %v", err)
	}
	if len(req.EventFilters) != 3 {
		t.Fatalf("EventFilters: got %d, want 3", len(req.EventFilters))
	}
}

// eventFilterWireFixture is a ReadRequestMessage carrying a single
// EventFilterIB with eventMin=40, as a peer encodes it. Written out by
// hand so the tag numbers are pinned independently of our own encoder —
// an encoder/decoder pair that shifts both halves together would keep a
// round-trip test green while every real controller's filter is misread.
//
//	15          struct, anonymous            — ReadRequestMessage
//	36 02       array, context tag 2         — EventFilters
//	15          struct, anonymous            — EventFilterIB
//	24 01 28    uint8, context tag 1, 40     — eventMin
//	18 18 18    end of struct/array/struct
//
// Tag layout per matter.js
// packages/types/src/protocol/types/TlvEventFilter.ts:16-17
// (`nodeId: TlvOptionalField(0, …)`, `eventMin: TlvField(1, …)`).
var eventFilterWireFixture = []byte{0x15, 0x36, 0x02, 0x15, 0x24, 0x01, 0x28, 0x18, 0x18, 0x18}

// TestReadEventFilterArray_DecodesPeerWireLayout decodes the literal
// fixture above. Before the tag numbers were corrected, EventMin (real
// context tag 1) landed in NodeID and EventMin stayed 0, so every
// filtered read replayed the whole event buffer.
func TestReadEventFilterArray_DecodesPeerWireLayout(t *testing.T) {
	t.Parallel()
	req, err := UnmarshalReadRequestTLV(tlv.NewDecoder(eventFilterWireFixture))
	if err != nil {
		t.Fatalf("UnmarshalReadRequestTLV: %v", err)
	}
	if len(req.EventFilters) != 1 {
		t.Fatalf("EventFilters: got %d, want 1", len(req.EventFilters))
	}
	got := req.EventFilters[0]
	if got.EventMin != 40 {
		t.Errorf("EventMin = %d, want 40 (context tag 1)", got.EventMin)
	}
	if got.HasNodeID {
		t.Errorf("NodeID = %d (HasNodeID=true), want absent — the fixture carries no context tag 0", got.NodeID)
	}
}

// TestHandleReadEventRequest_AppliesEventMinFromWire crosses the whole
// seam the tag numbers sit on: a peer-encoded EventFilterIB with
// eventMin=40 must make the read return only records with Number >= 40,
// rather than replaying every buffered event on each reconnect.
func TestHandleReadEventRequest_AppliesEventMinFromWire(t *testing.T) {
	t.Parallel()
	req, err := UnmarshalReadRequestTLV(tlv.NewDecoder(eventFilterWireFixture))
	if err != nil {
		t.Fatalf("UnmarshalReadRequestTLV: %v", err)
	}
	// A wildcard event path — the filter, not the path, does the gating.
	req.EventRequests = []ConcreteEventPath{{}}

	log := NewEventLog()
	log.SeedNumber(38)
	for range 4 { // numbers 39, 40, 41, 42
		log.Append(EventRecord{Priority: EventPriorityInfo, Endpoint: 2, Cluster: 0x0045, EventID: 0x00})
	}

	reports := HandleReadEventRequest(req, log)
	if len(reports) != 3 {
		t.Fatalf("reports: got %d, want 3 (numbers 40..42)", len(reports))
	}
	for _, r := range reports {
		if r.Number < 40 {
			t.Errorf("report Number %d is below the requested EventMin 40; the controller already holds it", r.Number)
		}
	}
}

// TestConcreteEventPath_WildcardHelpers covers the IsWildcard* methods.
func TestConcreteEventPath_WildcardHelpers(t *testing.T) {
	t.Parallel()

	wildcard := ConcreteEventPath{} // all Has* = false
	if !wildcard.IsWildcardEndpoint() {
		t.Error("IsWildcardEndpoint should be true when HasEndpoint=false")
	}
	if !wildcard.IsWildcardCluster() {
		t.Error("IsWildcardCluster should be true when HasCluster=false")
	}
	if !wildcard.IsWildcardEvent() {
		t.Error("IsWildcardEvent should be true when HasEvent=false")
	}

	concrete := ConcreteEventPath{
		Endpoint: 1, HasEndpoint: true,
		Cluster: 0x003B, HasCluster: true,
		Event: 0x01, HasEvent: true,
	}
	if concrete.IsWildcardEndpoint() {
		t.Error("IsWildcardEndpoint should be false when HasEndpoint=true")
	}
	if concrete.IsWildcardCluster() {
		t.Error("IsWildcardCluster should be false when HasCluster=true")
	}
	if concrete.IsWildcardEvent() {
		t.Error("IsWildcardEvent should be false when HasEvent=true")
	}
}
