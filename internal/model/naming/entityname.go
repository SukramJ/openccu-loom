// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package naming

import (
	"strings"
	"unicode"
)

// EntityDisplayName resolves the per-entity name a north-bound consumer
// (HA via MQTT discovery or via the REST drop-in) assigns to a data
// point, and reports whether the name is omitted.
//
// This is the single source of truth shared by the MQTT discovery
// builder and the REST data-point handler so both emit identical
// names. The resolution order mirrors HA-native integrations:
//
//  1. labelOmitted → ("", true): the parameter is flagged "primary" in
//     the embedded translation_custom catalogue (translation key
//     present, value empty). HA collapses the entity name to the
//     device name alone (MQTT emits `name: null`).
//  2. label != "" → (label, false): the locale-aware OCCU translation.
//  3. otherwise → (TitleCaseParameter(parameter), false): the
//     title-cased parameter fallback.
func EntityDisplayName(label string, labelOmitted bool, parameter string) (name string, omitted bool) {
	if labelOmitted {
		return "", true
	}
	if label != "" {
		return label, false
	}
	return TitleCaseParameter(parameter), false
}

// TitleCaseParameter mirrors Python's `str.title().replace("_", " ")`
// for HM parameter names: each underscore-delimited segment becomes
// title-cased and joined with spaces.
//
//	"RSSI_DEVICE"           → "Rssi Device"
//	"SET_POINT_TEMPERATURE" → "Set Point Temperature"
//	"LEVEL_2"               → "Level 2"
func TitleCaseParameter(parameter string) string {
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
