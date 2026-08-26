// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package calculated

import (
	"fmt"
	"sync"

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

	// mu guards every field below. The two writers are structurally different
	// and never share a goroutine: LOW_BAT_LIMIT arrives from the MASTER
	// paramset (a poller read-back or an operator-triggered refresh) while
	// OPERATING_VOLTAGE arrives on the callback dispatch. Reading the reference
	// pair without the lock lets a level be computed from a fresh limit against
	// a stale maximum, which publishes a wrong battery percentage.
	mu sync.Mutex

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

	emit emitState
}

// voltageInputs is the consistent snapshot [OperatingVoltageLevelSensor.recompute]
// computes from. It is taken under the lock so the live voltage and the
// reference pair it is measured against always belong to the same instant.
type voltageInputs struct {
	operatingVoltage float64
	lowBatLimit      float64
	voltageMax       float64
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
func NewOperatingVoltageLevelSensorWithIdentity(centralName, channelAddress string) *OperatingVoltageLevelSensor {
	s := &OperatingVoltageLevelSensor{
		Sensor: newDerivedFloatSensor(hmenum.CalculatedParameterOperatingVoltageLevel, centralName, channelAddress),
	}
	installSourceValidityGate(s.Sensor, &s.sourceSink)
	return s
}

// SetReferences stores the (lowBatLimit, voltageMax) reference pair
// and recomputes when an operating voltage is already on file.
// voltageMax must exceed lowBatLimit; otherwise the references are
// rejected. Also updates the lowBatLimit slot used by
// [AdditionalInformation] so it reflects the current operator-configured
// value (not just the factory default).
func (s *OperatingVoltageLevelSensor) SetReferences(lowBatLimit, voltageMax float64) {
	s.mu.Lock()
	in, ready := s.setReferencesLocked(lowBatLimit, voltageMax)
	s.mu.Unlock()
	if ready {
		s.recompute(in)
	}
}

// setReferencesLocked stores the reference pair and returns the snapshot to
// recompute from. The caller must hold s.mu.
func (s *OperatingVoltageLevelSensor) setReferencesLocked(lowBatLimit, voltageMax float64) (voltageInputs, bool) {
	if voltageMax <= lowBatLimit {
		s.hasRefs = false
		return voltageInputs{}, false
	}
	s.lowBatLimit = lowBatLimit
	s.voltageMax = voltageMax
	s.hasRefs = true
	return s.inputsLocked()
}

// inputsLocked returns the snapshot to recompute from, or ok == false while an
// input is still missing. The caller must hold s.mu.
func (s *OperatingVoltageLevelSensor) inputsLocked() (voltageInputs, bool) {
	if !s.hasOperating || !s.hasRefs {
		return voltageInputs{}, false
	}
	return voltageInputs{
		operatingVoltage: s.operatingVoltage,
		lowBatLimit:      s.lowBatLimit,
		voltageMax:       s.voltageMax,
	}, true
}

// applyLowBatLimit re-applies the operator-configured LOW_BAT_LIMIT against the
// voltage maximum the battery table supplied. Both are handled in one critical
// section: a MASTER re-read racing a live OPERATING_VOLTAGE push must never
// pair a new limit with a maximum read a moment earlier. Stays inert until the
// battery table has supplied a maximum.
func (s *OperatingVoltageLevelSensor) applyLowBatLimit(lowBatLimit float64) {
	s.mu.Lock()
	if s.voltageMax <= 0 {
		s.mu.Unlock()
		return
	}
	in, ready := s.setReferencesLocked(lowBatLimit, s.voltageMax)
	s.mu.Unlock()
	if ready {
		s.recompute(in)
	}
}

// setBatteryConfig stores the per-model battery configuration resolved at
// subscribe time, so [AdditionalInformation] can surface cell type and
// quantity and the reference pair has a maximum to measure against.
func (s *OperatingVoltageLevelSensor) setBatteryConfig(cfg BatteryConfig) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.voltageMax = cfg.VoltageMax()
	s.battery = &cfg
}

// setLowBatLimitDefault stores the factory default read from the MASTER
// LOW_BAT_LIMIT descriptor.
func (s *OperatingVoltageLevelSensor) setLowBatLimitDefault(v float64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lowBatLimitDefault = v
	s.hasLowBatDefault = true
}

// OnOperatingVoltage feeds a live OPERATING_VOLTAGE value.
func (s *OperatingVoltageLevelSensor) OnOperatingVoltage(v float64) {
	s.mu.Lock()
	s.operatingVoltage = v
	s.hasOperating = true
	in, ready := s.inputsLocked()
	s.mu.Unlock()
	if ready {
		s.recompute(in)
	}
}

func (s *OperatingVoltageLevelSensor) recompute(in voltageInputs) {
	v, ok := OperatingVoltageLevel(in.operatingVoltage, in.lowBatLimit, in.voltageMax)
	s.emit.feed(s.Sensor, v, ok, s.snapshotSources())
}

// CalculatedParameter returns the calculated parameter id this sensor
// emits.
func (s *OperatingVoltageLevelSensor) CalculatedParameter() hmenum.CalculatedParameter {
	return hmenum.CalculatedParameterOperatingVoltageLevel
}

// IsRefreshed reports whether the sensor has emitted at least one
// computed level.
func (s *OperatingVoltageLevelSensor) IsRefreshed() bool { return s.emit.refreshed() }

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
	s.mu.Lock()
	defer s.mu.Unlock()
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
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lowBatLimitDefault, s.hasLowBatDefault
}

// StateUncertain aggregates over the registered source DPs. When no
// sources are registered (test fixtures that call [OnOperatingVoltage]
// directly without [Subscribe]) the method falls back to [sourceSink]'s
// default "no sources → uncertain" semantic.
func (s *OperatingVoltageLevelSensor) StateUncertain() bool {
	return s.sourceSink.StateUncertain()
}
