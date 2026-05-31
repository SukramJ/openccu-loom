// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package health

import "testing"

func TestTrackerRecordsHealthy(t *testing.T) {
	h := NewTracker()
	h.Record("xmlrpc", Sample{Healthy: true})
	c, ok := h.Get("xmlrpc")
	if !ok || c.Status != StatusHealthy {
		t.Fatalf("got %+v ok=%v", c, ok)
	}
}

func TestTrackerDegradesOnFirstFailure(t *testing.T) {
	h := NewTracker()
	h.Record("xmlrpc", Sample{Healthy: true})
	h.Record("xmlrpc", Sample{Healthy: false})
	c, _ := h.Get("xmlrpc")
	if c.Status != StatusDegraded {
		t.Fatalf("status=%s, want degraded", c.Status)
	}
	h.Record("xmlrpc", Sample{Healthy: false})
	c, _ = h.Get("xmlrpc")
	if c.Status != StatusUnhealthy {
		t.Fatalf("status=%s, want unhealthy", c.Status)
	}
}

func TestTrackerOverall(t *testing.T) {
	h := NewTracker()
	if h.Overall() != StatusUnknown {
		t.Error("empty tracker should report unknown")
	}
	h.Record("a", Sample{Healthy: true})
	if h.Overall() != StatusHealthy {
		t.Error("single healthy should be healthy overall")
	}
	h.Record("b", Sample{Healthy: true})
	h.Record("b", Sample{Healthy: false})
	if h.Overall() != StatusDegraded {
		t.Errorf("expected degraded, got %s", h.Overall())
	}
	h.Record("b", Sample{Healthy: false})
	if h.Overall() != StatusUnhealthy {
		t.Errorf("expected unhealthy, got %s", h.Overall())
	}
}

func TestTrackerSnapshotSorted(t *testing.T) {
	h := NewTracker()
	h.Record("z", Sample{Healthy: true})
	h.Record("a", Sample{Healthy: true})
	snap := h.Snapshot()
	if len(snap) != 2 || snap[0].Name != "a" {
		t.Fatalf("snap=%+v", snap)
	}
}

func TestTrackerScore(t *testing.T) {
	h := NewTracker()
	if got := h.Score(); got != 0 {
		t.Fatalf("empty score = %v want 0", got)
	}
	h.Record("a", Sample{Healthy: true})
	h.Record("b", Sample{Healthy: true})
	if got := h.Score(); got != 1.0 {
		t.Fatalf("two healthy score = %v want 1.0", got)
	}
	// Mark a as degraded (one failure after healthy).
	h.Record("a", Sample{Healthy: false})
	if got := h.Score(); got != 0.75 {
		t.Fatalf("one degraded + one healthy = %v want 0.75", got)
	}
	// Escalate a to unhealthy.
	h.Record("a", Sample{Healthy: false})
	if got := h.Score(); got != 0.5 {
		t.Fatalf("one unhealthy + one healthy = %v want 0.5", got)
	}
	// Mark b as unhealthy too — both fail.
	h.Record("b", Sample{Healthy: false})
	h.Record("b", Sample{Healthy: false})
	if got := h.Score(); got != 0 {
		t.Fatalf("all unhealthy = %v want 0", got)
	}
}

func TestTrackerDegradedAndUnhealthyLists(t *testing.T) {
	h := NewTracker()
	h.Record("xml", Sample{Healthy: true})
	h.Record("json", Sample{Healthy: true})
	h.Record("bin", Sample{Healthy: true})

	// xml: degraded
	h.Record("xml", Sample{Healthy: false})
	// bin: unhealthy
	h.Record("bin", Sample{Healthy: false})
	h.Record("bin", Sample{Healthy: false})

	if got := h.DegradedComponents(); len(got) != 1 || got[0] != "xml" {
		t.Fatalf("DegradedComponents=%v want [xml]", got)
	}
	if got := h.UnhealthyComponents(); len(got) != 1 || got[0] != "bin" {
		t.Fatalf("UnhealthyComponents=%v want [bin]", got)
	}
}

// TestTrackerRecordEventReceived verifies that RecordEventReceived
// updates per-component last-event tracking in the history ring.
func TestTrackerRecordEventReceived(t *testing.T) {
	tr := NewTracker()
	tr.RecordEventReceived("HmIP-RF")

	hist := tr.History("HmIP-RF", 10)
	if len(hist) == 0 {
		t.Fatal("RecordEventReceived: expected at least 1 history entry")
	}
	last := hist[len(hist)-1]
	if last.Note != "event-received" {
		t.Fatalf("expected note 'event-received', got %q", last.Note)
	}
	if last.Timestamp.IsZero() {
		t.Fatal("Timestamp must not be zero")
	}
}

// TestTrackerRecordEventReceivedPreservesUnhealthy verifies that an
// event-received record does not reset an unhealthy status
// (events don't make a broken client healthy).
func TestTrackerRecordEventReceivedPreservesUnhealthy(t *testing.T) {
	tr := NewTracker()
	tr.Record("HmIP-RF", Sample{Healthy: false})
	tr.Record("HmIP-RF", Sample{Healthy: false}) // escalate to UNHEALTHY

	tr.RecordEventReceived("HmIP-RF")

	comp, _ := tr.Get("HmIP-RF")
	if comp.Status == StatusHealthy {
		t.Fatal("RecordEventReceived must not flip UNHEALTHY → HEALTHY")
	}
}

// TestTrackerRecordReconnectAttempt verifies that the reconnect counter
// increments and can be read back.
func TestTrackerRecordReconnectAttempt(t *testing.T) {
	tr := NewTracker()
	if tr.ReconnectAttempts("HmIP-RF") != 0 {
		t.Fatal("expected 0 on fresh tracker")
	}
	tr.RecordReconnectAttempt("HmIP-RF")
	tr.RecordReconnectAttempt("HmIP-RF")
	if got := tr.ReconnectAttempts("HmIP-RF"); got != 2 {
		t.Fatalf("expected 2, got %d", got)
	}
	// Other components are not affected.
	if tr.ReconnectAttempts("BidCos-RF") != 0 {
		t.Fatal("other component should be 0")
	}
}

// TestTrackerUnregister verifies that Remove clears component and
// history.
func TestTrackerUnregister(t *testing.T) {
	tr := NewTracker()
	tr.Record("HmIP-RF", Sample{Healthy: true})
	tr.RecordReconnectAttempt("HmIP-RF")

	tr.Unregister("HmIP-RF")

	if _, ok := tr.Get("HmIP-RF"); ok {
		t.Fatal("Unregister: component still present")
	}
	if hist := tr.History("HmIP-RF", 10); len(hist) != 0 {
		t.Fatalf("Unregister: history not cleared (len=%d)", len(hist))
	}
	if tr.ReconnectAttempts("HmIP-RF") != 0 {
		t.Fatal("Unregister: reconnect counter not cleared")
	}
}

// TestTrackerUnregisterIdempotent verifies that Unregister("unknown") is
// safe and idempotent.
func TestTrackerUnregisterIdempotent(t *testing.T) {
	tr := NewTracker()
	tr.Unregister("nope") // must not panic
	tr.Unregister("nope") // idempotent
}
