// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package adapter

import (
	"math"
	"testing"
)

func TestClassifyLinkParameter(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		input    string
		category LinkParamCategory
		group    KeypressGroup
		hidden   bool
		percent  bool
		lastVal  bool
		pairID   string
		selector TimeSelectorType
	}{
		{
			name:     "short on-time base",
			input:    "SHORT_ON_TIME_BASE",
			category: LinkParamCategoryTime,
			group:    KeypressGroupShort,
			pairID:   "SHORT_ON_TIME",
			selector: TimeSelectorTimeOnOff,
		},
		{
			name:     "long off-time factor",
			input:    "LONG_OFF_TIME_FACTOR",
			category: LinkParamCategoryTime,
			group:    KeypressGroupLong,
			pairID:   "LONG_OFF_TIME",
			selector: TimeSelectorTimeOnOff,
		},
		{
			name:     "plain ondelay base (common)",
			input:    "ONDELAY_TIME_BASE",
			category: LinkParamCategoryTime,
			group:    KeypressGroupCommon,
			pairID:   "ONDELAY_TIME",
			selector: TimeSelectorDelay,
		},
		{
			name:     "ramp-on factor",
			input:    "RAMP_ON_TIME_FACTOR",
			category: LinkParamCategoryTime,
			group:    KeypressGroupCommon,
			pairID:   "RAMP_ON_TIME",
			selector: TimeSelectorRampOnOff,
		},
		{
			name:     "time base without recognised stem falls back to OTHER selector",
			input:    "WEIRD_TIME_BASE",
			category: LinkParamCategoryTime,
			group:    KeypressGroupCommon,
			pairID:   "WEIRD_TIME",
			selector: "",
		},
		{
			name:     "jump target short",
			input:    "SHORT_JT_ON",
			category: LinkParamCategoryJumpTarget,
			group:    KeypressGroupShort,
			hidden:   true,
		},
		{
			name:     "condition transition long",
			input:    "LONG_CT_OFF",
			category: LinkParamCategoryCondition,
			group:    KeypressGroupLong,
			hidden:   true,
		},
		{
			name:     "level percent short",
			input:    "SHORT_ON_LEVEL",
			category: LinkParamCategoryLevel,
			group:    KeypressGroupShort,
			percent:  true,
			lastVal:  true,
		},
		{
			name:     "plain LEVEL",
			input:    "LEVEL",
			category: LinkParamCategoryLevel,
			group:    KeypressGroupCommon,
			percent:  true,
			lastVal:  true,
		},
		{
			name:     "dim min level",
			input:    "DIM_MIN_LEVEL",
			category: LinkParamCategoryLevel,
			group:    KeypressGroupCommon,
			percent:  true,
			lastVal:  true,
		},
		{
			name:     "multiexecute",
			input:    "MULTIEXECUTE",
			category: LinkParamCategoryAction,
			group:    KeypressGroupCommon,
			hidden:   true,
		},
		{
			name:     "action type long with prefix stem",
			input:    "LONG_COLOR_ACTION_TYPE",
			category: LinkParamCategoryAction,
			group:    KeypressGroupLong,
			hidden:   true,
		},
		{
			name:     "plain ACTION_TYPE (no leading stem) falls through to OTHER",
			input:    "ACTION_TYPE",
			category: LinkParamCategoryOther,
			group:    KeypressGroupCommon,
		},
		{
			name:     "unknown parameter falls through to OTHER",
			input:    "FOO_BAR",
			category: LinkParamCategoryOther,
			group:    KeypressGroupCommon,
		},
		{
			name:     "lowercase input is upper-cased",
			input:    "short_on_time_base",
			category: LinkParamCategoryTime,
			group:    KeypressGroupShort,
			pairID:   "SHORT_ON_TIME",
			selector: TimeSelectorTimeOnOff,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			meta := ClassifyLinkParameter(tc.input)
			if meta.Category != tc.category {
				t.Errorf("category: got %q, want %q", meta.Category, tc.category)
			}
			if meta.KeypressGroup != tc.group {
				t.Errorf("keypress group: got %q, want %q", meta.KeypressGroup, tc.group)
			}
			if meta.HiddenByDefault != tc.hidden {
				t.Errorf("hidden: got %v, want %v", meta.HiddenByDefault, tc.hidden)
			}
			if meta.DisplayAsPercent != tc.percent {
				t.Errorf("percent: got %v, want %v", meta.DisplayAsPercent, tc.percent)
			}
			if meta.HasLastValue != tc.lastVal {
				t.Errorf("has_last_value: got %v, want %v", meta.HasLastValue, tc.lastVal)
			}
			if meta.TimePairID != tc.pairID {
				t.Errorf("time pair id: got %q, want %q", meta.TimePairID, tc.pairID)
			}
			if meta.TimeSelectorType != tc.selector {
				t.Errorf("selector: got %q, want %q", meta.TimeSelectorType, tc.selector)
			}
		})
	}
}

func TestGetTimePresetsLocale(t *testing.T) {
	t.Parallel()
	en := GetTimePresets(TimeSelectorTimeOnOff, "en")
	de := GetTimePresets(TimeSelectorTimeOnOff, "de")
	if len(en) == 0 || len(en) != len(de) {
		t.Fatalf("len mismatch: en=%d de=%d", len(en), len(de))
	}
	if en[0].Label != "Not active" {
		t.Errorf("en[0] label: got %q, want %q", en[0].Label, "Not active")
	}
	if de[0].Label != "Nicht aktiv" {
		t.Errorf("de[0] label: got %q, want %q", de[0].Label, "Nicht aktiv")
	}
	// Permanent is the sentinel at factor=31 in the on/off list.
	last := en[len(en)-1]
	if last.Base != 7 || last.Factor != 31 || last.Label != "Permanent" {
		t.Errorf("last preset mismatch: %+v", last)
	}
	if GetTimePresets("unknown", "en") != nil {
		t.Errorf("unknown selector should return nil")
	}
}

func TestDecodeTimeValue(t *testing.T) {
	t.Parallel()
	cases := []struct {
		base, factor int
		want         float64
	}{
		{0, 1, 0.1},
		{1, 1, 1.0},
		{4, 2, 120.0},
		{7, 24, 86_400.0}, // 24h
		{99, 5, 5.0},      // unknown base → unit 1.0
	}
	for _, tc := range cases {
		got := DecodeTimeValue(tc.base, tc.factor)
		if math.Abs(got-tc.want) > 1e-9 {
			t.Errorf("DecodeTimeValue(%d,%d)=%v want %v", tc.base, tc.factor, got, tc.want)
		}
	}
}

func TestEncodeTimeValueFindsClosestPreset(t *testing.T) {
	t.Parallel()
	// Exact match on 1 s preset.
	b, f := EncodeTimeValue(1.0, TimeSelectorTimeOnOff)
	if b != 1 || f != 1 {
		t.Errorf("1.0s → (%d,%d), want (1,1)", b, f)
	}
	// 25s should snap to 30s preset (base=3, factor=3) in TIME_ON_OFF.
	b, f = EncodeTimeValue(25.0, TimeSelectorTimeOnOff)
	if b != 3 || f != 3 {
		t.Errorf("25.0s → (%d,%d), want (3,3)", b, f)
	}
	// Unknown selector returns zero pair.
	b, f = EncodeTimeValue(10.0, "nope")
	if b != 0 || f != 0 {
		t.Errorf("unknown selector → (%d,%d), want (0,0)", b, f)
	}
}
