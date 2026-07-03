// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package im

import (
	"testing"
	"time"
)

// TestEventLog_Append_StampsEpochMilliseconds locks that Append auto-stamps
// EpochMS in POSIX milliseconds (Matter §10.6.6.1 EpochTimestamp is POSIX
// ms — matter.js TlvPosixMs), not microseconds. A microsecond stamp would be
// ~1000× larger than the current millisecond wall-clock, putting the wire
// timestamp in the year ~55000 and reading 1000× off from the subscribe
// path (which already emits milliseconds).
func TestEventLog_Append_StampsEpochMilliseconds(t *testing.T) {
	t.Parallel()
	log := NewEventLog()

	before := uint64(time.Now().UnixMilli()) //nolint:gosec // wall-clock ms is non-negative
	log.Append(EventRecord{Endpoint: 1, Cluster: 0x0028, EventID: 0x00})
	after := uint64(time.Now().UnixMilli()) //nolint:gosec // wall-clock ms is non-negative

	recs := log.Query(1, 0x0028, 0x00, 0)
	if len(recs) != 1 {
		t.Fatalf("Query returned %d records, want 1", len(recs))
	}
	if ms := recs[0].EpochMS; ms < before || ms > after {
		t.Fatalf("EpochMS=%d not in [%d, %d] — expected POSIX milliseconds, got a value that looks like microseconds/seconds", ms, before, after)
	}
}
