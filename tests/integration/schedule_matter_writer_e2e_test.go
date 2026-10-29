// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

//go:build integration

package integration

import (
	"context"
	"reflect"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/central/adapter"
	"github.com/SukramJ/openccu-loom/internal/client"
	"github.com/SukramJ/openccu-loom/internal/client/backends"
	"github.com/SukramJ/openccu-loom/pkg/hmapi"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// TestSchedulesDomainPutClimateProfileWritesOneProgram drives the writer a
// Matter week-program commit goes through (SchedulesDomain.PutClimateProfile)
// against godevccu's HmIP-BWTH: the program written reads back as written,
// in the full-day form the Matter projection hands it, and the program
// next to it is untouched. The thermostat's programs are prefixed
// (P<n>_ENDTIME_…), so the write is also the proof the patch names one
// program's slots only.
func TestSchedulesDomainPutClimateProfileWritesOneProgram(t *testing.T) {
	h := newSPAHarness(t, []string{"HmIP-BWTH"})
	dev := h.findDevice("HmIP-BWTH")

	backend := backends.NewCcuBackend(h.caller, nil, nil)
	w := client.NewValueWriter()
	w.Register(h.central.Name(), hmtypes.ParseWireInterfaceID(dev.InterfaceID), backend)
	reg := central.NewRegistry()
	if err := reg.Register(h.central); err != nil {
		t.Fatalf("registry.Register: %v", err)
	}
	domain := adapter.NewSchedulesDomain(reg, w)
	ctx := context.Background()

	before, err := domain.GetClimateScheduleAuto(ctx, dev.Address)
	if err != nil {
		t.Fatalf("GetClimateScheduleAuto(%s): %v", dev.Address, err)
	}
	if len(before.Profiles) < 2 {
		t.Fatalf("HmIP-BWTH carries %d programs, the test needs two", len(before.Profiles))
	}

	full := func(periods ...hmapi.ClimatePeriod) hmapi.ClimateWeekday {
		return hmapi.ClimateWeekday{BaseTemperature: 18, Periods: periods}
	}
	p2 := hmapi.ClimateProfile{Weekdays: map[string]hmapi.ClimateWeekday{}}
	for _, d := range []string{"MONDAY", "TUESDAY", "WEDNESDAY", "THURSDAY", "FRIDAY", "SATURDAY", "SUNDAY"} {
		p2.Weekdays[d] = full(hmapi.ClimatePeriod{StartTime: "00:00", EndTime: "24:00", Temperature: 18})
	}
	p2.Weekdays["MONDAY"] = full(
		hmapi.ClimatePeriod{StartTime: "00:00", EndTime: "17:00", Temperature: 18},
		hmapi.ClimatePeriod{StartTime: "17:00", EndTime: "24:00", Temperature: 22.5},
	)
	if err := domain.PutClimateProfile(ctx, dev.Address, "P2", p2); err != nil {
		t.Fatalf("PutClimateProfile(P2): %v", err)
	}

	after, err := domain.GetClimateScheduleAuto(ctx, dev.Address)
	if err != nil {
		t.Fatalf("re-read GetClimateScheduleAuto(%s): %v", dev.Address, err)
	}
	mon := after.Profiles["P2"].Weekdays["MONDAY"]
	if mon.BaseTemperature != 18 || len(mon.Periods) != 1 ||
		mon.Periods[0] != (hmapi.ClimatePeriod{StartTime: "17:00", EndTime: "24:00", Temperature: 22.5}) {
		t.Errorf("P2 Monday after the write = %+v, want base 18 with 22.5 °C from 17:00", mon)
	}
	if tue := after.Profiles["P2"].Weekdays["TUESDAY"]; tue.BaseTemperature != 18 || len(tue.Periods) != 0 {
		t.Errorf("P2 Tuesday after the write = %+v, want a flat 18 °C day", tue)
	}
	if !reflect.DeepEqual(after.Profiles["P1"], before.Profiles["P1"]) {
		t.Errorf("P1 changed with a write of P2:\nbefore %+v\nafter  %+v", before.Profiles["P1"], after.Profiles["P1"])
	}
}
