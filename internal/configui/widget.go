// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package configui

import (
	"encoding/json"
	"math"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
)

// WidgetType enumerates the UI controls a frontend may render for a
// parameter. Mirrors the reference config panel's WidgetType — string
// values match exactly so the wire shape is portable across language
// boundaries.
type WidgetType string

// WidgetType values.
const (
	WidgetToggle          WidgetType = "toggle"
	WidgetSliderWithInput WidgetType = "slider_with_input"
	WidgetNumberInput     WidgetType = "number_input"
	WidgetRadioGroup      WidgetType = "radio_group"
	WidgetDropdown        WidgetType = "dropdown"
	WidgetTextInput       WidgetType = "text_input"
	WidgetButton          WidgetType = "button"
	WidgetReadOnly        WidgetType = "read_only"
)

// Heuristic thresholds that mirror the reference config panel's constants:
//
//	RADIO_GROUP_THRESHOLD : 4   — enums with ≤ 4 options use radios
//	SLIDER_RANGE_THRESHOLD: 20  — integer ranges ≤ 20 use sliders
//	FLOAT_SLIDER_RANGE    : 100 — float ranges ≤ 100 use sliders
const (
	radioGroupThreshold       = 4
	sliderIntRangeThreshold   = 20
	sliderFloatRangeThreshold = 100.0
)

// DetermineWidget picks an appropriate widget for the given parameter
// descriptor. Mirrors the reference config panel's determine_widget:
//
//   - BOOL                            → TOGGLE
//   - INTEGER (range ≤ 20)            → SLIDER_WITH_INPUT
//   - INTEGER (range > 20 or unknown) → NUMBER_INPUT
//   - FLOAT   (range ≤ 100)           → SLIDER_WITH_INPUT
//   - FLOAT   (range > 100 or unknown)→ NUMBER_INPUT
//   - ENUM    (options ≤ 4)           → RADIO_GROUP
//   - ENUM    (options > 4)           → DROPDOWN
//   - STRING                          → TEXT_INPUT
//   - ACTION                          → BUTTON
//   - everything else                 → READ_ONLY
//
// "Range unknown" means the descriptor lacks Min or Max — the Python
// reference defaults the missing value to 0, which here would lie
// about the actual span; we conservatively return NUMBER_INPUT
// instead so the slider does not look like it covers values it cannot
// reach.
func DetermineWidget(desc hmproto.ParameterData) WidgetType {
	switch desc.Type { //nolint:exhaustive // Dummy and Empty parameter types have no widget representation; callers treat the zero WidgetType as "no widget"
	case hmenum.ParameterTypeBool:
		return WidgetToggle
	case hmenum.ParameterTypeInteger:
		span, ok := numericSpan(desc.Min, desc.Max)
		if ok && span <= sliderIntRangeThreshold {
			return WidgetSliderWithInput
		}
		return WidgetNumberInput
	case hmenum.ParameterTypeFloat:
		span, ok := numericSpan(desc.Min, desc.Max)
		if ok && span <= sliderFloatRangeThreshold {
			return WidgetSliderWithInput
		}
		return WidgetNumberInput
	case hmenum.ParameterTypeEnum:
		if len(desc.ValueList) <= radioGroupThreshold {
			return WidgetRadioGroup
		}
		return WidgetDropdown
	case hmenum.ParameterTypeString:
		return WidgetTextInput
	case hmenum.ParameterTypeAction:
		return WidgetButton
	}
	return WidgetReadOnly
}

// numericSpan returns |max - min| as a float64 plus an ok flag that
// is true only when both bounds parsed cleanly.
func numericSpan(minRaw, maxRaw json.RawMessage) (float64, bool) {
	mn, mnOK := parseNumber(minRaw)
	mx, mxOK := parseNumber(maxRaw)
	if !mnOK || !mxOK {
		return 0, false
	}
	return math.Abs(mx - mn), true
}

func parseNumber(raw json.RawMessage) (float64, bool) {
	if len(raw) == 0 {
		return 0, false
	}
	var n json.Number
	if err := json.Unmarshal(raw, &n); err != nil {
		return 0, false
	}
	v, err := n.Float64()
	if err != nil {
		return 0, false
	}
	return v, true
}
