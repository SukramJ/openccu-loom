// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package hub

import "strings"

// AutoSysvarKind classifies a CCU-auto-generated system variable by what it
// measures. A CCU creates these itself — the operator never names them — and
// their names carry fixed tokens that say what the value is.
type AutoSysvarKind string

// The auto-generated sysvar kinds a CCU produces.
const (
	// AutoSysvarEnergyCounter is cumulative consumed energy.
	AutoSysvarEnergyCounter AutoSysvarKind = "energy_counter_total"
	// AutoSysvarEnergyCounterFeedIn is cumulative fed-in energy.
	AutoSysvarEnergyCounterFeedIn AutoSysvarKind = "energy_counter_feed_in_total"
	// AutoSysvarRainCounter is cumulative rainfall since the counter began.
	AutoSysvarRainCounter AutoSysvarKind = "rain_counter_total"
	// AutoSysvarRainCounterToday is rainfall since midnight.
	AutoSysvarRainCounterToday AutoSysvarKind = "rain_counter_today"
	// AutoSysvarRainCounterYesterday is the previous day's rainfall.
	AutoSysvarRainCounterYesterday AutoSysvarKind = "rain_counter_yesterday"
	// AutoSysvarSunshineCounter is cumulative sunshine duration.
	AutoSysvarSunshineCounter AutoSysvarKind = "sunshine_counter_total"
	// AutoSysvarSunshineCounterToday is sunshine duration since midnight.
	AutoSysvarSunshineCounterToday AutoSysvarKind = "sunshine_counter_today"
	// AutoSysvarSunshineCounterYesterday is the previous day's sunshine.
	AutoSysvarSunshineCounterYesterday AutoSysvarKind = "sunshine_counter_yesterday"
)

// AutoSysvarClass is what the domain knows about an auto-generated sysvar:
// what it measures, in which unit, and whether it only ever grows.
//
// Cumulative matters beyond presentation. A counter that only grows is read as
// a total over time; the same number read as an instantaneous sample produces
// a mean, which is wrong for every one of these and wrong silently — nothing
// errors, the statistic is simply not what it claims.
type AutoSysvarClass struct {
	// Kind names what the variable measures.
	Kind AutoSysvarKind
	// Unit is the CCU's unit for the value.
	Unit string
	// Cumulative reports whether the value only ever grows (modulo a reset).
	Cumulative bool
}

// autoSysvarTokens maps the CCU's fixed name tokens onto their class.
//
// Matching is by LONGEST matching token, because the base tokens are
// substrings of the specific ones (svEnergyCounter ⊂ svEnergyCounterFeedIn,
// svHmIPRainCounter ⊂ svHmIPRainCounterToday). A first-match scan would
// classify every specific variant as its base — today's rainfall reported as
// the all-time total, which is a plausible number and therefore not noticed.
var autoSysvarTokens = map[string]AutoSysvarClass{
	"svEnergyCounterFeedIn":          {AutoSysvarEnergyCounterFeedIn, "Wh", true},
	"svEnergyCounter":                {AutoSysvarEnergyCounter, "Wh", true},
	"svHmIPRainCounterToday":         {AutoSysvarRainCounterToday, "mm", true},
	"svHmIPRainCounterYesterday":     {AutoSysvarRainCounterYesterday, "mm", true},
	"svHmIPRainCounter":              {AutoSysvarRainCounter, "mm", true},
	"svHmIPSunshineCounterToday":     {AutoSysvarSunshineCounterToday, "min", true},
	"svHmIPSunshineCounterYesterday": {AutoSysvarSunshineCounterYesterday, "min", true},
	"svHmIPSunshineCounter":          {AutoSysvarSunshineCounter, "min", true},
}

// ClassifyAutoSysvar reports what an auto-generated sysvar name measures, or
// ok=false for an operator-named variable, which keeps its own name and
// carries no derivable semantics.
//
// Matching is case-sensitive: the CCU's tokens are fixed.
func ClassifyAutoSysvar(name string) (AutoSysvarClass, bool) {
	var (
		best   AutoSysvarClass
		bestLn int
	)
	for token, class := range autoSysvarTokens {
		if len(token) > bestLn && strings.Contains(name, token) {
			best, bestLn = class, len(token)
		}
	}
	return best, bestLn > 0
}
