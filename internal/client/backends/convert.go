// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package backends

import (
	"encoding/json"

	"github.com/SukramJ/openccu-loom/pkg/hmproto"
)

// toDeviceDescription converts a decoded struct into the typed
// [hmproto.DeviceDescription] by round-tripping through JSON. It
// Normalises a few CCU quirks first
// for boolean fields, while the real CCU sends true/false.
func toDeviceDescription(m map[string]any) (hmproto.DeviceDescription, error) {
	normaliseBoolFields(m, "ROAMING", "UPDATABLE", "DIRECT_LINK_DEACTIVATED")
	normaliseStringSlice(m, "CHILDREN", "PARAMSETS")
	buf, err := json.Marshal(m)
	if err != nil {
		return hmproto.DeviceDescription{}, err
	}
	var dd hmproto.DeviceDescription
	if err := json.Unmarshal(buf, &dd); err != nil {
		return hmproto.DeviceDescription{}, err
	}
	return dd, nil
}

// normaliseStringSlice normalises single-string → []string and []any →
// []string.
func normaliseStringSlice(m map[string]any, keys ...string) {
	for _, k := range keys {
		v, ok := m[k]
		if !ok {
			continue
		}
		switch x := v.(type) {
		case string:
			if x == "" {
				m[k] = []string{}
			} else {
				m[k] = []string{x}
			}
		case []any:
			out := make([]string, 0, len(x))
			for _, e := range x {
				if s, ok := e.(string); ok {
					out = append(out, s)
				}
			}
			m[k] = out
		}
	}
}

// normaliseBoolFields rewrites integer 0/1 values to real booleans
// for the named keys. Silent no-op when a field is absent or
// already a bool.
func normaliseBoolFields(m map[string]any, keys ...string) {
	for _, k := range keys {
		v, ok := m[k]
		if !ok {
			continue
		}
		switch x := v.(type) {
		case int:
			m[k] = x != 0
		case int32:
			m[k] = x != 0
		case int64:
			m[k] = x != 0
		case float64:
			m[k] = x != 0
		}
	}
}

// ParseDeviceDescriptions converts a slice of decoded structs (as returned
// by xmlRPCValueToGo from a newDevices callback) into typed
// [hmproto.DeviceDescription] values. Elements that are not
// map[string]any are silently skipped. Exported so callback handlers can
// reuse the normalisation logic without importing the unexported helper.
func ParseDeviceDescriptions(raw []any) []hmproto.DeviceDescription {
	out := make([]hmproto.DeviceDescription, 0, len(raw))
	for _, v := range raw {
		m, ok := v.(map[string]any)
		if !ok {
			continue
		}
		dd, err := toDeviceDescription(m)
		if err != nil {
			continue
		}
		out = append(out, dd)
	}
	return out
}

// toParameterData is the [ParameterData] counterpart.
func toParameterData(m map[string]any) (hmproto.ParameterData, error) {
	buf, err := json.Marshal(m)
	if err != nil {
		return hmproto.ParameterData{}, err
	}
	var pd hmproto.ParameterData
	if err := json.Unmarshal(buf, &pd); err != nil {
		return hmproto.ParameterData{}, err
	}
	return pd, nil
}
