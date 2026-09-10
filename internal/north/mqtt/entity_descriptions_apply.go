// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package mqtt

import (
	hacatalog "github.com/SukramJ/go-ha-catalog"
	hadiscovery "github.com/SukramJ/go-hamqtt/discovery"
)

// ApplyEntityDescription overlays
// HARegistryDescription for (component, parameter, model, unit, postfix)
// Onto a discovery body.
// REGISTRY is the authoritative HA-attribute source.
//
// When a rule matches, the helper applies it AUTHORITATIVELY: every
// HA-attribute field is set from the description, and any field the
// description leaves empty is deleted from the body. This mirrors HA-
// native behaviour: a rule covering a device sets exactly the
// attributes it declares; an empty field means "explicit default", not
// "fall through to a different table". Without authoritative
// replacement, the legacy Quantity-/EntityDescriptionFor pass would
// keep emitting a `device_class` (or other field) for parameters where
// the HA-native integration's device-specific rule purposefully omits
// it (canonical case: `HMW-IO-12-Sw14-DR FREQUENCY` — generic rule sets
// `device_class=frequency`, but the device-specific priority-10 rule
// drops it together with overriding the unit to `mHz`).
//
// Fields that
// the openccu-loom chain emits them on its own). When the lookup has
// no match these are deleted from the body so legacy
// `EntityDescriptionFor` values do not leak through. Other fields
// (`device_class`, `state_class`, `unit_of_measurement`) keep their
// fall-through to the Quantity-/legacy-derived defaults because those
// Are also OCCU-rooted via
// clearAuthoritativeFields drops the fields the HA integration is the sole
// source for when no rule matched. Without it the legacy
// EntityDescriptionFor's SuggestedDisplayPrecision (whose zero value emits an
// intPtr(0)) would leak `suggested_display_precision: 0` onto every number
// entity the integration has no rule for.
func clearAuthoritativeFields(comp *hadiscovery.Component) {
	comp.Precision = nil
	delete(comp.Extra, "translation_key")
}

// The function deletes [entityDescriptionAuthoritativeFields] from the body
// when no rule matches; for matched rules it applies authoritative
// replacement on every HA-attribute field. Returns the matched rule (nil on
// a miss) so a caller that needs a field this function doesn't copy into
// body — e.g. Multiplier, applied separately by [applyMultiplierSensor] /
// [applyMultiplierNumber] because it needs the parameter's live value,
// not a static body field — does not have to re-run the lookup.
func applyEntityDescription(comp *hadiscovery.Component, component, parameter, model, unit, postfix string) *HARegistryDescription {
	desc := HARegistryDescriptionLookup(component, parameter, model, unit, postfix, "")
	if desc == nil {
		clearAuthoritativeFields(comp)
		return nil
	}
	comp.DeviceClass = desc.DeviceClass
	comp.StateClass = hacatalog.StateClass(desc.StateClass)
	comp.EntityCategory = hacatalog.EntityCategory(desc.EntityCategory)
	comp.Icon = desc.Icon
	setTranslationKey(comp, desc.TranslationKey)
	if desc.UnitOfMeasurement != "" {
		// `unit_of_measurement` is special: an empty
		// `native_unit_of_measurement` in the rule means HA falls back
		// to `data_point.unit`.
		// Keep the legacy body value when the rule doesn't override.
		comp.UnitOfMeasure = desc.UnitOfMeasurement
	}
	if desc.SuggestedDisplayPrecision != nil {
		comp.Precision = hadiscovery.Ptr(*desc.SuggestedDisplayPrecision)
	} else {
		comp.Precision = nil
	}
	if desc.EnabledByDefault != nil {
		comp.EnabledByDefault = hadiscovery.Ptr(*desc.EnabledByDefault)
	} else {
		// HA's default for enabled_by_default is true, which the
		// MQTT-Discovery convention is to omit. Mirror that.
		comp.EnabledByDefault = nil
	}
	if len(desc.Options) > 0 {
		comp.Options = append([]string(nil), desc.Options...)
	}
	return desc
}

// setTranslationKey writes or clears the cross-stack parity marker.
//
// It lives in Extra because Home Assistant declares `translation_key` on no
// platform and drops it on receipt; the daemon publishes it anyway so the
// parity tooling can compare against the Python integration. See
// discoveryKeysHomeAssistantIgnores.
func setTranslationKey(comp *hadiscovery.Component, key string) {
	if key == "" {
		delete(comp.Extra, "translation_key")
		return
	}
	if comp.Extra == nil {
		comp.Extra = map[string]any{}
	}
	comp.Extra["translation_key"] = key
}

func applyEntityDescriptionStrict(comp *hadiscovery.Component, component, parameter, model, unit, postfix string) {
	desc := HARegistryDescriptionLookup(component, parameter, model, unit, postfix, "")
	if desc == nil {
		// No rule and no default → HA-native shows no description-
		// derived attributes. Match it.
		comp.DeviceClass = ""
		comp.StateClass = ""
		comp.EntityCategory = ""
		comp.Icon = ""
		setTranslationKey(comp, "")
		comp.Precision = nil
		comp.EnabledByDefault = nil
		return
	}
	applyEntityDescription(comp, component, parameter, model, unit, postfix)
}
