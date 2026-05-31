// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package calculated

import (
	"fmt"

	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// OperatingVoltageLevelSensor derives a battery-level percentage from
// OPERATING_VOLTAGE plus a (lowBatLimit, voltageMax) reference pair.
// The two reference points are typically config values that stay
// stable across the device lifetime; they can be updated via
// [SetReferences] whenever the paramset reload picks up new bounds.
//
// The inner [generic.Sensor] carries the canonical
// `<central>:<channelAddress>:CALCULATED/OPERATING_VOLTAGE_LEVEL`
// UniqueID via [generic.Spec.KeyNameOverride]; no outer
// BaseDataPointFields embed (V2 fix, PR-32 — see
// [DewPointSensor] for the rationale).
//
// [StateUncertain] aggregates over registered source DPs via the
// embedded [sourceSink].
type OperatingVoltageLevelSensor struct {
	*generic.Sensor[float64]
	sourceSink

	operatingVoltage float64
	hasOperating     bool

	lowBatLimit        float64
	lowBatLimitDefault float64
	hasLowBatDefault   bool
	voltageMax         float64
	hasRefs            bool

	// battery holds the resolved per-model config (type + quantity). Set once by
	// Subscribe via [LookupBatteryConfig]; nil when the model is not in the
	// battery table.
	battery *BatteryConfig

	last    float64
	hasLast bool
}

// NewOperatingVoltageLevelSensor constructs the sensor with no central
// / channel scoping. Multi-CCU-safe call sites MUST use
// [NewOperatingVoltageLevelSensorWithIdentity].
func NewOperatingVoltageLevelSensor() *OperatingVoltageLevelSensor {
	return NewOperatingVoltageLevelSensorWithIdentity("", "")
}

// NewOperatingVoltageLevelSensorWithIdentity constructs the sensor
// rooted at
// `<central>:<channelAddress>:CALCULATED/OPERATING_VOLTAGE_LEVEL`.
func NewOperatingVoltageLevelSensorWithIdentity(central, channelAddress string) *OperatingVoltageLevelSensor {
	return &OperatingVoltageLevelSensor{
		Sensor: newDerivedFloatSensor(hmenum.CalculatedParameterOperatingVoltageLevel, central, channelAddress),
	}
}

// SetReferences stores the (lowBatLimit, voltageMax) reference pair
// and recomputes when an operating voltage is already on file.
// voltageMax must exceed lowBatLimit; otherwise the references are
// rejected. Also updates the lowBatLimit slot used by
// [AdditionalInformation] so it reflects the current operator-configured
// value (not just the factory default).
func (s *OperatingVoltageLevelSensor) SetReferences(lowBatLimit, voltageMax float64) {
	if voltageMax <= lowBatLimit {
		s.hasRefs = false
		return
	}
	s.lowBatLimit = lowBatLimit
	s.voltageMax = voltageMax
	s.hasRefs = true
	s.recompute()
}

// OnOperatingVoltage feeds a live OPERATING_VOLTAGE value.
func (s *OperatingVoltageLevelSensor) OnOperatingVoltage(v float64) {
	s.operatingVoltage = v
	s.hasOperating = true
	s.recompute()
}

func (s *OperatingVoltageLevelSensor) recompute() {
	if !s.hasOperating || !s.hasRefs {
		return
	}
	v, ok := OperatingVoltageLevel(s.operatingVoltage, s.lowBatLimit, s.voltageMax)
	feedSink(s.Sensor, v, ok, &s.last, &s.hasLast, s.sources)
}

// CalculatedParameter returns the calculated parameter id this sensor
// emits.
func (s *OperatingVoltageLevelSensor) CalculatedParameter() hmenum.CalculatedParameter {
	return hmenum.CalculatedParameterOperatingVoltageLevel
}

// IsRefreshed reports whether the sensor has emitted at least one
// computed level.
func (s *OperatingVoltageLevelSensor) IsRefreshed() bool { return s.hasLast }

// AdditionalInformation key constants mirror
// `operating_voltage_level.py:22-26` string constants.
const (
	addinfoKeyBatteryQty         = "Battery Qty"
	addinfoKeyBatteryType        = "Battery Type"
	addinfoKeyLowBatLimit        = "Low Battery Limit"
	addinfoKeyLowBatLimitDefault = "Low Battery Limit Default"
	addinfoKeyVoltageMax         = "Voltage max"
)

// AdditionalInformation returns enriched battery metadata as a
// map[string]any when the sensor has resolved a battery configuration.
// Returns nil when the model is not in the battery table (unknown cell
// Type). The map keys exactly mirror
// `OperatingVoltageLevel.additional_information` dict
// (operating_voltage_level.py:100–114):
//
//	"Battery Qty" → int (cell quantity)
//	"Battery Type" → string (cell chemistry label)
//	"Low Battery Limit" → string formatted as "<V>V"
//	"Low Battery Limit Default" → string formatted as "<V>V"
//	"Voltage max" → string formatted as "<V>V"
func (s *OperatingVoltageLevelSensor) AdditionalInformation() map[string]any {
	if s.battery == nil {
		return nil
	}
	return map[string]any{
		addinfoKeyBatteryQty:         s.battery.Quantity,
		addinfoKeyBatteryType:        string(s.battery.Battery),
		addinfoKeyLowBatLimit:        fmt.Sprintf("%gV", s.lowBatLimit),
		addinfoKeyLowBatLimitDefault: fmt.Sprintf("%gV", s.lowBatLimitDefault),
		addinfoKeyVoltageMax:         fmt.Sprintf("%gV", s.voltageMax),
	}
}

// LowBatLimitDefault returns the default low-battery limit derived from the
// LOW_BAT_LIMIT parameter descriptor. Returns (0, false) when no default has
// been resolved yet.
func (s *OperatingVoltageLevelSensor) LowBatLimitDefault() (float64, bool) {
	return s.lowBatLimitDefault, s.hasLowBatDefault
}

// StateUncertain aggregates over the registered source DPs. When no
// sources are registered (test fixtures that call [OnOperatingVoltage]
// directly without [Subscribe]) the method falls back to [sourceSink]'s
// default "no sources → uncertain" semantic.
func (s *OperatingVoltageLevelSensor) StateUncertain() bool {
	return s.sourceSink.StateUncertain()
}
