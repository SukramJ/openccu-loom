// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package mqtt

import "strings"

// EntityDescription captures the per-parameter metadata MQTT consumers
// (Home Assistant Discovery, custom dashboards) expect on top of the
// raw CCU value. Fields the CCU itself already supplies (units,
// min/max, value-list) are not duplicated; this layer only carries
// the *enrichment* the wire protocol cannot provide.
type EntityDescription struct {
	// Key is the symbolic identifier used as MQTT topic / discovery
	// object_id. Multiple parameter aliases may resolve to the same
	// Key (e.g. ACTUAL_TEMPERATURE and TEMPERATURE both → "TEMPERATURE").
	Key string

	// DeviceClass is the Home Assistant device-class hint (e.g.
	// "temperature", "humidity", "power"). Empty when the consumer
	// should derive the value from the raw quantity instead.
	DeviceClass string

	// StateClass is the Home Assistant state-class hint
	// ("measurement", "total", "total_increasing"). Drives the long-
	// term-statistics behaviour in HA.
	StateClass string

	// UnitOfMeasurement overrides the CCU-reported unit. Used for
	// quantities the CCU sends with non-SI units (mHz, μm).
	UnitOfMeasurement string

	// EntityCategory is HA's classification of the entity ("config",
	// "diagnostic", or empty for primary state).
	EntityCategory string

	// EnabledByDefault indicates whether the entity should be visible
	// in HA without explicit operator action. Defaults to true; the
	// per-parameter overrides set it to false for diagnostics or
	// internal values.
	EnabledByDefault bool

	// Icon is a Material Design Icon string (e.g. "mdi:weather-rainy").
	Icon string

	// SuggestedDisplayPrecision controls how many decimal places HA
	// renders. -1 means "no preference"; 0 means "round to integer".
	// Mirrors Python's optional int.
	SuggestedDisplayPrecision int
}

// EntityCategory values mirror Home Assistant's literal constants.
const (
	EntityCategoryConfig     = "config"
	EntityCategoryDiagnostic = "diagnostic"
)

const (
	unitConcentrationCm3     = "1/cm³"
	unitConcentrationGramsM3 = "g/m³"
	unitMicrometers          = "µm" // U+00B5 MICRO SIGN
)

// devParam is the composite key for device-and-parameter lookups
// shared across every per-domain rule table in
// `entity_description_rules_*.go`.
type devParam struct {
	devicePrefix string
	parameter    string
}

// ---------------------------------------------------------------------------
// Extended rule type — unit / postfix / var_name_contains matching
// ---------------------------------------------------------------------------

// EntityDescriptionExtRule is a richer rule type that carries optional
// unit, postfix, and var_name_contains constraints in addition to the
// device-prefix and parameter constraints of the static devParam maps.
// It mirrors the Python EntityDescriptionRule.
//
// Extended rules are stored in per-domain slices (e.g.
// [sensorExtRules]) sorted by descending priority. [LookupExtRule] scans
// them after the fast static devParam maps; use them only when the
// extra dimensions are needed — the devParam maps are cheaper.
//
// All criteria are AND-combined; any nil/empty criterion is skipped.
type EntityDescriptionExtRule struct {
	// Description is the entity metadata returned on a match.
	Description EntityDescription

	// DevicePrefix, when non-empty, is matched as a prefix of the
	// device model string (case-sensitive, dash-boundary aware via
	// [hasModelPrefix]).
	DevicePrefix string

	// Parameter, when non-empty, is matched against the CCU parameter
	// name (case-insensitive).
	Parameter string

	// Unit, when non-empty, is matched against the wire unit of the
	// data point (exact, case-sensitive).
	Unit string

	// Postfix, when non-empty, is matched against the data point name
	// postfix — i.e. the suffix that follows the last underscore in a
	// compound parameter name (e.g. "_2", "_3"). Case-insensitive.
	Postfix string

	// VarNameContains, when non-empty, is matched as a
	// case-insensitive substring of the variable/key name. Mirrors the
	// Python `var_name_contains` field.
	VarNameContains string

	// Priority controls rule ordering: higher values are checked first.
	// Rules at the same priority are checked in declaration order.
	Priority int
}

// MatchesExt reports whether r matches the given data-point attributes.
// All non-empty criteria must match; empty/zero criteria are treated as
// "any". Mirrors EntityDescriptionRule.matches in registry.py:75-128.
func (r *EntityDescriptionExtRule) MatchesExt(deviceModel, parameter, unit, postfix, varName string) bool {
	if r.DevicePrefix != "" {
		if !hasModelPrefix(deviceModel, r.DevicePrefix) && deviceModel != r.DevicePrefix {
			return false
		}
	}
	if r.Parameter != "" {
		if !strings.EqualFold(parameter, r.Parameter) {
			return false
		}
	}
	if r.Unit != "" && unit != r.Unit {
		return false
	}
	if r.Postfix != "" {
		if !strings.EqualFold(postfix, r.Postfix) {
			return false
		}
	}
	if r.VarNameContains != "" {
		if !strings.Contains(strings.ToLower(varName), strings.ToLower(r.VarNameContains)) {
			return false
		}
	}
	return true
}

// LookupExtRuleInSlice scans rules (assumed sorted by descending
// Priority) and returns the first matching description. Returns
// ok=false when no rule matches. Pass empty strings for dimensions the
// caller does not have.
func LookupExtRuleInSlice(rules []EntityDescriptionExtRule, deviceModel, parameter, unit, postfix, varName string) (EntityDescription, bool) {
	for i := range rules {
		if rules[i].MatchesExt(deviceModel, parameter, unit, postfix, varName) {
			return rules[i].Description, true
		}
	}
	return EntityDescription{}, false
}

// LookupExtRuleForComponent dispatches to the per-domain extended rule
// slice for comp. Returns ok=false for components that have no extended
// rules registered.
func LookupExtRuleForComponent(comp HAComponent, deviceModel, parameter, unit, postfix, varName string) (EntityDescription, bool) {
	switch comp { //nolint:exhaustive // only domains with registered ext rules are handled; others fall through
	case HAComponentSensor:
		return LookupExtRuleInSlice(sensorExtRules, deviceModel, parameter, unit, postfix, varName)
	case HAComponentBinarySensor:
		return LookupExtRuleInSlice(binarySensorExtRules, deviceModel, parameter, unit, postfix, varName)
	case HAComponentNumber:
		return LookupExtRuleInSlice(numberExtRules, deviceModel, parameter, unit, postfix, varName)
	case HAComponentSwitch:
		return LookupExtRuleInSlice(switchExtRules, deviceModel, parameter, unit, postfix, varName)
	}
	return EntityDescription{}, false
}

// EntityDescriptionForExt is the extended variant of [EntityDescriptionFor]
// that additionally accepts unit, postfix, and varName for matching.
// Lookup precedence:
//  1. Per-domain devParam static tables (via [LookupRulesForComponent]).
//  2. Per-domain extended rule slices (via [LookupExtRuleForComponent]).
//  3. Event/Text bespoke tables.
//  4. Zero value.
func EntityDescriptionForExt(comp HAComponent, deviceModel, parameter, unit, postfix, varName string) MqttEntityDescription {
	// Tier 1 — fast static devParam maps.
	if d, ok := LookupRulesForComponent(comp, deviceModel, parameter); ok {
		return descToMqtt(d)
	}
	// Tier 2 — extended rule slices (unit / postfix / var_name_contains).
	if d, ok := LookupExtRuleForComponent(comp, deviceModel, parameter, unit, postfix, varName); ok {
		return descToMqtt(d)
	}
	// Tier 3 — event / text bespoke tables (same as EntityDescriptionFor).
	switch comp { //nolint:exhaustive // only event / text need bespoke fallback lookups
	case HAComponentEvent:
		if d, ok := LookupEvent(parameter); ok {
			return descToMqtt(d)
		}
	case HAComponentText:
		if d, ok := LookupTextDisplayByDevice(deviceModel); ok {
			return descToMqtt(d)
		}
	}
	return MqttEntityDescription{}
}

// hasModelPrefix reports whether `deviceModel` either equals the
// `prefix` exactly, or starts with `prefix-` (so a rule keyed on
// "HmIP-eTRV" still matches "HmIP-eTRV-2").
func hasModelPrefix(deviceModel, prefix string) bool {
	if deviceModel == prefix {
		return true
	}
	if len(deviceModel) > len(prefix)+1 && deviceModel[:len(prefix)] == prefix && deviceModel[len(prefix)] == '-' {
		return true
	}
	return false
}

// ---------------------------------------------------------------------------
// Press-type event descriptions
// ---------------------------------------------------------------------------

// eventDescriptionsByParameter holds HA Discovery enrichments for
// button-press event parameters. Every PRESS_* parameter maps to an
// HAComponentEvent entity; the description carries
// `device_class: button` and enabled-by-default = true so press events
// surface in the HA dashboard without operator action.
var eventDescriptionsByParameter = map[string]EntityDescription{
	"PRESS_SHORT":        {Key: "PRESS_SHORT", DeviceClass: "button", EnabledByDefault: true, SuggestedDisplayPrecision: -1},
	"PRESS_LONG":         {Key: "PRESS_LONG", DeviceClass: "button", EnabledByDefault: true, SuggestedDisplayPrecision: -1},
	"PRESS_LONG_RELEASE": {Key: "PRESS_LONG_RELEASE", DeviceClass: "button", EnabledByDefault: true, SuggestedDisplayPrecision: -1},
	"PRESS_LONG_START":   {Key: "PRESS_LONG_START", DeviceClass: "button", EnabledByDefault: true, SuggestedDisplayPrecision: -1},
}

// LookupEvent returns the event-entity description for a press-type
// parameter. Returns ok=false when no override applies.
func LookupEvent(parameter string) (EntityDescription, bool) {
	desc, ok := eventDescriptionsByParameter[parameter]
	return desc, ok
}

// ---------------------------------------------------------------------------
// TextDisplay entity descriptions
// ---------------------------------------------------------------------------

// textDescriptionsByDevice carries HA Discovery enrichments for HmIP
// text display devices (HmIP-WRCD and future variants). The entity is
// write-only by nature; `enabled_by_default: true` so it shows up in
// the HA dashboard.
var textDescriptionsByDevice = map[string]EntityDescription{
	"HmIP-WRCD": {Key: "TEXT_DISPLAY", EnabledByDefault: true, SuggestedDisplayPrecision: -1},
}

// LookupTextDisplayByDevice returns the text-display description for
// a device model. An exact hit takes precedence over a prefix match so
// future HmIP-WRCD variants (e.g. "HmIP-WRCD-2") still resolve.
func LookupTextDisplayByDevice(deviceModel string) (EntityDescription, bool) {
	if d, ok := textDescriptionsByDevice[deviceModel]; ok {
		return d, true
	}
	for k, d := range textDescriptionsByDevice {
		if hasModelPrefix(deviceModel, k) {
			return d, true
		}
	}
	return EntityDescription{}, false
}

// ---------------------------------------------------------------------------
// Unified public API — MqttEntityDescription + EntityDescriptionFor
// ---------------------------------------------------------------------------

// MqttEntityDescription carries optional HA Discovery overrides on top
// of the wire defaults. Pointer fields are nil when no override applies.
//
//nolint:revive // MqttEntityDescription intentionally includes the package qualifier.
type MqttEntityDescription struct {
	// DeviceClass overrides the Quantity-derived HA device_class when non-empty.
	DeviceClass string
	// StateClass overrides the ValueBehavior-derived HA state_class when non-empty.
	StateClass string
	// UnitOfMeasurement overrides the CCU-reported unit (e.g. "mHz", "µm").
	UnitOfMeasurement string
	// EntityCategory is "config", "diagnostic", or "" for primary state.
	EntityCategory string
	// EnabledDefault is nil when no override applies (inherit HA default = true).
	EnabledDefault *bool
	// Icon is an MDI reference string (e.g. "mdi:brightness-6").
	Icon string
	// SuggestedDisplayPrecision is nil when no override applies.
	SuggestedDisplayPrecision *int
}

// boolPtr returns a pointer to b.
func boolPtr(b bool) *bool { return &b }

// intPtr returns a pointer to i.
func intPtr(i int) *int { return &i }

// descToMqtt converts an internal [EntityDescription] to
// [MqttEntityDescription], populating pointer fields only where the
// internal value deviates from the "no override" sentinel:
//   - EnabledDefault is set when EnabledByDefault is false (default true).
//   - SuggestedDisplayPrecision is set when the internal value is >= 0.
func descToMqtt(d EntityDescription) MqttEntityDescription {
	m := MqttEntityDescription{
		DeviceClass:       d.DeviceClass,
		StateClass:        d.StateClass,
		UnitOfMeasurement: d.UnitOfMeasurement,
		EntityCategory:    d.EntityCategory,
		Icon:              d.Icon,
	}
	if !d.EnabledByDefault {
		m.EnabledDefault = boolPtr(false)
	}
	if d.SuggestedDisplayPrecision >= 0 {
		m.SuggestedDisplayPrecision = intPtr(d.SuggestedDisplayPrecision)
	}
	return m
}

// EntityDescriptionFor returns the override applicable to
// (component, deviceModel, parameter). Per-domain rule tables in
// `entity_description_rules_*.go` carry per-device + per-parameter
// overrides keyed by `(devicePrefix, parameter)`.
//
// `Event` and `Text` keep dedicated lookup tables here because they
// have no per-domain rule file.
func EntityDescriptionFor(comp HAComponent, deviceModel, parameter string) MqttEntityDescription {
	if d, ok := LookupRulesForComponent(comp, deviceModel, parameter); ok {
		return descToMqtt(d)
	}
	switch comp { //nolint:exhaustive // only event / text need bespoke fallback lookups; other components fall through to the descriptor defaults

	case HAComponentEvent:
		// Press-type sub-event entities: look up by parameter name.
		if d, ok := LookupEvent(parameter); ok {
			return descToMqtt(d)
		}
	case HAComponentText:
		// TextDisplay entities: look up by device model (exact + prefix).
		if d, ok := LookupTextDisplayByDevice(deviceModel); ok {
			return descToMqtt(d)
		}
	}
	return MqttEntityDescription{}
}
