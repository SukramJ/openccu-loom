// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package mqtt

import (
	"github.com/SukramJ/openccu-loom/internal/parameter"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// HA `device_class` mappings keyed on.Quantity].
// the openccu-loom discovery builder consults these instead of the
// older parameter-name-only heuristic so the same Quantity-detection
// logic feeds both the Python and Go integrations.

var quantityToSensorDeviceClass = map[hmenum.Quantity]string{
	hmenum.QuantityCO2:            "carbon_dioxide",
	hmenum.QuantityCurrent:        "current",
	hmenum.QuantityEnergy:         "energy",
	hmenum.QuantityEnum:           "enum",
	hmenum.QuantityFrequency:      "frequency",
	hmenum.QuantityGas:            "gas",
	hmenum.QuantityHumidity:       "humidity",
	hmenum.QuantityIlluminance:    "illuminance",
	hmenum.QuantityPM1:            "pm1",
	hmenum.QuantityPM10:           "pm10",
	hmenum.QuantityPM25:           "pm25",
	hmenum.QuantityPower:          "power",
	hmenum.QuantityPressure:       "atmospheric_pressure",
	hmenum.QuantitySignalStrength: "signal_strength",
	hmenum.QuantityTemperature:    "temperature",
	hmenum.QuantityVoltage:        "voltage",
	hmenum.QuantityVolumeFlowRate: "volume_flow_rate",
	hmenum.QuantityWindSpeed:      "wind_speed",
}

var quantityToBinarySensorDeviceClass = map[hmenum.Quantity]string{
	hmenum.QuantityBattery:   "battery",
	hmenum.QuantityHeat:      "heat",
	hmenum.QuantityMoisture:  "moisture",
	hmenum.QuantityMotion:    "motion",
	hmenum.QuantityOccupancy: "occupancy",
	hmenum.QuantityOpening:   "opening",
	hmenum.QuantityPresence:  "presence",
	hmenum.QuantityProblem:   "problem",
	hmenum.QuantityRunning:   "running",
	hmenum.QuantitySafety:    "safety",
	hmenum.QuantitySmoke:     "smoke",
	hmenum.QuantityTamper:    "tamper",
	hmenum.QuantityWindow:    "window",
}

var quantityToSwitchDeviceClass = map[hmenum.Quantity]string{
	hmenum.QuantityOutlet: "outlet",
	hmenum.QuantitySwitch: "switch",
}

var valueBehaviorToStateClass = map[hmenum.ValueBehavior]string{
	hmenum.ValueBehaviorInstantaneous: "measurement",
	hmenum.ValueBehaviorCumulative:    "total",
	hmenum.ValueBehaviorMonotonic:     "total_increasing",
}

// resolveSensorDeviceClass returns the HA `device_class` for a given
// (deviceModel, parameter, unit) tuple. Resolution order matches the
// fallback → empty.
func resolveSensorDeviceClass(deviceModel, param, unit string) string {
	md := parameter.MetadataFor(deviceModel, param, unit)
	if md.Quantity == hmenum.QuantityNone {
		return ""
	}
	return quantityToSensorDeviceClass[md.Quantity]
}

// resolveSensorStateClass mirrors [resolveSensorDeviceClass] for the
// HA `state_class` field. Returns empty when no value-behaviour
// classification applies.
func resolveSensorStateClass(deviceModel, param, unit string) string {
	md := parameter.MetadataFor(deviceModel, param, unit)
	if md.ValueBehavior == hmenum.ValueBehaviorNone {
		return ""
	}
	return valueBehaviorToStateClass[md.ValueBehavior]
}

// resolveBinarySensorDeviceClass returns the HA binary-sensor
// `device_class`. Device-and-parameter overrides take precedence
// over the bare parameter table.
func resolveBinarySensorDeviceClass(deviceModel, param string) string {
	q := parameter.BinarySensorQuantityFor(deviceModel, param)
	if q == hmenum.QuantityNone {
		return ""
	}
	return quantityToBinarySensorDeviceClass[q]
}

// resolveSwitchDeviceClass — currently driven by the outlet/switch
// quantity classification only (mirrors A2M). Returns empty when no
// classification applies.
func resolveSwitchDeviceClass(deviceModel, param string) string {
	md := parameter.MetadataFor(deviceModel, param, "")
	if md.Quantity == hmenum.QuantityNone {
		return ""
	}
	return quantityToSwitchDeviceClass[md.Quantity]
}

// componentDeviceClass routes to the right Quantity-based resolver
// for the chosen HA component. Keeps the discovery builder one-liner
// and the per-component map definitions co-located.
func componentDeviceClass(comp HAComponent, deviceModel, param, unit string) string {
	switch comp {
	case HAComponentSensor:
		return resolveSensorDeviceClass(deviceModel, param, unit)
	case HAComponentBinarySensor:
		return resolveBinarySensorDeviceClass(deviceModel, param)
	case HAComponentSwitch:
		return resolveSwitchDeviceClass(deviceModel, param)
	default:
		// Light, Number, Cover, Lock, Climate, Valve, Siren, Select, Button,
		// Event, Update, Text do not use a Quantity-based device_class lookup.
		return ""
	}
}
