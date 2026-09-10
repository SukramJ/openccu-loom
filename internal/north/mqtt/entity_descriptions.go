// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package mqtt

import (
	"strings"

	"github.com/SukramJ/openccu-loom/internal/ccudata"
	"github.com/SukramJ/openccu-loom/internal/payload"
)

// EntityCategory values are Home Assistant's literal constants. They
// are the payload-layer declarations, not a second spelling of them:
// the two planes publish into the same discovery bodies, so a copy that
// drifts places an entity in a category the other plane does not use.
const (
	EntityCategoryConfig     = payload.CombinedEntityCategoryConfig
	EntityCategoryDiagnostic = payload.CombinedEntityCategoryDiagnostic
)

// Unit spellings that carry a prefix Unicode encodes twice. Every one of
// them uses U+00B5 MICRO SIGN, never U+03BC GREEK SMALL LETTER MU, and the
// same spelling is what the domain model normalises CCU units to, so the
// discovery override and the raw plane agree.
//
// Home Assistant does not care which one arrives. Its canonical constant is
// the Greek letter (UnitOfDensity.MICROGRAMS_PER_CUBIC_METER), and
// sensor/__init__.py's _native_unit_of_measurement_compat maps the legacy
// sign onto it with `AMBIGUOUS_UNITS.get(unit, unit)` — a rewrite, not a
// rejection. An earlier version of this comment claimed a PM sensor
// published with the Greek letter never appears at all; that was wrong in
// both directions. Either spelling produces the entity, so the choice here
// is internal consistency with the raw plane, not a Home Assistant
// requirement.
const (
	unitConcentrationCm3     = "1/cm³"
	unitConcentrationGramsM3 = "g/m³"
	unitMicrogramsPerM3      = "µg/m³"
	unitMicrometers          = "µm"
)

// devParam is the composite key for device-and-parameter lookups
// shared across every per-domain rule table in
// `entity_description_rules_*.go`.
type devParam struct {
	devicePrefix string
	parameter    string
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
var eventDescriptionsByParameter = map[string]HARegistryDescription{
	"PRESS_SHORT":        {Key: "PRESS_SHORT", DeviceClass: "button"},
	"PRESS_LONG":         {Key: "PRESS_LONG", DeviceClass: "button"},
	"PRESS_LONG_RELEASE": {Key: "PRESS_LONG_RELEASE", DeviceClass: "button"},
	"PRESS_LONG_START":   {Key: "PRESS_LONG_START", DeviceClass: "button"},
}

// LookupEvent returns the event-entity description for a press-type
// parameter. Returns ok=false when no override applies.
func LookupEvent(parameter string) (HARegistryDescription, bool) {
	desc, ok := eventDescriptionsByParameter[parameter]
	return desc, ok
}

// EventDeviceClassForModel returns the HA `device_class` for a channel-level
// press/ring event entity: "doorbell" for the curated doorbell models
// (the shared device-semantics classification embedded from the
// upstream data package — the reference stack reads the same list),
// else the generic "button". The HA mqtt.event component accepts both.
func EventDeviceClassForModel(model string) string {
	if _, ok := ccudata.DoorbellModels()[model]; ok {
		return "doorbell"
	}
	return "button"
}

// doorbellRingSource is the press type that represents the ring on
// doorbell devices. HA requires doorbell event entities to support the
// standard "ring" event type (mandatory from HA 2027.4), so this type
// is announced and fired as "ring" instead of "press_short".
const doorbellRingSource = "press_short"

// DoorbellRingType is HA's standard doorbell event type.
const DoorbellRingType = "ring"

// MapDoorbellEventTypes rewrites the announced `event_types` of a
// doorbell-class entity: press_short becomes the standard "ring",
// every other type stays. Non-doorbell models pass through untouched.
func MapDoorbellEventTypes(model string, types []string) []string {
	if EventDeviceClassForModel(model) != "doorbell" {
		return types
	}
	out := make([]string, len(types))
	for i, t := range types {
		if t == doorbellRingSource {
			out[i] = DoorbellRingType
		} else {
			out[i] = t
		}
	}
	return out
}

// DoorbellEventType maps one runtime press type onto the announced
// vocabulary: "ring" for the doorbell models' press_short, the
// lower-cased press type otherwise.
func DoorbellEventType(model, pressType string) string {
	t := strings.ToLower(pressType)
	if t == doorbellRingSource && EventDeviceClassForModel(model) == "doorbell" {
		return DoorbellRingType
	}
	return t
}

// ---------------------------------------------------------------------------
// TextDisplay entity descriptions
// ---------------------------------------------------------------------------

// textDescriptionsByDevice carries HA Discovery enrichments for HmIP
// text display devices (HmIP-WRCD and future variants). The entity is
// write-only by nature; `enabled_by_default: true` so it shows up in
// the HA dashboard.
var textDescriptionsByDevice = map[string]HARegistryDescription{
	"HmIP-WRCD": {Key: "TEXT_DISPLAY"},
}

// LookupTextDisplayByDevice returns the text-display description for
// a device model. An exact hit takes precedence over a prefix match so
// future HmIP-WRCD variants (e.g. "HmIP-WRCD-2") still resolve.
func LookupTextDisplayByDevice(deviceModel string) (HARegistryDescription, bool) {
	if d, ok := textDescriptionsByDevice[deviceModel]; ok {
		return d, true
	}
	for k := range textDescriptionsByDevice {
		if hasModelPrefix(deviceModel, k) {
			return textDescriptionsByDevice[k], true
		}
	}
	return HARegistryDescription{}, false
}

// ---------------------------------------------------------------------------
// Unified public API — HARegistryDescription + EntityDescriptionFor
// ---------------------------------------------------------------------------

// EntityDescriptionFor returns the override applicable to
// (component, deviceModel, parameter). Per-domain rule tables in
// `entity_description_rules_*.go` carry per-device + per-parameter
// overrides keyed by `(devicePrefix, parameter)`.
//
// `Event` and `Text` keep dedicated lookup tables here because they
// have no per-domain rule file.
func EntityDescriptionFor(comp HAComponent, deviceModel, parameter string) HARegistryDescription {
	if d, ok := LookupRulesForComponent(comp, deviceModel, parameter); ok {
		return d
	}
	switch comp { //nolint:exhaustive // only event / text need bespoke fallback lookups; other components fall through to the descriptor defaults

	case HAComponentEvent:
		// Press-type sub-event entities: look up by parameter name.
		if d, ok := LookupEvent(parameter); ok {
			return d
		}
	case HAComponentText:
		// TextDisplay entities: look up by device model (exact + prefix).
		if d, ok := LookupTextDisplayByDevice(deviceModel); ok {
			return d
		}
	}
	return HARegistryDescription{}
}
