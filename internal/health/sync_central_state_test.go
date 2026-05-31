// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package health

import (
	"testing"
	"time"
)

// ---- L-A5-38: SyncCentralState ----

func TestSyncCentralStateNilFnIsNoOp(t *testing.T) {
	tr := NewTracker()
	tr.Record("c1", Sample{Healthy: true, Timestamp: time.Now()})
	// Must not panic.
	tr.SyncCentralState(nil)
}

func TestSyncCentralStateInvokesCallback(t *testing.T) {
	tr := NewTracker()
	tr.Record("c1", Sample{Healthy: true, Timestamp: time.Now()})

	var got Status
	tr.SyncCentralState(func(overall Status) {
		got = overall
	})
	if got != StatusHealthy {
		t.Fatalf("SyncCentralState callback status = %q, want healthy", got)
	}
}

func TestSyncCentralStateReflectsUnhealthy(t *testing.T) {
	tr := NewTracker()
	tr.Record("c1", Sample{Healthy: false, Timestamp: time.Now()})
	tr.Record("c1", Sample{Healthy: false, Timestamp: time.Now()}) // two to reach UNHEALTHY

	var got Status
	tr.SyncCentralState(func(overall Status) { got = overall })
	if got == StatusHealthy {
		t.Fatal("SyncCentralState should not report healthy after two unhealthy samples")
	}
}

func TestSyncCentralStateEmptyTracker(t *testing.T) {
	tr := NewTracker()
	var got Status
	tr.SyncCentralState(func(overall Status) { got = overall })
	if got != StatusUnknown {
		t.Fatalf("SyncCentralState on empty tracker = %q, want unknown", got)
	}
}
