// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package mqtt

import "strings"

// Home-Assistant quantity-class mapping.
//
// state_class semantics:
//
//   - "measurement"      — instantaneous reading (temperature,
//     humidity, voltage, RSSI, …)
//   - "total_increasing" — monotonically growing counter that may
//     reset to zero (energy meters)
//   - "total"            — value can decrease (e.g. cumulative water
//     storage); not used by HomeMatic at present.
//
// suggested_display_precision is sourced from the HA entity registry
// (HARegistryDescription), not from a parameter-name table, to avoid
// over-emitting vs. the HA-native integration.

// stateClassFor returns the HA state_class hint for parameter, or
// "" when none applies.
func stateClassFor(parameter string) string {
	switch strings.ToUpper(parameter) {
	case "ENERGY_COUNTER", "GAS_ENERGY_COUNTER", "IEC_ENERGY_COUNTER":
		return "total_increasing"
	case "ACTUAL_TEMPERATURE", "TEMPERATURE", "SET_POINT_TEMPERATURE", "SET_TEMPERATURE",
		"HUMIDITY",
		"POWER", "GAS_POWER", "IEC_POWER",
		"VOLTAGE", "OPERATING_VOLTAGE", "OPERATING_VOLTAGE_LEVEL",
		"CURRENT",
		"FREQUENCY",
		"AIR_PRESSURE",
		"BRIGHTNESS", "ILLUMINATION",
		"WIND_SPEED", "WIND_DIRECTION",
		"RAIN_COUNTER",
		"RSSI_DEVICE", "RSSI_PEER",
		"BATTERY_STATE",
		"LEVEL", "LEVEL_2", "LEVEL_SLATS":
		return "measurement"
	}
	return ""
}
