// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package generic

import (
	"strings"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// SwitchDPToSensor mirrors
// (model/generic/__init__.py:247). The map identifies wire
// parameters whose default classification (writable, e.g. LEVEL on
// HmIP-eTRV) is wrong: the parameter is in fact a derived,
// read-only sensor surface even though the descriptor lists
// `OPERATIONS=READ|WRITE`. Mirroring HA's expectation a sensor
// entity is rendered instead of a writable Number.
//
// Key is the device-model prefix (case-insensitive); value is the
// wire parameter that is forced to sensor.
var switchDPToSensor = map[string]hmenum.Parameter{
	"HmIP-eTRV":    hmenum.ParameterLevel,
	"HmIP-HEATING": hmenum.ParameterLevel,
}

// IsForceSensorParameter reports whether the (model, parameter)
// pair must be classified as a read-only sensor regardless of the
// CCU's operations descriptor.
// `_check_switch_to_sensor` predicate (model/generic/__init__.py:324).
//
// Model match is a case-insensitive prefix match — same convention
// As.
//
// Used by:
//
//   - MQTT discovery: the per-parameter component classifier
//     overrides Switch / Number with Sensor.
//   - REST / WS adapters: surfaces marked here are rendered
//     non-writable.
func IsForceSensorParameter(model string, parameter hmenum.Parameter) bool {
	if model == "" {
		return false
	}
	lowered := strings.ToLower(model)
	for prefix, param := range switchDPToSensor {
		if param != parameter {
			continue
		}
		if strings.HasPrefix(lowered, strings.ToLower(prefix)) {
			return true
		}
	}
	return false
}
