// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package weekprofile

import (
	"context"
	"sync/atomic"
	"testing"
)

// TestFireScheduleUpdated verifies that FireScheduleUpdated fires the
// registered change callbacks and publishes a value-changed event through
// the installed EventPublisher in a single call.
func TestFireScheduleUpdated(t *testing.T) {
	t.Parallel()

	dp := NewProfileDataPoint(ProfileDataPointConfig{
		CentralName:    "ccu1",
		ChannelAddress: "VCU0001:1",
		ScheduleType:   ScheduleTypeClimate,
		ProfileCount:   3,
	})

	// Register a change callback.
	var cbCount atomic.Int32
	dp.OnChange(func() { cbCount.Add(1) })

	// Attach a capturing publisher.
	pub := &profileCapturingPublisher{}
	dp.SetPublisher(pub)

	dp.FireScheduleUpdated(context.Background(), "P2")

	// Change callback must have fired exactly once.
	if got := cbCount.Load(); got != 1 {
		t.Errorf("change callback fired %d times, want 1", got)
	}

	// Publisher must have received exactly one call with the supplied value.
	calls := pub.snapshot()
	if len(calls) != 1 {
		t.Fatalf("publisher received %d calls, want 1", len(calls))
	}
	if got, want := calls[0].key, "ccu1:VCU0001:1:WEEKPROFILE"; got != want {
		t.Errorf("publisher key = %q, want %q", got, want)
	}
	if calls[0].value != "P2" {
		t.Errorf("publisher value = %v, want P2", calls[0].value)
	}
}

// TestFireScheduleUpdatedWithoutPublisher verifies that FireScheduleUpdated
// does not panic and still fires change callbacks when no EventPublisher is
// installed.
func TestFireScheduleUpdatedWithoutPublisher(t *testing.T) {
	t.Parallel()

	dp := NewProfileDataPoint(ProfileDataPointConfig{
		ScheduleType: ScheduleTypeClimate,
		ProfileCount: 1,
	})
	var cbCount atomic.Int32
	dp.OnChange(func() { cbCount.Add(1) })

	dp.FireScheduleUpdated(context.Background(), 42)

	if got := cbCount.Load(); got != 1 {
		t.Errorf("change callback fired %d times, want 1", got)
	}
}
