// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package adapter

import (
	"context"
	"errors"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/pkg/hmapi"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
)

// fixtureBareMonday returns a prefix-less MASTER paramset as carried by
// classic BidCos thermostats (HM-CC-RT-DN): bare ENDTIME_/TEMPERATURE_
// keys with no P<n>_ profile prefix, plus unrelated device-master keys
// (including a TEMPERATURE_ key that is NOT a weekday slot and must be
// ignored by the schedule parser).
func fixtureBareMonday() map[string]any {
	return map[string]any{
		"ENDTIME_MONDAY_1":     360, // 06:00
		"TEMPERATURE_MONDAY_1": 17.0,
		"ENDTIME_MONDAY_2":     1320, // 22:00
		"TEMPERATURE_MONDAY_2": 21.0,
		"ENDTIME_MONDAY_3":     1440, // 24:00
		"TEMPERATURE_MONDAY_3": 17.0,
		// Non-slot TEMPERATURE_ keys must not be mistaken for schedule slots.
		"TEMPERATURE_COMFORT":  21.0,
		"TEMPERATURE_LOWERING": 17.0,
		"GLOBAL_BUTTON_LOCK":   false,
	}
}

// TestParseClimateScheduleBareTreatedAsP1 asserts a prefix-less paramset
// is parsed as the single profile P1.
func TestParseClimateScheduleBareTreatedAsP1(t *testing.T) {
	t.Parallel()
	got, err := parseClimateSchedule(t.Context(), fixtureBareMonday())
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(got.Profiles) != 1 {
		t.Fatalf("expected exactly 1 profile, got %d: %+v", len(got.Profiles), got.Profiles)
	}
	p1, ok := got.Profiles["P1"]
	if !ok {
		t.Fatalf("bare schedule must map to P1, got %+v", got.Profiles)
	}
	if _, ok := p1.Weekdays["MONDAY"]; !ok {
		t.Fatalf("MONDAY missing in %+v", p1.Weekdays)
	}
}

// TestHasScheduleParamsBare asserts the shape check recognises bare keys.
func TestHasScheduleParamsBare(t *testing.T) {
	t.Parallel()
	if !hasScheduleParams(fixtureBareMonday()) {
		t.Error("hasScheduleParams must be true for a bare (prefix-less) schedule")
	}
	// A device-master with only non-slot TEMPERATURE_ keys is not a schedule.
	if hasScheduleParams(map[string]any{"TEMPERATURE_COMFORT": 21.0, "TEMPERATURE_OFFSET": 0}) {
		t.Error("hasScheduleParams must be false when no weekday-slot key is present")
	}
}

// TestSerializeClimateScheduleBareEmitsPrefixlessKeys asserts the bare
// serializer emits ENDTIME_/TEMPERATURE_ keys without any P<n>_ prefix.
func TestSerializeClimateScheduleBareEmitsPrefixlessKeys(t *testing.T) {
	t.Parallel()
	sched := &hmapi.ClimateSchedule{
		Profiles: map[string]hmapi.ClimateProfile{
			"P1": {
				Weekdays: map[string]hmapi.ClimateWeekday{
					"MONDAY": {
						BaseTemperature: 17.0,
						Periods: []hmapi.ClimatePeriod{
							{StartTime: "06:00", EndTime: "22:00", Temperature: 21.0},
						},
					},
				},
			},
		},
	}
	raw, err := serializeClimateScheduleBare(sched)
	if err != nil {
		t.Fatalf("serializeClimateScheduleBare: %v", err)
	}
	if len(raw) == 0 {
		t.Fatal("expected non-empty raw paramset")
	}
	for k := range raw {
		if slotPattern.FindStringSubmatch(k) == nil {
			t.Errorf("emitted key %q does not match the schedule slot pattern", k)
		}
		if len(k) >= 2 && k[0] == 'P' && k[1] >= '1' && k[1] <= '6' {
			t.Errorf("bare serializer must not emit a P<n>_ prefix, got %q", k)
		}
	}
	if _, ok := raw["ENDTIME_MONDAY_1"]; !ok {
		t.Errorf("expected a bare ENDTIME_MONDAY_1 key, got keys %v", keysOf(raw))
	}
}

// TestSerializeClimateScheduleBareRejectsNonP1 asserts a non-P1 profile
// is rejected rather than silently written as a no-op.
func TestSerializeClimateScheduleBareRejectsNonP1(t *testing.T) {
	t.Parallel()
	sched := &hmapi.ClimateSchedule{
		Profiles: map[string]hmapi.ClimateProfile{
			"P2": {Weekdays: map[string]hmapi.ClimateWeekday{
				"MONDAY": {BaseTemperature: 17.0},
			}},
		},
	}
	if _, err := serializeClimateScheduleBare(sched); err == nil {
		t.Error("expected an error when serialising a non-P1 profile to a bare-schema device")
	}
}

// TestClimateScheduleIsBare covers the schema-detection helper.
func TestClimateScheduleIsBare(t *testing.T) {
	t.Parallel()
	set := func(keys ...string) map[string]struct{} {
		m := make(map[string]struct{}, len(keys))
		for _, k := range keys {
			m[k] = struct{}{}
		}
		return m
	}
	cases := []struct {
		name string
		keys map[string]struct{}
		want bool
	}{
		{"bare", set("ENDTIME_MONDAY_1", "TEMPERATURE_MONDAY_1", "TEMPERATURE_COMFORT"), true},
		{"prefixed", set("P1_ENDTIME_MONDAY_1", "P1_TEMPERATURE_MONDAY_1"), false},
		{"mixed prefers prefixed", set("ENDTIME_MONDAY_1", "P2_ENDTIME_MONDAY_1"), false},
		{"empty", set(), false},
		{"nil", nil, false},
		{"no schedule keys", set("TEMPERATURE_COMFORT", "GLOBAL_BUTTON_LOCK"), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := climateScheduleIsBare(tc.keys); got != tc.want {
				t.Errorf("climateScheduleIsBare(%v) = %v, want %v", tc.keys, got, tc.want)
			}
		})
	}
}

// TestScheduleChannelAddress covers the device-root vs numbered-channel
// address mapping.
func TestScheduleChannelAddress(t *testing.T) {
	t.Parallel()
	if got := scheduleChannelAddress("VCU0000050", device.ChannelNumberDevice); got != "VCU0000050" {
		t.Errorf("device-root address: got %q, want %q", got, "VCU0000050")
	}
	if got := scheduleChannelAddress("VCU0000050", 4); got != "VCU0000050:4" {
		t.Errorf("numbered channel address: got %q, want %q", got, "VCU0000050:4")
	}
}

// TestFindScheduleChannelDeviceRootBareSchema asserts the device-root
// probe (Path 3) resolves a bare-schema device to the synthetic device
// channel when no dedicated schedule channel exists.
func TestFindScheduleChannelDeviceRootBareSchema(t *testing.T) {
	t.Parallel()
	// buildScheduleIOFixture registers "0001ABCD" without channels and
	// serves the given raw for every address, including the bare
	// device-root MASTER read that Path 3 performs.
	domain, _ := buildScheduleIOFixture(t, fixtureBareMonday())

	no, err := domain.FindScheduleChannel(context.Background(), "0001ABCD")
	if err != nil {
		t.Fatalf("FindScheduleChannel: %v", err)
	}
	if no != device.ChannelNumberDevice {
		t.Errorf("expected device-root channel %d, got %d", device.ChannelNumberDevice, no)
	}
}

// TestPutClimateScheduleFailsWhenDescriptionUnreadable pins the MASTER
// paramset description as a prerequisite of a schedule write.
//
// The description drives two decisions the surrounding code documents as
// silent CCU-side rejections when they go the wrong way: the bare-vs-prefixed
// schema for classic thermostats, and the filter that strips keys the device
// does not advertise. Treating an unreadable description as "no filtering,
// prefixed schema" therefore wrote a payload the CCU discards while the
// operator was told the schedule saved, complete with an audit row.
func TestPutClimateScheduleFailsWhenDescriptionUnreadable(t *testing.T) {
	t.Parallel()
	domain, backend := buildScheduleIOFixture(t, fixtureBareMonday())
	descErr := errors.New("getParamsetDescription: read timeout")
	backend.getParamsetDescriptionFn = func(_ context.Context, _ string, _ hmenum.ParamsetKey) (map[string]hmproto.ParameterData, error) {
		return nil, descErr
	}

	sched := &hmapi.ClimateSchedule{
		Kind: "climate",
		Profiles: map[string]hmapi.ClimateProfile{
			"P1": {Weekdays: map[string]hmapi.ClimateWeekday{
				"MONDAY": {
					BaseTemperature: 17.0,
					Periods:         []hmapi.ClimatePeriod{{StartTime: "06:00", EndTime: "22:00", Temperature: 21.0}},
				},
			}},
		},
	}
	err := domain.PutClimateSchedule(context.Background(), "0001ABCD", 1, sched)
	if !errors.Is(err, descErr) {
		t.Fatalf("PutClimateSchedule err = %v, want the description read error", err)
	}
	if n := backend.putCallCount(); n != 0 {
		t.Errorf("PutParamset called %d times without a usable description, want 0", n)
	}
}

// keysOf returns the sorted-agnostic key slice of a raw paramset for
// error messages.
func keysOf(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
