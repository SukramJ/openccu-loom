// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package climate

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"sync"

	"github.com/SukramJ/go-fabric/cluster/measurement"
	thermostatspec "github.com/SukramJ/go-fabric/cluster/spec/thermostat"
	"github.com/SukramJ/go-fabric/cluster/thermo"
	"github.com/SukramJ/go-fabric/cluster/wire"

	"github.com/SukramJ/openccu-loom/internal/model/custom"
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
// surface (ADR 0012) as a Matter Thermostat (0x0301) endpoint with two
// cluster servers, both go-fabric's since the ADR 0084 thermostat
// follow-up: Thermostat (0x0201, [thermo.ThermostatServer] with the
// Climate as its Sink) and ThermostatUserInterfaceConfiguration (0x0204,
// [thermo.ThermostatUIServer]). The Schedules cluster (0x0024) is
// intentionally NOT emitted (see [Climate.MatterClusterServers]).
var (
	_ interfaces.MatterEndpointSource     = (*Climate)(nil)
	_ interfaces.MatterClusterDataVersion = (*Climate)(nil)
	_ interfaces.MatterChangeNotifier     = (*Climate)(nil)
	_ thermo.Sink                         = climateThermoSink{}
)

// MatterDataVersion implements [interfaces.MatterClusterDataVersion]:
// the counter the ThermostatUserInterfaceConfiguration server bumps on
// every change it stores. The Thermostat server has no host tracker; the
// bridge hosts its DataVersion on the endpoint and advances it from the
// server's change notifier (go-fabric bridge/subscribe.go
// wireAttributeNotifiersLocked).
func (c *Climate) MatterDataVersion() uint32 { return c.dataVersion.Current() }

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
// definitions behind the servers.
const matterDeviceTypeThermostat uint16 = 0x0301

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

// MatterDeviceType implements [interfaces.MatterEndpointSource]. All
// climate variants — IP, RF, SimpleRF — surface as Matter Thermostat
// (0x0301); the cluster surface inside captures the differences.
func (c *Climate) MatterDeviceType() uint16 { return matterDeviceTypeThermostat }

// MatterClusterServers implements [interfaces.MatterEndpointSource].
// Thermostat + ThermostatUI, and nothing else.
//
// TemperatureMeasurement (0x0402) and RelativeHumidityMeasurement
// (0x0405) are deliberately NOT emitted here. The Device Library names
// both for device type 0x0301 as element=clientCluster (matter.js
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
	return []interfaces.MatterClusterServer{
		c.matterThermostatServer(),
		c.matterThermostatUIServer(),
	}
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

// matterThermoHolder keeps the one Thermostat server a Climate serves
// across [interfaces.MatterEndpointSource.MatterClusterServers] calls,
// for the same reason [generic.MatterOnOffHolder] does: the assembler
// re-reads the servers on every reassembly and the bridge keeps the ones
// it already serves.
type matterThermoHolder struct {
	mu  sync.Mutex
	srv *thermo.ThermostatServer
	ui  *thermo.ThermostatUIServer
}

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
func (c *Climate) matterThermoFeatures() uint32 {
	fm := thermo.ThermostatFeatureHEAT
	if c.Capabilities.SupportsCool {
		fm |= thermo.ThermostatFeatureCOOL
	}
	return fm
}

// matterThermostatServer builds the Climate's Thermostat server once:
// go-fabric's [thermo.ThermostatServer] with the Climate as its Sink —
// a SystemMode write becomes [Climate.SetMode], an occupied-setpoint
// write or a SetpointRaiseLower becomes [Climate.SetTemperature], both at
// [matterDispatchPriority] — and the CCU's confirmed values fed back
// through the inbound setters, which store without the Sink: the
// SET_POINT_TEMPERATURE data point's confirmed-update hook carries the
// setpoint (and the OFF sentinel [Climate.Mode] derives from it), the
// ACTUAL_TEMPERATURE hook the room temperature, and the mode recorders
// ([Climate.OnMode], [Climate.OnSetPointMode], [Climate.OnControlMode],
// [Climate.OnHeatingCooling]) the mode. The absolute setpoint limits are
// the device's own ([Climate.MinTemp] / [Climate.MaxTemp]).
//
// The server starts from the observed setpoint and temperature; before
// the CCU reported a setpoint it starts from go-fabric's 20.00 °C
// default cropped to the limits, which the first confirmed value
// replaces.
func (c *Climate) matterThermostatServer() *thermo.ThermostatServer {
	c.matterThermo.mu.Lock()
	defer c.matterThermo.mu.Unlock()
	if c.matterThermo.srv != nil {
		return c.matterThermo.srv
	}
	fm := c.matterThermoFeatures()
	cfg := thermo.DefaultThermostatConfig()
	cfg.Features = fm
	minSP, maxSP := celsiusToMatter(c.MinTemp()), celsiusToMatter(c.MaxTemp())
	if minSP >= maxSP {
		// No descriptor bounds and no capability bounds (a Climate
		// outside the profile catalogue): go-fabric's defaults keep the
		// server's limits well-formed instead of pinning every setpoint
		// to one value.
		minSP, maxSP = cfg.AbsMinHeatSetpointLimit, cfg.AbsMaxHeatSetpointLimit
	}
	cfg.Sink = climateThermoSink{c: c}
	cfg.AbsMinHeatSetpointLimit, cfg.AbsMaxHeatSetpointLimit = minSP, maxSP
	cfg.AbsMinCoolSetpointLimit, cfg.AbsMaxCoolSetpointLimit = minSP, maxSP
	initial := min(max(cfg.InitialHeatingSetpoint, minSP), maxSP)
	if sp, observed := c.Setpoint(); observed {
		initial = min(max(celsiusToMatter(sp), minSP), maxSP)
	}
	cfg.InitialHeatingSetpoint, cfg.InitialCoolingSetpoint = initial, initial
	srv := thermo.NewThermostatServer(cfg)
	c.matterThermo.srv = srv

	if c.setpoint != nil {
		_ = c.setpoint.OnConfirmedUpdate(func(_, _ float64) { c.syncMatterSetpoint(srv) })
	}
	if c.actualTemperature != nil {
		_ = c.actualTemperature.OnConfirmedUpdate(func(_, _ float64) { c.syncMatterLocalTemperature(srv) })
	}
	// A confirmation that landed between the initial read and the hook
	// registration is not lost: the current values are stored again,
	// which is a no-op when nothing changed.
	c.syncMatterSetpoint(srv)
	c.syncMatterLocalTemperature(srv)
	c.syncMatterModeTo(srv)
	return srv
}

// matterThermostatUIServer builds the
// ThermostatUserInterfaceConfiguration server once: matter.js's
// defaults (Celsius, no keypad lockout); a controller's writes are
// stored in process, as in matter.js, since HM devices expose neither
// display unit nor keypad lockout on the wire.
func (c *Climate) matterThermostatUIServer() *thermo.ThermostatUIServer {
	c.matterThermo.mu.Lock()
	defer c.matterThermo.mu.Unlock()
	if c.matterThermo.ui == nil {
		display := thermo.TemperatureDisplayCelsius
		ui, err := thermo.NewThermostatUserInterfaceConfiguration(thermo.ThermostatUIConfig{
			TemperatureDisplayMode: &display,
			DataVersion:            &c.dataVersion,
		})
		if err != nil {
			// invariant: the configuration is constant; a refusal is a
			// defect in the generated definition, not a runtime
			// condition.
			panic(err)
		}
		c.matterThermo.ui = ui
	}
	return c.matterThermo.ui
}

// syncMatterSetpoint stores the observed setpoint on srv — on both
// occupied setpoints when COOL is advertised, since HM has one setpoint
// — and the mode the setpoint implies (the OFF sentinel).
func (c *Climate) syncMatterSetpoint(srv *thermo.ThermostatServer) {
	sp, observed := c.Setpoint()
	if !observed {
		return
	}
	v := celsiusToMatter(sp)
	srv.SetOccupiedHeatingSetpoint(v)
	if c.matterThermoFeatures()&thermo.ThermostatFeatureCOOL != 0 {
		srv.SetOccupiedCoolingSetpoint(v)
	}
	c.syncMatterModeTo(srv)
}

// syncMatterLocalTemperature stores the observed room temperature on
// srv, null while none was observed.
func (c *Climate) syncMatterLocalTemperature(srv *thermo.ThermostatServer) {
	t, observed := c.CurrentTemperature()
	if !observed {
		srv.SetLocalTemperature(nil)
		return
	}
	v := celsiusToMatter(t)
	srv.SetLocalTemperature(&v)
}

// syncMatterModeTo stores the device's current mode on srv.
func (c *Climate) syncMatterModeTo(srv *thermo.ThermostatServer) {
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
	c.matterThermo.mu.Lock()
	srv := c.matterThermo.srv
	c.matterThermo.mu.Unlock()
	if srv != nil {
		c.syncMatterModeTo(srv)
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
func (c *Climate) systemModeFromHmMode(m Mode, fm uint32) uint8 {
	switch m {
	case ModeOff:
		return matterSysModeOff
	case ModeCool:
		// ModeCool only derives on hybrid profiles whose FeatureMap
		// carries COOL; clamp defensively so a heat-only FeatureMap can
		// never leak the "[COOL]"-gated value.
		if fm&thermo.ThermostatFeatureCOOL == 0 {
			return matterSysModeHeat
		}
		return matterSysModeCool
	case ModeAuto:
		if fm&thermo.ThermostatFeatureAUTO != 0 {
			return matterSysModeAuto
		}
		if fm&thermo.ThermostatFeatureCOOL != 0 && !c.IsHeating() {
			return matterSysModeCool
		}
		return matterSysModeHeat
	default: // ModeHeat and any future mode: HEAT is always advertised.
		return matterSysModeHeat
	}
}

// climateThermoSink is the [thermo.Sink] of a Climate: the CCU writes a
// controller's change becomes, called by the server after validation
// and before anything is stored; an error refuses the change.
type climateThermoSink struct{ c *Climate }

// SetSystemMode implements [thermo.Sink]: the mode the definition
// admitted for the feature set becomes [Climate.SetMode].
func (s climateThermoSink) SetSystemMode(ctx context.Context, mode uint8) error {
	m, err := matterToHmMode(mode)
	if err != nil {
		return err
	}
	return s.c.SetMode(ctx, m, matterDispatchPriority)
}

// SetSetpoint implements [thermo.Sink]: HM exposes one setpoint per
// Climate, so OccupiedHeatingSetpoint and OccupiedCoolingSetpoint both
// become [Climate.SetTemperature] and the companions the server's
// reconcile would move alongside carry nothing the device could take.
func (s climateThermoSink) SetSetpoint(ctx context.Context, _ uint32, value int16, _ []thermo.SetpointChange) error {
	return s.c.SetTemperature(ctx, matterToCelsius(value), matterDispatchPriority)
}
