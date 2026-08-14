// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

//go:build integration

// End-to-end integration test for the prefix-less ("bare") climate
// schedule schema used by classic BidCos thermostats (HM-CC-RT-DN)
// against the in-process godevccu simulator. HM-CC-RT-DN carries its
// single week profile as bare ENDTIME_/TEMPERATURE_ keys directly in
// the device-level MASTER paramset — no P<n>_ prefix and no dedicated
// schedule channel. The daemon must resolve the schedule to the
// synthetic device-root channel, read it as the single profile P1, and
// write it back with bare keys. If the write emitted the prefixed
// P<n>_ form, godevccu would not surface the change on re-read, so the
// round-trip below is authoritative proof of the correct wire shape.
package integration

import (
	"context"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/central/adapter"
	"github.com/SukramJ/openccu-loom/internal/client"
	"github.com/SukramJ/openccu-loom/internal/client/backends"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

func TestScheduleBareSchemaRoundTrip(t *testing.T) {
	h := newSPAHarness(t, []string{"HM-CC-RT-DN"})
	dev := h.findDevice("HM-CC-RT-DN")

	backend := backends.NewCcuBackend(h.caller, nil, nil)
	w := client.NewValueWriter()
	w.Register(h.central.Name(), hmtypes.ParseWireInterfaceID(dev.InterfaceID), backend)
	reg := central.NewRegistry()
	if err := reg.Register(h.central); err != nil {
		t.Fatalf("registry.Register: %v", err)
	}
	domain := adapter.NewSchedulesDomain(reg, w)

	ctx := context.Background()

	// --- Read: the bare-schema device resolves to a single-profile
	// climate schedule covering all seven weekdays, addressed via the
	// synthetic device-root channel.
	dto, err := domain.GetClimateScheduleAuto(ctx, dev.Address)
	if err != nil {
		t.Fatalf("GetClimateScheduleAuto(%s): %v", dev.Address, err)
	}
	if dto.Kind != "climate" {
		t.Fatalf("kind: got %q, want %q", dto.Kind, "climate")
	}
	if dto.Channel.Number != device.ChannelNumberDevice {
		t.Errorf("channel number: got %d, want device-root %d",
			dto.Channel.Number, device.ChannelNumberDevice)
	}
	if len(dto.Profiles) != 1 {
		t.Errorf("bare device must expose exactly one profile, got %d: %v",
			len(dto.Profiles), dto.Profiles)
	}
	p1, ok := dto.Profiles["P1"]
	if !ok {
		t.Fatalf("bare schedule must map to P1; profiles=%v", dto.Profiles)
	}
	if len(p1.Weekdays) != 7 {
		t.Errorf("expected 7 weekdays in P1, got %d", len(p1.Weekdays))
	}

	// --- Write: set Monday to a flat 19.5 °C and confirm the change
	// round-trips through the bare-key write path.
	mon := p1.Weekdays["MONDAY"]
	mon.BaseTemperature = 19.5
	mon.Periods = nil
	p1.Weekdays["MONDAY"] = mon
	dto.Profiles["P1"] = p1
	if err := domain.PutClimateScheduleAuto(ctx, dev.Address, dto); err != nil {
		t.Fatalf("PutClimateScheduleAuto(%s): %v", dev.Address, err)
	}

	got, err := domain.GetClimateScheduleAuto(ctx, dev.Address)
	if err != nil {
		t.Fatalf("re-read GetClimateScheduleAuto(%s): %v", dev.Address, err)
	}
	gm := got.Profiles["P1"].Weekdays["MONDAY"]
	if gm.BaseTemperature != 19.5 {
		t.Errorf("Monday base after bare write: got %v, want 19.5 "+
			"(a prefixed write would not surface here)", gm.BaseTemperature)
	}
	if len(gm.Periods) != 0 {
		t.Errorf("Monday periods after flat write: got %d, want 0 (%+v)",
			len(gm.Periods), gm.Periods)
	}
}
