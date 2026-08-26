// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package hmenum

// Quantity is the platform-independent classification of what a data point
// measures (temperature, power, motion, …).
type Quantity string

// Quantity values.
//
// QuantityNone is the openccu-loom-specific zero value used by the
// resolver to signal "no quantity classification available". The
// Remaining values mirror:1 (same string keys).
const (
	QuantityNone Quantity = ""

	// Sensor quantities.
	QuantityCO2            Quantity = "carbon_dioxide"
	QuantityCurrent        Quantity = "current"
	QuantityEnergy         Quantity = "energy"
	QuantityEnum           Quantity = "enum"
	QuantityFrequency      Quantity = "frequency"
	QuantityGas            Quantity = "gas"
	QuantityHumidity       Quantity = "humidity"
	QuantityIlluminance    Quantity = "illuminance"
	QuantityPM1            Quantity = "pm1"
	QuantityPM10           Quantity = "pm10"
	QuantityPM25           Quantity = "pm25"
	QuantityPower          Quantity = "power"
	QuantityPressure       Quantity = "pressure"
	QuantitySignalStrength Quantity = "signal_strength"
	QuantityTemperature    Quantity = "temperature"
	QuantityVoltage        Quantity = "voltage"
	QuantityVolumeFlowRate Quantity = "volume_flow_rate"
	QuantityWindSpeed      Quantity = "wind_speed"

	// Binary-sensor quantities.
	QuantityBattery   Quantity = "battery"
	QuantityHeat      Quantity = "heat"
	QuantityMoisture  Quantity = "moisture"
	QuantityMotion    Quantity = "motion"
	QuantityOccupancy Quantity = "occupancy"
	QuantityOpening   Quantity = "opening"
	QuantityPresence  Quantity = "presence"
	QuantityProblem   Quantity = "problem"
	QuantityRunning   Quantity = "running"
	QuantitySafety    Quantity = "safety"
	QuantitySmoke     Quantity = "smoke"
	QuantityTamper    Quantity = "tamper"
	QuantityWindow    Quantity = "window"

	// Cover quantities.
	QuantityBlind   Quantity = "blind"
	QuantityGarage  Quantity = "garage"
	QuantityShade   Quantity = "shade"
	QuantityShutter Quantity = "shutter"

	// Switch quantities.
	QuantityOutlet Quantity = "outlet"
	QuantitySwitch Quantity = "switch"

	// Button quantities.
	QuantityIdentify Quantity = "identify"
	QuantityRestart  Quantity = "restart"
	QuantityUpdate   Quantity = "update"

	// openccu-loom-specific quantities used outside the sensor-metadata
	// table above — kept for backwards compatibility with existing
	// UI/labelling code that consults Quantity directly. Not driven by
	// the sensor-metadata tables.
	QuantityDistance      Quantity = "distance"
	QuantitySpeed         Quantity = "speed"
	QuantityWindDirection Quantity = "wind_direction"
	QuantityPrecipitation Quantity = "precipitation"
	QuantityDuration      Quantity = "duration"
	QuantityVolume        Quantity = "volume"
)

// String returns the wire representation.
func (q Quantity) String() string { return string(q) }

// ValueBehavior describes how a numeric value evolves over time:
//
//   - Instantaneous — point-in-time reading (current temperature,
//     voltage). Maps to HA `state_class: measurement`.
//   - Cumulative — running total that may reset (energy counter
//     after a power cycle). Maps to `state_class: total`.
//   - Monotonic — running total that only increases (lifetime
//     energy consumption). Maps to `state_class: total_increasing`.
type ValueBehavior string

// ValueBehavior values.
const (
	ValueBehaviorNone          ValueBehavior = ""
	ValueBehaviorInstantaneous ValueBehavior = "instantaneous"
	ValueBehaviorCumulative    ValueBehavior = "cumulative"
	ValueBehaviorMonotonic     ValueBehavior = "monotonic"
)

// String returns the wire representation.
func (v ValueBehavior) String() string { return string(v) }

// CalculatedParameter is defined in calculated.go; see that file for
// the type declaration and all values.
