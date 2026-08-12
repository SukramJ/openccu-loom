// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package adapter

import (
	"fmt"
	"sort"
	"strconv"
	"testing"

	schedulemodel "github.com/SukramJ/openccu-loom/internal/model/schedule"
	"github.com/SukramJ/openccu-loom/internal/model/weekprofile"
)

// The `<NN>_WP_<FIELD>` wire format has one translation, shared by the
// REST/WS schedules domain (which speaks [hmapi.SimpleScheduleEntry])
// and the week-profile model (which speaks [schedulemodel.Simple]).
//
// It used to have two, written in parallel and never reconciled. Every
// defect in the format had to be found and fixed twice, and each time
// one half was missed: Sunday sat on bit 7 in both tables, the group
// limit was enforced on write but not on read, and the two disagreed on
// six of the eight condition names. Reading a lock schedule through the
// week-profile path dropped every slot the CCU encodes with the
// "permanent" duration sentinel — which is every door-lock action bar
// one, and every user-permission slot.
//
// These tests feed one raw paramset through both surfaces and assert
// they agree, in both directions. They are the guard that keeps the
// second table from growing back: a divergence here means someone
// added a translation instead of using the one in
// [weekprofile.ParseSimpleRawParamset] / [weekprofile.BuildSimpleRawParamset].

// wireEntry is the comparable projection of one schedule entry, reached
// from either surface. Slot number included so ordering differences
// surface as content differences rather than as index shifts.
type wireEntry struct {
	slot            int
	weekdays        string
	time            string
	condition       string
	astroType       string
	astroOffset     int
	targetChannels  string
	level           float64
	level2          string
	duration        string
	rampTime        string
	colorType       string
	colorValue      string
	outputBehaviour string
}

func optInt(p *int) string {
	if p == nil {
		return "-"
	}
	return strconv.Itoa(*p)
}

func optFloat(p *float64) string {
	if p == nil {
		return "-"
	}
	return fmt.Sprintf("%v", *p)
}

func joinSortedWire(in []string) string {
	cp := append([]string(nil), in...)
	sort.Strings(cp)
	return fmt.Sprintf("%v", cp)
}

// wireEntriesFromREST projects the REST/WS surface onto [wireEntry].
func wireEntriesFromREST(raw map[string]any) []wireEntry {
	entries := parseSimpleSchedule(raw)
	out := make([]wireEntry, 0, len(entries))
	for i := range entries {
		e := entries[i]
		out = append(out, wireEntry{
			slot:            e.SlotNo,
			weekdays:        joinSortedWire(e.Weekdays),
			time:            e.Time,
			condition:       e.Condition,
			astroType:       e.AstroType,
			astroOffset:     e.AstroOffsetMinutes,
			targetChannels:  joinSortedWire(e.TargetChannels),
			level:           e.Level,
			level2:          optFloat(e.Level2),
			duration:        e.Duration,
			rampTime:        e.RampTime,
			colorType:       optInt(e.ColorType),
			colorValue:      optInt(e.ColorValue),
			outputBehaviour: optInt(e.OutputBehaviour),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].slot < out[j].slot })
	return out
}

// wireEntriesFromWeekProfile projects the week-profile model onto [wireEntry].
func wireEntriesFromWeekProfile(t *testing.T, raw map[string]any) []wireEntry {
	t.Helper()
	s, err := weekprofile.ParseSimpleRawParamset(raw)
	if err != nil {
		t.Fatalf("ParseSimpleRawParamset: %v", err)
	}
	out := make([]wireEntry, 0, len(s.Entries))
	for _, slot := range s.Slots() {
		e := s.Entries[slot]
		days := make([]string, 0, len(e.Weekdays))
		for _, d := range e.Weekdays {
			days = append(days, string(d))
		}
		out = append(out, wireEntry{
			slot:            slot,
			weekdays:        joinSortedWire(days),
			time:            e.Time,
			condition:       string(e.Condition),
			astroType:       string(e.AstroType),
			astroOffset:     e.AstroOffsetMinutes,
			targetChannels:  joinSortedWire(e.TargetChannels),
			level:           e.Level,
			level2:          optFloat(e.Level2),
			duration:        e.Duration,
			rampTime:        e.RampTime,
			colorType:       optInt(e.ColorType),
			colorValue:      optInt(e.ColorValue),
			outputBehaviour: optInt(e.OutputBehaviour),
		})
	}
	return out
}

// wireParityCases covers every field the format carries, plus the shapes
// that historically diverged: astro conditions, the lock domain's
// duration sentinel, colour fields, groups past 24, and inactive groups.
func wireParityCases() []struct {
	name string
	raw  map[string]any
} {
	return []struct {
		name string
		raw  map[string]any
	}{
		{
			name: "plain switch slot",
			raw: map[string]any{
				"01_WP_WEEKDAY":         127,
				"01_WP_FIXED_HOUR":      7,
				"01_WP_FIXED_MINUTE":    30,
				"01_WP_LEVEL":           1.0,
				"01_WP_CONDITION":       0,
				"01_WP_TARGET_CHANNELS": 1,
			},
		},
		{
			name: "sunday only",
			raw: map[string]any{
				"01_WP_WEEKDAY":      1,
				"01_WP_FIXED_HOUR":   22,
				"01_WP_FIXED_MINUTE": 0,
				"01_WP_LEVEL":        0.0,
			},
		},
		{
			name: "every astro condition",
			raw: func() map[string]any {
				raw := map[string]any{}
				for id := range 8 {
					p := fmt.Sprintf("%02d_WP_", id+1)
					raw[p+"WEEKDAY"] = 2
					raw[p+"FIXED_HOUR"] = 6
					raw[p+"FIXED_MINUTE"] = 15
					raw[p+"LEVEL"] = 1.0
					raw[p+"CONDITION"] = id
					raw[p+"ASTRO_TYPE"] = id % 2
					raw[p+"ASTRO_OFFSET"] = -30 + id
				}
				return raw
			}(),
		},
		{
			name: "every time base for duration and ramp",
			raw: func() map[string]any {
				raw := map[string]any{}
				for base := range 8 {
					p := fmt.Sprintf("%02d_WP_", base+1)
					raw[p+"WEEKDAY"] = 127
					raw[p+"FIXED_HOUR"] = 12
					raw[p+"FIXED_MINUTE"] = 0
					raw[p+"LEVEL"] = 1.0
					raw[p+"DURATION_BASE"] = base
					raw[p+"DURATION_FACTOR"] = 12
					raw[p+"RAMP_TIME_BASE"] = base
					raw[p+"RAMP_TIME_FACTOR"] = 3
				}
				return raw
			}(),
		},
		{
			name: "lock duration sentinel",
			raw: map[string]any{
				"01_WP_WEEKDAY":         127,
				"01_WP_FIXED_HOUR":      8,
				"01_WP_FIXED_MINUTE":    0,
				"01_WP_LEVEL":           1.0,
				"01_WP_DURATION_BASE":   7,
				"01_WP_DURATION_FACTOR": 31,
				"01_WP_TARGET_CHANNELS": 1,
			},
		},
		{
			name: "cover slat level_2",
			raw: map[string]any{
				"01_WP_WEEKDAY":      64,
				"01_WP_FIXED_HOUR":   9,
				"01_WP_FIXED_MINUTE": 45,
				"01_WP_LEVEL":        0.5,
				"01_WP_LEVEL_2":      0.25,
			},
		},
		{
			name: "universal light colour fields",
			raw: map[string]any{
				"01_WP_WEEKDAY":    4,
				"01_WP_FIXED_HOUR": 20,
				"01_WP_LEVEL":      0.8,
				"01_WP_HUE_SATURATION_COLOR_TEMPERATURE_EFFECT_TYPE":  0,
				"01_WP_HUE_SATURATION_COLOR_TEMPERATURE_EFFECT_VALUE": 12345,
				"01_WP_OUTPUT_BEHAVIOUR":                              2,
			},
		},
		{
			name: "groups past 24",
			raw: func() map[string]any {
				raw := map[string]any{}
				for _, no := range []int{1, 24, 25, 40, 69, 75} {
					p := fmt.Sprintf("%02d_WP_", no)
					raw[p+"WEEKDAY"] = 127
					raw[p+"FIXED_HOUR"] = no % 24
					raw[p+"FIXED_MINUTE"] = 0
					raw[p+"LEVEL"] = 1.0
				}
				return raw
			}(),
		},
		{
			name: "inactive groups are skipped",
			raw: map[string]any{
				"01_WP_WEEKDAY":      0,
				"01_WP_FIXED_HOUR":   7,
				"01_WP_LEVEL":        1.0,
				"02_WP_WEEKDAY":      8,
				"02_WP_FIXED_HOUR":   7,
				"02_WP_FIXED_MINUTE": 5,
				"02_WP_LEVEL":        1.0,
			},
		},
		{
			name: "all target channels",
			raw: map[string]any{
				"01_WP_WEEKDAY":         127,
				"01_WP_FIXED_HOUR":      1,
				"01_WP_LEVEL":           1.0,
				"01_WP_TARGET_CHANNELS": (1 << 24) - 1,
			},
		},
	}
}

// TestSimpleScheduleReadAgreesAcrossSurfaces asserts that reading the
// same MASTER paramset through the REST schedules domain and through
// the week-profile model yields the same entries.
func TestSimpleScheduleReadAgreesAcrossSurfaces(t *testing.T) {
	t.Parallel()

	for _, tc := range wireParityCases() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			rest := wireEntriesFromREST(tc.raw)
			wp := wireEntriesFromWeekProfile(t, tc.raw)

			if len(rest) != len(wp) {
				t.Fatalf("entry count differs: REST %d, week-profile %d\nREST: %+v\nWP:   %+v",
					len(rest), len(wp), rest, wp)
			}
			for i := range rest {
				if rest[i] != wp[i] {
					t.Errorf("entry %d differs:\nREST: %+v\nWP:   %+v", i, rest[i], wp[i])
				}
			}
		})
	}
}

// TestSimpleScheduleWriteAgreesAcrossSurfaces asserts that serialising
// the same entries through either surface produces the same paramset.
// The entries come from a read of the case's own paramset, so a
// divergence found here is one the daemon can actually reach.
func TestSimpleScheduleWriteAgreesAcrossSurfaces(t *testing.T) {
	t.Parallel()

	for _, tc := range wireParityCases() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			restEntries := parseSimpleSchedule(tc.raw)
			restRaw, err := serializeSimpleSchedule(restEntries, 0)
			if err != nil {
				t.Fatalf("serializeSimpleSchedule: %v", err)
			}

			s, err := weekprofile.ParseSimpleRawParamset(tc.raw)
			if err != nil {
				t.Fatalf("ParseSimpleRawParamset: %v", err)
			}
			wpRaw, err := weekprofile.BuildSimpleRawParamset(s, 0)
			if err != nil {
				t.Fatalf("BuildSimpleRawParamset: %v", err)
			}

			assertParamsetsEqual(t, restRaw, wpRaw)
		})
	}
}

// TestSimpleScheduleDeactivationSweepAgrees pins the bounded sweep that
// clears deleted entries. The bound comes from the device: naming a
// group a channel does not declare fails the whole putParamset with
// fault -5, and a bound of 0 means "device unknown" — write the active
// groups, sweep nothing.
func TestSimpleScheduleDeactivationSweepAgrees(t *testing.T) {
	t.Parallel()

	raw := map[string]any{
		"03_WP_WEEKDAY":      127,
		"03_WP_FIXED_HOUR":   6,
		"03_WP_FIXED_MINUTE": 0,
		"03_WP_LEVEL":        1.0,
	}
	for _, bound := range []int{0, 1, 24, 69, 75} {
		t.Run(fmt.Sprintf("bound_%d", bound), func(t *testing.T) {
			t.Parallel()

			restRaw, err := serializeSimpleSchedule(parseSimpleSchedule(raw), bound)
			if err != nil {
				t.Fatalf("serializeSimpleSchedule: %v", err)
			}
			s, err := weekprofile.ParseSimpleRawParamset(raw)
			if err != nil {
				t.Fatalf("ParseSimpleRawParamset: %v", err)
			}
			wpRaw, err := weekprofile.BuildSimpleRawParamset(s, bound)
			if err != nil {
				t.Fatalf("BuildSimpleRawParamset: %v", err)
			}
			assertParamsetsEqual(t, restRaw, wpRaw)
		})
	}
}

// assertParamsetsEqual compares two flat MASTER paramsets key by key,
// reporting every difference rather than the first.
func assertParamsetsEqual(t *testing.T, rest, wp map[string]any) {
	t.Helper()

	keys := map[string]struct{}{}
	for k := range rest {
		keys[k] = struct{}{}
	}
	for k := range wp {
		keys[k] = struct{}{}
	}
	ordered := make([]string, 0, len(keys))
	for k := range keys {
		ordered = append(ordered, k)
	}
	sort.Strings(ordered)

	for _, k := range ordered {
		rv, rok := rest[k]
		wv, wok := wp[k]
		switch {
		case rok && !wok:
			t.Errorf("%s: REST wrote %v, week-profile wrote nothing", k, rv)
		case !rok && wok:
			t.Errorf("%s: week-profile wrote %v, REST wrote nothing", k, wv)
		case fmt.Sprintf("%v", rv) != fmt.Sprintf("%v", wv):
			t.Errorf("%s: REST %v, week-profile %v", k, rv, wv)
		}
	}
}

// TestSimpleScheduleWeekdayMaskAgrees walks all 128 masks through both
// surfaces. Sunday on the wrong bit was present in both tables and cost
// a release; the exhaustive sweep is cheap enough to keep.
func TestSimpleScheduleWeekdayMaskAgrees(t *testing.T) {
	t.Parallel()

	for mask := 1; mask < 128; mask++ {
		raw := map[string]any{
			"01_WP_WEEKDAY":      mask,
			"01_WP_FIXED_HOUR":   12,
			"01_WP_FIXED_MINUTE": 0,
			"01_WP_LEVEL":        1.0,
		}
		rest := wireEntriesFromREST(raw)
		wp := wireEntriesFromWeekProfile(t, raw)
		if len(rest) != 1 || len(wp) != 1 {
			t.Fatalf("mask %d: REST %d entries, week-profile %d entries", mask, len(rest), len(wp))
		}
		if rest[0].weekdays != wp[0].weekdays {
			t.Errorf("mask %d: REST %s, week-profile %s", mask, rest[0].weekdays, wp[0].weekdays)
		}
	}
}

// TestSimpleScheduleConditionRoundTripAgrees pins both directions of the
// condition translation across the surfaces: the names must match, and
// re-serialising must select the integer the name promises.
func TestSimpleScheduleConditionRoundTripAgrees(t *testing.T) {
	t.Parallel()

	for id := range 8 {
		raw := map[string]any{
			"01_WP_WEEKDAY":      127,
			"01_WP_FIXED_HOUR":   6,
			"01_WP_FIXED_MINUTE": 0,
			"01_WP_LEVEL":        1.0,
			"01_WP_CONDITION":    id,
			"01_WP_ASTRO_TYPE":   1,
		}
		restEntries := parseSimpleSchedule(raw)
		if len(restEntries) != 1 {
			t.Fatalf("condition %d: REST parsed %d entries", id, len(restEntries))
		}
		s, err := weekprofile.ParseSimpleRawParamset(raw)
		if err != nil {
			t.Fatalf("condition %d: ParseSimpleRawParamset: %v", id, err)
		}
		if len(s.Entries) != 1 {
			t.Fatalf("condition %d: week-profile parsed %d entries", id, len(s.Entries))
		}
		if got, want := restEntries[0].Condition, string(s.Entries[1].Condition); got != want {
			t.Errorf("condition %d: REST %q, week-profile %q", id, got, want)
		}
		if got := weekprofile.WireForCondition(schedulemodel.Condition(restEntries[0].Condition)); got != id {
			t.Errorf("condition %d: re-serialises as %d", id, got)
		}
	}
}
