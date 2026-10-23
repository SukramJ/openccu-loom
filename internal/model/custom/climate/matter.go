// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package climate

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"github.com/SukramJ/go-fabric/cluster/measurement"
	thermostatspec "github.com/SukramJ/go-fabric/cluster/spec/thermostat"
	tuidef "github.com/SukramJ/go-fabric/cluster/spec/thermostatuserinterfaceconfiguration"
	"github.com/SukramJ/go-fabric/cluster/wire"
	fabricdevice "github.com/SukramJ/go-fabric/device"

	"github.com/SukramJ/openccu-loom/internal/model/custom"
	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/interfaces"
)

// matterDispatchPriority is the southbound urgency every Matter-driven
// write and invoke carries. The bridge is a controller-facing
// foreground path — a tap in a Matter app must not queue behind a
// background refresh — so it dispatches at High, and the cluster
// contract no longer negotiates it per call.
//
// Spelled out as a constant rather than left to a variable: the zero
// value of [hmenum.CommandPriority] is Critical, so anything that
// reached these calls defaulted would silently escalate every bridged
// command.
const matterDispatchPriority = hmenum.CommandPriorityHigh

// Compile-time assertion: Climate participates in the Matter source
// surface (ADR 0012) as a Matter Thermostat (0x0301) device with two
// declared clusters: Thermostat (0x0201, with the Climate as its Sink)
// and ThermostatUserInterfaceConfiguration (0x0204). The Schedules
// cluster (0x0024) is intentionally NOT declared (see
// [Climate.MatterDevice]).
var (
	_ interfaces.MatterDeviceSource   = (*Climate)(nil)
	_ interfaces.MatterChangeNotifier = (*Climate)(nil)
	_ thermostatspec.Sink             = climateThermoSink{}
)

// OnMatterValueChanged implements [interfaces.MatterChangeNotifier].
// A Climate projects several wire-backed data points onto the Thermostat /
// TemperatureMeasurement / RelativeHumidityMeasurement clusters; fan every
// value-bearing DP into the callback so an external CCU change (setpoint
// adjusted at the wall dial, ambient temperature/humidity reading) dirty-
// marks the endpoint and reaches Apple's Subscribe. Each DP's own
// OnMatterValueChanged guards a nil receiver, so absent DPs contribute a
// no-op unsubscribe. The Thermostat server lists LocalTemperature and
// SystemMode as its reportable attributes, so this dirty-mark is what
// reports a temperature reading; the setpoints and a mode the device
// took on itself are reported by the server's own change notifier once
// the confirmed value reaches it.
func (c *Climate) OnMatterValueChanged(cb func()) func() {
	if c == nil || cb == nil {
		return func() {}
	}
	return custom.CombineUnsubs(
		c.setpoint.OnMatterValueChanged(cb),
		c.actualTemperature.OnMatterValueChanged(cb),
		c.humidity.OnMatterValueChanged(cb),
		c.humidityInt.OnMatterValueChanged(cb),
	)
}

// The Thermostat device type (matter.js
// packages/node/src/devices/thermostat.ts). The two clusters' ids,
// revisions, attribute and command lists come from go-fabric's generated
// definitions behind the kit's servers.
const matterDeviceTypeThermostat = uint16(fabricdevice.ThermostatID)

// Matter Thermostat SystemModeEnum values (thermostat-cluster.element.ts
// SystemModeEnum), as the generated definition spells them.
const (
	matterSysModeOff  = uint8(thermostatspec.SystemModeOff)
	matterSysModeAuto = uint8(thermostatspec.SystemModeAuto)
	matterSysModeCool = uint8(thermostatspec.SystemModeCool)
	matterSysModeHeat = uint8(thermostatspec.SystemModeHeat)
)

// errMatterUnsupportedMode is the refusal of a SystemMode the Climate
// domain has no mode for. The modes a feature set does not admit (Cool
// without COOL, Auto without AUTO) never reach it: the server's
// definition refuses them with CONSTRAINT_ERROR first (matter.js
// ThermostatServer.ts #assertSystemModeChanging).
var errMatterUnsupportedMode = errors.New("matter: unsupported SystemMode for this device")

// celsiusToMatter encodes a Celsius reading as Matter's centi-degree int16
// through the one encoder, in [measurement.CelsiusToInt16]. It used to be a
// byte-identical copy of it, clamps and comments included — two encoders for
// one wire format, either of which could have been corrected alone.
func celsiusToMatter(c float64) int16 { return measurement.CelsiusToInt16(c) }

// matterToCelsius is the inverse of [celsiusToMatter].
func matterToCelsius(m int16) float64 { return float64(m) / 100 }

// matterToHmMode maps a written SystemModeEnum value back onto the
// Climate domain Mode.
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

// MatterDeviceType implements [interfaces.MatterDeviceSource]. All
// climate variants — IP, RF, SimpleRF — surface as Matter Thermostat
// (0x0301); the cluster surface inside captures the differences.
func (c *Climate) MatterDeviceType() uint16 { return matterDeviceTypeThermostat }

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

// matterThermoFeatures is the Thermostat FeatureMap this Climate
// advertises. HEAT is always set; COOL follows the profile's
// SupportsCool capability (hybrid wall thermostats driven by
// HEATING_COOLING).
//
// AUTO is deliberately NEVER advertised — not even on SupportsCool
// profiles. Matter AutoMode means independent dual setpoints and
// mandates MinSetpointDeadBand (0x0019, conformance "AUTO") — matter.js
// packages/model/src/standard/elements/thermostat-cluster.element.ts:100-104
// — while HM climates are single-setpoint devices (the Cool setpoint
// here aliases the one HM setpoint). Advertising AUTO over one setpoint
// would promise a deadband the device cannot keep.
func (c *Climate) matterThermoFeatures() thermostatspec.Feature {
	fm := thermostatspec.FeatureHeating
	if c.Capabilities.SupportsCool {
		fm |= thermostatspec.FeatureCooling
	}
	return fm
}

// MatterDevice implements [interfaces.MatterDeviceSource]: a Thermostat
// with two declared clusters, built once per Climate.
//
// Thermostat (0x0201) runs on the Climate as its Sink. The absolute
// limits are the descriptor's MIN / MAX (the capability fallback in
// [Climate.MinTemp] / [Climate.MaxTemp]), one range for heating and
// cooling (with COOL) since HM has one setpoint; without usable bounds (a Climate
// outside the profile catalogue) the kit server keeps the element's
// defaults, so its limits stay well-formed instead of pinning every
// setpoint to one value. The occupied setpoints start from the observed
// setpoint cropped to the limits; the confirmed setpoint, room
// temperature and mode reach the server through its inbound setters
// ([thermostatInbound]) from the data points' confirmed-update hooks.
//
// ThermostatUserInterfaceConfiguration (0x0204), optional on the device
// type, is declared with matter.js's defaults (Celsius, no keypad
// lockout); a controller's writes are stored in process, as in matter.js,
// since HM devices expose neither display unit nor keypad lockout on the
// wire.
//
// TemperatureMeasurement (0x0402) and RelativeHumidityMeasurement
// (0x0405) are deliberately NOT declared. The Device Library names both
// for device type 0x0301 as element=clientCluster (matter.js
// packages/model/src/standard/elements/thermostat-device.element.ts): a
// thermostat CONSUMES those readings from another endpoint, it does not
// serve them. Serving them anyway made the endpoint non-conformant, and
// Alexa recognises a bridged endpoint only by the clusters its device
// type specifies (matter.js docs/ECOSYSTEMS).
//
// Nothing is lost by dropping them. Apple reads the temperature from the
// Thermostat cluster's own LocalTemperature attribute, and the channel's
// ACTUAL_TEMPERATURE / HUMIDITY data points already materialise as their
// own TemperatureSensor (0x0302) and HumiditySensor (0x0307) endpoints
// through the generic measurement path — which carries a real
// TemperatureMeasurement cluster for the controllers that prefer one.
// That path is not gated on north.matter.include_measurements (that flag
// governs calculated data points), so the sensors appear either way.
//
// Schedules cluster (0x0024) is intentionally NOT declared: matter.js's
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
func (c *Climate) MatterDevice(opts fabricdevice.Options) (*fabricdevice.Device, error) {
	return c.matterDevice.Device(func() (*fabricdevice.Device, error) {
		var initial thermostatspec.Patch
		features := c.matterThermoFeatures()
		cool := features&thermostatspec.FeatureCooling != 0
		minSP, maxSP := celsiusToMatter(c.MinTemp()), celsiusToMatter(c.MaxTemp())
		if minSP < maxSP {
			initial.AbsMinHeatSetpointLimit, initial.AbsMaxHeatSetpointLimit = &minSP, &maxSP
			if cool {
				initial.AbsMinCoolSetpointLimit, initial.AbsMaxCoolSetpointLimit = &minSP, &maxSP
			}
			if sp, observed := c.Setpoint(); observed {
				v := min(max(celsiusToMatter(sp), minSP), maxSP)
				initial.OccupiedHeatingSetpoint = &v
				if cool {
					initial.OccupiedCoolingSetpoint = &v
				}
			}
		}
		d, err := generic.NewMatterDevice(c, matterDeviceTypeThermostat, opts,
			thermostatspec.Declare{
				Features: features,
				Initial:  initial,
				Sink:     climateThermoSink{c: c},
			},
			tuidef.Declare{})
		if err != nil {
			return nil, err
		}
		srv := thermostatInboundOf(d)
		if c.setpoint != nil {
			_ = c.setpoint.OnConfirmedUpdate(func(_, _ float64) { c.syncMatterSetpoint(srv) })
		}
		if c.actualTemperature != nil {
			_ = c.actualTemperature.OnConfirmedUpdate(func(_, _ float64) { c.syncMatterLocalTemperature(srv) })
		}
		// A confirmation that landed between the initial read and the
		// hook registration is not lost: the current values are stored
		// again, which is a no-op when nothing changed. Without usable
		// bounds this is also where the observed setpoint arrives,
		// clamped to the server's default limits.
		c.syncMatterSetpoint(srv)
		c.syncMatterLocalTemperature(srv)
		c.syncMatterModeTo(srv)
		return d, nil
	})
}

// thermostatInbound is the inbound side of the kit's Thermostat server:
// the values the device took on itself, stored and reported without the
// Sink. A setpoint outside the effective limits is clamped to them (a CCU
// reports setpoints a write would be refused for), which the typed Set,
// held to the limits as a write is, would refuse instead; so the
// Climate uses these setters and not the typed Set. A device without a
// Thermostat server stores nothing.
type thermostatInbound interface {
	SetLocalTemperature(t *int16)
	SetSystemMode(mode uint8)
	SetOccupiedHeatingSetpoint(v int16)
	SetOccupiedCoolingSetpoint(v int16)
}

// noThermostat is the inbound side of a device without a Thermostat
// server: nothing to store.
type noThermostat struct{}

func (noThermostat) SetLocalTemperature(*int16)       {}
func (noThermostat) SetSystemMode(uint8)              {}
func (noThermostat) SetOccupiedHeatingSetpoint(int16) {}
func (noThermostat) SetOccupiedCoolingSetpoint(int16) {}

// thermostatInboundOf returns the inbound side of d's Thermostat server.
func thermostatInboundOf(d *fabricdevice.Device) thermostatInbound {
	if d == nil {
		return noThermostat{}
	}
	if srv, ok := d.Server(thermostatspec.ClusterID).(thermostatInbound); ok {
		return srv
	}
	return noThermostat{}
}

// syncMatterSetpoint stores the observed setpoint on srv — on both
// occupied setpoints when COOL is advertised, since HM has one setpoint
// — and the mode the setpoint implies (the OFF sentinel).
func (c *Climate) syncMatterSetpoint(srv thermostatInbound) {
	sp, observed := c.Setpoint()
	if !observed {
		return
	}
	v := celsiusToMatter(sp)
	srv.SetOccupiedHeatingSetpoint(v)
	if c.matterThermoFeatures()&thermostatspec.FeatureCooling != 0 {
		srv.SetOccupiedCoolingSetpoint(v)
	}
	c.syncMatterModeTo(srv)
}

// syncMatterLocalTemperature stores the observed room temperature on
// srv, null while none was observed.
func (c *Climate) syncMatterLocalTemperature(srv thermostatInbound) {
	t, observed := c.CurrentTemperature()
	if !observed {
		srv.SetLocalTemperature(nil)
		return
	}
	v := celsiusToMatter(t)
	srv.SetLocalTemperature(&v)
}

// syncMatterModeTo stores the device's current mode on srv.
func (c *Climate) syncMatterModeTo(srv thermostatInbound) {
	m, observed := c.Mode()
	if !observed {
		return
	}
	srv.SetSystemMode(c.systemModeFromHmMode(m, c.matterThermoFeatures()))
}

// syncMatterMode stores the device's current mode on the Thermostat
// server, if one was built. The mode recorders call it after they
// released the Climate's lock, so a mode the device took on itself —
// the wall dial, a CCU program, the week program — reaches a subscribed
// controller as a SystemMode report.
func (c *Climate) syncMatterMode() {
	if d := c.matterDevice.Current(); d != nil {
		c.syncMatterModeTo(thermostatInboundOf(d))
	}
}

// systemModeFromHmMode maps the Climate domain Mode onto Matter's
// SystemModeEnum, clamped to the values the FeatureMap fm makes legal:
// Auto(1) has conformance "AUTO" and Cool(3) "[COOL]" — matter.js
// packages/model/src/standard/elements/thermostat-cluster.element.ts:556-566.
// A report must never surface a value the FeatureMap forbids:
// controllers (Apple Home, Google Home) echo the read SystemMode back on
// state sync, and a non-conformant Auto answer turns that echo into the
// CONSTRAINT_ERROR the server's definition raises on writes. HM's AUTO
// (week-program) mode is a single-setpoint schedule, not Matter AutoMode
// (independent dual setpoints + deadband); without the AUTO feature it
// surfaces as Heat — or Cool when the wrapped hybrid device currently
// operates in its COOLING direction (the same HEATING_COOLING signal that
// derives ModeCool for MANU, see [Climate.OnSetPointMode]).
// Profile-overlay states (away, boost) collapse onto their parent mode —
// Matter has no native equivalents.
func (c *Climate) systemModeFromHmMode(m Mode, fm thermostatspec.Feature) uint8 {
	switch m {
	case ModeOff:
		return matterSysModeOff
	case ModeCool:
		// ModeCool only derives on hybrid profiles whose FeatureMap
		// carries COOL; clamp defensively so a heat-only FeatureMap can
		// never leak the "[COOL]"-gated value.
		if fm&thermostatspec.FeatureCooling == 0 {
			return matterSysModeHeat
		}
		return matterSysModeCool
	case ModeAuto:
		if fm&thermostatspec.FeatureAutoMode != 0 {
			return matterSysModeAuto
		}
		if fm&thermostatspec.FeatureCooling != 0 && !c.IsHeating() {
			return matterSysModeCool
		}
		return matterSysModeHeat
	default: // ModeHeat and any future mode: HEAT is always advertised.
		return matterSysModeHeat
	}
}

// climateThermoSink is the [thermostatspec.Sink] of a Climate: the CCU writes a
// controller's change becomes, called by the server after validation
// and before anything is stored; an error refuses the change.
type climateThermoSink struct{ c *Climate }

// SetSystemMode implements [thermostatspec.Sink]: the mode the definition
// admitted for the feature set becomes [Climate.SetMode].
func (s climateThermoSink) SetSystemMode(ctx context.Context, mode uint8) error {
	m, err := matterToHmMode(mode)
	if err != nil {
		return err
	}
	return s.c.SetMode(ctx, m, matterDispatchPriority)
}

// SetSetpoint implements [thermostatspec.Sink]: HM exposes one setpoint per
// Climate, so OccupiedHeatingSetpoint and OccupiedCoolingSetpoint both
// become [Climate.SetTemperature] and the companions the server's
// reconcile would move alongside carry nothing the device could take.
func (s climateThermoSink) SetSetpoint(ctx context.Context, _ uint32, value int16, _ []thermostatspec.SetpointChange) error {
	return s.c.SetTemperature(ctx, matterToCelsius(value), matterDispatchPriority)
}
