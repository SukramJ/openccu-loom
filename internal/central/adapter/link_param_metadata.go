// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package adapter

import (
	"math"
	"strings"
)

// LinkParamCategory is the functional category of a LINK paramset
// parameter.
type LinkParamCategory string

// Functional categories for LINK paramset parameters. These are a
// Straight port of 's enum — same string values so
// the SPA can reuse classification logic verbatim.
const (
	LinkParamCategoryTime       LinkParamCategory = "time"
	LinkParamCategoryLevel      LinkParamCategory = "level"
	LinkParamCategoryJumpTarget LinkParamCategory = "jump_target"
	LinkParamCategoryCondition  LinkParamCategory = "condition"
	LinkParamCategoryAction     LinkParamCategory = "action"
	LinkParamCategoryOther      LinkParamCategory = "other"
)

// KeypressGroup partitions LINK parameters into short-press / long-
// press / common groups so the SPA can render them under labelled
// sub-headings.
type KeypressGroup string

// Keypress-duration groups. COMMON covers parameters without a
// SHORT_/LONG_ prefix (e.g. a plain ON_TIME variant).
const (
	KeypressGroupShort  KeypressGroup = "short"
	KeypressGroupLong   KeypressGroup = "long"
	KeypressGroupCommon KeypressGroup = "common"
)

// TimeSelectorType identifies one of the canonical preset lists for
// *_TIME_BASE / *_TIME_FACTOR pairs.
type TimeSelectorType string

// Preset-list kinds. Names mirror for cross-lang
// SPA compatibility (camelCase strings, not snake_case).
const (
	TimeSelectorTimeOnOff TimeSelectorType = "timeOnOff"
	TimeSelectorDelay     TimeSelectorType = "delay"
	TimeSelectorRampOnOff TimeSelectorType = "rampOnOff"
)

// TimePreset is one (base, factor) option in a preset list plus its
// localised labels.
type TimePreset struct {
	Base    int
	Factor  int
	LabelEn string
	LabelDe string
}

// LinkParamMeta is the classifier result for a single LINK parameter.
type LinkParamMeta struct {
	Category         LinkParamCategory
	KeypressGroup    KeypressGroup
	DisplayAsPercent bool
	HasLastValue     bool
	HiddenByDefault  bool
	TimePairID       string
	TimeSelectorType TimeSelectorType
}

// timeBaseSeconds maps the CCU TIME_BASE base value to its duration
// unit in seconds.
var timeBaseSeconds = map[int]float64{
	0: 0.1,
	1: 1.0,
	2: 5.0,
	3: 10.0,
	4: 60.0,
	5: 300.0,
	6: 600.0,
	7: 3600.0,
}

var timeOnOffPresets = []TimePreset{
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

var delayPresets = []TimePreset{
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

var rampOnOffPresets = []TimePreset{
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

// presetsByType maps a selector type to its ordered preset list.
var presetsByType = map[TimeSelectorType][]TimePreset{
	TimeSelectorTimeOnOff: timeOnOffPresets,
	TimeSelectorDelay:     delayPresets,
	TimeSelectorRampOnOff: rampOnOffPresets,
}

// timeTypeByStem maps the stripped time-stem portion of a parameter
// name (e.g. "ON_TIME", "ONDELAY_TIME") to the appropriate selector.
var timeTypeByStem = map[string]TimeSelectorType{
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

const (
	baseSuffix   = "_BASE"
	factorSuffix = "_FACTOR"
	jtMarker     = "JT_"
	ctMarker     = "CT_"
)

var (
	levelSuffixes  = []string{"_LEVEL", "_DIM_MIN_LEVEL", "_DIM_MAX_LEVEL"}
	actionSuffixes = []string{"_ACTION_TYPE", "_MULTIEXECUTE"}
)

// stripKeypressPrefix splits a SHORT_/LONG_ prefix from an upper-case
// parameter name and returns (group, remainder). COMMON is returned
// when no prefix is present.
func stripKeypressPrefix(paramUpper string) (group KeypressGroup, remainder string) {
	if strings.HasPrefix(paramUpper, "SHORT_") {
		return KeypressGroupShort, paramUpper[len("SHORT_"):]
	}
	if strings.HasPrefix(paramUpper, "LONG_") {
		return KeypressGroupLong, paramUpper[len("LONG_"):]
	}
	return KeypressGroupCommon, paramUpper
}

// ClassifyLinkParameter returns metadata for a LINK paramset
// parameter. Pure function; safe to call from any goroutine. Port of
// Classify_link_parameter in.
func ClassifyLinkParameter(parameterID string) LinkParamMeta {
	upper := strings.ToUpper(parameterID)
	group, suffix := stripKeypressPrefix(upper)

	// TIME_BASE / TIME_FACTOR pairs. The suffix has to end with
	// *exactly* _TIME_BASE or _TIME_FACTOR — other _BASE / _FACTOR
	// parameters (rare but possible) fall through to OTHER.
	if strings.HasSuffix(suffix, baseSuffix) || strings.HasSuffix(suffix, factorSuffix) {
		isTimeBase := strings.HasSuffix(suffix, "_TIME_BASE")
		isTimeFactor := strings.HasSuffix(suffix, "_TIME_FACTOR")
		if isTimeBase || isTimeFactor {
			var stem string
			if isTimeBase {
				stem = suffix[:len(suffix)-len(baseSuffix)]
			} else {
				stem = suffix[:len(suffix)-len(factorSuffix)]
			}
			pairID := stem
			if group != KeypressGroupCommon {
				pairID = strings.ToUpper(string(group)) + "_" + stem
			}
			return LinkParamMeta{
				Category:         LinkParamCategoryTime,
				KeypressGroup:    group,
				TimePairID:       pairID,
				TimeSelectorType: timeTypeByStem[stem],
			}
		}
	}

	// Jump targets: JT_ON, JT_OFF, JT_ONDELAY, …
	if strings.Contains(suffix, jtMarker) {
		return LinkParamMeta{
			Category:        LinkParamCategoryJumpTarget,
			KeypressGroup:   group,
			HiddenByDefault: true,
		}
	}

	// Condition transitions: CT_ON, CT_OFF, …
	if strings.Contains(suffix, ctMarker) {
		return LinkParamMeta{
			Category:        LinkParamCategoryCondition,
			KeypressGroup:   group,
			HiddenByDefault: true,
		}
	}

	// Level parameters — dim levels, ON_LEVEL, OFF_LEVEL, etc.
	if suffix == "LEVEL" || hasAnySuffix(suffix, levelSuffixes) {
		return LinkParamMeta{
			Category:         LinkParamCategoryLevel,
			KeypressGroup:    group,
			DisplayAsPercent: true,
			HasLastValue:     true,
		}
	}

	// Action / multi-execute — advanced, hidden by default.
	if suffix == "MULTIEXECUTE" || hasAnySuffix(suffix, actionSuffixes) {
		return LinkParamMeta{
			Category:        LinkParamCategoryAction,
			KeypressGroup:   group,
			HiddenByDefault: true,
		}
	}

	return LinkParamMeta{
		Category:      LinkParamCategoryOther,
		KeypressGroup: group,
	}
}

func hasAnySuffix(s string, suffixes []string) bool {
	for _, suf := range suffixes {
		if strings.HasSuffix(s, suf) {
			return true
		}
	}
	return false
}

// LocalisedTimePreset is a TimePreset with its label resolved to one
// specific locale — the shape the UI schema carries over the wire.
type LocalisedTimePreset struct {
	Base   int    `json:"base"`
	Factor int    `json:"factor"`
	Label  string `json:"label"`
}

// GetTimePresets returns the preset list for a selector type,
// localised to `locale`. Empty result when the selector is unknown.
func GetTimePresets(selector TimeSelectorType, locale string) []LocalisedTimePreset {
	presets := presetsByType[selector]
	if len(presets) == 0 {
		return nil
	}
	result := make([]LocalisedTimePreset, 0, len(presets))
	for _, p := range presets {
		label := p.LabelEn
		if locale == "de" {
			label = p.LabelDe
		}
		result = append(result, LocalisedTimePreset{Base: p.Base, Factor: p.Factor, Label: label})
	}
	return result
}

// DecodeTimeValue converts a (base, factor) pair to seconds using the
// CCU's TIME_BASE unit table.
func DecodeTimeValue(base, factor int) float64 {
	unit, ok := timeBaseSeconds[base]
	if !ok {
		unit = 1.0
	}
	return unit * float64(factor)
}

// EncodeTimeValue finds the closest preset (base, factor) pair for a
// given duration in seconds. Returns (0, 0) when the selector has no
// presets registered.
func EncodeTimeValue(seconds float64, selector TimeSelectorType) (base, factor int) {
	presets := presetsByType[selector]
	if len(presets) == 0 {
		return 0, 0
	}
	bestDiff := math.Inf(1)
	for _, p := range presets {
		cur := DecodeTimeValue(p.Base, p.Factor)
		diff := math.Abs(cur - seconds)
		if diff < bestDiff {
			bestDiff = diff
			base = p.Base
			factor = p.Factor
		}
	}
	return base, factor
}
