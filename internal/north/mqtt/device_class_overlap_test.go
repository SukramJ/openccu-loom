// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package mqtt

import (
	"sort"
	"testing"
)

// deviceClassDisagreements records every parameter the adapter's fallback
// table classifies differently from the domain.
//
// The two are consulted in sequence (discovery.go:559): the domain answers for
// sensor, binary_sensor and switch components, and this table answers for
// everything else — number, light, cover, lock, select, event, text — where
// componentDeviceClass returns "" by construction. So the entries are not dead
// weight, and deleting them would drop device_class on those components.
//
// What the disagreements mean is that ONE parameter is classified two ways
// depending on which component it lands on. Resolving them is a payload change
// on the non-sensor components, which is why they are recorded here rather
// than quietly aligned: the list is the decision, not the omission.
var deviceClassDisagreements = map[string]string{
	"BATTERY_STATE":            "adapter says battery, the domain says voltage — it is a voltage reading used as a battery indicator",
	"DOOR_STATE":               "adapter says door, the domain says enum — the domain models it as a multi-state, HA's door class is binary",
	"GAS_ENERGY_COUNTER":       "adapter says energy, the domain says gas — HA distinguishes the two and the domain's is the specific one",
	"PRESENCE_DETECTION_STATE": "adapter says motion, the domain says presence — HA distinguishes momentary motion from sustained presence",
	"WINDOW_STATE":             "adapter says door, the domain says window",
}

// TestDeviceClassFallbackDisagreementsAreRecorded fails when the adapter's
// fallback table disagrees with the domain on a parameter nobody decided
// about.
//
// A fallback that quietly answers differently from the authority it falls back
// FROM is the shape this audit keeps finding: each side internally consistent,
// the disagreement invisible until someone compares them. This makes the next
// one fail a build.
func TestDeviceClassFallbackDisagreementsAreRecorded(t *testing.T) {
	t.Parallel()
	params := []string{
		"ACTUAL_TEMPERATURE", "AIR_PRESSURE", "APPARENT_TEMPERATURE", "BATTERY_STATE", "BRIGHTNESS",
		"CONFIG_PENDING", "CURRENT", "DEW_POINT", "DOOR_STATE", "ENERGY_COUNTER", "FREQUENCY",
		"FROST_POINT", "GAS_ENERGY_COUNTER", "GAS_POWER", "HUMIDITY", "ILLUMINATION",
		"INTRUSION_ALARM", "LOW_BAT", "MOTION", "OPERATING_VOLTAGE", "OPERATING_VOLTAGE_LEVEL",
		"POWER", "PRESENCE_DETECTION_STATE", "RAINING", "RSSI_DEVICE", "RSSI_PEER",
		"SET_POINT_TEMPERATURE", "SET_TEMPERATURE", "SMOKE_ALARM", "STICKY_UNREACH", "TEMPERATURE",
		"UNREACH", "UPDATE_PENDING", "VOLTAGE", "WINDOW_OPEN", "WINDOW_STATE", "WIND_SPEED",
	}
	sort.Strings(params)
	seen := map[string]bool{}
	for _, p := range params {
		adapter, ok := deviceClassFor(p)
		if !ok {
			continue
		}
		model := resolveSensorDeviceClass("", p, "")
		if model == "" {
			model = resolveBinarySensorDeviceClass("", p)
		}
		if model == "" || model == adapter {
			continue
		}
		seen[p] = true
		if _, recorded := deviceClassDisagreements[p]; !recorded {
			t.Errorf("%s: the fallback says %q, the domain says %q — decide which, then record it here",
				p, adapter, model)
		}
	}
	for p := range deviceClassDisagreements {
		if !seen[p] {
			t.Errorf("%s no longer disagrees — drop it from deviceClassDisagreements", p)
		}
	}
}
