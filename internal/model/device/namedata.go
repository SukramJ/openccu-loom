// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package device

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/SukramJ/openccu-loom/internal/model/naming"
)

// BuildDataPointName is the openccu-loom equivalent of the Python
// reference implementation's data-point naming resolver. It resolves
// the per-data-point name quadruple from a (channel, parameter,
// parameter-translation) triple.
//
// Resolution steps (mirroring the Python reference line-by-line):
//
//  1. Compute the channel base name — the operator-assigned channel
//     name, with `f"{model} {address}"` auto-defaults rejected.
//     Falls back to `f"{device.name}:{ch.no}"`.
//  2. Build the parameter-name fallback `parameter.title().
//     replace("_", " ")` (e.g. RSSI_DEVICE → "Rssi Device").
//  3. When the parameter exists on more than one channel of the
//     same device AND the channel is non-zero AND the channel name
//     alone does not identify the channel, append `" chN"` so two
//     STATE entities of a 2-channel switch don't collide. The name
//     is considered ambiguous when it carries a `:N` channel suffix
//     (device-derived or `<name>:<no>`-scheme names) or when another
//     channel providing the same parameter resolves to the same
//     name. A unique custom channel name keeps its clean data point
//     name without the postfix.
//  4. When the channel name has the `<dev>:N` shape, drop the `:N`
//     suffix — `composeName` later strips the leftover device
//     prefix.
//
// `parameterTranslation` — typically the OCCU label produced by
// the channeltype-aware lookup chain — is carried unchanged into
// [naming.NameData.TranslatedParameterName] (with the same `" chN"`
// postfix appended). Pass an empty string when no translation
// exists; consumers will then fall back to the title-cased form.
//
// The returned value is the canonical [naming.NameData] consumed by
// every north-bound adapter. The DP construction pipeline calls this
// function once and caches the result on the data point itself
// (datapoint.BaseDataPointFields.SetNameData).
func BuildDataPointName(channel *Channel, parameter, parameterTranslation string) naming.NameData {
	if channel == nil {
		return naming.EmptyNameData
	}
	deviceName := ""
	model := ""
	if channel.device != nil {
		deviceName = channel.device.Name
		model = channel.device.Model
	}
	channelName := baseChannelName(channel, model, deviceName)
	if channelName == "" {
		return naming.EmptyNameData
	}

	pName := naming.TitleCaseParameter(parameter)
	cName := stripChannelAddressSuffix(channelName)
	nameHasChannelNo := cName != channelName

	postfix := ""
	if channel.Number != 0 && channel.IsParameterInMultipleChannels(parameter) &&
		(nameHasChannelNo || isChannelNameAmbiguous(channel, parameter, channelName, model, deviceName)) {
		postfix = fmt.Sprintf(" ch%d", channel.Number)
	}

	return naming.NameData{
		DeviceName:     deviceName,
		ChannelName:    cName,
		ParameterName:  strings.TrimSpace(pName + postfix),
		ChannelPostfix: strings.TrimSpace(postfix),
	}.WithTranslatedParameter(parameterTranslation)
}

// BuildCustomDataPointName resolves the name quadruple for a channel's
// custom data point. It is the custom-DP sibling of
// [BuildDataPointName], mirroring the Python reference's
// `get_custom_data_point_name` (model/support.py):
//
//  1. A channel whose name carries the `:N` suffix (device-derived or
//     `<name>:<no>` scheme) and that is the device's only primary of
//     its kind — or whose custom DP opts out of multi-channel naming —
//     renders the optional postfix alone (button locks). With an empty
//     postfix the name collapses to the device name.
//  2. Any other `:N`-suffixed channel carries the channel-group marker
//     `ch<no>` (primary) / `vch<no>` (secondary); the digits follow
//     the name suffix, matching the reference's name-split semantics.
//  3. A custom channel name without the `:N` shape is used verbatim.
//
// `postfix` is the raw wire postfix (e.g. "BUTTON_LOCK", title-cased
// here); `postfixTranslation` its locale label — empty when none
// exists.
func BuildCustomDataPointName(channel *Channel, postfix, postfixTranslation string) naming.NameData {
	if channel == nil {
		return naming.EmptyNameData
	}
	deviceName := ""
	model := ""
	if channel.device != nil {
		deviceName = channel.device.Name
		model = channel.device.Model
	}
	channelName := baseChannelName(channel, model, deviceName)
	if channelName == "" {
		return naming.EmptyNameData
	}
	cName := stripChannelAddressSuffix(channelName)
	if cName == channelName {
		return naming.NameData{DeviceName: deviceName, ChannelName: channelName}
	}
	// The single-primary collapse only applies ON the primary channel:
	// HasSinglePrimaryCustomDP counts primaries and therefore returns
	// true even when invoked from a secondary (same trap the MQTT
	// discovery name builder documents). Mirrors the reference's
	// per-channel `is_only_primary_channel`.
	isOnlyPrimary := channel.IsCustomDPPrimaryChannel() && channel.HasSinglePrimaryCustomDP()
	if isOnlyPrimary || channel.IgnoreMultipleChannelsForName() {
		return naming.NameData{
			DeviceName:              deviceName,
			ChannelName:             cName,
			ParameterName:           naming.TitleCaseParameter(postfix),
			TranslatedParameterName: postfixTranslation,
		}
	}
	marker := "vch"
	if channel.IsCustomDPPrimaryChannel() {
		marker = "ch"
	}
	pName := marker + channelName[len(cName)+1:]
	return naming.NameData{
		DeviceName:    deviceName,
		ChannelName:   cName,
		ParameterName: pName,
	}
}

// ParameterTranslator resolves a locale-aware parameter label. The
// (label, found) result distinguishes "no entry" from an explicit-empty
// translation — the "primary parameter" marker in the embedded
// translation_custom catalogue.
type ParameterTranslator interface {
	ChannelTypedParameterLabelOk(channelType, parameter string) (string, bool)
}

// TranslatedDataPointLabel resolves the locale-aware entity label for a
// (channel, parameter) and whether the label is omitted (the parameter
// is flagged "primary" → consumers collapse the entity name to the
// device name alone).
//
// It mirrors the MQTT EventBridge resolution (internal/central/adapter
// eventbridge.go): look up the channel-typed translation, treat an
// explicit-empty translation as the primary marker, and fold the
// translation into the channel-aware name via [BuildDataPointName].
// Both the EventBridge (MQTT discovery) and the REST data-point handler
// feed the returned label/flag into [naming.EntityDisplayName] so every
// north-bound consumer emits identical entity names.
func TranslatedDataPointLabel(
	channel *Channel, parameter, channelType string, labels ParameterTranslator,
) (label string, labelOmitted bool) {
	nd, labelOmitted := TranslatedDataPointNameData(channel, parameter, channelType, labels)
	return nd.TranslatedName(), labelOmitted
}

// TranslatedDataPointNameData is the [TranslatedDataPointLabel] variant
// that exposes the full [naming.NameData] instead of the composed
// label. The REST data-point summary uses it to also ship the
// channel-level collapsed name ([naming.NameData.CollapsedName]) when
// the label is omitted, so REST consumers never re-compose entity
// names client-side.
func TranslatedDataPointNameData(
	channel *Channel, parameter, channelType string, labels ParameterTranslator,
) (nd naming.NameData, labelOmitted bool) {
	translation, labelOmitted := TranslatedParameterLabel(parameter, channelType, labels)
	return BuildDataPointName(channel, parameter, translation), labelOmitted
}

// TranslatedParameterLabel is the channel-independent core of
// [TranslatedDataPointLabel]: it resolves the locale-aware translation
// for a (channelType, parameter) pair and whether the label is omitted
// (the explicit-empty "primary parameter" marker in the embedded
// translation_custom catalogue).
//
// Consumers that compose the device / channel context themselves — the
// Matter endpoint assembler builds its NodeLabel from device-name +
// channel-name and only needs the parameter-level portion as a suffix —
// call this directly and feed the result into
// [naming.EntityDisplayName], so the per-parameter display name stays
// identical across MQTT, REST, and Matter.
func TranslatedParameterLabel(
	parameter, channelType string, labels ParameterTranslator,
) (translation string, labelOmitted bool) {
	if labels == nil {
		return "", false
	}
	translation, translated := labels.ChannelTypedParameterLabelOk(channelType, parameter)
	return translation, translated && translation == ""
}

// isChannelNameAmbiguous reports whether another channel of the same
// device provides the given parameter AND resolves to the same channel
// name — i.e. the name alone cannot identify the channel and the `" chN"`
// postfix is required. Mirrors the Python reference implementation's
// `support.py::_is_channel_name_ambiguous`.
func isChannelNameAmbiguous(channel *Channel, parameter, channelName, model, deviceName string) bool {
	if channel.device == nil {
		return false
	}
	for _, sibling := range channel.device.Channels() {
		if sibling.Address == channel.Address {
			continue
		}
		if sibling.HasParameter(parameter) &&
			baseChannelName(sibling, model, deviceName) == channelName {
			return true
		}
	}
	return false
}

// baseChannelName implements
// returns the operator-assigned channel name when it is a real label,
// otherwise the synthetic `f"{device.name}:{ch.no}"` form.
func baseChannelName(channel *Channel, model, deviceName string) string {
	autoDefault := strings.TrimSpace(model + " " + channel.Address)
	if channel.Name != "" && channel.Name != autoDefault {
		return channel.Name
	}
	if deviceName == "" {
		return ""
	}
	return fmt.Sprintf("%s:%d", deviceName, channel.Number)
}

// stripChannelAddressSuffix removes a trailing `:N` channel-number
// suffix (where N is purely numeric). Mirrors the Python reference
// implementation's `support.py::_check_channel_name_with_channel_no` + the
// `channel_name.split(ADDRESS_SEPARATOR)[0]` truncation.
func stripChannelAddressSuffix(channelName string) string {
	idx := strings.LastIndexByte(channelName, ':')
	if idx <= 0 {
		return channelName
	}
	if _, err := strconv.Atoi(channelName[idx+1:]); err != nil {
		return channelName
	}
	return channelName[:idx]
}
