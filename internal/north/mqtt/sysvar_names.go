// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package mqtt

import "strings"

// autoSysvarClass describes the friendly presentation of a CCU-auto-generated
// system variable whose raw name is a machine token
// (`svEnergyCounter_<ise_id>_<addr>:<ch>`). The CCU synthesises these per
// energy-metering / weather channel; without this mapping HA shows the raw
// token as both the entity name and the entity_id. The friendly name is
// resolved from the i18n catalogues via translationKey; the HA device_class,
// unit and (cumulative) state_class mirror the reference HA integration's hub
// entity-description rules for the svEnergyCounter / svHmIPRainCounter /
// svHmIPSunshineCounter system variables.
type autoSysvarClass struct {
	// contains is the substring of the sysvar name that identifies the class
	// (matches the reference's `var_name_contains`).
	contains string
	// translationKey is the i18n catalogue key of the friendly name. It drives
	// both the HA display name and — for a freshly discovered entity — the
	// entity_id slug.
	translationKey string
	// deviceClass is the HA `device_class` ("" = omit).
	deviceClass string
	// unit is the HA `unit_of_measurement` ("" = omit).
	unit string
	// stateClass is the HA `state_class`. These are cumulative counters, so it
	// is `total_increasing` (long-term statistics), NOT `measurement`.
	stateClass string
}

// autoSysvarClasses is the classification table, most-specific first purely for
// readability — matching is by LONGEST matching `contains` (see
// [classifyAutoSysvar]), so order does not affect correctness. Longest-match is
// required because the base tokens are substrings of the specific ones
// (`svEnergyCounter` ⊂ `svEnergyCounterFeedIn`; `svHmIPRainCounter` ⊂
// `svHmIPRainCounterToday`), so a first-match-wins scan would mis-classify the
// specific variants as their base.
var autoSysvarClasses = []autoSysvarClass{
	{"svEnergyCounterFeedIn", "discovery.energy_counter_feed_in_total", "energy", "Wh", "total_increasing"},
	{"svEnergyCounter", "discovery.energy_counter_total", "energy", "Wh", "total_increasing"},
	{"svHmIPRainCounterToday", "discovery.rain_counter_today", "", "mm", "total_increasing"},
	{"svHmIPRainCounterYesterday", "discovery.rain_counter_yesterday", "", "mm", "total_increasing"},
	{"svHmIPRainCounter", "discovery.rain_counter_total", "", "mm", "total_increasing"},
	{"svHmIPSunshineCounterToday", "discovery.sunshine_counter_today", "duration", "min", "total_increasing"},
	{"svHmIPSunshineCounterYesterday", "discovery.sunshine_counter_yesterday", "duration", "min", "total_increasing"},
	{"svHmIPSunshineCounter", "discovery.sunshine_counter_total", "duration", "min", "total_increasing"},
}

// classifyAutoSysvar returns the presentation class for a CCU-auto-generated
// sysvar name, or ok=false when the name is a regular (operator-named) sysvar
// that must keep its own name. Matching is case-sensitive (the CCU tokens are
// fixed) and longest-`contains`-wins.
func classifyAutoSysvar(name string) (autoSysvarClass, bool) {
	var (
		best   autoSysvarClass
		bestLn int
	)
	for _, c := range autoSysvarClasses {
		if len(c.contains) > bestLn && strings.Contains(name, c.contains) {
			best, bestLn = c, len(c.contains)
		}
	}
	return best, bestLn > 0
}
