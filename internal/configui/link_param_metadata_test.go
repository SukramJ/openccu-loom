// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package configui

import (
	"testing"
	"time"
)

// --- ClassifyLinkParameter ---

func TestClassifyLinkParameter_TimeBase(t *testing.T) {
	cases := []struct {
		name             string
		wantCategory     LinkParamCategory
		wantKeypressGrp  LinkKeypressGroup
		wantSelectorType TimeSelectorType
	}{
		{"ON_TIME_BASE", LinkParamCategoryTime, LinkKeypressCommon, TimeSelectorTimeOnOff},
		{"OFF_TIME_BASE", LinkParamCategoryTime, LinkKeypressCommon, TimeSelectorTimeOnOff},
		{"ONDELAY_TIME_FACTOR", LinkParamCategoryTime, LinkKeypressCommon, TimeSelectorDelay},
		{"OFFDELAY_TIME_FACTOR", LinkParamCategoryTime, LinkKeypressCommon, TimeSelectorDelay},
		{"RAMP_ON_TIME_BASE", LinkParamCategoryTime, LinkKeypressCommon, TimeSelectorRampOnOff},
		{"RAMPOFF_TIME_FACTOR", LinkParamCategoryTime, LinkKeypressCommon, TimeSelectorRampOnOff},
		{"SHORT_ON_TIME_BASE", LinkParamCategoryTime, LinkKeypressShort, TimeSelectorTimeOnOff},
		{"LONG_RAMP_ON_TIME_BASE", LinkParamCategoryTime, LinkKeypressLong, TimeSelectorRampOnOff},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			meta := ClassifyLinkParameter(tc.name)
			if meta.Category != tc.wantCategory {
				t.Errorf("category=%s want %s", meta.Category, tc.wantCategory)
			}
			if meta.KeypressGroup != tc.wantKeypressGrp {
				t.Errorf("keypress_group=%s want %s", meta.KeypressGroup, tc.wantKeypressGrp)
			}
			if meta.TimeSelectorType != tc.wantSelectorType {
				t.Errorf("time_selector_type=%s want %s", meta.TimeSelectorType, tc.wantSelectorType)
			}
			if meta.TimePairID == "" {
				t.Errorf("time_pair_id must not be empty for time parameters")
			}
		})
	}
}

func TestClassifyLinkParameter_JumpTarget(t *testing.T) {
	for _, name := range []string{"JT_ON", "JT_OFF", "JT_ONDELAY", "SHORT_JT_ON"} {
		t.Run(name, func(t *testing.T) {
			meta := ClassifyLinkParameter(name)
			if meta.Category != LinkParamCategoryJumpTarget {
				t.Errorf("category=%s want jump_target", meta.Category)
			}
			if !meta.HiddenByDefault {
				t.Errorf("jump targets must be hidden_by_default")
			}
		})
	}
}

func TestClassifyLinkParameter_Condition(t *testing.T) {
	for _, name := range []string{"CT_ON", "CT_OFF", "SHORT_CT_RAMPOFF"} {
		t.Run(name, func(t *testing.T) {
			meta := ClassifyLinkParameter(name)
			if meta.Category != LinkParamCategoryCondition {
				t.Errorf("category=%s want condition", meta.Category)
			}
			if !meta.HiddenByDefault {
				t.Errorf("condition params must be hidden_by_default")
			}
		})
	}
}

func TestClassifyLinkParameter_Level(t *testing.T) {
	for _, name := range []string{"LEVEL", "DIM_MIN_LEVEL", "SHORT_LEVEL"} {
		t.Run(name, func(t *testing.T) {
			meta := ClassifyLinkParameter(name)
			if meta.Category != LinkParamCategoryLevel {
				t.Errorf("category=%s want level", meta.Category)
			}
			if !meta.DisplayAsPercent {
				t.Errorf("level params must have display_as_percent")
			}
		})
	}
}

func TestClassifyLinkParameter_Action(t *testing.T) {
	// MULTIEXECUTE (suffix == "MULTIEXECUTE") and names that end with
	// _MULTIEXECUTE classify as action. _ACTION_TYPE requires a prefix
	// before the underscore (e.g. "SOME_ACTION_TYPE").
	// Verified against Python: SHORT_ACTION_TYPE → other (suffix "ACTION_TYPE"
	// does not end with "_ACTION_TYPE"); MULTIEXECUTE → action.
	actionCases := []string{"MULTIEXECUTE", "SHORT_MULTIEXECUTE", "SOME_ACTION_TYPE"}
	for _, name := range actionCases {
		t.Run(name, func(t *testing.T) {
			meta := ClassifyLinkParameter(name)
			if meta.Category != LinkParamCategoryAction {
				t.Errorf("category=%s want action", meta.Category)
			}
			if !meta.HiddenByDefault {
				t.Errorf("action params must be hidden_by_default")
			}
		})
	}
}

func TestClassifyLinkParameter_ActionBareActionTypeFallsToOther(t *testing.T) {
	// Python: ACTION_TYPE → other, SHORT_ACTION_TYPE → other.
	// suffix "ACTION_TYPE" does not end with "_ACTION_TYPE".
	for _, name := range []string{"ACTION_TYPE", "SHORT_ACTION_TYPE"} {
		t.Run(name, func(t *testing.T) {
			meta := ClassifyLinkParameter(name)
			if meta.Category != LinkParamCategoryOther {
				t.Errorf("%s: category=%s want other", name, meta.Category)
			}
		})
	}
}

func TestClassifyLinkParameter_Other(t *testing.T) {
	meta := ClassifyLinkParameter("SOME_UNKNOWN_PARAM")
	if meta.Category != LinkParamCategoryOther {
		t.Errorf("category=%s want other", meta.Category)
	}
}

func TestClassifyLinkParameter_LowercaseInput(t *testing.T) {
	meta := ClassifyLinkParameter("short_jt_on")
	if meta.Category != LinkParamCategoryJumpTarget {
		t.Errorf("lowercase input must be normalised: category=%s want jump_target", meta.Category)
	}
	if meta.KeypressGroup != LinkKeypressShort {
		t.Errorf("lowercase input: keypress=%s want short", meta.KeypressGroup)
	}
}

// --- GetTimePresets ---

func TestGetTimePresets_UnknownSelectorTypeReturnsNil(t *testing.T) {
	if got := GetTimePresets(TimeSelectorUnknown, "en"); got != nil {
		t.Fatalf("unknown type must return nil, got %v", got)
	}
}

func TestGetTimePresets_LocaleDE(t *testing.T) {
	presets := GetTimePresets(TimeSelectorTimeOnOff, "de")
	if len(presets) == 0 {
		t.Fatal("expected presets for TIME_ON_OFF")
	}
	// First preset is "Nicht aktiv" in German.
	if presets[0].Label != "Nicht aktiv" {
		t.Errorf("first preset label=%q want Nicht aktiv", presets[0].Label)
	}
}

func TestGetTimePresets_LocaleEN(t *testing.T) {
	presets := GetTimePresets(TimeSelectorTimeOnOff, "en")
	if presets[0].Label != "Not active" {
		t.Errorf("first preset label=%q want Not active", presets[0].Label)
	}
}

func TestGetTimePresets_DelayCount(t *testing.T) {
	if len(GetTimePresets(TimeSelectorDelay, "en")) != len(delayPresets) {
		t.Errorf("delay preset count mismatch")
	}
}

// --- DecodeTimeValue ---

func TestDecodeTimeValue_RoundTrips(t *testing.T) {
	cases := []struct {
		base, factor int
		wantSeconds  float64
	}{
		// base=0 → 0.1s/unit: factor=1 → 100ms
		{0, 1, 0.1},
		// base=1 → 1s/unit: factor=1 → 1s
		{1, 1, 1.0},
		// base=4 → 60s/unit: factor=1 → 60s = 1min
		{4, 1, 60.0},
		// base=7 → 3600s/unit: factor=1 → 3600s = 1h
		{7, 1, 3600.0},
		// base=0, factor=0 → 0s (not active)
		{0, 0, 0.0},
	}
	for _, tc := range cases {
		d := DecodeTimeValue(tc.base, tc.factor)
		got := d.Seconds()
		if got != tc.wantSeconds {
			t.Errorf("DecodeTimeValue(%d,%d)=%v want %vs", tc.base, tc.factor, d, tc.wantSeconds)
		}
	}
}

func TestDecodeTimeValue_UnknownBaseDefaultsTo1sUnit(t *testing.T) {
	// base=99 is not in the map; should treat unit as 1.0.
	d := DecodeTimeValue(99, 5)
	if d != 5*time.Second {
		t.Errorf("unknown base: got %v want 5s", d)
	}
}

// --- EncodeTimeValue ---

func TestEncodeTimeValue_ExactMatches(t *testing.T) {
	cases := []struct {
		d            time.Duration
		selectorType TimeSelectorType
		wantBase     int
		wantFactor   int
	}{
		// 0.1s → base=0, factor=1 (timeOnOff)
		{100 * time.Millisecond, TimeSelectorTimeOnOff, 0, 1},
		// 1s → base=1, factor=1 (timeOnOff)
		{time.Second, TimeSelectorTimeOnOff, 1, 1},
		// 60s → base=4, factor=1 (timeOnOff)
		{time.Minute, TimeSelectorTimeOnOff, 4, 1},
		// 3600s → base=7, factor=1 (timeOnOff)
		{time.Hour, TimeSelectorTimeOnOff, 7, 1},
		// 5s → base=2, factor=1 (delay — present in delay list)
		{5 * time.Second, TimeSelectorDelay, 2, 1},
	}
	for _, tc := range cases {
		b, f := EncodeTimeValue(tc.d, tc.selectorType)
		if b != tc.wantBase || f != tc.wantFactor {
			t.Errorf("EncodeTimeValue(%v, %s)=(%d,%d) want (%d,%d)",
				tc.d, tc.selectorType, b, f, tc.wantBase, tc.wantFactor)
		}
	}
}

func TestEncodeTimeValue_RoundTrip(t *testing.T) {
	// For each preset list, encode(decode(b,f), selectorType) must reproduce (b,f).
	typedPresets := []struct {
		selectorType TimeSelectorType
		presets      []TimeBasePreset
	}{
		{TimeSelectorTimeOnOff, timeOnOffPresets},
		{TimeSelectorDelay, delayPresets},
		{TimeSelectorRampOnOff, rampOnOffPresets},
	}
	for _, tp := range typedPresets {
		for _, p := range tp.presets {
			d := DecodeTimeValue(p.BaseVal, p.FactorVal)
			b, f := EncodeTimeValue(d, tp.selectorType)
			if b != p.BaseVal || f != p.FactorVal {
				t.Errorf("%s round-trip fail for (%d,%d): got (%d,%d) via %v",
					tp.selectorType, p.BaseVal, p.FactorVal, b, f, d)
			}
		}
	}
}
