// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package adapter

import "github.com/SukramJ/openccu-loom/internal/ccudata"

// ParameterLabelAdapter renders a human-readable parameter name from
// the CCU translation archive, pre-bound to a locale. It satisfies
// [handlers.ParameterLabeler] and [ui.ParameterLabeler] so both the
// REST and UI layers share the same translation surface.
//
// A nil translations pointer is tolerated — the adapter then returns
// the raw parameter string and callers see no label.
type ParameterLabelAdapter struct {
	translations *ccudata.Translations
	locale       string
}

// NewParameterLabelAdapter builds the adapter.
func NewParameterLabelAdapter(t *ccudata.Translations, locale string) *ParameterLabelAdapter {
	return &ParameterLabelAdapter{translations: t, locale: locale}
}

// ParameterLabel returns the localised label or the empty string when
// no translation exists. Callers decide whether to fall back to the
// raw parameter name.
func (a *ParameterLabelAdapter) ParameterLabel(parameter string) string {
	if a == nil || a.translations == nil {
		return ""
	}
	return a.translations.ParameterLabel(a.locale, "", parameter)
}

// ChannelTypedParameterLabel mirrors
// `get_parameter_translation(parameter, channel_type, locale)`: the
// channel-type-specific lookup is consulted first, then the bare
// parameter, then the SHORT_/LONG_ LINK fallback. Used by the MQTT
// EventBridge to render HA Discovery `name` fields.
func (a *ParameterLabelAdapter) ChannelTypedParameterLabel(channelType, parameter string) string {
	if a == nil || a.translations == nil {
		return ""
	}
	return a.translations.ParameterLabel(a.locale, channelType, parameter)
}

// ChannelTypedParameterLabelOk is the (label, found) variant of
// [ChannelTypedParameterLabel]; the bool distinguishes "no entry"
// from an explicit-empty translation (the "primary parameter" marker
// in the embedded translation_custom catalogue).
func (a *ParameterLabelAdapter) ChannelTypedParameterLabelOk(channelType, parameter string) (string, bool) {
	if a == nil || a.translations == nil {
		return "", false
	}
	return a.translations.ParameterLabelOk(a.locale, channelType, parameter)
}

// ChannelTypeLabel resolves the localised channel-type label via the
// OCCU `channel_types_<locale>` table. Returns the empty string when
// no translation exists; callers fall back to the raw type token.
func (a *ParameterLabelAdapter) ChannelTypeLabel(channelType string) string {
	if a == nil || a.translations == nil {
		return ""
	}
	return a.translations.ChannelType(a.locale, channelType)
}

// ChannelTypedValueLabel resolves the localised display string for a single
// ENUM value via the OCCU `parameter_values_<locale>` table. It mirrors
// `get_value_translation(parameter, channel_type, value, locale)`: the
// channel-typed lookup is consulted first, then the bare parameter+value, then
// the value-only fallback. Returns the raw value verbatim when no translation
// exists, so callers can detect "untranslated" by comparing against the input.
func (a *ParameterLabelAdapter) ChannelTypedValueLabel(channelType, parameter, value string) string {
	if a == nil || a.translations == nil {
		return value
	}
	return a.translations.ParameterValue(a.locale, channelType, parameter, value)
}

// MqttParameterLabelAdapter wraps a [*ParameterLabelAdapter] in the
// channel-type-aware shape `mqtt.ParameterLabeler` requires. The
// concrete type stays here so the mqtt package keeps no dependency
// on `internal/ccudata`.
type MqttParameterLabelAdapter struct {
	inner *ParameterLabelAdapter
}

// NewMqttParameterLabelAdapter returns the MQTT-shaped wrapper.
func NewMqttParameterLabelAdapter(inner *ParameterLabelAdapter) *MqttParameterLabelAdapter {
	return &MqttParameterLabelAdapter{inner: inner}
}

// ParameterLabel implements `mqtt.ParameterLabeler`.
func (a *MqttParameterLabelAdapter) ParameterLabel(channelType, parameter string) string {
	if a == nil || a.inner == nil {
		return ""
	}
	return a.inner.ChannelTypedParameterLabel(channelType, parameter)
}

// ParameterLabelOk implements `mqtt.ParameterLabeler`.
func (a *MqttParameterLabelAdapter) ParameterLabelOk(channelType, parameter string) (string, bool) {
	if a == nil || a.inner == nil {
		return "", false
	}
	return a.inner.ChannelTypedParameterLabelOk(channelType, parameter)
}
