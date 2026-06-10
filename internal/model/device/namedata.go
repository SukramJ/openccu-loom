// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package device

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"

	"github.com/SukramJ/openccu-loom/internal/model/naming"
)

// BuildDataPointName is the openccu-loom equivalent of
// It
// resolves the per-data-point name quadruple from a (channel,
// parameter, parameter-translation) triple.
//
// Resolution steps (mirroring AHM line-by-line):
//
//  1. Compute the channel base name — the operator-assigned channel
//     name, with `f"{model} {address}"` auto-defaults rejected.
//     Falls back to `f"{device.name}:{ch.no}"`.
//  2. Build the parameter-name fallback `parameter.title().
//     replace("_", " ")` (e.g. RSSI_DEVICE → "Rssi Device").
//  3. When the parameter exists on more than one channel of the
//     same device AND the channel is non-zero, append `" chN"` so
//     two STATE entities of a 2-channel switch don't collide.
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

	pName := titleCaseParameter(parameter)
	postfix := ""
	if channel.IsParameterInMultipleChannels(parameter) && channel.Number != 0 {
		postfix = fmt.Sprintf(" ch%d", channel.Number)
	}

	cName := stripChannelAddressSuffix(channelName)

	translated := ""
	if parameterTranslation != "" {
		translated = strings.TrimSpace(parameterTranslation + postfix)
	}

	return naming.NameData{
		DeviceName:              deviceName,
		ChannelName:             cName,
		ParameterName:           strings.TrimSpace(pName + postfix),
		TranslatedParameterName: translated,
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
	translation, translated := "", false
	if labels != nil {
		translation, translated = labels.ChannelTypedParameterLabelOk(channelType, parameter)
	}
	labelOmitted = translated && translation == ""
	label = BuildDataPointName(channel, parameter, translation).TranslatedName()
	return label, labelOmitted
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
// suffix (where N is purely numeric). Mirrors AHM
// `support.py::_check_channel_name_with_channel_no` + the
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

// titleCaseParameter mirrors Python's `str.title().replace("_", " ")`
// for HM parameter names: each underscore-delimited segment becomes
// title-cased and joined with spaces.
//
//	"RSSI_DEVICE"           → "Rssi Device"
//	"SET_POINT_TEMPERATURE" → "Set Point Temperature"
//	"LEVEL_2"               → "Level 2"
func titleCaseParameter(parameter string) string {
	if parameter == "" {
		return ""
	}
	parts := strings.Split(parameter, "_")
	for i, p := range parts {
		if p == "" {
			continue
		}
		runes := []rune(strings.ToLower(p))
		runes[0] = unicode.ToUpper(runes[0])
		parts[i] = string(runes)
	}
	return strings.Join(parts, " ")
}
