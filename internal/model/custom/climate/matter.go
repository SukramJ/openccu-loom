// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package climate

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"github.com/SukramJ/openccu-loom/internal/north/matter/cluster/wire"
	"github.com/SukramJ/openccu-loom/internal/north/matter/im"
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

// climateSystemModeUnsupportedError is a typed [im.StatusCodeError]
// returned when a SystemMode write requests Cool or Auto on a Climate
// whose FeatureMap does not advertise the corresponding feature (e.g.
// SystemMode=Cool on a heating-only HmIP valve). Mirrors matter.js
// ThermostatServer.ts:#assertSystemModeChanging, which throws
// StatusResponse.ConstraintErrorError for a SystemMode value the current
// feature/ControlSequenceOfOperation configuration forbids —
// packages/node/src/behaviors/thermostat/ThermostatServer.ts:615-632. The
// SystemModeEnum conformance table itself ties Cool to "[COOL]" and Auto
// to "AUTO" — packages/model/src/standard/elements/thermostat-cluster.element.ts:558-559.
type climateSystemModeUnsupportedError struct{ msg string }

func (e climateSystemModeUnsupportedError) Error() string { return e.msg }

// MatterStatusCode implements [im.StatusCodeError] so the dispatcher
// encodes ConstraintError instead of falling back to the generic
// StatusFailure — see internal/north/matter/endpoint/dispatcher.go
// writeErrorStatus.
func (climateSystemModeUnsupportedError) MatterStatusCode() im.StatusCode {
	return im.StatusConstraintError
}

var _ im.StatusCodeError = climateSystemModeUnsupportedError{}

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
		matterDay := uint8(matterDayInt) //nolint:gosec // bounded by array size 7; see #20
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
		return uint16(v), true //nolint:gosec // bounded by the if above; see #20
	case int32:
		if v < 0 || v > 1440 {
			return 0, false
		}
		return uint16(v), true //nolint:gosec // bounded by the if above; see #20
	case int64:
		if v < 0 || v > 1440 {
			return 0, false
		}
		return uint16(v), true //nolint:gosec // bounded by the if above; see #20
	case float64:
		if v < 0 || v > 1440 {
			return 0, false
		}
		return uint16(v), true //nolint:gosec // bounded by the if above; see #20
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

// featureMap computes the Thermostat FeatureMap this server actually
// reports. Every climate profile registered in init.go sets
// Capabilities.SupportsCool=false today, so COOL (and therefore AUTO,
// which requires both HEAT and COOL) never light up in production — but
// keying off the capability instead of hardcoding means a future
// heat+cool HM profile flips both bits here automatically, and the
// attribute gating in MatterRead/MatterAttributes/MatterWrite below
// follows without further changes. AUTO requires HEAT+COOL per the
// FeatureMap conformance table (HEAT/COOL: "AUTO, O.a+") — matter.js
// packages/model/src/standard/elements/thermostat-cluster.element.ts:25-26,29.
func (s climateThermostatServer) featureMap() uint32 {
	fm := matterThermFeatureHeat
	if s.c.Capabilities.SupportsCool {
		fm |= matterThermFeatureCool | matterThermFeatureAuto
	}
	return fm
}

// controlSequenceOfOperation derives the mandatory
// ControlSequenceOfOperation value (Matter §4.3.7.4.3) from the
// FeatureMap so a future heat+cool profile reports CoolingAndHeating
// instead of the previously hardcoded HeatingOnly. The bridge exposes
// this attribute read-only because it tracks the wrapped HM device's
// fixed HEAT/COOL capability — there is nothing for a controller to
// change.
func controlSequenceOfOperation(fm uint32) uint8 {
	hasHeat := fm&matterThermFeatureHeat != 0
	hasCool := fm&matterThermFeatureCool != 0
	switch {
	case hasHeat && hasCool:
		return matterCtrlSeqHeatingAndCooling
	case hasCool:
		return matterCtrlSeqCoolingOnly
	default:
		return matterCtrlSeqHeatingOnly
	}
}

// systemModeAllowed reports whether raw is a legal SystemMode value for
// the FeatureMap fm reports. Cool(3) conformance is "[COOL]" and Auto(1)
// conformance is "AUTO" — both disallowed when the corresponding feature
// bit is absent. Mirrors matter.js
// packages/model/src/standard/elements/thermostat-cluster.element.ts:558-559.
func systemModeAllowed(raw uint8, fm uint32) bool {
	switch raw {
	case matterSysModeCool:
		return fm&matterThermFeatureCool != 0
	case matterSysModeAuto:
		return fm&matterThermFeatureAuto != 0
	default:
		return true
	}
}

func (s climateThermostatServer) MatterRead(attrID uint32) (any, bool) {
	fm := s.featureMap()
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
	case matterAttrThermOccupiedHeatSp:
		// HM exposes a single setpoint per Climate; the Heat setpoint in
		// Matter maps back to that one value. Same null-on-unknown
		// rationale as LocalTemperature above — OccupiedHeatingSetpoint
		// is mandatory conformance "HEAT" (always present here; every
		// climate profile sets Capabilities.SupportsHeat).
		t, ok := s.c.Setpoint()
		if !ok {
			return nil, true
		}
		return celsiusToMatter(t), true
	case matterAttrThermOccupiedCoolSp:
		// Conformance "COOL" (thermostat-cluster.element.ts:66-68):
		// disallowed without the COOL feature. Not present on any
		// registered climate profile today — kept feature-gated so a
		// future heat+cool HM device lights it up via featureMap alone.
		if fm&matterThermFeatureCool == 0 {
			return nil, false
		}
		t, ok := s.c.Setpoint()
		if !ok {
			return nil, true
		}
		return celsiusToMatter(t), true
	case matterAttrThermMinHeatSp:
		return celsiusToMatter(s.c.MinTemp()), true
	case matterAttrThermMaxHeatSp:
		return celsiusToMatter(s.c.MaxTemp()), true
	case matterAttrThermMinCoolSp:
		// Conformance "[COOL]" (thermostat-cluster.element.ts:92-95).
		if fm&matterThermFeatureCool == 0 {
			return nil, false
		}
		return celsiusToMatter(s.c.MinTemp()), true
	case matterAttrThermMaxCoolSp:
		// Conformance "[COOL]" (thermostat-cluster.element.ts:96-98).
		if fm&matterThermFeatureCool == 0 {
			return nil, false
		}
		return celsiusToMatter(s.c.MaxTemp()), true
	case matterAttrThermControlSeq:
		return controlSequenceOfOperation(fm), true
	case matterAttrThermSystemMode:
		// SystemMode is mandatory. Null-on-unknown so a briefly-
		// unreachable bridged device doesn't break HAP service
		// construction (see LocalTemperature comment above).
		m, ok := s.c.Mode()
		if !ok {
			return nil, true
		}
		return hmModeToMatter(m), true
	case matterAttrThermRunningMode:
		// Conformance "TEVT & AUTO, [AUTO]" (thermostat-cluster.element.ts:117-120):
		// disallowed without the AUTO feature. Apple Home reads it during
		// HAP rebuild only when FeatureMap advertises AUTO.
		if fm&matterThermFeatureAuto == 0 {
			return nil, false
		}
		m, ok := s.c.Mode()
		if !ok {
			return nil, true
		}
		return hmModeToMatter(m), true
	case matterAttrFeatureMap:
		return fm, true
	case matterAttrClusterRevision:
		return matterThermClusterRevision, true
	default:
		return nil, false
	}
}

func (s climateThermostatServer) MatterWrite(ctx context.Context, attrID uint32, value any, priority hmenum.CommandPriority) error {
	var err error
	switch attrID {
	case matterAttrThermOccupiedHeatSp:
		v, ok := value.(int16)
		if !ok {
			return fmt.Errorf("%w: setpoint write expected int16, got %T", errMatterValueType, value)
		}
		err = s.c.SetTemperature(ctx, matterToCelsius(v), priority)
	case matterAttrThermOccupiedCoolSp:
		// Conformance "COOL" — not a writable attribute at all when the
		// FeatureMap omits COOL, so this reaches the wire as an unknown
		// attribute rather than silently retargeting the single HM
		// heating setpoint.
		if s.featureMap()&matterThermFeatureCool == 0 {
			return fmt.Errorf("%w: 0x%04X", errMatterUnknownAttribute, attrID)
		}
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
		fm := s.featureMap()
		if !systemModeAllowed(raw, fm) {
			return climateSystemModeUnsupportedError{
				fmt.Sprintf("matter: SystemMode=%d not supported by FeatureMap 0x%02X", raw, fm),
			}
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

// MatterAttributes lists every Thermostat (0x0201) attribute the server
// implements via [MatterRead], feature-gated against [featureMap] so the
// advertised set matches conformance exactly: OccupiedCoolingSetpoint /
// MinCoolSetpointLimit / MaxCoolSetpointLimit require COOL, and
// ThermostatRunningMode requires AUTO (thermostat-cluster.element.ts:66-68,
// 92-98, 117-120). Advertising a disallowed attribute is as much a HAP
// service rebuild hazard for Apple Home as omitting a mandatory one, so
// on a heating-only device (every profile registered today) this list
// covers only LocalTemperature, the Heat-gated setpoints/limits,
// ControlSequenceOfOperation and SystemMode. Without this method the
// dispatcher falls back to MatterReportable's three-attribute
// subscription surface — which is fine for change-driven subscribes but
// starves Apple's HAP mapper.
func (s climateThermostatServer) MatterAttributes() []uint32 {
	fm := s.featureMap()
	attrs := []uint32{
		matterAttrThermLocalTemperature,
		matterAttrThermOccupiedHeatSp,
		matterAttrThermMinHeatSp,
		matterAttrThermMaxHeatSp,
		matterAttrThermControlSeq,
		matterAttrThermSystemMode,
	}
	if fm&matterThermFeatureCool != 0 {
		attrs = append(
			attrs,
			matterAttrThermOccupiedCoolSp,
			matterAttrThermMinCoolSp,
			matterAttrThermMaxCoolSp,
		)
	}
	if fm&matterThermFeatureAuto != 0 {
		attrs = append(attrs, matterAttrThermRunningMode)
	}
	return attrs
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

// extractSetpointRaiseLower pulls (mode, amount) out of a
// SetpointRaiseLower request. Mode is context tag 0 (enum8), Amount is
// context tag 1 (int8). SetpointRaiseLower has no typed decoder in the
// bridge, so the real wire path lands here as the tag-keyed map[uint8]any
// that decodeGenericTagMap produces — unsigned ints as uint64, signed
// ints as int64 (see internal/north/matter/bridge/fields_reader.go). The
// string-keyed shape is kept for the in-package tests.
func extractSetpointRaiseLower(fields any) (mode uint8, amount int8, err error) {
	switch v := fields.(type) {
	case map[uint8]any:
		if rawMode, ok := v[0]; ok {
			m, ok := wireSetpointMode(rawMode)
			if !ok {
				return 0, 0, fmt.Errorf("%w: SetpointRaiseLower mode expected integer, got %T", errMatterValueType, rawMode)
			}
			mode = m
		}
		rawAmount, ok := v[1]
		if !ok {
			return 0, 0, fmt.Errorf("%w: SetpointRaiseLower missing amount (tag 1)", errMatterValueType)
		}
		amount, ok = wireSetpointAmount(rawAmount)
		if !ok {
			return 0, 0, fmt.Errorf("%w: SetpointRaiseLower amount expected integer, got %T", errMatterValueType, rawAmount)
		}
		return mode, amount, nil
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
		return 0, 0, fmt.Errorf("%w: SetpointRaiseLower expected map[uint8]any, got %T", errMatterValueType, fields)
	}
}

// wireSetpointMode reads the SetpointRaiseLower Mode enum from a value
// decoded by decodeGenericTagMap (unsigned ints land as uint64).
func wireSetpointMode(raw any) (uint8, bool) {
	switch n := raw.(type) {
	case uint64:
		return uint8(n & 0xFF), true
	case uint8:
		return n, true
	default:
		return 0, false
	}
}

// wireSetpointAmount reads the SetpointRaiseLower Amount (signed int8)
// from a value decoded by decodeGenericTagMap (signed ints land as
// int64).
func wireSetpointAmount(raw any) (int8, bool) {
	switch n := raw.(type) {
	case int64:
		return int8(n), true //nolint:gosec // field is a signed byte per spec; see #20
	case int8:
		return n, true
	case int:
		return int8(n), true //nolint:gosec // field is a signed byte per spec; see #20
	default:
		return 0, false
	}
}
