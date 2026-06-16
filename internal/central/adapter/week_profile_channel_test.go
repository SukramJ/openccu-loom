// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// week_profile_channel_test.go covers climateChannelLoader.Load and
// climateChannelSaver.Save with un-wired channels (nil refresher and
// nil writer), plus scheduleQueryAdapter nil-channel paths.

package adapter

import (
	"context"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/pkg/hmapi"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// ============================================================
// climateChannelLoader.Load — nil refresher path
// ============================================================

func TestClimateChannelLoaderLoadNilRefresher(t *testing.T) {
	t.Parallel()
	dev := device.New(device.Config{Address: "WPLOAD001", InterfaceID: "HmIP-RF", Model: "HmIP-eTRV-2"})
	ch := dev.AddChannel("WPLOAD001:1", 1, "CLIMATECONTROL_VENT_DRIVE", hmenum.ParamsetKeyValues)
	// No refresher set → returns ErrChannelNotWired
	loader := &climateChannelLoader{ch: ch}
	_, err := loader.Load(context.Background())
	if err == nil {
		t.Fatal("no refresher must return error")
	}
}

// ============================================================
// climateChannelSaver.Save — nil writer path
// ============================================================

func TestClimateChannelSaverSaveNilWriter(t *testing.T) {
	t.Parallel()
	dev := device.New(device.Config{Address: "WPSAVE001", InterfaceID: "HmIP-RF", Model: "HmIP-eTRV-2"})
	ch := dev.AddChannel("WPSAVE001:1", 1, "CLIMATECONTROL_VENT_DRIVE", hmenum.ParamsetKeyValues)
	// No writer set → returns ErrChannelNotWired
	saver := &climateChannelSaver{ch: ch, priority: hmenum.CommandPriorityLow}
	err := saver.Save(context.Background(), nil)
	if err == nil {
		t.Fatal("no writer must return error")
	}
}

// ============================================================
// scheduleToMap / mapToSchedule — additional paths
// ============================================================

func TestScheduleToMapNonNilSchedule(t *testing.T) {
	t.Parallel()
	s := &hmapi.ClimateSchedule{
		Profiles: map[string]hmapi.ClimateProfile{
			"P1": {
				Weekdays: map[string]hmapi.ClimateWeekday{
					"MONDAY": {
						BaseTemperature: 18.0,
						Periods: []hmapi.ClimatePeriod{
							{StartTime: "06:00", EndTime: "22:00", Temperature: 21.5},
						},
					},
				},
			},
		},
	}
	m, err := scheduleToMap(s)
	if err != nil {
		t.Fatalf("scheduleToMap: %v", err)
	}
	if m == nil {
		t.Error("scheduleToMap: expected non-nil map")
	}
	back, err := mapToSchedule(m)
	if err != nil {
		t.Fatalf("mapToSchedule: %v", err)
	}
	if back == nil {
		t.Error("mapToSchedule: got nil schedule")
	}
}
