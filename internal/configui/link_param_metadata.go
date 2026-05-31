// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package configui

import (
	"math"
	"strings"
	"time"
)

// LinkParamCategory describes the functional role of a LINK paramset
// parameter. Mirrors the reference config panel's LinkParamCategory.
type LinkParamCategory string

// LinkParamCategory values.
const (
	LinkParamCategoryTime       LinkParamCategory = "time"
	LinkParamCategoryLevel      LinkParamCategory = "level"
	LinkParamCategoryJumpTarget LinkParamCategory = "jump_target"
	LinkParamCategoryCondition  LinkParamCategory = "condition"
	LinkParamCategoryAction     LinkParamCategory = "action"
	LinkParamCategoryOther      LinkParamCategory = "other"
)

// LinkKeypressGroup identifies whether a link parameter applies to
// SHORT presses, LONG presses, or both (COMMON). Mirrors the reference
// config panel's KeypressGroup.
type LinkKeypressGroup string

// LinkKeypressGroup values.
const (
	LinkKeypressShort  LinkKeypressGroup = "short"
	LinkKeypressLong   LinkKeypressGroup = "long"
	LinkKeypressCommon LinkKeypressGroup = "common"
)

// TimeSelectorType identifies the preset list to offer for a time-
// valued LINK parameter. Mirrors the reference config panel's
// TimeSelectorType.
type TimeSelectorType string

// TimeSelectorType values.
const (
	TimeSelectorTimeOnOff TimeSelectorType = "timeOnOff"
	TimeSelectorDelay     TimeSelectorType = "delay"
	TimeSelectorRampOnOff TimeSelectorType = "rampOnOff"
	TimeSelectorUnknown   TimeSelectorType = ""
)

// TimeBasePreset is one entry in a preset list for a time-valued
// LINK parameter. BaseVal and FactorVal are the raw CCU integer values;
// LabelEN / LabelDE are the display strings. Mirrors the reference
// config panel's TimePreset.
type TimeBasePreset struct {
	BaseVal   int
	FactorVal int
	LabelEN   string
	LabelDE   string
}

// LinkParamMetadata is the classification of a single LINK parameter
// as returned by [ClassifyLinkParameter]. Mirrors the reference config
// panel's LinkParamMeta.
type LinkParamMetadata struct {
	Category         LinkParamCategory
	KeypressGroup    LinkKeypressGroup
	DisplayAsPercent bool
	HasLastValue     bool
	HiddenByDefault  bool
	TimePairID       string
	TimeSelectorType TimeSelectorType
}

// timeBaseUnits maps a TIME_BASE integer value to its duration
// multiplier. Mirrors the reference config panel's _TIME_BASE_UNITS.
var timeBaseUnits = map[int]float64{
	0: 0.1,
	1: 1.0,
	2: 5.0,
	3: 10.0,
	4: 60.0,
	5: 300.0,
	6: 600.0,
	7: 3600.0,
}

var timeOnOffPresets = []TimeBasePreset{
	{0, 0, "Not active", "Nicht aktiv"},
	{0, 1, "100 ms", "100 ms"},
	{1, 1, "1 s", "1 s"},
	{1, 2, "2 s", "2 s"},
	{1, 3, "3 s", "3 s"},
	{2, 1, "5 s", "5 s"},
	{3, 1, "10 s", "10 s"},
	{3, 3, "30 s", "30 s"},
	{4, 1, "1 min", "1 min"},
	{4, 2, "2 min", "2 min"},
	{5, 1, "5 min", "5 min"},
	{6, 1, "10 min", "10 min"},
	{6, 3, "30 min", "30 min"},
	{7, 1, "1 h", "1 h"},
	{7, 2, "2 h", "2 h"},
	{7, 3, "3 h", "3 h"},
	{7, 5, "5 h", "5 h"},
	{7, 8, "8 h", "8 h"},
	{7, 12, "12 h", "12 h"},
	{7, 24, "24 h", "24 h"},
	{7, 31, "Permanent", "Permanent"},
}

var delayPresets = []TimeBasePreset{
	{0, 0, "Not active", "Nicht aktiv"},
	{2, 1, "5 s", "5 s"},
	{3, 1, "10 s", "10 s"},
	{3, 3, "30 s", "30 s"},
	{4, 1, "1 min", "1 min"},
	{4, 2, "2 min", "2 min"},
	{5, 1, "5 min", "5 min"},
	{6, 1, "10 min", "10 min"},
	{6, 3, "30 min", "30 min"},
	{7, 1, "1 h", "1 h"},
}

var rampOnOffPresets = []TimeBasePreset{
	{0, 0, "Not active", "Nicht aktiv"},
	{0, 2, "200 ms", "200 ms"},
	{0, 5, "500 ms", "500 ms"},
	{1, 1, "1 s", "1 s"},
	{1, 2, "2 s", "2 s"},
	{1, 5, "5 s", "5 s"},
	{1, 10, "10 s", "10 s"},
	{1, 20, "20 s", "20 s"},
	{1, 30, "30 s", "30 s"},
}

var presetsByType = map[TimeSelectorType][]TimeBasePreset{
	TimeSelectorTimeOnOff: timeOnOffPresets,
	TimeSelectorDelay:     delayPresets,
	TimeSelectorRampOnOff: rampOnOffPresets,
}

// timeTypeMap maps a time-stem suffix to its TimeSelectorType.
// Mirrors the reference config panel's _TIME_TYPE_MAP.
var timeTypeMap = map[string]TimeSelectorType{
	"ON_TIME":        TimeSelectorTimeOnOff,
	"OFF_TIME":       TimeSelectorTimeOnOff,
	"ONDELAY_TIME":   TimeSelectorDelay,
	"OFFDELAY_TIME":  TimeSelectorDelay,
	"ON_DELAY_TIME":  TimeSelectorDelay,
	"OFF_DELAY_TIME": TimeSelectorDelay,
	"RAMP_ON_TIME":   TimeSelectorRampOnOff,
	"RAMP_OFF_TIME":  TimeSelectorRampOnOff,
	"RAMPON_TIME":    TimeSelectorRampOnOff,
	"RAMPOFF_TIME":   TimeSelectorRampOnOff,
}

// levelSuffixes are the parameter name endings that indicate a level
// (brightness/position) parameter.
var levelSuffixes = []string{"_LEVEL", "_DIM_MIN_LEVEL", "_DIM_MAX_LEVEL"}

// actionSuffixes are the parameter name endings that indicate an
// action-type parameter.
var actionSuffixes = []string{"_ACTION_TYPE", "_MULTIEXECUTE"}

// stripKeypressPrefix removes a SHORT_ or LONG_ prefix and returns
// the corresponding [LinkKeypressGroup] plus the remaining suffix.
func stripKeypressPrefix(upper string) (group LinkKeypressGroup, suffix string) {
	if strings.HasPrefix(upper, "SHORT_") {
		return LinkKeypressShort, upper[6:]
	}
	if strings.HasPrefix(upper, "LONG_") {
		return LinkKeypressLong, upper[5:]
	}
	return LinkKeypressCommon, upper
}

// ClassifyLinkParameter classifies a LINK paramset parameter by name
// and returns its metadata for use in form-schema generation. Mirrors
// the reference config panel's classify_link_parameter.
func ClassifyLinkParameter(name string) LinkParamMetadata {
	upper := strings.ToUpper(name)
	kg, suffix := stripKeypressPrefix(upper)

	// TIME_BASE / TIME_FACTOR pairs.
	if strings.HasSuffix(suffix, "_TIME_BASE") || strings.HasSuffix(suffix, "_TIME_FACTOR") {
		isBase := strings.HasSuffix(suffix, "_TIME_BASE")
		var timeStem string
		if isBase {
			timeStem = suffix[:len(suffix)-len("_TIME_BASE")] + "_TIME"
		} else {
			timeStem = suffix[:len(suffix)-len("_TIME_FACTOR")] + "_TIME"
		}
		// Reconstruct the pair id including keypress prefix.
		var pairID string
		if kg != LinkKeypressCommon {
			pairID = strings.ToUpper(string(kg)) + "_" + timeStem
		} else {
			pairID = timeStem
		}
		selectorType := timeTypeMap[timeStem]
		return LinkParamMetadata{
			Category:         LinkParamCategoryTime,
			KeypressGroup:    kg,
			TimePairID:       pairID,
			TimeSelectorType: selectorType,
		}
	}

	// Jump targets: contain "JT_".
	if strings.Contains(suffix, "JT_") {
		return LinkParamMetadata{
			Category:        LinkParamCategoryJumpTarget,
			KeypressGroup:   kg,
			HiddenByDefault: true,
		}
	}

	// Condition transitions: contain "CT_".
	if strings.Contains(suffix, "CT_") {
		return LinkParamMetadata{
			Category:        LinkParamCategoryCondition,
			KeypressGroup:   kg,
			HiddenByDefault: true,
		}
	}

	// Level parameters.
	isLevel := suffix == "LEVEL"
	for _, s := range levelSuffixes {
		if strings.HasSuffix(suffix, s) {
			isLevel = true
			break
		}
	}
	if isLevel {
		return LinkParamMetadata{
			Category:         LinkParamCategoryLevel,
			KeypressGroup:    kg,
			DisplayAsPercent: true,
			HasLastValue:     true,
		}
	}

	// Action type.
	isAction := suffix == "MULTIEXECUTE"
	for _, s := range actionSuffixes {
		if strings.HasSuffix(suffix, s) {
			isAction = true
			break
		}
	}
	if isAction {
		return LinkParamMetadata{
			Category:        LinkParamCategoryAction,
			KeypressGroup:   kg,
			HiddenByDefault: true,
		}
	}

	return LinkParamMetadata{
		Category:      LinkParamCategoryOther,
		KeypressGroup: kg,
	}
}

// GetTimePresets returns the preset list for a time selector, using
// the given locale ("de" for German, anything else for English). Each
// entry's Label field is the localised display string; Value carries
// the decoded duration in seconds as a float64. Mirrors the reference
// config panel's get_time_presets.
func GetTimePresets(selectorType TimeSelectorType, locale string) []TimePreset {
	presets, ok := presetsByType[selectorType]
	if !ok {
		return nil
	}
	out := make([]TimePreset, 0, len(presets))
	for _, p := range presets {
		label := p.LabelEN
		if locale == "de" {
			label = p.LabelDE
		}
		out = append(out, TimePreset{
			Value: DecodeTimeValue(p.BaseVal, p.FactorVal),
			Label: label,
		})
	}
	return out
}

// DecodeTimeValue converts a TIME_BASE + TIME_FACTOR integer pair into
// a [time.Duration]. The formula is: seconds = base_unit(base) * factor.
// Mirrors the reference config panel's decode_time_value.
func DecodeTimeValue(base, factor int) time.Duration {
	unit, ok := timeBaseUnits[base]
	if !ok {
		unit = 1.0
	}
	seconds := unit * float64(factor)
	return time.Duration(seconds * float64(time.Second))
}

// EncodeTimeValue finds the closest (base, factor) pair for the given
// duration within the preset list for selectorType. When selectorType
// is [TimeSelectorUnknown], all preset lists are searched.
// Returns (0, 0) when no presets are available.
// Mirrors the reference config panel's encode_time_value.
func EncodeTimeValue(d time.Duration, selectorType TimeSelectorType) (base, factor int) {
	seconds := d.Seconds()
	bestBase, bestFactor := 0, 0
	bestDiff := math.Inf(1)

	search := func(presets []TimeBasePreset) {
		for _, p := range presets {
			decoded := DecodeTimeValue(p.BaseVal, p.FactorVal).Seconds()
			diff := math.Abs(decoded - seconds)
			if diff < bestDiff {
				bestDiff = diff
				bestBase = p.BaseVal
				bestFactor = p.FactorVal
			}
		}
	}

	if selectorType != TimeSelectorUnknown {
		if presets, ok := presetsByType[selectorType]; ok {
			search(presets)
		}
	} else {
		for _, presets := range presetsByType {
			search(presets)
		}
	}
	return bestBase, bestFactor
}
