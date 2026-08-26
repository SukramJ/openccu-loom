// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

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

// ---- Buffer harvesting ---------------------------------------------------

// harvestLog builds a small-buffer EventLog for the harvesting tests. The
// production sizes are in the thousands; forcing a harvest at those numbers
// would say the same thing far more slowly.
func harvestLog(minAllowance, maxAllowance, minInfo, minDebug int) *EventLog {
	return newEventLogWithBuffer(bufferConfig{
		MinEventAllowance: minAllowance,
		MaxEventAllowance: maxAllowance,
		MinInfoAllowance:  minInfo,
		MinDebugAllowance: minDebug,
	})
}

func appendN(log *EventLog, n int, priority EventPriority, cluster, event uint32) {
	for range n {
		log.Append(EventRecord{Priority: priority, Endpoint: 0, Cluster: cluster, EventID: event})
	}
}

// TestEventLog_HarvestKeepsCriticalUnderInfoFlood is the regression this
// buffer exists for. The boot-once BasicInformation StartUp (0x0028/0x00) and
// GeneralDiagnostics BootReason (0x0033/0x03) events are Critical and are
// never re-emitted; a controller reads them out-of-band at Subscribe-Initial.
// Ordinary Info traffic — a CCU interface flap flips Reachable on every
// bridged device at once — must not be able to push them out.
//
// The predecessor buffer held a fixed 64 Critical records with no regard for
// the other classes, and a 36-device central flapping once produced enough
// Critical events (Reachable was miscategorised too) to evict both.
func TestEventLog_HarvestKeepsCriticalUnderInfoFlood(t *testing.T) {
	t.Parallel()
	log := harvestLog(20, 24, 4, 2)

	log.Append(EventRecord{Priority: EventPriorityCritical, Cluster: 0x0028, EventID: 0x00})
	log.Append(EventRecord{Priority: EventPriorityCritical, Cluster: 0x0033, EventID: 0x03})
	appendN(log, 500, EventPriorityInfo, 0x0039, 0x03)

	if got := log.Query(0xFFFF, 0x0028, 0x00, 0); len(got) != 1 {
		t.Errorf("StartUp records after the flood: got %d, want 1", len(got))
	}
	if got := log.Query(0xFFFF, 0x0033, 0x03, 0); len(got) != 1 {
		t.Errorf("BootReason records after the flood: got %d, want 1", len(got))
	}
}

// TestEventLog_HarvestBoundsTheBuffer pins that harvesting actually bounds
// the buffer rather than merely reordering it: an unbounded log would answer
// a wildcard read with every record ever appended.
func TestEventLog_HarvestBoundsTheBuffer(t *testing.T) {
	t.Parallel()
	log := harvestLog(20, 24, 4, 2)
	appendN(log, 500, EventPriorityInfo, 0x0039, 0x03)

	got := log.Query(0xFFFF, 0xFFFFFFFF, 0xFFFFFFFF, 0)
	if len(got) > 24 {
		t.Errorf("buffered records: got %d, want <= 24 (MaxEventAllowance)", len(got))
	}
	if len(got) < 20 {
		t.Errorf("buffered records: got %d, want >= 20 (MinEventAllowance)", len(got))
	}
}

// TestEventLog_HarvestDropsOldestWithinAClass pins FIFO order inside one
// priority class: the records that survive are the most recent ones, and
// their Numbers stay ascending — a controller tracking the last EventNumber
// it saw must never observe a descending step.
func TestEventLog_HarvestDropsOldestWithinAClass(t *testing.T) {
	t.Parallel()
	log := harvestLog(20, 24, 0, 0)
	appendN(log, 100, EventPriorityInfo, 0x0039, 0x03)

	got := log.Query(0xFFFF, 0xFFFFFFFF, 0xFFFFFFFF, 0)
	if len(got) == 0 {
		t.Fatal("no records retained")
	}
	if last := got[len(got)-1].Number; last != 100 {
		t.Errorf("newest retained Number=%d, want 100", last)
	}
	for i := 1; i < len(got); i++ {
		if got[i].Number <= got[i-1].Number {
			t.Fatalf("Numbers not ascending at %d: %d after %d", i, got[i].Number, got[i-1].Number)
		}
	}
}

// TestEventLog_HarvestFloorProtectsInfoFromDebugFlood pins the per-class
// floor: Debug is harvested first, so a Debug flood must not carry the most
// recent Info records out with it.
func TestEventLog_HarvestFloorProtectsInfoFromDebugFlood(t *testing.T) {
	t.Parallel()
	log := harvestLog(20, 24, 5, 0)

	appendN(log, 5, EventPriorityInfo, 0x0039, 0x03)
	appendN(log, 500, EventPriorityDebug, 0x0006, 0x00)

	if got := log.Query(0xFFFF, 0x0039, 0x03, 0); len(got) != 5 {
		t.Errorf("Info records under a Debug flood: got %d, want 5 (MinInfoAllowance)", len(got))
	}
}

// TestEventLog_HarvestDropsCriticalOnlyWhenNothingElseRemains pins the other
// half of the ordering: Critical is not immortal. A buffer holding nothing
// but Critical records still harvests, and drops its oldest.
func TestEventLog_HarvestDropsCriticalOnlyWhenNothingElseRemains(t *testing.T) {
	t.Parallel()
	log := harvestLog(20, 24, 4, 2)
	appendN(log, 100, EventPriorityCritical, 0x0028, 0x00)

	got := log.Query(0xFFFF, 0xFFFFFFFF, 0xFFFFFFFF, 0)
	if len(got) > 24 {
		t.Fatalf("buffered records: got %d, want <= 24", len(got))
	}
	if last := got[len(got)-1].Number; last != 100 {
		t.Errorf("newest retained Number=%d, want 100", last)
	}
}

// ---- TestEventLog_QueryMinNumber -----------------------------------------

// TestEventLog_QueryEventMinIsInclusive pins the EventFilterIB.EventMin
// semantics: minNumber is an INCLUSIVE lower bound, so Query(minNumber=10)
// returns the record whose Number == 10 as well as every later one. Mirrors
// matter.js OccurrenceManager.ts #findMinEventNumberIndex ("first event number
// that is greater than or equal to eventMin") and chip EventManagement.cpp
// IncludeEventInReport (drops only Number < mStartingEventNumber). A controller
// following the chip convention sends EventMin = lastSeen+1, so an exclusive
// (>) bound would silently drop the event whose Number == EventMin.
func TestEventLog_QueryEventMinIsInclusive(t *testing.T) {
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

	// Numbers run 1..20; EventMin=10 must return 10..20 (11 records).
	got := log.Query(0xFFFF, 0xFFFFFFFF, 0xFFFFFFFF, 10)
	if len(got) != 11 {
		t.Fatalf("Query(minNumber=10): got %d records, want 11 (inclusive of Number==10)", len(got))
	}
	// The boundary record whose Number == EventMin must be present.
	if got[0].Number != 10 {
		t.Errorf("first record Number=%d, want 10 (EventMin is inclusive)", got[0].Number)
	}
	for _, r := range got {
		if r.Number < 10 {
			t.Errorf("record with Number=%d returned for minNumber=10 (below inclusive floor)", r.Number)
		}
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

// ---- TestEventLog_SeedNumber ----------------------------------------------

// TestEventLog_SeedNumber_AdvancesCounter verifies that SeedNumber raises
// the event-number counter so the next Append is assigned base+1 — used at
// boot to resume numbering past the persisted ceiling (Matter §7.14.2.1
// monotonicity).
func TestEventLog_SeedNumber_AdvancesCounter(t *testing.T) {
	t.Parallel()
	log := NewEventLog()
	log.SeedNumber(1000)

	num := log.Append(EventRecord{Priority: EventPriorityCritical, Endpoint: 0, Cluster: 0x0028, EventID: 0x00})
	if num != 1001 {
		t.Fatalf("Append after SeedNumber(1000): Number=%d, want 1001", num)
	}
}

// TestEventLog_SeedNumber_IgnoresLowerBase verifies that SeedNumber never
// moves the counter backwards — a base at or below the current counter is
// a no-op.
func TestEventLog_SeedNumber_IgnoresLowerBase(t *testing.T) {
	t.Parallel()
	log := NewEventLog()

	// Advance the counter to 5 via five appends.
	var last uint64
	for range 5 {
		last = log.Append(EventRecord{Priority: EventPriorityCritical, Endpoint: 0, Cluster: 0x0028, EventID: 0x00})
	}
	if last != 5 {
		t.Fatalf("setup: last Number=%d, want 5", last)
	}

	log.SeedNumber(3) // below current counter — must be ignored

	num := log.Append(EventRecord{Priority: EventPriorityCritical, Endpoint: 0, Cluster: 0x0028, EventID: 0x00})
	if num != 6 {
		t.Fatalf("Append after SeedNumber(3) (below current): Number=%d, want 6 (counter must not move backwards)", num)
	}
}

// ---- TestEventLog_SetCounterPersistence -----------------------------------

// TestEventLog_SetCounterPersistence_CeilingAheadOfHandedOutNumber verifies
// that whenever the persist fn fires, the ceiling it is handed is strictly
// greater than the Number the triggering Append just handed out, and that
// successive ceilings are monotonically increasing. This is the crash-
// safety property: the ceiling must always cover the number a caller can
// already observe.
func TestEventLog_SetCounterPersistence_CeilingAheadOfHandedOutNumber(t *testing.T) {
	t.Parallel()
	log := NewEventLog()

	type call struct {
		atNumber uint64 // the Number the triggering Append handed out
		ceiling  uint64
	}
	var calls []call
	var lastNumber uint64
	log.SetCounterPersistence(func(ceiling uint64) {
		calls = append(calls, call{atNumber: lastNumber + 1, ceiling: ceiling})
	}, 16)

	const n = 40
	for range n {
		lastNumber = log.Append(EventRecord{Priority: EventPriorityCritical, Endpoint: 0, Cluster: 0x0028, EventID: 0x00})
	}

	if len(calls) == 0 {
		t.Fatal("SetCounterPersistence: persist fn never called across 40 appends")
	}
	prevCeiling := uint64(0)
	for i, c := range calls {
		if c.ceiling <= c.atNumber {
			t.Errorf("call %d: ceiling=%d must be > handed-out number %d", i, c.ceiling, c.atNumber)
		}
		if c.ceiling <= prevCeiling {
			t.Errorf("call %d: ceiling=%d not monotonically increasing over previous %d", i, c.ceiling, prevCeiling)
		}
		prevCeiling = c.ceiling
	}
}

// TestEventLog_SetCounterPersistence_CalledAtMostOncePerEpoch verifies the
// call count over N appends matches the epoch cadence — the persist fn
// fires once when SetCounterPersistence forces an initial persist, then
// again every epoch handed-out numbers, not once per Append.
func TestEventLog_SetCounterPersistence_CalledAtMostOncePerEpoch(t *testing.T) {
	t.Parallel()
	log := NewEventLog()

	var callCount int
	log.SetCounterPersistence(func(uint64) { callCount++ }, 16)

	const n = 40
	for range n {
		log.Append(EventRecord{Priority: EventPriorityCritical, Endpoint: 0, Cluster: 0x0028, EventID: 0x00})
	}

	// Ceiling starts at 0 (forced first-persist), then re-persists every
	// 16 handed-out numbers: triggers at Number=1, 17, 33 for n=40.
	const want = 3
	if callCount != want {
		t.Fatalf("persist called %d times over %d appends, want %d", callCount, n, want)
	}
}

// TestEventLog_CrashRestart_NeverReusesNumber simulates a crash-restart: a
// fresh log seeded from the last ceiling persisted by a previous log
// instance must never hand out a Number the previous instance already
// handed to a caller, even when the crash happens immediately after the
// last successful Append (so the previous instance's own in-memory
// counter, one past the ceiling's safety margin, is lost).
func TestEventLog_CrashRestart_NeverReusesNumber(t *testing.T) {
	t.Parallel()

	log1 := NewEventLog()
	var lastCeiling uint64
	log1.SetCounterPersistence(func(ceiling uint64) { lastCeiling = ceiling }, 16)

	var lastHandedOut uint64
	// Stop right before the second ceiling would be persisted (that
	// happens on the 17th append) — the tightest margin between the
	// persisted ceiling (17, from the first append) and the last
	// number actually handed out (16).
	for range 16 {
		lastHandedOut = log1.Append(EventRecord{Priority: EventPriorityCritical, Endpoint: 0, Cluster: 0x0028, EventID: 0x00})
	}
	if lastCeiling == 0 {
		t.Fatal("setup: persist fn was never called")
	}

	// log1 "crashes" here — its in-memory counter is lost, only the
	// last persisted ceiling survives.
	log2 := NewEventLog()
	log2.SeedNumber(lastCeiling)
	next := log2.Append(EventRecord{Priority: EventPriorityCritical, Endpoint: 0, Cluster: 0x0028, EventID: 0x00})

	if next <= lastHandedOut {
		t.Fatalf("log2 handed out Number=%d, which is <= log1's last handed-out Number=%d — reuse after crash-restart", next, lastHandedOut)
	}
}
