// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package adapter

import "testing"

// P0-6: Spot-checks for the JSON ↔ DTO conversion in
// ScheduleQueryAdapter. The adapter is otherwise covered by the WS
// command tests; this file exercises the helper functions in isolation.

func TestSplitChannelAddressWithChannel(t *testing.T) {
	t.Parallel()
	dev, ch := splitChannelAddress("0001ABCD:7")
	if dev != "0001ABCD" || ch != 7 {
		t.Fatalf("got dev=%q ch=%d", dev, ch)
	}
}

func TestSplitChannelAddressNoChannel(t *testing.T) {
	t.Parallel()
	dev, ch := splitChannelAddress("0001ABCD")
	if dev != "0001ABCD" || ch != 0 {
		t.Fatalf("got dev=%q ch=%d", dev, ch)
	}
}

func TestSplitChannelAddressNonNumericChannel(t *testing.T) {
	t.Parallel()
	dev, ch := splitChannelAddress("0001ABCD:NOPE")
	if dev != "0001ABCD:NOPE" || ch != 0 {
		t.Fatalf("got dev=%q ch=%d", dev, ch)
	}
}

func TestScheduleToMapNilReturnsEmpty(t *testing.T) {
	t.Parallel()
	out, err := scheduleToMap(nil)
	if err != nil {
		t.Fatalf("err=%v", err)
	}
	if len(out) != 0 {
		t.Fatalf("expected empty, got %v", out)
	}
}

func TestMapToScheduleRoundTrips(t *testing.T) {
	t.Parallel()
	in := map[string]any{
		"kind":   "climate",
		"domain": "thermostat",
	}
	dto, err := mapToSchedule(in)
	if err != nil {
		t.Fatalf("err=%v", err)
	}
	if dto == nil {
		t.Fatal("dto is nil")
	}
	if dto.Kind != "climate" {
		t.Fatalf("dto.Kind=%q", dto.Kind)
	}
}

func TestNilDomainReturnsError(t *testing.T) {
	t.Parallel()
	a := NewScheduleQueryAdapter(nil)
	if _, err := a.GetDeviceSchedule(t.Context(), "X"); err == nil {
		t.Fatal("expected error for nil domain")
	}
}
