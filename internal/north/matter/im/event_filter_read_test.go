// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package im

import (
	"testing"

	"github.com/SukramJ/openccu-loom/internal/north/matter/tlv"
)

// buildReadRequestWithEventFilters builds a minimal ReadRequest TLV that
// contains only an EventFilters array at context tag 2. Each filter is
// encoded as a struct (anon tag inside array) with tag 1=NodeID and tag
// 2=EventMin per Matter §10.6.4 EventFilterIB.
func buildReadRequestWithEventFilters(t *testing.T, filters []EventMinimumNumber) []byte {
	t.Helper()
	enc := tlv.NewEncoder()
	enc.StartStruct(tlv.AnonymousTag())
	enc.StartArray(tlv.ContextTag(tagReadReqEventFilters))
	for _, f := range filters {
		enc.StartStruct(tlv.AnonymousTag()) // struct inside array → anonymous
		if f.HasNodeID {
			enc.PutUint(tlv.ContextTag(1), f.NodeID)
		}
		enc.PutUint(tlv.ContextTag(2), f.EventMin)
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
