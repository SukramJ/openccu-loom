// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

// Package naming holds the cached presentation strings every data
// point exposes to north-bound consumers (MQTT, REST, WS, UI). It is
// the openccu-loom equivalent of the [DataPointNameData] / [PathData]
// Type families in.
//
// The package lives one level above [internal/model/device] and
// [internal/model/datapoint] so both can depend on the value types
// without creating an import cycle. Channel-aware factory functions
// (`BuildDataPointName`) live in `internal/model/device` because
// they need a `*device.Channel`; the value types they return are
// defined here.
package naming

import "strings"

// NameData is the canonical name quadruple every channel-bound data point
// caches.
//
// Three derived names are exposed on the type:
//
// - [NameData.Name] — the entity name with the device-name prefix stripped.
// This is what HA expects in the MQTT discovery `name` field; the MQTT bridge
// prepends the device's name from the `device` block automatically, so
// duplicating it here would yield "Wohnzimmer Wohnzimmer Schalter" in the
// frontend.
//
// - [NameData.TranslatedName] — uses the locale-aware
// [NameData.TranslatedParameterName] (OCCU translation) when set, otherwise
// falls back to [NameData.Name].
//
// - [NameData.FullName] / [NameData.TranslatedFullName] — re-prepend the
// device name. Useful for log messages, audit entries, REST list views.
type NameData struct {
	// DeviceName is the CCU-assigned device name (e.g.
	// "Wohnzimmer Heizung"). Falls back to "<Model>_<Address>" when
	// the operator never named the device.
	DeviceName string

	// ChannelName is the per-channel name with the address-suffix stripped.
	ChannelName string

	// ParameterName is the title-cased parameter name plus the
	// `" chN"` multi-channel postfix when applicable
	// (e.g. "Set Point Temperature ch3"). Empty for entities that
	// represent the channel as a whole (custom DPs after postfix
	// resolution).
	ParameterName string

	// TranslatedParameterName carries the locale-aware OCCU label
	// for the parameter (e.g. "Solltemperatur ch3"). Empty when no
	// translation exists — callers fall back to [ParameterName].
	TranslatedParameterName string

	// ChannelPostfix is the bare multi-channel disambiguation marker
	// ("ch3") when the postfix applies, empty otherwise. It is already
	// folded into [ParameterName] / [TranslatedParameterName]; it is
	// kept separately so the channel-level collapse
	// ([NameData.CollapsedName]) can re-apply it without string
	// surgery on the composed names.
	ChannelPostfix string
}

// EmptyNameData is the zero-value sentinel returned when none of the
// inputs allow a meaningful name to be built (no device, empty
// channel name).
var EmptyNameData = NameData{}

// IsZero reports whether the name data is empty (no device, no
// channel, no parameter, no translation). Callers use it to detect
// the EmptyNameData sentinel without depending on equality of the
// struct itself.
func (n NameData) IsZero() bool {
	return n.DeviceName == "" && n.ChannelName == "" && n.ParameterName == "" && n.TranslatedParameterName == ""
}

// Name returns the entity name without the device prefix.
func (n NameData) Name() string {
	return composeName(n.ChannelName, n.ParameterName, n.DeviceName)
}

// WithTranslatedParameter returns a copy of n carrying translation as its
// [NameData.TranslatedParameterName], with the already-decided
// [NameData.ChannelPostfix] re-applied. An empty translation leaves n
// unchanged, so the caller keeps the [ParameterName] fallback.
//
// This is the only place a translation and the multi-channel postfix are
// joined. A caller that composes the pair itself has to re-derive when
// the postfix applies, and re-deriving is how the MQTT discovery name
// drifted away from the REST one: the copy tested two of the three
// conditions the postfix rule actually has, so a data point on a named
// channel of a multi-channel device (FROST_PROTECTION on HmIP-BWTH
// channels 1 and 8) was published as "Frostschutz ch1" over MQTT while
// REST called it "Frostschutz". Whether the postfix applies is decided
// once, by the authority that builds the NameData.
func (n NameData) WithTranslatedParameter(translation string) NameData {
	if translation == "" {
		return n
	}
	n.TranslatedParameterName = strings.TrimSpace(translation + " " + n.ChannelPostfix)
	return n
}

// TranslatedName uses [NameData.TranslatedParameterName] when set,
// else falls back to [NameData.Name]. The device-name prefix is
// stripped the same way as in [NameData.Name].
func (n NameData) TranslatedName() string {
	if n.TranslatedParameterName == "" {
		return n.Name()
	}
	return composeName(n.ChannelName, n.TranslatedParameterName, n.DeviceName)
}

// CollapsedName is the channel-level entity name used when the
// parameter's label is omitted (the "primary parameter" marker): the
// channel name plus the multi-channel marker, with the device-name
// prefix stripped like [NameData.Name]. It may be empty when the
// collapse reduces to the device name alone (a derived channel name on
// a single-channel parameter) — consumers then fall back to the device
// name.
func (n NameData) CollapsedName() string {
	return composeName(n.ChannelName, n.ChannelPostfix, n.DeviceName)
}

// FullName re-prepends the device name. Returns [NameData.DeviceName]
// alone when the entity name is empty (channel-level entity).
func (n NameData) FullName() string {
	name := n.Name()
	if n.DeviceName == "" {
		return name
	}
	if name == "" {
		return n.DeviceName
	}
	return n.DeviceName + " " + name
}

// TranslatedFullName is the [NameData.FullName] variant using
// [NameData.TranslatedParameterName].
func (n NameData) TranslatedFullName() string {
	name := n.TranslatedName()
	if n.DeviceName == "" {
		return name
	}
	if name == "" {
		return n.DeviceName
	}
	return n.DeviceName + " " + name
}

// composeName combines channel + parameter and strips the device
// prefix when the joined string starts with it. This is the exact
// Behaviour
// Heizung Solltemperatur" duplication.
func composeName(channelName, parameterName, deviceName string) string {
	var base string
	switch {
	case channelName != "" && parameterName != "":
		base = channelName + " " + parameterName
	case channelName != "":
		base = channelName
	default:
		base = parameterName
	}
	base = strings.TrimSpace(base)
	if deviceName != "" && strings.HasPrefix(base, deviceName) {
		base = strings.TrimSpace(base[len(deviceName):])
	}
	return base
}
