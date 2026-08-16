// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// Package thermo provides the Matter Thermostat cluster server (0x0201).
// The server supports HEAT-only, COOL-only, and HEAT+COOL (with optional
// AUTO) configurations, enforcing conformance rules on feature-gated
// attributes per Matter Application Cluster Specification §4.3.
package thermo

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/SukramJ/openccu-loom/internal/north/matter/cluster"
	"github.com/SukramJ/openccu-loom/internal/north/matter/im"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/interfaces"
)

// Thermostat cluster ID and revision per Matter §4.3.
const (
	ThermostatClusterID       uint32 = 0x0201
	ThermostatClusterRevision uint16 = 11 // Mirrors matter.js packages/model/src/standard/elements/thermostat-cluster.element.ts:21 (revision 11)
)

// Feature bits per Matter §4.3.4
// (packages/model/src/standard/elements/thermostat-cluster.element.ts).
const (
	ThermostatFeatureHEAT uint32 = 1 << 0 // bit 0 — Heating
	ThermostatFeatureCOOL uint32 = 1 << 1 // bit 1 — Cooling
	ThermostatFeatureAUTO uint32 = 1 << 5 // bit 5 — AutoMode (requires HEAT+COOL)
	ThermostatFeatureLTNE uint32 = 1 << 6 // bit 6 — LocalTemperatureNotExposed
)

// Thermostat attribute IDs per Matter §4.3.6.
const (
	thermoAttrLocalTemperature            uint32 = 0x0000
	thermoAttrAbsMinHeatSetpointLimit     uint32 = 0x0003
	thermoAttrAbsMaxHeatSetpointLimit     uint32 = 0x0004
	thermoAttrAbsMinCoolSetpointLimit     uint32 = 0x0005
	thermoAttrAbsMaxCoolSetpointLimit     uint32 = 0x0006
	thermoAttrLocalTemperatureCalibration uint32 = 0x0010
	thermoAttrOccupiedCoolingSetpoint     uint32 = 0x0011
	thermoAttrOccupiedHeatingSetpoint     uint32 = 0x0012
	thermoAttrMinHeatSetpointLimit        uint32 = 0x0015
	thermoAttrMaxHeatSetpointLimit        uint32 = 0x0016
	thermoAttrMinCoolSetpointLimit        uint32 = 0x0017
	thermoAttrMaxCoolSetpointLimit        uint32 = 0x0018
	thermoAttrMinSetpointDeadBand         uint32 = 0x0019
	thermoAttrControlSequenceOfOperation  uint32 = 0x001B
	thermoAttrSystemMode                  uint32 = 0x001C
	thermoAttrThermostatRunningMode       uint32 = 0x001E
)

// ThermostatConfig holds the static configuration for a Thermostat
// cluster server instance. All temperature values are in units of
// 0.01°C (Matter signed int16), e.g. 2000 = 20.00°C.
type ThermostatConfig struct {
	// Features selects the active feature set. Use the
	// ThermostatFeature* constants. AUTO requires HEAT+COOL.
	Features uint32
	// AbsMinHeatSetpointLimit is the absolute minimum heating setpoint
	// (conformance [HEAT]). Default: 700 (7.00°C).
	AbsMinHeatSetpointLimit int16
	// AbsMaxHeatSetpointLimit is the absolute maximum heating setpoint
	// (conformance [HEAT]). Default: 3000 (30.00°C).
	AbsMaxHeatSetpointLimit int16
	// AbsMinCoolSetpointLimit is the absolute minimum cooling setpoint
	// (conformance [COOL]). Default: 1600 (16.00°C).
	AbsMinCoolSetpointLimit int16
	// AbsMaxCoolSetpointLimit is the absolute maximum cooling setpoint
	// (conformance [COOL]). Default: 3200 (32.00°C).
	AbsMaxCoolSetpointLimit int16
	// InitialHeatingSetpoint is the initial OccupiedHeatingSetpoint
	// (conformance HEAT). Default: 2000 (20.00°C).
	InitialHeatingSetpoint int16
	// InitialCoolingSetpoint is the initial OccupiedCoolingSetpoint
	// (conformance COOL). Default: 2600 (26.00°C).
	InitialCoolingSetpoint int16
}

// DefaultThermostatConfig returns a heating+cooling thermostat config with
// sensible European defaults.
func DefaultThermostatConfig() ThermostatConfig {
	return ThermostatConfig{
		Features:                ThermostatFeatureHEAT | ThermostatFeatureCOOL | ThermostatFeatureAUTO,
		AbsMinHeatSetpointLimit: 700,
		AbsMaxHeatSetpointLimit: 3000,
		AbsMinCoolSetpointLimit: 1600,
		AbsMaxCoolSetpointLimit: 3200,
		InitialHeatingSetpoint:  2000,
		InitialCoolingSetpoint:  2600,
	}
}

// ThermostatServer is the Matter Thermostat cluster server (0x0201).
// It enforces feature-conformance rules: AUTO is only set when both HEAT
// and COOL are active; COOL-only attributes are suppressed without COOL;
// HEAT-only attributes are suppressed without HEAT; LocalTemperatureCalibration
// is only served when LTNE is not set.
type ThermostatServer struct {
	mu       sync.RWMutex
	features uint32

	localTemp      *int16 // nullable per Matter §4.3.6.1
	localTempCalib int16

	// HEAT-gated state
	absMinHeat int16
	absMaxHeat int16
	occupHeat  int16
	minHeat    int16
	maxHeat    int16

	// COOL-gated state
	absMinCool int16
	absMaxCool int16
	occupCool  int16
	minCool    int16
	maxCool    int16

	// AUTO-gated state
	minSetpointDeadBand int8 // 0.1°C units; default 20 = 2.0°C

	systemMode uint8 // SystemModeEnum value
}

// NewThermostatServer constructs the cluster. AUTO is silently cleared
// from the feature set when HEAT+COOL are not both present.
func NewThermostatServer(cfg ThermostatConfig) *ThermostatServer {
	features := cfg.Features
	// AUTO requires HEAT+COOL — clear it if either is missing.
	if features&ThermostatFeatureAUTO != 0 {
		if features&ThermostatFeatureHEAT == 0 || features&ThermostatFeatureCOOL == 0 {
			features &^= ThermostatFeatureAUTO
		}
	}

	s := &ThermostatServer{
		features:            features,
		localTempCalib:      0,
		minSetpointDeadBand: 20, // 2.0°C default per matter.js packages/model/src/standard/elements/thermostat-cluster.element.ts MinSetpointDeadBand default
	}

	if features&ThermostatFeatureHEAT != 0 {
		s.absMinHeat = cfg.AbsMinHeatSetpointLimit
		s.absMaxHeat = cfg.AbsMaxHeatSetpointLimit
		s.occupHeat = cfg.InitialHeatingSetpoint
		s.minHeat = cfg.AbsMinHeatSetpointLimit
		s.maxHeat = cfg.AbsMaxHeatSetpointLimit
	}
	if features&ThermostatFeatureCOOL != 0 {
		s.absMinCool = cfg.AbsMinCoolSetpointLimit
		s.absMaxCool = cfg.AbsMaxCoolSetpointLimit
		s.occupCool = cfg.InitialCoolingSetpoint
		s.minCool = cfg.AbsMinCoolSetpointLimit
		s.maxCool = cfg.AbsMaxCoolSetpointLimit
	}

	// SystemMode default: 1 (Auto) when AUTO feature; 4 (Heat) when HEAT-only;
	// 3 (Cool) when COOL-only.
	switch {
	case features&ThermostatFeatureAUTO != 0:
		s.systemMode = 1 // Auto
	case features&ThermostatFeatureHEAT != 0:
		s.systemMode = 4 // Heat
	case features&ThermostatFeatureCOOL != 0:
		s.systemMode = 3 // Cool
	default:
		s.systemMode = 0 // Off
	}
	return s
}

// MatterClusterID returns 0x0201.
func (s *ThermostatServer) MatterClusterID() uint32 { return ThermostatClusterID }

// SetLocalTemperature updates the LocalTemperature attribute (quality X —
// nullable). Pass nil to set null (sensor unavailable).
func (s *ThermostatServer) SetLocalTemperature(t *int16) {
	s.mu.Lock()
	s.localTemp = t
	s.mu.Unlock()
}

// MatterRead implements [interfaces.MatterClusterServer].
// Feature-gated attributes return (nil, false) when their required feature
// is absent — the IM dispatcher handles the UnsupportedAttribute response.
func (s *ThermostatServer) MatterRead(attrID uint32) (any, bool) { //nolint:gocyclo,funlen // wire/dispatch table over many attribute/opcode cases
	s.mu.RLock()
	defer s.mu.RUnlock()

	switch attrID {
	case thermoAttrLocalTemperature:
		if s.localTemp == nil {
			return nil, true // nullable quality X: null value, attribute present
		}
		return *s.localTemp, true

	case thermoAttrAbsMinHeatSetpointLimit:
		if s.features&ThermostatFeatureHEAT == 0 {
			return nil, false
		}
		return s.absMinHeat, true
	case thermoAttrAbsMaxHeatSetpointLimit:
		if s.features&ThermostatFeatureHEAT == 0 {
			return nil, false
		}
		return s.absMaxHeat, true

	case thermoAttrAbsMinCoolSetpointLimit:
		if s.features&ThermostatFeatureCOOL == 0 {
			return nil, false
		}
		return s.absMinCool, true
	case thermoAttrAbsMaxCoolSetpointLimit:
		if s.features&ThermostatFeatureCOOL == 0 {
			return nil, false
		}
		return s.absMaxCool, true

	case thermoAttrLocalTemperatureCalibration:
		// Conformance [!LTNE]: only served when LTNE feature is NOT set.
		if s.features&ThermostatFeatureLTNE != 0 {
			return nil, false
		}
		return s.localTempCalib, true

	case thermoAttrOccupiedCoolingSetpoint:
		if s.features&ThermostatFeatureCOOL == 0 {
			return nil, false
		}
		return s.occupCool, true

	case thermoAttrOccupiedHeatingSetpoint:
		if s.features&ThermostatFeatureHEAT == 0 {
			return nil, false
		}
		return s.occupHeat, true

	case thermoAttrMinHeatSetpointLimit:
		if s.features&ThermostatFeatureHEAT == 0 {
			return nil, false
		}
		return s.minHeat, true
	case thermoAttrMaxHeatSetpointLimit:
		if s.features&ThermostatFeatureHEAT == 0 {
			return nil, false
		}
		return s.maxHeat, true

	case thermoAttrMinCoolSetpointLimit:
		if s.features&ThermostatFeatureCOOL == 0 {
			return nil, false
		}
		return s.minCool, true
	case thermoAttrMaxCoolSetpointLimit:
		if s.features&ThermostatFeatureCOOL == 0 {
			return nil, false
		}
		return s.maxCool, true

	case thermoAttrMinSetpointDeadBand:
		// Conformance AUTO.
		if s.features&ThermostatFeatureAUTO == 0 {
			return nil, false
		}
		return s.minSetpointDeadBand, true

	case thermoAttrControlSequenceOfOperation:
		// Mandatory (M conformance) per matter.js
		// thermostat-cluster.element.ts (id 0x1b). Derived from the
		// supported modes: 4=CoolingAndHeating, 2=HeatingOnly, 0=CoolingOnly.
		// matter.js declares this RW; the bridge exposes it read-only because
		// the value follows the wrapped HM device's immutable HEAT/COOL
		// capability — there is nothing for a controller to change. Being
		// immutable it is also (correctly) not in MatterReportable.
		return controlSequenceOfOperation(s.features), true
	case thermoAttrSystemMode:
		return s.systemMode, true

	case cluster.AttrGlobalFeatureMap:
		return s.features, true
	case cluster.AttrGlobalClusterRevision:
		return ThermostatClusterRevision, true
	}
	return nil, false
}

// MatterWrite handles writable attributes (setpoints, SystemMode).
func (s *ThermostatServer) MatterWrite(_ context.Context, attrID uint32, value any, _ hmenum.CommandPriority) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	switch attrID {
	case thermoAttrOccupiedHeatingSetpoint:
		if s.features&ThermostatFeatureHEAT == 0 {
			return errors.New("thermostat: OccupiedHeatingSetpoint not supported (no HEAT feature)")
		}
		v, ok := cluster.AsInt16(value)
		if !ok {
			return fmt.Errorf("thermostat: OccupiedHeatingSetpoint: expected numeric, got %T", value)
		}
		// Reject values outside [minHeat, maxHeat] per matter.js
		// ThermostatServer.ts:#assertSetpointWithinLimits (lines 879-892).
		if v < s.minHeat {
			return thermoConstraintErr{fmt.Sprintf("thermostat: OccupiedHeatingSetpoint %d below MinHeatSetpointLimit %d", v, s.minHeat)}
		}
		if v > s.maxHeat {
			return thermoConstraintErr{fmt.Sprintf("thermostat: OccupiedHeatingSetpoint %d above MaxHeatSetpointLimit %d", v, s.maxHeat)}
		}
		s.occupHeat = v
		return nil
	case thermoAttrOccupiedCoolingSetpoint:
		if s.features&ThermostatFeatureCOOL == 0 {
			return errors.New("thermostat: OccupiedCoolingSetpoint not supported (no COOL feature)")
		}
		v, ok := cluster.AsInt16(value)
		if !ok {
			return fmt.Errorf("thermostat: OccupiedCoolingSetpoint: expected numeric, got %T", value)
		}
		// Reject values outside [minCool, maxCool] per matter.js
		// ThermostatServer.ts:#assertSetpointWithinLimits (lines 879-892).
		if v < s.minCool {
			return thermoConstraintErr{fmt.Sprintf("thermostat: OccupiedCoolingSetpoint %d below MinCoolSetpointLimit %d", v, s.minCool)}
		}
		if v > s.maxCool {
			return thermoConstraintErr{fmt.Sprintf("thermostat: OccupiedCoolingSetpoint %d above MaxCoolSetpointLimit %d", v, s.maxCool)}
		}
		s.occupCool = v
		return nil
	case thermoAttrSystemMode:
		v, ok := cluster.AsUint8(value)
		if !ok {
			return fmt.Errorf("thermostat: SystemMode: expected numeric, got %T", value)
		}
		// Validate mode against ControlSequenceOfOperation per matter.js
		// ThermostatServer.ts:#assertSystemModeChanging (lines 615-634):
		// CoolingOnly forbids Heat (4) and EmergencyHeat (5); HeatingOnly
		// forbids Cool (3) and Precooling (7). matter.js also groups
		// CoolingAndHeatingWithReheat (5) with CoolingOnly and
		// HeatingWithReheat (3) with HeatingOnly, but controlSequenceOfOperation
		// only ever derives 0 (CoolingOnly), 2 (HeatingOnly), or 4
		// (CoolingAndHeating — forbids nothing) from this bridge's heating /
		// cooling feature bits, so the reheat sequences never arise and need
		// no arm here.
		csoo := controlSequenceOfOperation(s.features)
		switch csoo {
		case 0: // CoolingOnly
			if v == 4 || v == 5 { // Heat, EmergencyHeat
				return thermoConstraintErr{fmt.Sprintf("thermostat: SystemMode %d not allowed in CoolingOnly sequence", v)}
			}
		case 2: // HeatingOnly
			if v == 3 || v == 7 { // Cool, Precooling
				return thermoConstraintErr{fmt.Sprintf("thermostat: SystemMode %d not allowed in HeatingOnly sequence", v)}
			}
		}
		s.systemMode = v
		return nil
	default:
		return fmt.Errorf("thermostat: attribute 0x%04X is not writable", attrID)
	}
}

// MatterInvoke handles SetpointRaiseLower and weekly-schedule commands.
func (s *ThermostatServer) MatterInvoke(_ context.Context, cmdID uint32, fields any, _ hmenum.CommandPriority) (any, error) {
	switch cmdID {
	case 0x00: // SetpointRaiseLower
		return nil, s.handleSetpointRaiseLower(fields)
	default:
		return nil, im.UnsupportedCommandf("thermostat: command 0x%02X not supported", cmdID)
	}
}

// abs16 returns the absolute value of an int16. Used by the coordinated
// Both-mode setpoint clamp to pick the more limiting overshoot.
func abs16(x int16) int16 {
	if x < 0 {
		return -x
	}
	return x
}

// handleSetpointRaiseLower implements the SetpointRaiseLower command per
// matter.js ThermostatServer.ts:setpointRaiseLower (lines 157-242).
// mode=Heat without HEAT feature → InvalidCommand; mode=Cool without COOL
// feature → InvalidCommand; otherwise apply amount*10 delta, clamped to limits.
func (s *ThermostatServer) handleSetpointRaiseLower(fields any) error { //nolint:funlen // single-purpose setpoint command handler with many mode/feature branches
	// Decode fields: expect map[string]any with "mode" (uint8) and "amount" (int8).
	m, ok := fields.(map[string]any)
	if !ok {
		// Bare nil or untyped call with no fields: treat as Both mode, amount 0.
		return nil
	}
	var mode uint8
	var amount int8
	if rawMode, has := m["mode"]; has {
		switch v := rawMode.(type) {
		case uint8:
			mode = v
		case int:
			mode = uint8(v) //nolint:gosec // field bound 0-2 by spec; see #20
		}
	}
	if rawAmt, has := m["amount"]; has {
		switch v := rawAmt.(type) {
		case int8:
			amount = v
		case int:
			amount = int8(v) //nolint:gosec // field is signed byte by spec; see #20
		}
	}

	// matter.js ThermostatServer.ts:158-166: reject Heat/Cool modes when feature absent.
	switch mode {
	case 1: // Heat
		if s.features&ThermostatFeatureHEAT == 0 {
			return thermoInvalidCommandErr{"thermostat: SetpointRaiseLower mode=Heat requires HEAT feature"}
		}
	case 2: // Cool
		if s.features&ThermostatFeatureCOOL == 0 {
			return thermoInvalidCommandErr{"thermostat: SetpointRaiseLower mode=Cool requires COOL feature"}
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	// amount is in 0.1°C steps in the command; setpoints are in 0.01°C units.
	// matter.js ThermostatServer.ts:169: amount *= 10.
	delta := int16(amount) * 10

	clampHeat := func(v int16) int16 {
		if v < s.minHeat {
			return s.minHeat
		}
		if v > s.maxHeat {
			return s.maxHeat
		}
		return v
	}
	clampCool := func(v int16) int16 {
		if v < s.minCool {
			return s.minCool
		}
		if v > s.maxCool {
			return s.maxCool
		}
		return v
	}

	switch mode {
	case 0: // Both
		heat := s.features&ThermostatFeatureHEAT != 0
		cool := s.features&ThermostatFeatureCOOL != 0
		switch {
		case heat && cool:
			// Coordinated clamp that preserves the deadband: compute each
			// setpoint's overshoot past its limit, then subtract the more
			// limiting overshoot from BOTH setpoints so their spacing is
			// kept. Clamping each independently would skew the deadband.
			// matter.js ThermostatServer.ts:170-189.
			desiredCool := s.occupCool + delta
			coolLimit := desiredCool - clampCool(desiredCool)
			desiredHeat := s.occupHeat + delta
			heatLimit := desiredHeat - clampHeat(desiredHeat)
			if coolLimit != 0 || heatLimit != 0 {
				if abs16(coolLimit) <= abs16(heatLimit) {
					desiredHeat -= heatLimit
					desiredCool -= heatLimit
				} else {
					desiredHeat -= coolLimit
					desiredCool -= coolLimit
				}
			}
			s.occupCool = desiredCool
			s.occupHeat = desiredHeat
		case cool:
			s.occupCool = clampCool(s.occupCool + delta)
		default: // heating-only (matter.js falls through to the heating setpoint)
			s.occupHeat = clampHeat(s.occupHeat + delta)
		}
	case 1: // Heat
		s.occupHeat = clampHeat(s.occupHeat + delta)
	case 2: // Cool
		s.occupCool = clampCool(s.occupCool + delta)
	default:
		return thermoInvalidCommandErr{fmt.Sprintf("thermostat: SetpointRaiseLower unsupported mode %d", mode)}
	}
	return nil
}

// controlSequenceOfOperation derives the ControlSequenceOfOperationEnum
// (Matter Thermostat §4.3.7) from the advertised feature set.
func controlSequenceOfOperation(features uint32) uint8 {
	hasHeat := features&ThermostatFeatureHEAT != 0
	hasCool := features&ThermostatFeatureCOOL != 0
	switch {
	case hasHeat && hasCool:
		return 4 // CoolingAndHeating
	case hasCool:
		return 0 // CoolingOnly
	default:
		return 2 // HeatingOnly (HM thermostats are heating by default)
	}
}

// MatterReportable returns attributes that emit reports on change.
func (s *ThermostatServer) MatterReportable() []uint32 {
	return []uint32{
		thermoAttrLocalTemperature,
		thermoAttrSystemMode,
	}
}

// MatterAttributes returns the feature-gated attribute set this server
// advertises. Only attributes whose feature requirements are met are
// included — chip-tool and Apple Home validate AttributeList conformance.
func (s *ThermostatServer) MatterAttributes() []uint32 {
	s.mu.RLock()
	f := s.features
	s.mu.RUnlock()

	attrs := []uint32{
		thermoAttrLocalTemperature,
		thermoAttrControlSequenceOfOperation,
		thermoAttrSystemMode,
	}
	if f&ThermostatFeatureHEAT != 0 {
		attrs = append(
			attrs,
			thermoAttrAbsMinHeatSetpointLimit,
			thermoAttrAbsMaxHeatSetpointLimit,
			thermoAttrOccupiedHeatingSetpoint,
			thermoAttrMinHeatSetpointLimit,
			thermoAttrMaxHeatSetpointLimit,
		)
	}
	if f&ThermostatFeatureCOOL != 0 {
		attrs = append(
			attrs,
			thermoAttrAbsMinCoolSetpointLimit,
			thermoAttrAbsMaxCoolSetpointLimit,
			thermoAttrOccupiedCoolingSetpoint,
			thermoAttrMinCoolSetpointLimit,
			thermoAttrMaxCoolSetpointLimit,
		)
	}
	if f&ThermostatFeatureAUTO != 0 {
		attrs = append(attrs, thermoAttrMinSetpointDeadBand)
	}
	if f&ThermostatFeatureLTNE == 0 {
		attrs = append(attrs, thermoAttrLocalTemperatureCalibration)
	}
	return attrs
}

// thermoConstraintErr is a typed [im.StatusCodeError] returned when a
// setpoint or SystemMode write violates limit constraints.
// Mirrors matter.js ThermostatServer.ts:#assertSetpointWithinLimits and
// #assertSystemModeChanging which both throw ConstraintErrorError.
type thermoConstraintErr struct{ msg string }

func (e thermoConstraintErr) Error() string                 { return e.msg }
func (thermoConstraintErr) MatterStatusCode() im.StatusCode { return im.StatusConstraintError }

// thermoInvalidCommandErr is a typed [im.StatusCodeError] returned when a
// SetpointRaiseLower request is invalid.
// Mirrors matter.js ThermostatServer.ts:setpointRaiseLower which throws
// InvalidCommandError for unsupported modes.
type thermoInvalidCommandErr struct{ msg string }

func (e thermoInvalidCommandErr) Error() string                 { return e.msg }
func (thermoInvalidCommandErr) MatterStatusCode() im.StatusCode { return im.StatusInvalidCommand }

// Compile-time assertions.
var (
	_ interfaces.MatterClusterServer          = (*ThermostatServer)(nil)
	_ interfaces.MatterClusterAttributeLister = (*ThermostatServer)(nil)
	_ im.StatusCodeError                      = thermoConstraintErr{}
	_ im.StatusCodeError                      = thermoInvalidCommandErr{}
)
