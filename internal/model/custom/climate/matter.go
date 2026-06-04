// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package climate

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"github.com/SukramJ/openccu-loom/internal/north/matter/cluster/wire"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/interfaces"
)

// Compile-time assertion: Climate participates in the Matter source
// surface (ADR 0012) as a Matter Thermostat (0x0301) endpoint
// with four cluster servers: Thermostat (0x0201),
// ThermostatUserInterfaceConfiguration (0x0204), TemperatureMeasurement
// (0x0402), and RelativeHumidityMeasurement (0x0405) — the last is
// emitted only when the channel carries a HUMIDITY parameter.
// The Schedules cluster (0x0024) is also included; the week-profile
// mapping is a stub returning an empty slice until the full conversion
// from the CCU week-program format is implemented (post-0.1.0, see ADR 0012).
var (
	_ interfaces.MatterEndpointSource     = (*Climate)(nil)
	_ interfaces.MatterClusterDataVersion = (*Climate)(nil)
)

// MatterDataVersion implements [interfaces.MatterClusterDataVersion].
// Shared across all cluster servers that project this Climate (Thermostat,
// ThermostatUI, TemperatureMeasurement, RelativeHumidityMeasurement).
// Bumped on every successful write / invoke so DataVersionFilter
// evaluation correctly detects cluster changes.
func (c *Climate) MatterDataVersion() uint32 { return c.dataVersion.Current() }

// Matter constants follow the Matter Application Cluster Specification
// §4.3 (Thermostat), §4.4 (ThermostatUserInterfaceConfiguration),
// §2.3 (TemperatureMeasurement) and §2.6 (RelativeHumidityMeasurement).
// Cluster revisions and attribute IDs mirror
// packages/model/src/standard/elements/ in matter.js HEAD.
// They live here next to the projection;
// internal/north/matter/cluster/{thermostat,thermostatui,tempmeasurement,humiditymeasurement}/
// may later import them.
const (
	matterDeviceTypeThermostat uint16 = 0x0301

	matterClusterThermostat                  uint32 = 0x0201
	matterClusterThermostatUI                uint32 = 0x0204
	matterClusterTemperatureMeasurement      uint32 = 0x0402
	matterClusterRelativeHumidityMeasurement uint32 = 0x0405
	matterClusterSchedules                   uint32 = wire.SchedulesClusterID

	// Thermostat (0x0201) attribute IDs (subset).
	matterAttrThermLocalTemperature uint32 = 0x0000
	matterAttrThermOccupiedCoolSp   uint32 = 0x0011
	matterAttrThermOccupiedHeatSp   uint32 = 0x0012
	matterAttrThermMinHeatSp        uint32 = 0x0015
	matterAttrThermMaxHeatSp        uint32 = 0x0016
	matterAttrThermMinCoolSp        uint32 = 0x0017
	matterAttrThermMaxCoolSp        uint32 = 0x0018
	matterAttrThermControlSeq       uint32 = 0x001B
	matterAttrThermSystemMode       uint32 = 0x001C
	matterAttrThermRunningMode      uint32 = 0x001E

	// ThermostatUserInterfaceConfiguration (0x0204) attribute IDs.
	matterAttrUITempDisplayMode uint32 = 0x0000
	matterAttrUIKeypadLockout   uint32 = 0x0001

	// Generic measurement attribute (TemperatureMeasurement /
	// RelativeHumidityMeasurement / IlluminanceMeasurement all use the
	// same conventional ID 0x0000 for MeasuredValue).
	matterAttrMeasuredValue uint32 = 0x0000

	matterAttrFeatureMap      uint32 = 0xFFFC
	matterAttrClusterRevision uint32 = 0xFFFD

	// Thermostat command IDs.
	matterCmdSetpointRaiseLower uint32 = 0x00

	// Cluster revisions: Thermostat 10, ThermostatUI 2,
	// TemperatureMeasurement 5, RelativeHumidityMeasurement 4.
	// Pinned via docs/parity/matter/matter-schema-snapshot.json.
	matterThermClusterRevision    uint16 = 10
	matterThermUIClusterRevision  uint16 = 2
	matterTempMeasClusterRevision uint16 = 5
	matterHumidityClusterRevision uint16 = 4

	// Matter Thermostat SystemMode enum values (spec 4.3.7.4.4).
	matterSysModeOff  uint8 = 0
	matterSysModeAuto uint8 = 1
	matterSysModeCool uint8 = 3
	matterSysModeHeat uint8 = 4

	// ControlSequenceOfOperation values (spec 4.3.7.4.3).
	matterCtrlSeqCoolingOnly       uint8 = 0
	matterCtrlSeqHeatingOnly       uint8 = 2
	matterCtrlSeqHeatingAndCooling uint8 = 4

	// Thermostat FeatureMap bits (spec 4.3.5).
	matterThermFeatureHeat uint32 = 1 << 0
	matterThermFeatureCool uint32 = 1 << 1
	matterThermFeatureAuto uint32 = 1 << 5

	// TemperatureDisplayMode values: 0 = Celsius, 1 = Fahrenheit.
	matterTempDisplayCelsius uint8 = 0

	// KeypadLockoutEnum values (0x0204 / 0x0001). HM devices do not expose
	// keypad lockout; the bridge always reports NoLockout (0).
	matterKeypadLockoutNone uint8 = 0
)

var (
	errMatterUnknownAttribute = errors.New("matter: unknown attribute")
	errMatterUnknownCommand   = errors.New("matter: unknown command")
	errMatterValueType        = errors.New("matter: unexpected value type")
	errMatterUnsupportedMode  = errors.New("matter: unsupported SystemMode for this device")
)

// celsiusToMatter encodes an HM temperature (°C) into Matter's int16
// 0.01°C convention. Saturates at int16 bounds rather than wrapping.
func celsiusToMatter(c float64) int16 {
	v := c * 100
	if v > 32767 {
		return 32767
	}
	if v < -32768 {
		return -32768
	}
	return int16(v)
}

// matterToCelsius is the inverse of [celsiusToMatter].
func matterToCelsius(m int16) float64 { return float64(m) / 100 }

// humidityToMatter encodes an HM humidity (% RH, 0..100) into Matter's
// uint16 0.01% convention.
func humidityToMatter(h float64) uint16 {
	v := h * 100
	if v < 0 {
		return 0
	}
	if v > 10000 {
		return 10000
	}
	return uint16(v)
}

// hmModeToMatter maps the Climate domain Mode onto Matter's
// SystemMode enum. Profile-overlay states (away, boost) collapse onto
// their parent mode — Matter has no native equivalents and the
// Thermostat-schedule surface is not yet wired (see ADR 0012).
func hmModeToMatter(m Mode) uint8 {
	switch m {
	case ModeAuto:
		return matterSysModeAuto
	case ModeHeat:
		return matterSysModeHeat
	case ModeCool:
		return matterSysModeCool
	case ModeOff:
		return matterSysModeOff
	default:
		return matterSysModeAuto
	}
}

// matterToHmMode is the inverse of [hmModeToMatter] used by SystemMode
// writes.
func matterToHmMode(m uint8) (Mode, error) {
	switch m {
	case matterSysModeOff:
		return ModeOff, nil
	case matterSysModeAuto:
		return ModeAuto, nil
	case matterSysModeHeat:
		return ModeHeat, nil
	case matterSysModeCool:
		return ModeCool, nil
	default:
		return "", fmt.Errorf("%w: SystemMode=%d", errMatterUnsupportedMode, m)
	}
}

// MatterDeviceType implements [interfaces.MatterEndpointSource]. All
// climate variants — IP, RF, SimpleRF — surface as Matter Thermostat
// (0x0301); the cluster surface inside captures the differences.
func (c *Climate) MatterDeviceType() uint16 { return matterDeviceTypeThermostat }

// MatterClusterServers implements [interfaces.MatterEndpointSource].
// Thermostat + ThermostatUI + TemperatureMeasurement are always
// emitted; RelativeHumidityMeasurement is conditional on the channel
// carrying a HUMIDITY parameter.
//
// Schedules cluster (0x0024) is intentionally NOT emitted: matter.js's
// MatterDefinition (the de-facto reference implementation tracking
// Matter Core 1.5.1) does not include a cluster at ID 0x0024 — the
// Schedules cluster is either pre-publication / draft spec, or the
// reference dropped it. Apple Home's HAP-service mapper rejects an
// endpoint that advertises an unknown cluster ID with
// "Failed to rebuild HAP services" / HAPErrorDomain Code=24, killing
// pair after Subscribe-Initial. Verified by decoding the
// outbound 540-report Subscribe-Initial via @matter/types' TlvDataReport
// schema: the four Thermostat endpoints all carried 0x24 with
// Status_0x84 in `attr=5`, and Apple sent RemoveFabric immediately
// after reading those reports. The Schedules infrastructure stays in
// the wire package for revival once a canonical Matter Schedules cluster
// ships in matter.js or the spec.
func (c *Climate) MatterClusterServers() []interfaces.MatterClusterServer {
	servers := []interfaces.MatterClusterServer{
		climateThermostatServer{c: c},
		climateThermostatUIServer{c: c},
		climateTempMeasServer{c: c},
	}
	if c.humidity != nil {
		servers = append(servers, climateHumidityServer{c: c})
	}
	return servers
}

// MatterScheduleEntries implements [wire.SchedulesSource]. It maps
// the CCU week-program slots from the channel's MASTER paramset to
// Matter ScheduleEntry tuples.
//
// HM week-program parameter naming convention:
//
//	P<N>_TEMPERATURE_<DAY>_<SLOT>  (FLOAT, °C)
//	P<N>_ENDTIME_<DAY>_<SLOT>      (INTEGER, minutes since midnight)
//
// N selects the week program (1..6 typical), DAY is the full English
// day name (MONDAY..SUNDAY), SLOT is 1..13. The currently-active week
// program is derived from `Climate.Profile()`; when the climate is
// in a non-week-program profile (BOOST, AWAY, ComfortEco, NONE), the
// schedule is empty.
//
// Day-of-week conversion HM → Matter: the §11.20 ScheduleEntry uses
// 0=Sunday..6=Saturday; HM has no canonical numbering but we map by
// the full English name.
//
// Returns nil when the channel has no week-program parameters
// populated (early-boot, MASTER paramset not yet ingested) — the
// empty schedule is a valid Matter response.
func (c *Climate) MatterScheduleEntries() []wire.ScheduleEntry {
	if c == nil || c.channelRef == nil {
		return nil
	}
	profile, ok := c.Profile()
	if !ok {
		return nil
	}
	idx, ok := profileWeekIndex(profile)
	if !ok {
		// BOOST / AWAY / NONE: no week-program transitions.
		return nil
	}
	prefix := fmt.Sprintf("P%d_", idx+1)
	out := make([]wire.ScheduleEntry, 0, 7*13)
	for matterDayInt, hmDay := range matterDayOfWeekFromHMName {
		matterDay := uint8(matterDayInt) //nolint:gosec // bounded by array size 7
		for slot := 1; slot <= 13; slot++ {
			endtimeKey := hmenum.Parameter(prefix + "ENDTIME_" + hmDay + "_" + strconv.Itoa(slot))
			tempKey := hmenum.Parameter(prefix + "TEMPERATURE_" + hmDay + "_" + strconv.Itoa(slot))
			endDP := c.channelRef.MasterParameter(endtimeKey)
			tempDP := c.channelRef.MasterParameter(tempKey)
			if endDP == nil || tempDP == nil {
				continue
			}
			endRaw, ok := endDP.RawValue()
			if !ok {
				continue
			}
			tempRaw, ok := tempDP.RawValue()
			if !ok {
				continue
			}
			minutes, ok := toUint16Minutes(endRaw)
			if !ok {
				continue
			}
			temp, ok := toFloat64(tempRaw)
			if !ok {
				continue
			}
			out = append(out, wire.ScheduleEntry{
				DayOfWeek:      matterDay,
				TransitionTime: minutes,
				Setpoint:       temp,
			})
		}
	}
	return out
}

// matterDayOfWeekFromHMName maps a Matter §11.20 day-of-week index
// (0=Sunday..6=Saturday) to the HM full-English-name segment used in
// the P<N>_TEMPERATURE_<DAY>_<SLOT> / P<N>_ENDTIME_<DAY>_<SLOT>
// MASTER parameter names.
var matterDayOfWeekFromHMName = [7]string{
	"SUNDAY", "MONDAY", "TUESDAY", "WEDNESDAY", "THURSDAY", "FRIDAY", "SATURDAY",
}

// toUint16Minutes coerces a raw MASTER value (the wire encoder
// usually delivers it as an int64 or float64 depending on
// transport) into the 0..1440 minutes-of-day range Matter
// ScheduleEntry expects.
func toUint16Minutes(raw any) (uint16, bool) {
	switch v := raw.(type) {
	case int:
		if v < 0 || v > 1440 {
			return 0, false
		}
		return uint16(v), true //nolint:gosec // bounded by the if above
	case int32:
		if v < 0 || v > 1440 {
			return 0, false
		}
		return uint16(v), true //nolint:gosec // bounded by the if above
	case int64:
		if v < 0 || v > 1440 {
			return 0, false
		}
		return uint16(v), true //nolint:gosec // bounded by the if above
	case float64:
		if v < 0 || v > 1440 {
			return 0, false
		}
		return uint16(v), true //nolint:gosec // bounded by the if above
	default:
		return 0, false
	}
}

// toFloat64 coerces a raw MASTER value to float64 for Setpoint.
func toFloat64(raw any) (float64, bool) {
	switch v := raw.(type) {
	case float64:
		return v, true
	case float32:
		return float64(v), true
	case int:
		return float64(v), true
	case int32:
		return float64(v), true
	case int64:
		return float64(v), true
	default:
		return 0, false
	}
}

// Compile-time assertion: Climate implements [wire.SchedulesSource].
var _ wire.SchedulesSource = (*Climate)(nil)

// climateThermostatServer projects Climate onto the Matter Thermostat
// cluster (0x0201).
type climateThermostatServer struct{ c *Climate }

func (s climateThermostatServer) MatterClusterID() uint32 { return matterClusterThermostat }

func (s climateThermostatServer) MatterRead(attrID uint32) (any, bool) {
	switch attrID {
	case matterAttrThermLocalTemperature:
		// LocalTemperature is mandatory for the Thermostat cluster
		// (Matter §4.3.9.1). When the bridged HmIP device is currently
		// unreachable (CCU offline / circuit-breaker open / no
		// observation yet) we return (nil, true) → TLV null + Success.
		// Apple Home's HAP-service rebuild tolerates a null mandatory
		// attribute ("temporarily unknown") but treats
		// UnsupportedAttribute as a structural error and aborts with
		// HAPErrorDomain Code=24.
		t, ok := s.c.CurrentTemperature()
		if !ok {
			return nil, true
		}
		return celsiusToMatter(t), true
	case matterAttrThermOccupiedHeatSp, matterAttrThermOccupiedCoolSp:
		// HM exposes a single setpoint per Climate; both heat and cool
		// setpoints in Matter map back to that one value. The
		// ControlSequenceOfOperation attribute (heating-only) tells
		// Matter controllers which one is meaningful. Same null-on-
		// unknown rationale as LocalTemperature above — both
		// setpoints are mandatory in the matter.js OnOffPlugInUnit
		// sibling pattern, so Apple Home expects the cluster to
		// surface them even when the underlying device is offline.
		t, ok := s.c.Setpoint()
		if !ok {
			return nil, true
		}
		return celsiusToMatter(t), true
	case matterAttrThermMinHeatSp, matterAttrThermMinCoolSp:
		return celsiusToMatter(s.c.MinTemp()), true
	case matterAttrThermMaxHeatSp, matterAttrThermMaxCoolSp:
		return celsiusToMatter(s.c.MaxTemp()), true
	case matterAttrThermControlSeq:
		// HmIP heating valves are heating-only. Widening to
		// HeatingAndCooling requires a cooling-capable HM device
		// profile that surfaces a SupportsCool capability.
		return matterCtrlSeqHeatingOnly, true
	case matterAttrThermSystemMode, matterAttrThermRunningMode:
		// SystemMode is mandatory; RunningMode is optional but Apple
		// Home reads both during HAP rebuild. Null-on-unknown so a
		// briefly-unreachable bridged device doesn't break HAP
		// service construction (see LocalTemperature comment above).
		m, ok := s.c.Mode()
		if !ok {
			return nil, true
		}
		return hmModeToMatter(m), true
	case matterAttrFeatureMap:
		// HmIP heating valves are heat-only; AUTO requires both HEAT and COOL
		// (spec feature-conformance table: HEAT conformance "AUTO, O.a+", COOL
		// conformance "AUTO, O.a+"). Advertising AUTO without COOL violates the
		// invariant and causes commissioners to negotiate an unsupported
		// setpoint-deadband attribute (0x0025 MinSetpointDeadBand).
		// Mirrors matter.js packages/model/src/standard/elements/thermostat-cluster.element.ts:24-28.
		return matterThermFeatureHeat, true
	case matterAttrClusterRevision:
		return matterThermClusterRevision, true
	default:
		return nil, false
	}
}

func (s climateThermostatServer) MatterWrite(ctx context.Context, attrID uint32, value any, priority hmenum.CommandPriority) error {
	var err error
	switch attrID {
	case matterAttrThermOccupiedHeatSp, matterAttrThermOccupiedCoolSp:
		v, ok := value.(int16)
		if !ok {
			return fmt.Errorf("%w: setpoint write expected int16, got %T", errMatterValueType, value)
		}
		err = s.c.SetTemperature(ctx, matterToCelsius(v), priority)
	case matterAttrThermSystemMode:
		raw, ok := value.(uint8)
		if !ok {
			return fmt.Errorf("%w: SystemMode write expected uint8, got %T", errMatterValueType, value)
		}
		mode, e := matterToHmMode(raw)
		if e != nil {
			return e
		}
		err = s.c.SetMode(ctx, mode, priority)
	default:
		return fmt.Errorf("%w: 0x%04X", errMatterUnknownAttribute, attrID)
	}
	if err != nil {
		return err
	}
	s.c.dataVersion.Bump()
	return nil
}

func (s climateThermostatServer) MatterInvoke(ctx context.Context, cmdID uint32, fields any, priority hmenum.CommandPriority) (any, error) {
	if cmdID != matterCmdSetpointRaiseLower {
		return nil, fmt.Errorf("%w: 0x%02X", errMatterUnknownCommand, cmdID)
	}
	mode, amount, err := extractSetpointRaiseLower(fields)
	if err != nil {
		return nil, err
	}
	cur, ok := s.c.Setpoint()
	if !ok {
		// No baseline — the command can't be applied as a delta. The
		// spec allows refusing here; the bridge will translate this
		// into a FAILURE status.
		return nil, errors.New("matter: SetpointRaiseLower needs an observed setpoint")
	}
	// `amount` is in 0.1°C units; positive raises, negative lowers.
	delta := float64(amount) / 10
	target := cur + delta
	// `mode` (Heat/Cool/Both) is informational on heating-only HM
	// devices — every effective adjust hits the single setpoint.
	_ = mode
	if err := s.c.SetTemperature(ctx, target, priority); err != nil {
		return nil, err
	}
	s.c.dataVersion.Bump()
	return nil, nil
}

func (s climateThermostatServer) MatterReportable() []uint32 {
	return []uint32{
		matterAttrThermLocalTemperature,
		matterAttrThermOccupiedHeatSp,
		matterAttrThermSystemMode,
	}
}

// MatterAttributes lists every Thermostat (0x0201) attribute the
// server implements via [MatterRead]. Apple Home's HAP service rebuild
// reads the full attribute set during the post-CommissioningComplete
// wildcard subscribe and bails with HAPErrorDomain Code=24 if the
// reported list does not cover the cluster's mandatory surface
// (LocalTemperature, OccupiedHeatingSetpoint, OccupiedCoolingSetpoint,
// MinHeat/Cool, MaxHeat/Cool, ControlSequenceOfOperation, SystemMode,
// LocalTemperatureNotExposed). Without this method the dispatcher
// falls back to MatterReportable's three-attribute subscription
// surface — which is fine for change-driven subscribes but starves
// Apple's HAP mapper.
func (s climateThermostatServer) MatterAttributes() []uint32 {
	return []uint32{
		matterAttrThermLocalTemperature,
		matterAttrThermOccupiedCoolSp,
		matterAttrThermOccupiedHeatSp,
		matterAttrThermMinHeatSp,
		matterAttrThermMaxHeatSp,
		matterAttrThermMinCoolSp,
		matterAttrThermMaxCoolSp,
		matterAttrThermControlSeq,
		matterAttrThermSystemMode,
		matterAttrThermRunningMode,
	}
}

// climateThermostatUIServer projects Climate onto the
// ThermostatUserInterfaceConfiguration cluster (0x0204). HM devices
// always report Celsius and do not expose KeypadLockout / scheduling
// visibility through the wire layer; the projection is therefore
// largely static.
type climateThermostatUIServer struct{ c *Climate }

func (s climateThermostatUIServer) MatterClusterID() uint32 { return matterClusterThermostatUI }

func (s climateThermostatUIServer) MatterRead(attrID uint32) (any, bool) {
	switch attrID {
	case matterAttrUITempDisplayMode:
		return matterTempDisplayCelsius, true
	case matterAttrUIKeypadLockout:
		// HM devices do not expose keypad lockout; always report NoLockout.
		return matterKeypadLockoutNone, true
	case matterAttrFeatureMap:
		return uint32(0), true
	case matterAttrClusterRevision:
		return matterThermUIClusterRevision, true
	default:
		return nil, false
	}
}

func (s climateThermostatUIServer) MatterWrite(_ context.Context, attrID uint32, _ any, _ hmenum.CommandPriority) error {
	return fmt.Errorf("%w: 0x%04X", errMatterUnknownAttribute, attrID)
}

func (s climateThermostatUIServer) MatterInvoke(_ context.Context, cmdID uint32, _ any, _ hmenum.CommandPriority) (any, error) {
	return nil, fmt.Errorf("%w: 0x%02X", errMatterUnknownCommand, cmdID)
}

func (s climateThermostatUIServer) MatterReportable() []uint32 { return nil }

// MatterAttributes lists the ThermostatUserInterfaceConfiguration
// (0x0204) surface. Both mandatory attributes — TemperatureDisplayMode
// (0x0000) and KeypadLockout (0x0001) — are included so Apple Home's
// HAP service rebuild does not abort on a missing mandatory attribute.
func (s climateThermostatUIServer) MatterAttributes() []uint32 {
	return []uint32{matterAttrUITempDisplayMode, matterAttrUIKeypadLockout}
}

// climateTempMeasServer projects ACTUAL_TEMPERATURE onto the
// TemperatureMeasurement cluster (0x0402). This duplicates the
// LocalTemperature attribute on the Thermostat cluster — Matter
// controllers may consult either; emitting both improves the
// compatibility profile (HA Matter Server prefers TemperatureMeasurement
// for chart history while Apple Home reads LocalTemperature).
type climateTempMeasServer struct{ c *Climate }

func (s climateTempMeasServer) MatterClusterID() uint32 {
	return matterClusterTemperatureMeasurement
}

func (s climateTempMeasServer) MatterRead(attrID uint32) (any, bool) {
	switch attrID {
	case matterAttrMeasuredValue:
		// MeasuredValue is mandatory but legitimately null when the
		// device is unreachable / has no observation yet — return
		// (nil, true) so the dispatcher emits TLV null + Success
		// rather than UnsupportedAttribute. Apple Home tolerates null
		// here but flags UnsupportedAttribute as a structural error.
		t, ok := s.c.CurrentTemperature()
		if !ok {
			return nil, true
		}
		return celsiusToMatter(t), true
	case matterAttrFeatureMap:
		return uint32(0), true
	case matterAttrClusterRevision:
		return matterTempMeasClusterRevision, true
	default:
		return nil, false
	}
}

func (s climateTempMeasServer) MatterWrite(_ context.Context, attrID uint32, _ any, _ hmenum.CommandPriority) error {
	return fmt.Errorf("%w: 0x%04X", errMatterUnknownAttribute, attrID)
}

func (s climateTempMeasServer) MatterInvoke(_ context.Context, cmdID uint32, _ any, _ hmenum.CommandPriority) (any, error) {
	return nil, fmt.Errorf("%w: 0x%02X", errMatterUnknownCommand, cmdID)
}

func (s climateTempMeasServer) MatterReportable() []uint32 {
	return []uint32{matterAttrMeasuredValue}
}

// MatterAttributes covers the TemperatureMeasurement (0x0402) cluster's
// projected surface. Single MeasuredValue attribute today; FeatureMap
// + ClusterRevision are dispatched as globals.
func (s climateTempMeasServer) MatterAttributes() []uint32 {
	return []uint32{matterAttrMeasuredValue}
}

// climateHumidityServer projects HUMIDITY onto the
// RelativeHumidityMeasurement cluster (0x0405). Only emitted when
// Climate's humidity slot is non-nil; see [Climate.MatterClusterServers].
type climateHumidityServer struct{ c *Climate }

func (s climateHumidityServer) MatterClusterID() uint32 {
	return matterClusterRelativeHumidityMeasurement
}

func (s climateHumidityServer) MatterRead(attrID uint32) (any, bool) {
	switch attrID {
	case matterAttrMeasuredValue:
		// Same null-on-unknown rationale as
		// [climateTempMeasServer.MatterRead].
		h, ok := s.c.Humidity()
		if !ok {
			return nil, true
		}
		return humidityToMatter(h), true
	case matterAttrFeatureMap:
		return uint32(0), true
	case matterAttrClusterRevision:
		return matterHumidityClusterRevision, true
	default:
		return nil, false
	}
}

func (s climateHumidityServer) MatterWrite(_ context.Context, attrID uint32, _ any, _ hmenum.CommandPriority) error {
	return fmt.Errorf("%w: 0x%04X", errMatterUnknownAttribute, attrID)
}

func (s climateHumidityServer) MatterInvoke(_ context.Context, cmdID uint32, _ any, _ hmenum.CommandPriority) (any, error) {
	return nil, fmt.Errorf("%w: 0x%02X", errMatterUnknownCommand, cmdID)
}

func (s climateHumidityServer) MatterReportable() []uint32 {
	return []uint32{matterAttrMeasuredValue}
}

// MatterAttributes mirrors [climateTempMeasServer.MatterAttributes]
// for the RelativeHumidityMeasurement (0x0405) cluster.
func (s climateHumidityServer) MatterAttributes() []uint32 {
	return []uint32{matterAttrMeasuredValue}
}

// extractSetpointRaiseLower pulls (mode, amount) out of the request
// payload. The bridge has already TLV-decoded the payload; we accept
// either a struct-shaped map or a raw 2-element tuple. A typed request
// struct from internal/north/matter/cluster/thermostat/ may replace
// this once that package exists.
func extractSetpointRaiseLower(fields any) (mode uint8, amount int8, err error) {
	switch v := fields.(type) {
	case map[string]any:
		rawMode, ok := v["mode"]
		if !ok {
			return 0, 0, fmt.Errorf("%w: SetpointRaiseLower missing mode", errMatterValueType)
		}
		mode, ok = rawMode.(uint8)
		if !ok {
			return 0, 0, fmt.Errorf("%w: SetpointRaiseLower mode expected uint8, got %T", errMatterValueType, rawMode)
		}
		rawAmount, ok := v["amount"]
		if !ok {
			return 0, 0, fmt.Errorf("%w: SetpointRaiseLower missing amount", errMatterValueType)
		}
		amount, ok = rawAmount.(int8)
		if !ok {
			return 0, 0, fmt.Errorf("%w: SetpointRaiseLower amount expected int8, got %T", errMatterValueType, rawAmount)
		}
		return mode, amount, nil
	default:
		return 0, 0, fmt.Errorf("%w: SetpointRaiseLower expected map[string]any, got %T", errMatterValueType, fields)
	}
}
