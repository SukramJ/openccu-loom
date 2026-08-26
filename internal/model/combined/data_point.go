// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

// Package combined adds the P2 parity surface for CombinedDataPoint
// Types. The methods mirror
// delegated properties and state_properties
// (combined/data_point.py:114-240). They are declared here on each
// concrete combined type.
package combined

import (
	"time"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// combinedSignature builds the canonical Signature string for a combined DP:
//
//	{category}//{combined_parameter}
//
// The middle segment is intentionally empty: combined DPs are not bound to a
// specific device model. Matches the Python format from data_point.py.
func combinedSignature(category hmenum.DataPointCategory, combinedParam string) string {
	return string(category) + "//" + combinedParam
}

// --- HSColor P2 surface ---

// Default returns nil. HSColor has no meaningful default in the paramset sense.
func (h *HSColor) Default() any { return nil }

// Max returns (0, false). HSColor has no declared numeric max.
func (h *HSColor) Max() (float64, bool) { return 0, false }

// Min returns (0, false). HSColor has no declared numeric min.
func (h *HSColor) Min() (float64, bool) { return 0, false }

// Service returns false. HSColor is never a write-only service point.
func (h *HSColor) Service() bool { return false }

// Translation returns "". CCU translation not surfaced on combined DPs.
func (h *HSColor) Translation() string { return "" }

// Values returns nil. HSColor has no enum value list.
func (h *HSColor) Values() []string { return nil }

// DataPointNamePostfix returns "".
func (h *HSColor) DataPointNamePostfix() string { return "" }

// HasDataPoints reports whether both hue and saturation inputs have been
// received.
func (h *HSColor) HasDataPoints() bool {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.hueObserved && h.satObserved
}

// IsStatusValid reports whether the HSColor state is valid, i.e. both hue and
// saturation have been observed.
func (h *HSColor) IsStatusValid() bool {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.hueObserved && h.satObserved
}

// Multiplier returns 1.0.
func (h *HSColor) Multiplier() float64 { return 1.0 }

// ParamsetKey returns "COMBINED".
func (h *HSColor) ParamsetKey() string { return "COMBINED" }

// TranslationKey returns "hs_color".
func (h *HSColor) TranslationKey() string { return "hs_color" }

// ModifiedAt returns the most recent timestamp when either the hue or saturation
// component was last modified. Returns the zero time until the first observation.
// Delegates to the embedded BaseDataPointFields which is updated by OnHue and
// OnSaturation via MarkModified.
func (h *HSColor) ModifiedAt() time.Time { return h.BaseDataPointFields.ModifiedAt() }

// RefreshedAt returns the most recent timestamp when either the hue or saturation
// component was last refreshed (received from the CCU, regardless of change).
// Delegates to the embedded BaseDataPointFields which is updated by OnHue and
// OnSaturation via MarkRefreshed.
func (h *HSColor) RefreshedAt() time.Time { return h.BaseDataPointFields.RefreshedAt() }

// IsStateChange reports whether a meaningful state change occurred.
// Returns true when both hue and saturation have been observed.
func (h *HSColor) IsStateChange() bool {
	_, ok := h.Value()
	return ok
}

// Signature returns the stable cross-stack identifier in the format
// "light/{model}/HSCOLOR".
func (h *HSColor) Signature() string {
	return combinedSignature(hmenum.DataPointCategoryLight, "HSCOLOR")
}

// IsValid reports whether both hue and saturation source DPs are non-dummy
// (i.e. have been observed).
func (h *HSColor) IsValid() bool {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.hueObserved && h.satObserved
}

// IsReadable returns false. Combined DPs have _operations = Operations.WRITE
// (combined/data_point.py:110) which does not include the READ flag.
func (h *HSColor) IsReadable() bool { return false }

// IsWritable returns true. Combined DPs have _operations = Operations.WRITE
// (combined/data_point.py:110).
func (h *HSColor) IsWritable() bool { return true }

// --- Timer P2 surface ---

// Default returns the default duration in seconds from the underlying value
// parameter descriptor, captured at Subscribe time. Returns nil when no
// default was available (e.g. before Subscribe is called or the parameter
// descriptor carries no default).
func (t *Timer) Default() any {
	t.mu.RLock()
	defer t.mu.RUnlock()
	if !t.hasDefault {
		return nil
	}
	return t.defaultSeconds
}

// Max returns the upper-bound seconds threshold.
func (t *Timer) Max() (float64, bool) {
	return float64(timerUpperBoundSeconds), true
}

// Min returns (0, false) — no meaningful minimum for a timer.
func (t *Timer) Min() (float64, bool) { return 0, false }

// HasUnit reports whether the unit parameter is wired.
func (t *Timer) HasUnit() bool { return t.UnitParameter != "" }

// IsValid reports whether the value parameter is wired.
func (t *Timer) IsValid() bool { return t.ValueParameter != "" }

// Service returns false. Timer is not a service-only point.
func (t *Timer) Service() bool { return false }

// Translation returns "".
func (t *Timer) Translation() string { return "" }

// Values returns nil.
func (t *Timer) Values() []string { return nil }

// DataPointNamePostfix returns "".
func (t *Timer) DataPointNamePostfix() string { return "" }

// HasDataPoints reports whether the timer has been observed.
func (t *Timer) HasDataPoints() bool {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.observed
}

// IsStatusValid reports whether the timer state is valid.
func (t *Timer) IsStatusValid() bool {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.observed
}

// Multiplier returns 1.0.
func (t *Timer) Multiplier() float64 { return 1.0 }

// ParamsetKey returns "COMBINED".
func (t *Timer) ParamsetKey() string { return "COMBINED" }

// TranslationKey returns "timer".
func (t *Timer) TranslationKey() string { return "timer" }

// ModifiedAt returns zero — Timer has no source DP aggregation.
func (t *Timer) ModifiedAt() time.Time { return time.Time{} }

// RefreshedAt returns zero.
func (t *Timer) RefreshedAt() time.Time { return time.Time{} }

// IsStateChange reports whether a meaningful state change occurred.
func (t *Timer) IsStateChange() bool {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.observed
}

// Signature returns the stable cross-stack identifier in the format
// "switch/{model}/{combined_parameter}". The combined parameter defaults to
// "DURATION" when [Timer.CombinedParameter] is not set.
func (t *Timer) Signature() string {
	param := string(t.CombinedParameter)
	if param == "" {
		param = string(ParameterDuration)
	}
	return combinedSignature(hmenum.DataPointCategorySwitch, param)
}

// IsReadable returns false. Combined DPs have _operations = Operations.WRITE.
func (t *Timer) IsReadable() bool { return false }

// IsWritable returns true. Combined DPs have _operations = Operations.WRITE.
func (t *Timer) IsWritable() bool { return true }

// --- LevelCombined P2 surface ---

// ParamsetKey returns "COMBINED".
func (l *LevelCombined) ParamsetKey() string { return "COMBINED" }

// TranslationKey returns "level_combined".
func (l *LevelCombined) TranslationKey() string { return "level_combined" }

// DataPointNamePostfix returns "".
func (l *LevelCombined) DataPointNamePostfix() string { return "" }

// HasDataPoints reports whether both LEVEL and LEVEL_2 have been observed.
func (l *LevelCombined) HasDataPoints() bool { return l.IsRefreshed() }

// IsStatusValid reports whether the level composite is ready.
func (l *LevelCombined) IsStatusValid() bool {
	_, ok := l.Value()
	return ok
}

// ModifiedAt returns zero — LevelCombined has no source DP aggregation.
func (l *LevelCombined) ModifiedAt() time.Time { return time.Time{} }

// RefreshedAt returns zero.
func (l *LevelCombined) RefreshedAt() time.Time { return time.Time{} }

// IsReadable returns false. Combined DPs have _operations = Operations.WRITE.
func (l *LevelCombined) IsReadable() bool { return false }

// IsWritable returns true. Combined DPs have _operations = Operations.WRITE.
func (l *LevelCombined) IsWritable() bool { return true }

// Signature returns the stable cross-stack identifier in the format
// "cover/{model}/LEVEL_COMBINED".
func (l *LevelCombined) Signature() string {
	return combinedSignature(hmenum.DataPointCategoryCover, "LEVEL_COMBINED")
}
