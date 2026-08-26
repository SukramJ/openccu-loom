// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package mqtt

// ApplyEntityDescription overlays
// EntityDescription for (component, parameter, model, unit, postfix)
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
var entityDescriptionAuthoritativeFields = []string{
	"suggested_display_precision",
	"translation_key",
}

// The function deletes [entityDescriptionAuthoritativeFields] from the body
// when no rule matches; for matched rules it applies authoritative
// replacement on every HA-attribute field. Returns the matched rule (nil on
// a miss) so a caller that needs a field this function doesn't copy into
// body — e.g. Multiplier, applied separately by [applyMultiplierSensor] /
// [applyMultiplierNumber] because it needs the parameter's live value,
// not a static body field — does not have to re-run the lookup.
func applyEntityDescription(body map[string]any, component, parameter, model, unit, postfix string) *HARegistryDescription {
	desc := HARegistryDescriptionLookup(component, parameter, model, unit, postfix, "")
	if desc == nil {
		// Strict ownership of fields the HA integration is the sole source for
		// without this the legacy EntityDescriptionFor's
		// SuggestedDisplayPrecision (zero-value-emits-intPtr(0)) would
		// leak `suggested_display_precision: 0` for every Number-Entity
		// the HA integration has no rule for.
		for _, k := range entityDescriptionAuthoritativeFields {
			delete(body, k)
		}
		return nil
	}
	setOrDeleteString(body, "device_class", desc.DeviceClass)
	setOrDeleteString(body, "state_class", desc.StateClass)
	setOrDeleteString(body, "entity_category", desc.EntityCategory)
	setOrDeleteString(body, "icon", desc.Icon)
	setOrDeleteString(body, "translation_key", desc.TranslationKey)
	if desc.UnitOfMeasurement != "" {
		// `unit_of_measurement` is special: an empty
		// `native_unit_of_measurement` in the rule means HA falls back
		// to `data_point.unit`.
		// Keep the legacy body value when the rule doesn't override.
		body["unit_of_measurement"] = desc.UnitOfMeasurement
	}
	if desc.SuggestedDisplayPrecision != nil {
		body["suggested_display_precision"] = *desc.SuggestedDisplayPrecision
	} else {
		delete(body, "suggested_display_precision")
	}
	if desc.EnabledByDefault != nil {
		body["enabled_by_default"] = *desc.EnabledByDefault
	} else {
		// HA's default for enabled_by_default is true, which the
		// MQTT-Discovery convention is to omit. Mirror that.
		delete(body, "enabled_by_default")
	}
	if len(desc.Options) > 0 {
		opts := make([]any, len(desc.Options))
		for i, v := range desc.Options {
			opts[i] = v
		}
		body["options"] = opts
	}
	return desc
}

// setOrDeleteString is the per-field authoritative-replacement
// helper: write the value if non-empty, otherwise drop the key.
func setOrDeleteString(body map[string]any, key, value string) {
	if value != "" {
		body[key] = value
	} else {
		delete(body, key)
	}
}

// applyEntityDescriptionStrict is the strict variant for aggregated
// custom-DPs (cover, lock, light, valve, siren). When no rule
// Matches and no per-category default exists
// the helper purges every HA-attribute field from the body so the
// resulting MQTT-Discovery payload mirrors HA-native behaviour
// ("the device has no device_class" rather than "the legacy
// openccu-loom table guessed a device_class"). Use the regular
// `applyEntityDescription` for per-parameter entities, where
// openccu-loom's Quantity-based fall-through is still useful.
func applyEntityDescriptionStrict(body map[string]any, component, parameter, model, unit, postfix string) {
	desc := HARegistryDescriptionLookup(component, parameter, model, unit, postfix, "")
	if desc == nil {
		// No rule and no default → HA-native shows no description-
		// derived attributes. Match it.
		delete(body, "device_class")
		delete(body, "state_class")
		delete(body, "entity_category")
		delete(body, "icon")
		delete(body, "translation_key")
		delete(body, "suggested_display_precision")
		delete(body, "enabled_by_default")
		return
	}
	applyEntityDescription(body, component, parameter, model, unit, postfix)
}
