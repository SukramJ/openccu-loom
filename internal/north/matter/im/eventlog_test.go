// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package im

import (
	"testing"
)

// ---- TestEventLog_AppendMonotonic ----------------------------------------

// TestEventLog_AppendMonotonic verifies that five consecutive Append calls
// produce strictly increasing, consecutive event numbers starting at 1.
func TestEventLog_AppendMonotonic(t *testing.T) {
	t.Parallel()
	log := NewEventLog()

	for i := range 5 {
		num := log.Append(EventRecord{
			Priority: EventPriorityCritical,
			Endpoint: 0,
			Cluster:  0x0028,
			EventID:  0x00,
			Payload:  nil,
		})
		want := uint64(i + 1)
		if num != want {
			t.Errorf("Append #%d: got Number=%d, want %d", i+1, num, want)
		}
	}

	// Verify the stored records carry the same numbers.
	records := log.Query(0xFFFF, 0xFFFFFFFF, 0xFFFFFFFF, 0)
	if len(records) != 5 {
		t.Fatalf("Query returned %d records, want 5", len(records))
	}
	for i, r := range records {
		want := uint64(i + 1)
		if r.Number != want {
			t.Errorf("records[%d].Number=%d, want %d", i, r.Number, want)
		}
	}
}

// ---- TestEventLog_QueryWildcard ------------------------------------------

// TestEventLog_QueryWildcard verifies that wildcard filters correctly match
// subsets of events from multiple endpoints and clusters.
func TestEventLog_QueryWildcard(t *testing.T) {
	t.Parallel()
	log := NewEventLog()

	// Endpoint 1, Cluster 0x0028 (BasicInformation), Event 0x00 (StartUp)
	log.Append(EventRecord{Priority: EventPriorityCritical, Endpoint: 1, Cluster: 0x0028, EventID: 0x00})
	// Endpoint 1, Cluster 0x0033 (GeneralDiagnostics), Event 0x03 (BootReason)
	log.Append(EventRecord{Priority: EventPriorityCritical, Endpoint: 1, Cluster: 0x0033, EventID: 0x03})
	// Endpoint 2, Cluster 0x0039 (BridgedDeviceBasicInformation), Event 0x02 (ReachableChanged)
	log.Append(EventRecord{Priority: EventPriorityCritical, Endpoint: 2, Cluster: 0x0039, EventID: 0x02})
	// Endpoint 2, Cluster 0x001F (AccessControl), Event 0x00 (EntryChanged)
	log.Append(EventRecord{Priority: EventPriorityInfo, Endpoint: 2, Cluster: 0x001F, EventID: 0x00})

	t.Run("wildcard_all", func(t *testing.T) {
		t.Parallel()
		got := log.Query(0xFFFF, 0xFFFFFFFF, 0xFFFFFFFF, 0)
		if len(got) != 4 {
			t.Errorf("wildcard_all: got %d records, want 4", len(got))
		}
	})
	t.Run("endpoint_wildcard_cluster_concrete", func(t *testing.T) {
		t.Parallel()
		// Match only BasicInformation events on any endpoint.
		got := log.Query(0xFFFF, 0x0028, 0xFFFFFFFF, 0)
		if len(got) != 1 {
			t.Errorf("endpoint_wildcard_cluster_concrete: got %d records, want 1", len(got))
		}
		if len(got) > 0 && got[0].Cluster != 0x0028 {
			t.Errorf("unexpected cluster 0x%04X", got[0].Cluster)
		}
	})
	t.Run("endpoint_concrete_cluster_wildcard", func(t *testing.T) {
		t.Parallel()
		// Match all events on endpoint 2.
		got := log.Query(2, 0xFFFFFFFF, 0xFFFFFFFF, 0)
		if len(got) != 2 {
			t.Errorf("endpoint_concrete_cluster_wildcard: got %d records, want 2", len(got))
		}
		for _, r := range got {
			if r.Endpoint != 2 {
				t.Errorf("unexpected endpoint %d", r.Endpoint)
			}
		}
	})
	t.Run("concrete_event_match", func(t *testing.T) {
		t.Parallel()
		// Match only BootReason (Event 0x03) on any endpoint / cluster.
		got := log.Query(0xFFFF, 0xFFFFFFFF, 0x03, 0)
		if len(got) != 1 {
			t.Errorf("concrete_event_match: got %d records, want 1", len(got))
		}
		if len(got) > 0 && got[0].EventID != 0x03 {
			t.Errorf("unexpected EventID 0x%02X", got[0].EventID)
		}
	})
}

// ---- TestEventLog_PriorityCapEviction ------------------------------------

// TestEventLog_PriorityCapEviction verifies that appending more Critical
// events than the cap causes the oldest to be evicted in FIFO order.
func TestEventLog_PriorityCapEviction(t *testing.T) {
	t.Parallel()
	// Use a small cap (8) to force eviction without appending 100 records.
	log := newEventLogWithCaps(8, 32, 16)

	for i := range 100 {
		log.Append(EventRecord{
			Priority: EventPriorityCritical,
			Endpoint: 0,
			Cluster:  0x0028,
			EventID:  0x00,
			Payload:  i,
		})
	}

	records := log.Query(0xFFFF, 0xFFFFFFFF, 0xFFFFFFFF, 0)
	// Should have exactly 8 records (cap).
	if len(records) != 8 {
		t.Fatalf("after eviction: got %d records, want 8 (cap)", len(records))
	}
	// The retained records must be the most recent ones (Numbers 93..100).
	wantFirst := uint64(93)
	if records[0].Number != wantFirst {
		t.Errorf("oldest retained Number=%d, want %d (cap eviction)", records[0].Number, wantFirst)
	}
	wantLast := uint64(100)
	if records[7].Number != wantLast {
		t.Errorf("newest retained Number=%d, want %d", records[7].Number, wantLast)
	}
	// Payloads must be ordered 92..99 (0-indexed loop values for events 93..100).
	for i, r := range records {
		wantPayload := 100 - 8 + i // 92, 93, ..., 99
		if r.Payload != wantPayload {
			t.Errorf("records[%d].Payload=%v, want %d", i, r.Payload, wantPayload)
		}
	}
}

// ---- TestEventLog_QueryMinNumber -----------------------------------------

// TestEventLog_QueryMinNumber verifies that Query with minNumber=10 returns
// only events with Number > 10.
func TestEventLog_QueryMinNumber(t *testing.T) {
	t.Parallel()
	log := NewEventLog()

	for i := range 20 {
		log.Append(EventRecord{
			Priority: EventPriorityCritical,
			Endpoint: 0,
			Cluster:  0x0028,
			EventID:  0x00,
			Payload:  i,
		})
	}

	got := log.Query(0xFFFF, 0xFFFFFFFF, 0xFFFFFFFF, 10)
	if len(got) != 10 {
		t.Fatalf("Query(minNumber=10): got %d records, want 10", len(got))
	}
	// All returned numbers must be > 10.
	for _, r := range got {
		if r.Number <= 10 {
			t.Errorf("record with Number=%d returned for minNumber=10", r.Number)
		}
	}
	// First record should have Number=11.
	if got[0].Number != 11 {
		t.Errorf("first record Number=%d, want 11", got[0].Number)
	}
}

// ---- TestEventLog_MultiPriorityQuery -------------------------------------

// TestEventLog_MultiPriorityQuery verifies that Query spans all priority
// buckets and returns results sorted by Number.
func TestEventLog_MultiPriorityQuery(t *testing.T) {
	t.Parallel()
	log := NewEventLog()

	// Mix priorities — numbers will be 1, 2, 3, 4 in insertion order.
	log.Append(EventRecord{Priority: EventPriorityCritical, Endpoint: 1, Cluster: 0x0028, EventID: 0x00})
	log.Append(EventRecord{Priority: EventPriorityInfo, Endpoint: 1, Cluster: 0x0033, EventID: 0x03})
	log.Append(EventRecord{Priority: EventPriorityDebug, Endpoint: 1, Cluster: 0x0039, EventID: 0x02})
	log.Append(EventRecord{Priority: EventPriorityCritical, Endpoint: 1, Cluster: 0x001F, EventID: 0x00})

	got := log.Query(0xFFFF, 0xFFFFFFFF, 0xFFFFFFFF, 0)
	if len(got) != 4 {
		t.Fatalf("multi-priority Query: got %d records, want 4", len(got))
	}
	// Must be sorted by Number ascending.
	for i := 1; i < len(got); i++ {
		if got[i].Number <= got[i-1].Number {
			t.Errorf("records not sorted: got[%d].Number=%d <= got[%d].Number=%d",
				i, got[i].Number, i-1, got[i-1].Number)
		}
	}
}

// ---- TestHandleReadEventRequest ------------------------------------------

// TestHandleReadEventRequest verifies that HandleReadEventRequest returns
// EventReports matching the EventRequests in the ReadRequest.
func TestHandleReadEventRequest(t *testing.T) {
	t.Parallel()
	log := NewEventLog()

	log.Append(EventRecord{Priority: EventPriorityCritical, Endpoint: 0, Cluster: 0x0028, EventID: 0x00, Payload: "startup"})
	log.Append(EventRecord{Priority: EventPriorityCritical, Endpoint: 0, Cluster: 0x0033, EventID: 0x03, Payload: "bootreason"})
	log.Append(EventRecord{Priority: EventPriorityCritical, Endpoint: 1, Cluster: 0x0039, EventID: 0x02, Payload: "reachable"})

	t.Run("wildcard_returns_all", func(t *testing.T) {
		t.Parallel()
		req := ReadRequest{
			EventRequests: []ConcreteEventPath{
				{}, // full wildcard
			},
		}
		got := HandleReadEventRequest(req, log)
		if len(got) != 3 {
			t.Fatalf("wildcard: got %d EventReports, want 3", len(got))
		}
	})

	t.Run("concrete_cluster_filter", func(t *testing.T) {
		t.Parallel()
		req := ReadRequest{
			EventRequests: []ConcreteEventPath{
				{Cluster: 0x0028, HasCluster: true},
			},
		}
		got := HandleReadEventRequest(req, log)
		if len(got) != 1 {
			t.Fatalf("concrete cluster: got %d EventReports, want 1", len(got))
		}
		if got[0].Path.Cluster != 0x0028 {
			t.Errorf("unexpected cluster 0x%04X", got[0].Path.Cluster)
		}
	})

	t.Run("nil_log_returns_nil", func(t *testing.T) {
		t.Parallel()
		req := ReadRequest{EventRequests: []ConcreteEventPath{{}}}
		got := HandleReadEventRequest(req, nil)
		if got != nil {
			t.Errorf("nil log: expected nil, got %v", got)
		}
	})

	t.Run("no_event_requests_returns_nil", func(t *testing.T) {
		t.Parallel()
		req := ReadRequest{}
		got := HandleReadEventRequest(req, log)
		if got != nil {
			t.Errorf("no event requests: expected nil, got %v", got)
		}
	})

	t.Run("overlapping_paths_no_duplicates", func(t *testing.T) {
		t.Parallel()
		// Two paths that both match the same record.
		req := ReadRequest{
			EventRequests: []ConcreteEventPath{
				{}, // wildcard → all 3
				{Cluster: 0x0028, HasCluster: true, Event: 0x00, HasEvent: true}, // exact match for record 1
			},
		}
		got := HandleReadEventRequest(req, log)
		// Should be 3, not 4 (no duplicate for record 1).
		if len(got) != 3 {
			t.Fatalf("overlapping: got %d EventReports, want 3 (no dups)", len(got))
		}
	})
}
