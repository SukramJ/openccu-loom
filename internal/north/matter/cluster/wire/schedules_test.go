// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package wire_test

import (
	"context"
	"errors"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/north/matter/cluster/wire"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// stubSchedulesSource is a test-double [wire.SchedulesSource] that returns a
// fixed slice of entries. Callers set Entries directly.
type stubSchedulesSource struct {
	Entries []wire.ScheduleEntry
}

func (s *stubSchedulesSource) MatterScheduleEntries() []wire.ScheduleEntry {
	return s.Entries
}

// newSchedulesServer is a test helper that constructs a SchedulesServer
// backed by the provided entries.
func newSchedulesServer(entries []wire.ScheduleEntry) *wire.SchedulesServer {
	return wire.NewSchedulesServer(&stubSchedulesSource{Entries: entries})
}

// TestSchedulesClusterID asserts that the cluster ID is 0x0024 per Matter §11.20.
func TestSchedulesClusterID(t *testing.T) {
	t.Parallel()
	srv := newSchedulesServer(nil)
	if got := srv.MatterClusterID(); got != 0x0024 {
		t.Fatalf("MatterClusterID = 0x%04X, want 0x0024", got)
	}
}

// TestSchedulesClusterRevisionIs1 locks the ClusterRevision to 1.
func TestSchedulesClusterRevisionIs1(t *testing.T) {
	t.Parallel()
	srv := newSchedulesServer(nil)
	v, ok := srv.MatterRead(wire.SchedulesAttrClusterRevision)
	if !ok {
		t.Fatal("ClusterRevision read returned ok=false")
	}
	if got := v.(uint16); got != wire.SchedulesClusterRevision {
		t.Fatalf("ClusterRevision = %d, want %d", got, wire.SchedulesClusterRevision)
	}
}

// TestSchedulesFeatureMapIsZero confirms no optional features are enabled.
func TestSchedulesFeatureMapIsZero(t *testing.T) {
	t.Parallel()
	srv := newSchedulesServer(nil)
	v, ok := srv.MatterRead(wire.SchedulesAttrFeatureMap)
	if !ok {
		t.Fatal("FeatureMap read returned ok=false")
	}
	if got := v.(uint32); got != 0 {
		t.Fatalf("FeatureMap = 0x%08X, want 0x00000000", got)
	}
}

// TestSchedulesNumberOfSchedulesGroupsByDay asserts that NumberOfSchedules
// counts distinct DayOfWeek groups, not raw entry count.
// 14 entries spanning 3 distinct days → NumberOfSchedules = 3.
func TestSchedulesNumberOfSchedulesGroupsByDay(t *testing.T) {
	t.Parallel()
	entries := make14EntriesFor3Days()
	srv := newSchedulesServer(entries)
	v, ok := srv.MatterRead(wire.SchedulesAttrNumberOfSchedules)
	if !ok {
		t.Fatal("NumberOfSchedules read returned ok=false")
	}
	if got := v.(uint8); got != 3 {
		t.Fatalf("NumberOfSchedules = %d, want 3 (14 entries over 3 days)", got)
	}
}

// TestSchedulesNumberOfScheduleTransitionsCountsAllEntries asserts that
// NumberOfScheduleTransitions is the total raw entry count, not the day count.
func TestSchedulesNumberOfScheduleTransitionsCountsAllEntries(t *testing.T) {
	t.Parallel()
	entries := make14EntriesFor3Days()
	srv := newSchedulesServer(entries)
	v, ok := srv.MatterRead(wire.SchedulesAttrNumberOfScheduleTransitions)
	if !ok {
		t.Fatal("NumberOfScheduleTransitions read returned ok=false")
	}
	if got := v.(uint8); got != 14 {
		t.Fatalf("NumberOfScheduleTransitions = %d, want 14", got)
	}
}

// TestSchedulesNumberOfScheduleTransitionsPerDayIsMaxPerGroup asserts the
// per-day maximum (not average, not total): day0=6, day1=4, day2=4 → max=6.
func TestSchedulesNumberOfScheduleTransitionsPerDayIsMaxPerGroup(t *testing.T) {
	t.Parallel()
	entries := make14EntriesFor3Days() // day0=6, day1=4, day2=4
	srv := newSchedulesServer(entries)
	v, ok := srv.MatterRead(wire.SchedulesAttrNumberOfScheduleTransitionsPerDay)
	if !ok {
		t.Fatal("NumberOfScheduleTransitionsPerDay read returned ok=false")
	}
	if got := v.(uint8); got != 6 {
		t.Fatalf("NumberOfScheduleTransitionsPerDay = %d, want 6", got)
	}
}

// TestSchedulesReadSchedulesReturnsGroupedStructs verifies that the Schedules
// attribute returns a non-empty grouped slice when entries are present.
func TestSchedulesReadSchedulesReturnsGroupedStructs(t *testing.T) {
	t.Parallel()
	entries := make14EntriesFor3Days()
	srv := newSchedulesServer(entries)
	v, ok := srv.MatterRead(wire.SchedulesAttrSchedules)
	if !ok {
		t.Fatal("Schedules attribute read returned ok=false")
	}
	groups, ok := v.([]wire.ScheduleStruct)
	if !ok {
		t.Fatalf("Schedules attribute value type = %T, want []wire.ScheduleStruct", v)
	}
	if len(groups) != 3 {
		t.Fatalf("Schedules grouped count = %d, want 3", len(groups))
	}
	// Spot-check: first group is day 0 with 6 transitions.
	if groups[0].DayOfWeek != 0 {
		t.Errorf("groups[0].DayOfWeek = %d, want 0", groups[0].DayOfWeek)
	}
	if len(groups[0].Transitions) != 6 {
		t.Errorf("groups[0] transition count = %d, want 6", len(groups[0].Transitions))
	}
}

// TestSchedulesEmptySourceReturnsZeros verifies that an empty source yields
// zero for all count attributes and (nil, false) for the Schedules array.
func TestSchedulesEmptySourceReturnsZeros(t *testing.T) {
	t.Parallel()
	srv := newSchedulesServer(nil)

	for _, tc := range []struct {
		name   string
		attrID uint32
	}{
		{"NumberOfSchedules", wire.SchedulesAttrNumberOfSchedules},
		{"NumberOfScheduleTransitions", wire.SchedulesAttrNumberOfScheduleTransitions},
		{"NumberOfScheduleTransitionsPerDay", wire.SchedulesAttrNumberOfScheduleTransitionsPerDay},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			v, ok := srv.MatterRead(tc.attrID)
			if !ok {
				t.Fatalf("%s returned ok=false", tc.name)
			}
			if got := v.(uint8); got != 0 {
				t.Fatalf("%s = %d, want 0 for empty source", tc.name, got)
			}
		})
	}

	// Schedules attribute returns nil, false when there are no entries.
	v, ok := srv.MatterRead(wire.SchedulesAttrSchedules)
	if ok || v != nil {
		t.Fatalf("empty Schedules attribute = (%v, %v), want (nil, false)", v, ok)
	}
}

// TestSchedulesWriteIsRejected asserts every write attempt returns
// ErrSchedulesReadOnly.
func TestSchedulesWriteIsRejected(t *testing.T) {
	t.Parallel()
	srv := newSchedulesServer(nil)
	err := srv.MatterWrite(context.Background(), wire.SchedulesAttrSchedules, nil, hmenum.CommandPriorityHigh)
	if !errors.Is(err, wire.ErrSchedulesReadOnly) {
		t.Fatalf("MatterWrite err = %v, want ErrSchedulesReadOnly", err)
	}
}

// TestSchedulesInvokeIsRejected asserts every invoke attempt returns
// ErrSchedulesReadOnly.
func TestSchedulesInvokeIsRejected(t *testing.T) {
	t.Parallel()
	srv := newSchedulesServer(nil)
	_, err := srv.MatterInvoke(context.Background(), 0x00, nil, hmenum.CommandPriorityHigh)
	if !errors.Is(err, wire.ErrSchedulesReadOnly) {
		t.Fatalf("MatterInvoke err = %v, want ErrSchedulesReadOnly", err)
	}
}

// TestSchedulesOptionalHandleAttributesReturnFalse asserts the two optional
// handle attributes (ScheduleProgrammingHandle, NextScheduleHandle) return
// ok=false, consistent with null / not-present semantics per Matter §11.20.5.
func TestSchedulesOptionalHandleAttributesReturnFalse(t *testing.T) {
	t.Parallel()
	srv := newSchedulesServer(nil)
	for _, attrID := range []uint32{
		wire.SchedulesAttrScheduleProgrammingHandle,
		wire.SchedulesAttrNextScheduleHandle,
	} {
		v, ok := srv.MatterRead(attrID)
		if ok || v != nil {
			t.Errorf("optional attr 0x%04X = (%v, %v), want (nil, false)", attrID, v, ok)
		}
	}
}

// TestSchedulesUnknownAttributeReturnsFalse asserts that reads of unknown
// attribute IDs return (nil, false) — the bridge maps this to a Matter
// UNSUPPORTED_ATTRIBUTE status.
func TestSchedulesUnknownAttributeReturnsFalse(t *testing.T) {
	t.Parallel()
	srv := newSchedulesServer(nil)
	v, ok := srv.MatterRead(0x9999)
	if ok || v != nil {
		t.Fatalf("unknown attr = (%v, %v), want (nil, false)", v, ok)
	}
}

// make14EntriesFor3Days returns 14 ScheduleEntry values spread over three
// days: day 0 = 6 entries, day 1 = 4 entries, day 2 = 4 entries. This is the
// canonical test fixture for count + grouping assertions.
func make14EntriesFor3Days() []wire.ScheduleEntry {
	entries := make([]wire.ScheduleEntry, 0, 14)
	// Day 0: 6 transitions at 0, 60, 120, 360, 480, 1320 minutes.
	for _, t := range []uint16{0, 60, 120, 360, 480, 1320} {
		entries = append(entries, wire.ScheduleEntry{DayOfWeek: 0, TransitionTime: t, Setpoint: 21.0})
	}
	// Day 1: 4 transitions.
	for _, t := range []uint16{0, 360, 480, 1320} {
		entries = append(entries, wire.ScheduleEntry{DayOfWeek: 1, TransitionTime: t, Setpoint: 20.0})
	}
	// Day 2: 4 transitions.
	for _, t := range []uint16{0, 360, 480, 1320} {
		entries = append(entries, wire.ScheduleEntry{DayOfWeek: 2, TransitionTime: t, Setpoint: 19.5})
	}
	return entries
}
