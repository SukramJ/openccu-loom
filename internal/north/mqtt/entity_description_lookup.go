// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package mqtt

// This file holds the per-domain rule lookups. [LookupRulesForComponent]
// below is the single entry point into them, and it is what
// EntityDescriptionFor (entity_descriptions.go) consults before falling back
// to the wire defaults.
//
// The rule tables themselves live one per domain and are derived from the
// HA-integration reference (`entity_helpers/descriptions/`), which is the
// single source of truth for per-device entity-category / enabled-default /
// icon overrides.
//
// A domain has either one table or two, depending on whether its overrides
// are per-parameter:
//
//   - <domain>RulesByDeviceAndParam — most specific (mirrors the Python
//     `priority=10` device-overrides).
//   - <domain>RulesByParam — generic per-parameter rule. Domains whose
//     overrides are device-wide (cover, siren, valve) carry only the first
//     table, with an empty parameter slot, and route through
//     [lookupDeviceOnlyRules].
//
// Lookup precedence:
//
//  1. Exact (deviceModel, parameter) hit.
//  2. (devicePrefix, parameter) prefix hit (devicePrefix is a prefix of
//     deviceModel).
//  3. Param-only hit.
//  4. Zero value (no entry → discovery falls back to wire defaults).

// lookupDeviceOnlyRules walks a device-only rule map (parameter slot
// always empty in the keys) and returns the first match keyed on the
// device model — exact hit first, then any device-prefix match.
// Domain-only tables (cover / siren / valve) use this helper because
// they have no per-parameter constraint.
func lookupDeviceOnlyRules(byDevice map[devParam]EntityDescription, deviceModel string) (EntityDescription, bool) {
	if byDevice == nil {
		return EntityDescription{}, false
	}
	if d, ok := byDevice[devParam{deviceModel, ""}]; ok {
		return d, true
	}
	for k, d := range byDevice {
		if k.parameter != "" {
			continue
		}
		if hasModelPrefix(deviceModel, k.devicePrefix) {
			return d, true
		}
	}
	return EntityDescription{}, false
}

// lookupRulesByDeviceAndParam consults `byDevice` first (exact +
// prefix), then falls through to `byParam`. Used by every per-domain
// route below to avoid repeating the same boilerplate.
func lookupRulesByDeviceAndParam(
	byDevice map[devParam]EntityDescription,
	byParam map[string]EntityDescription,
	deviceModel, parameter string,
) (EntityDescription, bool) {
	if byDevice != nil {
		if d, ok := byDevice[devParam{deviceModel, parameter}]; ok {
			return d, true
		}
		for k, d := range byDevice {
			if k.parameter != parameter {
				continue
			}
			if hasModelPrefix(deviceModel, k.devicePrefix) {
				return d, true
			}
		}
	}
	if byParam != nil {
		if d, ok := byParam[parameter]; ok {
			return d, true
		}
	}
	return EntityDescription{}, false
}

// LookupSensorRule routes a sensor lookup through the
// the upstream HA-integration reference rule tables. Returns the merged description when
// any tier produces a hit.
func LookupSensorRule(deviceModel, parameter string) (EntityDescription, bool) {
	return lookupRulesByDeviceAndParam(
		sensorRulesByDeviceAndParam,
		sensorRulesByParam,
		deviceModel, parameter,
	)
}

// LookupBinarySensorRule — see [LookupSensorRule].
func LookupBinarySensorRule(deviceModel, parameter string) (EntityDescription, bool) {
	return lookupRulesByDeviceAndParam(
		binarySensorRulesByDeviceAndParam,
		binarySensorRulesByParam,
		deviceModel, parameter,
	)
}

// LookupNumberRule — see [LookupSensorRule].
func LookupNumberRule(deviceModel, parameter string) (EntityDescription, bool) {
	return lookupRulesByDeviceAndParam(
		numberRulesByDeviceAndParam,
		numberRulesByParam,
		deviceModel, parameter,
	)
}

// LookupSwitchRule — see [LookupSensorRule].
func LookupSwitchRule(deviceModel, parameter string) (EntityDescription, bool) {
	return lookupRulesByDeviceAndParam(
		switchRulesByDeviceAndParam,
		switchRulesByParam,
		deviceModel, parameter,
	)
}

// LookupCoverRule — see [LookupSensorRule]. Cover rules are device-
// only (every entry's parameter slot is the empty string), so the
// `parameter` argument is ignored. The lookup returns the first
// (exact or prefix) device match.
func LookupCoverRule(deviceModel, _ string) (EntityDescription, bool) {
	return lookupDeviceOnlyRules(coverRulesByDeviceAndParam, deviceModel)
}

// LookupLockRule — see [LookupSensorRule].
// (the upstream HA-integration reference has only a param-only rule for lock.)
func LookupLockRule(deviceModel, parameter string) (EntityDescription, bool) {
	return lookupRulesByDeviceAndParam(
		nil,
		lockRulesByParam,
		deviceModel, parameter,
	)
}

// LookupSirenRule — see [LookupSensorRule]. Like Cover, siren rules
// are device-only.
func LookupSirenRule(deviceModel, _ string) (EntityDescription, bool) {
	return lookupDeviceOnlyRules(sirenRulesByDeviceAndParam, deviceModel)
}

// LookupValveRule — see [LookupSensorRule]. Device-only.
func LookupValveRule(deviceModel, _ string) (EntityDescription, bool) {
	return lookupDeviceOnlyRules(valveRulesByDeviceAndParam, deviceModel)
}

// LookupButtonRule — see [LookupSensorRule].
func LookupButtonRule(deviceModel, parameter string) (EntityDescription, bool) {
	return lookupRulesByDeviceAndParam(
		nil,
		buttonRulesByParam,
		deviceModel, parameter,
	)
}

// LookupSelectRule — see [LookupSensorRule].
func LookupSelectRule(deviceModel, parameter string) (EntityDescription, bool) {
	return lookupRulesByDeviceAndParam(
		nil,
		selectRulesByParam,
		deviceModel, parameter,
	)
}

// LookupRulesForComponent dispatches to the right per-domain
// helper based on the HA component. Returns the merged description and
// true if the the upstream HA-integration reference tables carry an override for this
// (component, deviceModel, parameter) tuple. Components without
// the upstream HA-integration reference rules (climate, light, update, text, event,
// text_display) return false — the discovery payload falls through to
// the descriptor / classifier defaults.
func LookupRulesForComponent(comp HAComponent, deviceModel, parameter string) (EntityDescription, bool) {
	switch comp { //nolint:exhaustive // climate / light / event / update / text return descriptor defaults — explicit fallthrough at the end

	case HAComponentSensor:
		return LookupSensorRule(deviceModel, parameter)
	case HAComponentBinarySensor:
		return LookupBinarySensorRule(deviceModel, parameter)
	case HAComponentNumber:
		return LookupNumberRule(deviceModel, parameter)
	case HAComponentSwitch:
		return LookupSwitchRule(deviceModel, parameter)
	case HAComponentCover:
		return LookupCoverRule(deviceModel, parameter)
	case HAComponentLock:
		return LookupLockRule(deviceModel, parameter)
	case HAComponentSiren:
		return LookupSirenRule(deviceModel, parameter)
	case HAComponentValve:
		return LookupValveRule(deviceModel, parameter)
	case HAComponentButton:
		return LookupButtonRule(deviceModel, parameter)
	case HAComponentSelect:
		return LookupSelectRule(deviceModel, parameter)
	}
	return EntityDescription{}, false
}
