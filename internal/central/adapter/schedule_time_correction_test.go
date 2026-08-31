// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package adapter

import (
	"context"
	"strings"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/central/registry"
	"github.com/SukramJ/openccu-loom/internal/client"
	"github.com/SukramJ/openccu-loom/internal/client/backends"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/pkg/hmapi"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
)

// newCorrectionTestDomain builds a real SchedulesDomain over a capturing
// backend, so a test asserts what reached PutParamset rather than what a stub
// was told to answer.
func newCorrectionTestDomain(t *testing.T, name, addr string) (*SchedulesDomain, *climateFieldFilterFakeOperations) {
	t.Helper()
	c, err := central.New(central.Config{Name: name})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	reg := central.NewRegistry()
	if err := reg.Register(c); err != nil {
		t.Fatalf("reg.Register: %v", err)
	}
	dev := device.New(device.Config{
		InterfaceID: "HmIP-RF",
		Interface:   hmenum.InterfaceHmIPRF,
		Address:     addr,
		Model:       "HmIP-eTRV-3",
	})
	dev.AddChannel(addr+":1", 1, "CLIMATECONTROL_RECEIVER", hmenum.ParamsetKeyValues)
	c.ModelRegistry.Put(dev)
	c.DeviceRegistry.Put(registry.DeviceEntry{Interface: wireHmIPRF, Address: addr, Model: "HmIP-eTRV-3"})
	c.DescRegistry.Put(wireHmIPRF, hmproto.DeviceDescription{Address: addr, Type: "HmIP-eTRV-3"})

	// An empty description is the pass-through case: nothing is filtered, so
	// every key the serializer emits reaches the fake untouched.
	fake := &climateFieldFilterFakeOperations{fakeOperations: &fakeOperations{kind: backends.KindCCU}}
	w := client.NewValueWriter()
	w.Register(name, "HmIP-RF", fake)
	return NewSchedulesDomain(reg, w), fake
}

func correctionTestSchedule(end string) *hmapi.ClimateSchedule {
	return &hmapi.ClimateSchedule{
		Kind: "climate",
		Profiles: map[string]hmapi.ClimateProfile{
			"P1": {Weekdays: map[string]hmapi.ClimateWeekday{
				"MONDAY": {
					BaseTemperature: 17.0,
					Periods:         []hmapi.ClimatePeriod{{StartTime: "06:00", EndTime: end, Temperature: 21.0}},
				},
			}},
		},
	}
}

// TestPutClimateScheduleReportsTheClampedEndTime is the guard for the whole
// point of the correction: an end time of 24:30 must reach the device as
// 23:55 AND come back named, so an operator whose schedule was altered can be
// told. Asserting only the wire value would leave the reporting channel free
// to be dropped without a test noticing -- which is how the value would be
// corrected silently, the failure this replaces.
func TestPutClimateScheduleReportsTheClampedEndTime(t *testing.T) {
	t.Parallel()
	sd, fake := newCorrectionTestDomain(t, "ccu-clamp-report1", "DEVCLAMP1")

	corrections, err := sd.PutClimateSchedule(context.Background(), "DEVCLAMP1", 1, correctionTestSchedule("24:30"))
	if err != nil {
		t.Fatalf("PutClimateSchedule: %v", err)
	}

	if len(corrections) != 1 {
		t.Fatalf("corrections = %+v, want exactly one", corrections)
	}
	got := corrections[0]
	want := hmapi.ClimateTimeCorrection{
		Profile: "P1", Weekday: "MONDAY", Period: 0,
		Field: "end_time", Requested: "24:30", Applied: "23:55",
	}
	if got != want {
		t.Errorf("correction = %+v, want %+v", got, want)
	}

	// And the corrected value is what actually reached the wire: 23:55 is
	// 1435 minutes. 1470 would be the uncorrected 24:30, which the read path
	// rejects and drops -- the original defect.
	//
	// The slot ordinal is not asserted: a period starting at 06:00 is preceded
	// by a base stretch, so the period's own end time lands in slot 2, and
	// pinning the ordinal would pin the expansion rather than the correction.
	//
	// Bite proof, stated because it is not the usual one-line kind: the wire
	// value rests on TWO mechanisms that cover for each other -- the grammar's
	// clamp in ParseClimateTimeCorrecting, and the normalizer rewriting the
	// submitted DTO. Removing either alone leaves this assertion green,
	// because the other still produces 1435. Removing both writes 1470 and
	// this fails. The report assertion above is the part carried by the
	// normalizer alone.
	ends := mondayEndTimes(t, fake)
	if len(ends) == 0 {
		t.Fatal("no ENDTIME reached the backend")
	}
	if _, bad := ends[1470]; bad {
		t.Error("ENDTIME 1470 reached the device: the uncorrected 24:30 was written")
	}
	if _, ok := ends[1435]; !ok {
		t.Errorf("ENDTIMEs written = %v, want one of them to be 1435 (23:55)", ends)
	}
}

// mondayEndTimes collects every P<n>_ENDTIME_MONDAY_<i> value the domain
// wrote, keyed by value.
func mondayEndTimes(t *testing.T, fake *climateFieldFilterFakeOperations) map[int]struct{} {
	t.Helper()
	out := map[int]struct{}{}
	for _, call := range fake.putCalls {
		for k, v := range call {
			if !strings.Contains(k, "_ENDTIME_MONDAY_") {
				continue
			}
			n, ok := v.(int)
			if !ok {
				t.Fatalf("%s = %v (%T), want an int", k, v, v)
			}
			out[n] = struct{}{}
		}
	}
	return out
}

// TestPutClimateScheduleReportsNothingForAnOrdinarySchedule is the negative
// control for the guard above. Without it, a normalizer that reported every
// time it inspected -- corrected or not -- would pass the positive case while
// telling the operator their untouched schedule had been altered.
func TestPutClimateScheduleReportsNothingForAnOrdinarySchedule(t *testing.T) {
	t.Parallel()
	sd, _ := newCorrectionTestDomain(t, "ccu-clamp-report2", "DEVCLAMP2")

	corrections, err := sd.PutClimateSchedule(context.Background(), "DEVCLAMP2", 1, correctionTestSchedule("22:00"))
	if err != nil {
		t.Fatalf("PutClimateSchedule: %v", err)
	}
	if len(corrections) != 0 {
		t.Errorf("corrections = %+v, want none for a schedule that needed no correction", corrections)
	}
}

// TestPutClimateScheduleDoesNotCorrectEndOfDay pins the boundary that is
// deliberately not the CCU editor's behaviour: 24:00 is the firmware's own
// weekday terminator, so it must pass through as 1440 and report nothing.
func TestPutClimateScheduleDoesNotCorrectEndOfDay(t *testing.T) {
	t.Parallel()
	sd, fake := newCorrectionTestDomain(t, "ccu-clamp-report3", "DEVCLAMP3")

	corrections, err := sd.PutClimateSchedule(context.Background(), "DEVCLAMP3", 1, correctionTestSchedule("24:00"))
	if err != nil {
		t.Fatalf("PutClimateSchedule: %v", err)
	}
	if len(corrections) != 0 {
		t.Errorf("corrections = %+v, want none: 24:00 is the terminator, not a correctable time", corrections)
	}
	ends := mondayEndTimes(t, fake)
	if _, ok := ends[1440]; !ok {
		t.Errorf("ENDTIMEs written = %v, want the 24:00 terminator to reach the device as 1440", ends)
	}
	if _, bad := ends[1435]; bad {
		t.Error("ENDTIME 1435 reached the device: 24:00 was clamped, destroying the terminator")
	}
}
