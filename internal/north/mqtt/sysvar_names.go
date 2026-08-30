// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package mqtt

import "github.com/SukramJ/openccu-loom/internal/model/hub"

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
	// translationKey is the i18n catalogue key of the friendly name. It drives
	// both the HA display name and — for a freshly discovered entity — the
	// entity_id slug.
	translationKey string
	// deviceClass is the HA `device_class` ("" = omit).
	deviceClass string
	// stateClass is the HA `state_class`. The domain reports these counters as
	// cumulative, which renders as `total_increasing` (long-term statistics)
	// rather than `measurement`.
	stateClass string
	// unit is the HA `unit_of_measurement`, carried over from the domain's
	// class ("" = omit).
	unit string
}

// autoSysvarPresentation renders the domain's [hub.AutoSysvarKind] into Home
// Assistant's vocabulary. What each token MEANS — the quantity, its unit and
// whether it only ever grows — is the domain's answer and lives in
// internal/model/hub; this table is the translation of that answer, which is
// the part HA cares about and no other plane shares.
var autoSysvarPresentation = map[hub.AutoSysvarKind]autoSysvarClass{
	hub.AutoSysvarEnergyCounterFeedIn:      {translationKey: "discovery.energy_counter_feed_in_total", deviceClass: "energy", stateClass: "total_increasing"},
	hub.AutoSysvarEnergyCounter:            {translationKey: "discovery.energy_counter_total", deviceClass: "energy", stateClass: "total_increasing"},
	hub.AutoSysvarRainCounterToday:         {translationKey: "discovery.rain_counter_today", deviceClass: "", stateClass: "total_increasing"},
	hub.AutoSysvarRainCounterYesterday:     {translationKey: "discovery.rain_counter_yesterday", deviceClass: "", stateClass: "total_increasing"},
	hub.AutoSysvarRainCounter:              {translationKey: "discovery.rain_counter_total", deviceClass: "", stateClass: "total_increasing"},
	hub.AutoSysvarSunshineCounterToday:     {translationKey: "discovery.sunshine_counter_today", deviceClass: "duration", stateClass: "total_increasing"},
	hub.AutoSysvarSunshineCounterYesterday: {translationKey: "discovery.sunshine_counter_yesterday", deviceClass: "duration", stateClass: "total_increasing"},
	hub.AutoSysvarSunshineCounter:          {translationKey: "discovery.sunshine_counter_total", deviceClass: "duration", stateClass: "total_increasing"},
}

// classifyAutoSysvar returns the HA presentation of a CCU-auto-generated
// sysvar, or ok=false for an operator-named one that keeps its own name.
//
// The classification itself — which token means what, in which unit, and
// whether it is cumulative — comes from [hub.ClassifyAutoSysvar]. This adds
// only the Home Assistant rendering.
func classifyAutoSysvar(name string) (autoSysvarClass, bool) {
	domain, ok := hub.ClassifyAutoSysvar(name)
	if !ok {
		return autoSysvarClass{}, false
	}
	pres, ok := autoSysvarPresentation[domain.Kind]
	if !ok {
		// The domain grew a kind this plane does not render yet. Declining is
		// correct: an entity with no translation key would show its raw CCU
		// name, which is what the classification exists to avoid.
		return autoSysvarClass{}, false
	}
	pres.unit = domain.Unit
	return pres, true
}
