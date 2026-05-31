// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package im

import (
	"testing"

	"github.com/SukramJ/openccu-loom/internal/north/matter/tlv"
)

// ---- ConcreteEventPath.Matches ----

func TestConcreteEventPath_Matches(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name      string
		sub       ConcreteEventPath // the subscriber's path (may have wildcards)
		fired     ConcreteEventPath // the concrete event that fired
		wantMatch bool
	}{
		{
			name: "wildcard_endpoint_matches_concrete",
			sub: ConcreteEventPath{
				Cluster: 0x003B, HasCluster: true,
				Event: 0x01, HasEvent: true,
				// HasEndpoint intentionally false → wildcard
			},
			fired: ConcreteEventPath{
				Endpoint: 5, HasEndpoint: true,
				Cluster: 0x003B, HasCluster: true,
				Event: 0x01, HasEvent: true,
			},
			wantMatch: true,
		},
		{
			name: "wildcard_cluster_matches_concrete",
			sub: ConcreteEventPath{
				Endpoint: 1, HasEndpoint: true,
				Event: 0x01, HasEvent: true,
				// HasCluster intentionally false → wildcard
			},
			fired: ConcreteEventPath{
				Endpoint: 1, HasEndpoint: true,
				Cluster: 0x003B, HasCluster: true,
				Event: 0x01, HasEvent: true,
			},
			wantMatch: true,
		},
		{
			name: "exact_match",
			sub: ConcreteEventPath{
				Endpoint: 2, HasEndpoint: true,
				Cluster: 0x003B, HasCluster: true,
				Event: 0x02, HasEvent: true,
			},
			fired: ConcreteEventPath{
				Endpoint: 2, HasEndpoint: true,
				Cluster: 0x003B, HasCluster: true,
				Event: 0x02, HasEvent: true,
			},
			wantMatch: true,
		},
		{
			name: "mismatch_endpoint_returns_false",
			sub: ConcreteEventPath{
				Endpoint: 1, HasEndpoint: true,
				Cluster: 0x003B, HasCluster: true,
				Event: 0x01, HasEvent: true,
			},
			fired: ConcreteEventPath{
				Endpoint: 9, HasEndpoint: true,
				Cluster: 0x003B, HasCluster: true,
				Event: 0x01, HasEvent: true,
			},
			wantMatch: false,
		},
		{
			name: "mismatch_cluster_returns_false",
			sub: ConcreteEventPath{
				Endpoint: 1, HasEndpoint: true,
				Cluster: 0x003B, HasCluster: true,
				Event: 0x01, HasEvent: true,
			},
			fired: ConcreteEventPath{
				Endpoint: 1, HasEndpoint: true,
				Cluster: 0x0006, HasCluster: true,
				Event: 0x01, HasEvent: true,
			},
			wantMatch: false,
		},
		{
			name: "mismatch_event_returns_false",
			sub: ConcreteEventPath{
				Endpoint: 1, HasEndpoint: true,
				Cluster: 0x003B, HasCluster: true,
				Event: 0x01, HasEvent: true,
			},
			fired: ConcreteEventPath{
				Endpoint: 1, HasEndpoint: true,
				Cluster: 0x003B, HasCluster: true,
				Event: 0x03, HasEvent: true,
			},
			wantMatch: false,
		},
		{
			name: "full_wildcard_matches_anything",
			sub:  ConcreteEventPath{}, // no Has* set
			fired: ConcreteEventPath{
				Endpoint: 99, HasEndpoint: true,
				Cluster: 0xABCD, HasCluster: true,
				Event: 0xFF, HasEvent: true,
			},
			wantMatch: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := tc.sub.Matches(tc.fired)
			if got != tc.wantMatch {
				t.Errorf("Matches() = %v, want %v (sub=%+v fired=%+v)", got, tc.wantMatch, tc.sub, tc.fired)
			}
		})
	}
}

// ---- ConcreteEventPath.MarshalTLV roundtrip ----

// encodePathArray wraps one or more ConcreteEventPath into an Array,
// returning the wire bytes. The array is opened anonymously so that
// readEventPathArray (which expects the array already opened) can
// consume its children.
func encodePathArray(t *testing.T, paths []ConcreteEventPath) []byte {
	t.Helper()
	enc := tlv.NewEncoder()
	enc.StartArray(tlv.AnonymousTag())
	for _, p := range paths {
		p.MarshalTLV(enc, tlv.AnonymousTag())
	}
	if err := enc.EndContainer(); err != nil {
		t.Fatalf("encodePathArray EndContainer: %v", err)
	}
	wire, err := enc.Bytes()
	if err != nil {
		t.Fatalf("encodePathArray Bytes: %v", err)
	}
	return wire
}

func TestConcreteEventPath_MarshalTLV_Roundtrip(t *testing.T) {
	t.Parallel()
	cases := []ConcreteEventPath{
		{
			Endpoint: 3, HasEndpoint: true,
			Cluster: 0x003B, HasCluster: true,
			Event: 0x01, HasEvent: true,
			IsUrgent: true,
		},
		{
			// Wildcard endpoint + cluster → only event is concrete
			Event: 0x02, HasEvent: true,
		},
		{
			// Node set
			Node: 0xDEAD, HasNode: true,
			Endpoint: 1, HasEndpoint: true,
			Cluster: 0x0006, HasCluster: true,
			Event: 0x00, HasEvent: true,
		},
	}

	for _, want := range cases {
		wire := encodePathArray(t, []ConcreteEventPath{want})
		dec := tlv.NewDecoder(wire)

		// Consume the array opener.
		el, err := dec.Next()
		if err != nil || !el.IsContainer {
			t.Fatalf("expected array container, err=%v el=%+v", err, el)
		}

		// Now readEventPathArray reads until EndContainer.
		got, err := readEventPathArray(dec)
		if err != nil {
			t.Fatalf("readEventPathArray: %v", err)
		}
		if len(got) != 1 {
			t.Fatalf("expected 1 path, got %d", len(got))
		}
		if got[0] != want {
			t.Errorf("roundtrip mismatch:\n  got  %+v\n  want %+v", got[0], want)
		}
	}
}

// ---- EventReport.marshal (Status-IB) ----

func TestEventReport_Marshal_StatusIB(t *testing.T) {
	t.Parallel()
	rep := EventReport{
		Path: ConcreteEventPath{
			Endpoint: 1, HasEndpoint: true,
			Cluster: 0x003B, HasCluster: true,
			Event: 0x01, HasEvent: true,
		},
		Status:   StatusIB{Status: StatusFailure},
		IsStatus: true,
	}

	enc := tlv.NewEncoder()
	enc.StartArray(tlv.AnonymousTag())
	rep.marshal(enc, func(_ *tlv.Encoder, _ tlv.Tag, _ AttributeValue) {})
	if err := enc.EndContainer(); err != nil {
		t.Fatalf("EndContainer: %v", err)
	}
	wire, err := enc.Bytes()
	if err != nil {
		t.Fatalf("Bytes: %v", err)
	}

	dec := tlv.NewDecoder(wire)
	// consume array opener
	if _, err := dec.Next(); err != nil {
		t.Fatalf("Next (array): %v", err)
	}
	// consume struct opener (EventReportIB)
	el, err := dec.Next()
	if err != nil {
		t.Fatalf("Next (EventReportIB struct): %v", err)
	}
	if !el.IsContainer || el.Type != tlv.TypeStructure {
		t.Fatalf("expected struct, got type=0x%02X", el.Type)
	}
	// The first field inside the EventReportIB must be tagEventReportStatus (0x00)
	inner, err := dec.Next()
	if err != nil {
		t.Fatalf("Next (inner): %v", err)
	}
	if inner.Tag.Kind != tlv.TagKindContext || inner.Tag.Number != uint32(tagEventReportStatus) {
		t.Errorf("expected context tag %d (EventStatusIB), got %+v", tagEventReportStatus, inner.Tag)
	}
}

// ---- EventReport.marshal (Data-IB) ----

func TestEventReport_Marshal_DataIB(t *testing.T) {
	t.Parallel()
	rep := EventReport{
		Path: ConcreteEventPath{
			Endpoint: 2, HasEndpoint: true,
			Cluster: 0x003B, HasCluster: true,
			Event: 0x01, HasEvent: true,
		},
		Number:    42,
		Priority:  EventPriorityCritical,
		Timestamp: 1000,
		Data:      AttributeValue{Value: uint8(1)},
		IsStatus:  false,
	}

	enc := tlv.NewEncoder()
	enc.StartArray(tlv.AnonymousTag())
	rep.marshal(enc, func(e *tlv.Encoder, tag tlv.Tag, _ AttributeValue) {
		e.PutUint(tag, 1)
	})
	if err := enc.EndContainer(); err != nil {
		t.Fatalf("EndContainer: %v", err)
	}
	wire, err := enc.Bytes()
	if err != nil {
		t.Fatalf("Bytes: %v", err)
	}

	dec := tlv.NewDecoder(wire)
	// consume array opener
	if _, err := dec.Next(); err != nil {
		t.Fatalf("Next (array): %v", err)
	}
	// consume struct opener (EventReportIB)
	el, err := dec.Next()
	if err != nil {
		t.Fatalf("Next (EventReportIB struct): %v", err)
	}
	if !el.IsContainer || el.Type != tlv.TypeStructure {
		t.Fatalf("expected struct, got type=0x%02X", el.Type)
	}
	// The first field inside the EventReportIB must be tagEventReportData (0x01)
	inner, err := dec.Next()
	if err != nil {
		t.Fatalf("Next (inner): %v", err)
	}
	if inner.Tag.Kind != tlv.TagKindContext || inner.Tag.Number != uint32(tagEventReportData) {
		t.Errorf("expected context tag %d (EventDataIB), got %+v", tagEventReportData, inner.Tag)
	}
}

// TestEventReport_Marshal_DataIB_MatterJsParity locks the wire-byte
// layout of EventDataIB against matter.js's
// `packages/types/src/protocol/types/TlvEventData.ts` so we cannot
// silently revert to the previous incorrect layout (Data at context tag 5)
// that broke chip-tool with `CHIP Error 0x26: Wrong TLV type`.
//
// The pinned mapping per matter.js (Matter Core Spec §10.6.9, table 80):
//
//	tag 0  Path                       (List)
//	tag 1  EventNumber                (uint64)
//	tag 2  Priority                   (enum8 ≡ uint8)
//	tag 3  EpochTimestamp             (uint64, optional)
//	tag 4  SystemTimestamp            (uint64, optional)
//	tag 5  DeltaEpochTimestamp        (uint64, optional)
//	tag 6  DeltaSystemTimestamp       (uint64, optional)
//	tag 7  Data                       (TlvAny, optional)
//
// The test feeds an EventReport with a non-null payload and walks the
// inner EventDataIB; every tag we encounter MUST be in the spec set.
// Specifically:
//   - tag 7 carries the data — never 5 (DeltaEpoch).
//   - EventNumber and EpochTimestamp use the 8-byte unsigned-int wire
//     type (0x07) regardless of magnitude — chip-tool's strict-width
//     IM-decoder otherwise rejects the IB.
func TestEventReport_Marshal_DataIB_MatterJsParity(t *testing.T) {
	t.Parallel()
	rep := EventReport{
		Path: ConcreteEventPath{
			Endpoint: 0, HasEndpoint: true,
			Cluster: 0x0028, HasCluster: true,
			Event: 0x0000, HasEvent: true,
		},
		Number:    1,
		Priority:  EventPriorityCritical,
		Timestamp: 1_000_000_000_000, // arbitrary 64-bit value
		Data:      AttributeValue{Value: uint32(5)},
		IsStatus:  false,
	}

	enc := tlv.NewEncoder()
	enc.StartArray(tlv.AnonymousTag())
	rep.marshal(enc, func(e *tlv.Encoder, tag tlv.Tag, v AttributeValue) {
		// Emulate the bridge's default writer for a uint32 payload.
		e.PutUint32(tag, v.Value.(uint32))
	})
	if err := enc.EndContainer(); err != nil {
		t.Fatalf("EndContainer: %v", err)
	}
	wire, err := enc.Bytes()
	if err != nil {
		t.Fatalf("Bytes: %v", err)
	}

	// Drill into the inner EventDataIB and collect (tag → TLV type).
	dec := tlv.NewDecoder(wire)
	if _, err := dec.Next(); err != nil { // array opener
		t.Fatalf("array opener: %v", err)
	}
	if _, err := dec.Next(); err != nil { // EventReportIB struct opener
		t.Fatalf("EventReportIB opener: %v", err)
	}
	dataIB, err := dec.Next() // EventDataIB struct opener (tag = tagEventReportData)
	if err != nil {
		t.Fatalf("EventDataIB opener: %v", err)
	}
	if dataIB.Tag.Kind != tlv.TagKindContext || dataIB.Tag.Number != uint32(tagEventReportData) {
		t.Fatalf("expected context tag %d (EventDataIB), got %+v", tagEventReportData, dataIB.Tag)
	}

	type field struct {
		Tag     uint8
		TlvType tlv.ElementType
	}
	var got []field
	depth := 1 // we're inside the EventDataIB struct
	for depth > 0 {
		el, err := dec.Next()
		if err != nil {
			t.Fatalf("decode EventDataIB body: %v", err)
		}
		if el.IsEndContainer {
			depth--
			continue
		}
		if depth == 1 && el.Tag.Kind == tlv.TagKindContext {
			got = append(got, field{Tag: uint8(el.Tag.Number), TlvType: el.Type}) //nolint:gosec // context tag number is 0..7 in spec-conforming TLV
		}
		if el.IsContainer {
			depth++
		}
	}

	want := []field{
		{Tag: tagEventDataPath, TlvType: tlv.TypeList},
		{Tag: tagEventDataNumber, TlvType: tlv.TypeUnsignedInt8},
		{Tag: tagEventDataPriority, TlvType: tlv.TypeUnsignedInt1},
		{Tag: tagEventDataEpochTimestamp, TlvType: tlv.TypeUnsignedInt8},
		{Tag: tagEventDataData, TlvType: tlv.TypeUnsignedInt4},
	}
	if len(got) != len(want) {
		t.Fatalf("EventDataIB field count: got %d, want %d (got=%+v)", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("field[%d]: got %+v, want %+v", i, got[i], want[i])
		}
	}

	// No tag should ever land in the forbidden slot 5
	// (DeltaEpochTimestamp) — the pre-fix code wrote Data there.
	for _, f := range got {
		if f.Tag == tagEventDataDeltaEpochTimestamp {
			t.Errorf("EventDataIB unexpectedly carries tag 5 (DeltaEpochTimestamp); pre-2026-05-11 layout regressed")
		}
	}
}

// TestEventReport_Marshal_DataIB_NullPayload_OmitsData verifies the
// Data field at tag 7 is skipped entirely when the payload is nil/null.
// matter.js declares `data` as TlvOptionalField and never emits an
// explicit `null` placeholder; mirroring that keeps the wire compact and
// avoids the controller having to ignore a meaningless null.
func TestEventReport_Marshal_DataIB_NullPayload_OmitsData(t *testing.T) {
	t.Parallel()
	rep := EventReport{
		Path: ConcreteEventPath{
			Endpoint: 0, HasEndpoint: true,
			Cluster: 0x0028, HasCluster: true,
			Event: 0x0001, HasEvent: true, // ShutDown — no fields
		},
		Number:    7,
		Priority:  EventPriorityCritical,
		Timestamp: 42,
		Data:      AttributeValue{IsNull: true},
		IsStatus:  false,
	}

	enc := tlv.NewEncoder()
	enc.StartArray(tlv.AnonymousTag())
	called := false
	rep.marshal(enc, func(_ *tlv.Encoder, _ tlv.Tag, _ AttributeValue) {
		called = true
	})
	if called {
		t.Errorf("expected EventDataWriter NOT to be invoked for nil payload; matter.js omits the optional Data field")
	}
	if err := enc.EndContainer(); err != nil {
		t.Fatalf("EndContainer: %v", err)
	}

	// And no tag-7 element on the wire.
	wire, err := enc.Bytes()
	if err != nil {
		t.Fatalf("Bytes: %v", err)
	}
	dec := tlv.NewDecoder(wire)
	if _, err := dec.Next(); err != nil { // array
		t.Fatalf("array opener: %v", err)
	}
	if _, err := dec.Next(); err != nil { // EventReportIB
		t.Fatalf("EventReportIB opener: %v", err)
	}
	if _, err := dec.Next(); err != nil { // EventDataIB
		t.Fatalf("EventDataIB opener: %v", err)
	}
	for {
		el, err := dec.Next()
		if err != nil {
			t.Fatalf("decode EventDataIB body: %v", err)
		}
		if el.IsEndContainer {
			break
		}
		if el.Tag.Kind == tlv.TagKindContext && el.Tag.Number == uint32(tagEventDataData) {
			t.Errorf("EventDataIB unexpectedly carries tag 7 (Data) for a nil payload")
		}
		if el.IsContainer {
			// Drain nested container so the outer loop keeps walking.
			depth := 1
			for depth > 0 {
				nel, nerr := dec.Next()
				if nerr != nil {
					t.Fatalf("drain: %v", nerr)
				}
				if nel.IsEndContainer {
					depth--
				} else if nel.IsContainer {
					depth++
				}
			}
		}
	}
}

// ---- ReportData.MarshalTLV: EventReports array only emitted when non-empty ----

// nilValueWriter satisfies the AttributeValueWriter signature without
// writing anything — used when testing structural TLV shape only.
func nilValueWriter(_ *tlv.Encoder, _ tlv.Tag, _ AttributeValue) {}

// decodeReportDataTags walks the top-level ReportData struct and
// collects every context tag number it finds at depth 1.
func decodeReportDataTags(t *testing.T, wire []byte) map[uint8]bool {
	t.Helper()
	dec := tlv.NewDecoder(wire)
	// Consume outer struct opener (anonymous).
	el, err := dec.Next()
	if err != nil || !el.IsContainer || el.Type != tlv.TypeStructure {
		t.Fatalf("expected ReportData struct opener, err=%v el=%+v", err, el)
	}
	found := make(map[uint8]bool)
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
		// Record context tags at top-level BEFORE incrementing depth for containers.
		if depth == 1 && el.Tag.Kind == tlv.TagKindContext {
			found[uint8(el.Tag.Number)] = true //nolint:gosec // context tag number is 0..7 in spec-conforming TLV
		}
		if el.IsContainer {
			depth++
		}
	}
	return found
}

func TestReportData_MarshalTLV_NoEventReports_ArrayAbsent(t *testing.T) {
	t.Parallel()
	rd := ReportData{
		HasSubscription: true,
		SubscriptionID:  7,
		Reports:         nil,
		EventReports:    nil, // empty → must NOT emit tagReportEventReports
	}
	enc := tlv.NewEncoder()
	rd.MarshalTLV(enc, nilValueWriter)
	wire, err := enc.Bytes()
	if err != nil {
		t.Fatalf("Bytes: %v", err)
	}
	tags := decodeReportDataTags(t, wire)
	if tags[tagReportEventReports] {
		t.Error("tagReportEventReports emitted even though EventReports was empty")
	}
}

func TestReportData_MarshalTLV_WithEventReports_ArrayPresent(t *testing.T) {
	t.Parallel()
	rd := ReportData{
		HasSubscription: true,
		SubscriptionID:  8,
		EventReports: []EventReport{
			{
				Path: ConcreteEventPath{
					Endpoint: 1, HasEndpoint: true,
					Cluster: 0x003B, HasCluster: true,
					Event: 0x01, HasEvent: true,
				},
				IsStatus: true,
				Status:   StatusIB{Status: StatusSuccess},
			},
		},
	}
	enc := tlv.NewEncoder()
	rd.MarshalTLV(enc, nilValueWriter)
	wire, err := enc.Bytes()
	if err != nil {
		t.Fatalf("Bytes: %v", err)
	}
	tags := decodeReportDataTags(t, wire)
	if !tags[tagReportEventReports] {
		t.Error("tagReportEventReports absent even though EventReports was non-empty")
	}
}

// ---- BuildEventReports ----

// TestBuildEventReports_EmptyPaths verifies that an empty path slice
// produces nil output — no allocation, no panic.
// Mirrors matter.js packages/protocol/src/action/server/EventReadResponse.ts
// (EventReadResponse.process early-return when eventRequests is empty).
func TestBuildEventReports_EmptyPaths(t *testing.T) {
	t.Parallel()
	log := NewEventLog()
	log.Append(EventRecord{
		Priority: EventPriorityInfo,
		Endpoint: 1,
		Cluster:  0x003B,
		EventID:  0x01,
		Payload:  uint8(1),
	})
	got := BuildEventReports(nil, log, nil)
	if got != nil {
		t.Errorf("expected nil for empty paths, got %v", got)
	}
}

// TestBuildEventReports_OneEvent verifies that a single concrete path
// produces one [EventReport] with the correct Priority and Path fields.
// Mirrors matter.js packages/protocol/src/action/server/EventReadResponse.ts
// (EventReadResponse.#asValue) and EventHandler.ts (getEvents) for the
// per-path log query + EventReport construction.
func TestBuildEventReports_OneEvent(t *testing.T) {
	t.Parallel()
	log := NewEventLog()
	num := log.Append(EventRecord{
		Priority: EventPriorityCritical,
		Endpoint: 3,
		Cluster:  0x003B,
		EventID:  0x02,
		Payload:  "switch-press",
	})

	paths := []ConcreteEventPath{
		{
			Endpoint: 3, HasEndpoint: true,
			Cluster: 0x003B, HasCluster: true,
			Event: 0x02, HasEvent: true,
		},
	}
	got := BuildEventReports(paths, log, nil)
	if len(got) != 1 {
		t.Fatalf("expected 1 EventReport, got %d", len(got))
	}
	rep := got[0]
	if rep.Priority != EventPriorityCritical {
		t.Errorf("Priority: got %v, want EventPriorityCritical", rep.Priority)
	}
	if rep.Number != num {
		t.Errorf("Number: got %d, want %d", rep.Number, num)
	}
	if rep.Path.Endpoint != 3 || rep.Path.Cluster != 0x003B || rep.Path.Event != 0x02 {
		t.Errorf("Path mismatch: got %+v", rep.Path)
	}
	if !rep.Path.HasEndpoint || !rep.Path.HasCluster || !rep.Path.HasEvent {
		t.Errorf("Path Has* flags not set: got %+v", rep.Path)
	}
	if rep.IsStatus {
		t.Error("IsStatus must be false for a data report")
	}
}
