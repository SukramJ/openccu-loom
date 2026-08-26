// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package mqtt

import "github.com/SukramJ/openccu-loom/pkg/hmenum"

// componentFromCategory maps a model-derived [hmenum.DataPointCategory]
// to the HA-Discovery component string. This is the canonical,
// authoritative mapping — the model encodes which HA platform to use for
// every wire data point. Routing the discovery payload through the model
// means openccu-loom and the HA integration always agree on the platform
// for a given wire DP.
//
// Returns ("", false) when the category is empty (zero value),
// "undefined", or a category that does not surface as an HA entity:
//
//   - action — write-only fire-and-forget parameters (COMBINED_PARAMETER,
//     RAMP_STOP, …) have no HA platform in the reference stack.
//   - action_number — the reference stack exposes these only through an
//     explicit per-parameter whitelist that is currently EMPTY, so
//     ON_TIME / RAMP_TIME / DURATION_VALUE never surface as number
//     entities. They stay writable through the per-DP command topic and
//     the custom-DP service methods (`set_on_time`, …).
//   - event_group / no_create — internal book-keeping categories.
func componentFromCategory(c hmenum.DataPointCategory) (HAComponent, bool) {
	switch c { //nolint:exhaustive // Undefined/action/action_number categories do not surface as an HA entity; caller falls through to heuristic
	case hmenum.DataPointCategorySensor,
		hmenum.DataPointCategoryHubSensor:
		return HAComponentSensor, true
	case hmenum.DataPointCategoryBinarySensor,
		hmenum.DataPointCategoryHubBinarySensor:
		return HAComponentBinarySensor, true
	case hmenum.DataPointCategoryNumber,
		hmenum.DataPointCategoryHubNumber:
		return HAComponentNumber, true
	case hmenum.DataPointCategorySwitch,
		hmenum.DataPointCategoryScheduleSwitch,
		hmenum.DataPointCategoryHubSwitch:
		return HAComponentSwitch, true
	case hmenum.DataPointCategoryButton,
		hmenum.DataPointCategoryHubButton:
		return HAComponentButton, true
	case hmenum.DataPointCategorySelect,
		hmenum.DataPointCategoryActionSelect,
		hmenum.DataPointCategoryHubSelect:
		return HAComponentSelect, true
	case hmenum.DataPointCategoryClimate:
		return HAComponentClimate, true
	case hmenum.DataPointCategoryCover:
		return HAComponentCover, true
	case hmenum.DataPointCategoryLock:
		return HAComponentLock, true
	case hmenum.DataPointCategoryLight:
		return HAComponentLight, true
	case hmenum.DataPointCategoryValve:
		return HAComponentValve, true
	case hmenum.DataPointCategoryAlarmControlPanel:
		return HAComponentAlarmControlPanel, true
	case hmenum.DataPointCategorySiren:
		return HAComponentSiren, true
	case hmenum.DataPointCategoryEvent,
		hmenum.DataPointCategoryEventGroup:
		return HAComponentEvent, true
	case hmenum.DataPointCategoryText,
		hmenum.DataPointCategoryTextDisplay,
		hmenum.DataPointCategoryHubText:
		return HAComponentText, true
	case hmenum.DataPointCategoryUpdate,
		hmenum.DataPointCategoryHubUpdate:
		return HAComponentUpdate, true
	case hmenum.DataPointCategoryWeekProfile:
		// WeekProfile surfaces as a select entity (the operator picks a
		// week-profile id from a list).
		return HAComponentSelect, true
	}
	return "", false
}

// isIntegerParameter reports whether the descriptor on `ev` carries
// `Type=INTEGER`. Used by the Number-builder to pick the right
// `step` default (1.0 for integers, 0.01 for floats — mirrors
// ).
func isIntegerParameter(ev Event) bool {
	return ev.descType() == hmenum.ParameterTypeInteger
}

// resolveComponent picks the HA-Discovery component for an [Event]
// via [componentFromCategory](ev.Category) — the model-driven,
// authoritative path. Every Source must populate Event.Category
// (via hmenum.DataPointCategory) so the bridge can route without
// inspecting parameter names.
//
// Returns ("", false) when the category does not produce a
// classification — the caller skips the HA-Discovery emission entirely
// (no synthetic entity, no logged warning). Per ADR 0011 there is no
// parameter-name fallback; unclassified events simply do not surface
// as HA entities.
func resolveComponent(ev Event) (HAComponent, bool) {
	return componentFromCategory(ev.Category)
}
