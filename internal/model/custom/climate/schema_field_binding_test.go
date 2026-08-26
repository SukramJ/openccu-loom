// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package climate

import (
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/custom"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// putStringSensor installs a STRING/ENUM-shaped VALUES sensor DP on ch — used
// where the test wants to feed an already-resolved ENUM label directly
// (mirroring the firmwares that spell HEATING_COOLING out instead of
// pushing a VALUE_LIST index).
func putStringSensor(ch *device.Channel, param hmenum.Parameter) *generic.Sensor[string] {
	dp := generic.NewSensor[string](generic.Spec{
		Key: hmtypes.DataPointKey{
			ChannelAddress: ch.Address,
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(param),
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeEnum,
			Operations: hmenum.OperationsRead | hmenum.OperationsEvent,
		},
	})
	ch.Put(dp)
	return dp
}

// ipThermostatFixture is the set of wire DPs a materialised HmIP-BWTH
// carries for the fields this slice covers, keyed by the accessor tests
// need to feed them.
type ipThermostatFixture struct {
	climate, relay                   *device.Channel
	setPointMode, activeProfile      *generic.Sensor[int32]
	boostMode, partyMode, relayState *generic.BinarySensor
	heatingCooling                   *generic.Sensor[string]
	level, concentration             *generic.Sensor[float64]
}

// newIPThermostatFixture builds a minimal HmIP-BWTH the way the real
// IPThermostat profile schema sees it, materialised through the real
// registry: channel numbers, group rebasing (addChannelGroupsToDevice) and
// constructor wiring all come from production code, not a hand-picked
// shortcut. The climate channel (1) carries the mode/profile VALUES
// parameters the schema's `Fields` map declares (own-channel, offset 0);
// channel 9 carries the relay STATE the schema maps at channel offset 8
// ([ChannelFields][8]).
func newIPThermostatFixture(t *testing.T) ipThermostatFixture {
	t.Helper()
	dev := device.New(device.Config{InterfaceID: "HmIP-RF", Address: "000B1709AF4FE1", Model: "HmIP-BWTH"})
	climateCh := dev.AddChannel("000B1709AF4FE1:1", 1, "HEATING_CLIMATECONTROL_TRANSCEIVER", hmenum.ParamsetKeyValues)
	f := ipThermostatFixture{
		climate:        climateCh,
		setPointMode:   putIntSensor(climateCh, hmenum.ParameterSetPointMode),
		boostMode:      putBoolSensor(climateCh, hmenum.ParameterBoostMode),
		activeProfile:  putIntSensor(climateCh, hmenum.ParameterActiveProfile),
		heatingCooling: putStringSensor(climateCh, hmenum.ParameterHeatingCooling),
		level:          putFloatSensor(climateCh, hmenum.ParameterLevel),
		partyMode:      putBoolSensor(climateCh, hmenum.ParameterPartyMode),
		concentration:  putFloatSensor(climateCh, hmenum.ParameterConcentration),
	}
	f.relay = dev.AddChannel("000B1709AF4FE1:9", 9, "SWITCH_VIRTUAL_RECEIVER", hmenum.ParamsetKeyValues)
	f.relayState = putBoolSensor(f.relay, hmenum.ParameterState)

	if err := custom.CreateCustomDataPoints(dev, custom.DefaultRegistry()); err != nil {
		t.Fatalf("materialize custom data points: %v", err)
	}
	return f
}

// climateOnCh returns the Climate custom DP attached to ch, failing the
// test if none is attached or the type does not match.
func climateOnCh(t *testing.T, ch *device.Channel) *Climate {
	t.Helper()
	cdp := ch.CustomDataPoint()
	c, ok := cdp.(*Climate)
	if !ok {
		t.Fatalf("custom data point on %s is %T, want *Climate", ch.Address, cdp)
	}
	return c
}

// TestIPThermostatBWTHModeProfileFieldsBindThroughSubscribe covers the
// unbound-schema-fields candidates the guard found for the fields the
// profile's `Fields` map (own channel, no cross-channel offset) declares:
// SET_POINT_MODE, BOOST_MODE, ACTIVE_PROFILE. Each reaches its consumer
// through the Subscribe wiring rather than a stored pointer — the
// reflection walk cannot see it, but the wire value drives Mode()/
// Profile() end to end through the real profile registry.
func TestIPThermostatBWTHModeProfileFieldsBindThroughSubscribe(t *testing.T) {
	t.Parallel()
	f := newIPThermostatFixture(t)
	c := climateOnCh(t, f.climate)

	f.setPointMode.OnEvent(0) // AUTO
	if got, ok := c.Mode(); !ok || got != ModeAuto {
		t.Fatalf("Mode() = (%v, %v), want (auto, true) after SET_POINT_MODE=0", got, ok)
	}

	f.activeProfile.OnEvent(2)
	if got, ok := c.Profile(); !ok || got != ProfileWeekProgram2 {
		t.Errorf("Profile() = (%v, %v), want (week_program_2, true) after ACTIVE_PROFILE=2 in AUTO", got, ok)
	}

	f.boostMode.OnEvent(true)
	if got, ok := c.Profile(); !ok || got != ProfileBoost {
		t.Errorf("Profile() = (%v, %v), want (boost, true) after BOOST_MODE=true", got, ok)
	}

	f.setPointMode.OnEvent(1) // MANU
	if got, ok := c.Mode(); !ok || got != ModeHeat {
		t.Errorf("Mode() = (%v, %v), want (heat, true) after SET_POINT_MODE=1", got, ok)
	}
}

// TestIPThermostatBWTHHeatingCoolingAndLevelBindThroughSubscribe covers
// HEATING_COOLING (own channel, `Fields` map) and LEVEL (own channel,
// `ChannelFields[0]`, which rebases to the same channel as `ch` itself):
// both drive Activity() through the Subscribe wiring.
func TestIPThermostatBWTHHeatingCoolingAndLevelBindThroughSubscribe(t *testing.T) {
	t.Parallel()
	f := newIPThermostatFixture(t)
	c := climateOnCh(t, f.climate)

	f.heatingCooling.OnEvent("COOLING")
	f.level.OnEvent(0.5)
	if got, observed := c.Activity(); !observed || got != ActivityCooling {
		t.Errorf("Activity() = (%v, %v), want (cooling, true) after HEATING_COOLING=COOLING + LEVEL=0.5", got, observed)
	}
}

// TestIPThermostatBWTHRelayStateBindsThroughActivityStateChannels covers
// the profile-mapped STATE channel (offset 8 → absolute channel 9 for a
// climate channel rooted at 1): Config.ActivityStateChannels, resolved
// from the rebased schema at construction time, is what lets Subscribe
// reach a parameter that lives on a sibling channel instead of the
// custom DP's own.
func TestIPThermostatBWTHRelayStateBindsThroughActivityStateChannels(t *testing.T) {
	t.Parallel()
	f := newIPThermostatFixture(t)
	c := climateOnCh(t, f.climate)

	f.level.OnEvent(0) // idle from the climate channel's own LEVEL
	f.relayState.OnEvent(true)
	if got, observed := c.Activity(); !observed || got != ActivityHeating {
		t.Errorf("Activity() = (%v, %v), want (heating, true) after relay ch9 STATE=true", got, observed)
	}
}

// TestIPThermostatConcentrationReachesIndependentSensorDataPoint covers
// the CONCENTRATION field: the IPThermostat schema maps it `Visible` on
// the custom DP's own channel, but — matching the Python reference
// (model/custom/climate.py declares no DataPointField for it at all;
// model/custom/profile.py:785 only marks it visible) — Climate holds no
// pointer to it on purpose. The value still reaches its consumer: the
// materializer force-promotes the underlying generic data point to
// CDP_VISIBLE independent of custom-DP composition
// (internal/model/custom/materialize.go's applyFieldValueToChannel), so
// it surfaces as its own CO2/air-quality sensor entity.
func TestIPThermostatConcentrationReachesIndependentSensorDataPoint(t *testing.T) {
	t.Parallel()
	f := newIPThermostatFixture(t)

	forced, ok := f.concentration.ForcedUsage()
	if !ok || forced != hmenum.DataPointUsageCDPVisible {
		t.Errorf("CONCENTRATION ForcedUsage() = (%v, %v), want (CDPVisible, true) — "+
			"the schema's Visible() marking must promote it independent of Climate's own composition", forced, ok)
	}
}

// TestIPThermostatPartyModeBindsThroughSubscribe pins the one field of
// this slice that was a genuine defect: PARTY_MODE (own channel, `Fields`
// map) was never subscribed at all — no OnPartyMode, no accessor — so the
// device-carried, schema-resolved parameter reached no consumer. Fixed by
// wiring it through Subscribe like OPTIMUM_START_STOP.
func TestIPThermostatPartyModeBindsThroughSubscribe(t *testing.T) {
	t.Parallel()
	f := newIPThermostatFixture(t)
	c := climateOnCh(t, f.climate)

	if _, ok := c.PartyMode(); ok {
		t.Fatal("PartyMode() must be unobserved before any wire event")
	}

	f.partyMode.OnEvent(true)

	got, ok := c.PartyMode()
	if !ok || !got {
		t.Errorf("PartyMode() = (%v, %v), want (true, true) after PARTY_MODE=true", got, ok)
	}
}

// newRfThermostatFixture builds a minimal HM-CC-RT-DN through the real
// registry. Both fields this slice covers for RfThermostat — CONTROL_MODE
// (`Fields` map, own channel) and VALVE_STATE (`ChannelFields[0]`, which
// rebases to the same channel 4 the profile registers as primary) — live
// on the thermostat's own channel, so no cross-channel resolution is
// exercised here; the point is that Subscribe reaches them at all.
func newRfThermostatFixture(t *testing.T) (ch *device.Channel, controlMode *generic.Sensor[string], valveState *generic.Sensor[float64]) {
	t.Helper()
	dev := device.New(device.Config{InterfaceID: "BidCos-RF", Address: "MEQ0111222", Model: "HM-CC-RT-DN"})
	ch = dev.AddChannel("MEQ0111222:4", 4, "CLIMATECONTROL_RT_TRANSCEIVER", hmenum.ParamsetKeyValues)
	controlMode = putStringSensor(ch, hmenum.ParameterControlMode)
	valveState = putFloatSensor(ch, hmenum.ParameterValveState)

	if err := custom.CreateCustomDataPoints(dev, custom.DefaultRegistry()); err != nil {
		t.Fatalf("materialize custom data points: %v", err)
	}
	return ch, controlMode, valveState
}

// TestRfThermostatControlModeAndValveStateBindThroughSubscribe covers
// RfThermostat.control_mode and RfThermostat.valve_state.
func TestRfThermostatControlModeAndValveStateBindThroughSubscribe(t *testing.T) {
	t.Parallel()
	ch, controlMode, valveState := newRfThermostatFixture(t)
	c := climateOnCh(t, ch)

	controlMode.OnEvent("MANU-MODE")
	if got, ok := c.Mode(); !ok || got != ModeHeat {
		t.Fatalf("Mode() = (%v, %v), want (heat, true) after CONTROL_MODE=MANU-MODE", got, ok)
	}

	controlMode.OnEvent("AUTO-MODE")
	valveState.OnEvent(42)
	if got, observed := c.Activity(); !observed || got != ActivityHeating {
		t.Errorf("Activity() = (%v, %v), want (heating, true) after VALVE_STATE=42", got, observed)
	}
}

// TestRfThermostatGroupControlModeBindsThroughSubscribe covers
// RfThermostatGroup.control_mode — same `Fields` map, own-channel
// resolution as RfThermostat, on the HM-CC-VG-1 heating-group profile.
func TestRfThermostatGroupControlModeBindsThroughSubscribe(t *testing.T) {
	t.Parallel()
	dev := device.New(device.Config{InterfaceID: "BidCos-RF", Address: "MEQ0333444", Model: "HM-CC-VG-1"})
	ch := dev.AddChannel("MEQ0333444:1", 1, "CLIMATECONTROL_VG_TRANSCEIVER", hmenum.ParamsetKeyValues)
	controlMode := putStringSensor(ch, hmenum.ParameterControlMode)

	if err := custom.CreateCustomDataPoints(dev, custom.DefaultRegistry()); err != nil {
		t.Fatalf("materialize custom data points: %v", err)
	}
	c := climateOnCh(t, ch)

	controlMode.OnEvent("BOOST-MODE")
	if got, ok := c.Profile(); !ok || got != ProfileBoost {
		t.Errorf("Profile() = (%v, %v), want (boost, true) after CONTROL_MODE=BOOST-MODE", got, ok)
	}
}

// newIPThermostatGroupFixture builds a minimal HmIP-HEATING heating-group
// device through the real registry. LEVEL rebases to the group's own
// channel (`ChannelFields[0]`, primary channel 1); STATE rebases to
// channel 4 (`ChannelFields[3]`), reached through
// Config.ActivityStateChannels exactly like the HmIP-BWTH relay channel.
func newIPThermostatGroupFixture(t *testing.T) (climateCh, stateCh *device.Channel, level *generic.Sensor[float64], partyMode, state *generic.BinarySensor) {
	t.Helper()
	dev := device.New(device.Config{InterfaceID: "HmIP-RF", Address: "0002AB1709AF01", Model: "HmIP-HEATING"})
	climateCh = dev.AddChannel("0002AB1709AF01:1", 1, "HEATING_THERMOSTAT_CHANNEL", hmenum.ParamsetKeyValues)
	level = putFloatSensor(climateCh, hmenum.ParameterLevel)
	partyMode = putBoolSensor(climateCh, hmenum.ParameterPartyMode)
	stateCh = dev.AddChannel("0002AB1709AF01:4", 4, "SWITCH_VIRTUAL_RECEIVER", hmenum.ParamsetKeyValues)
	state = putBoolSensor(stateCh, hmenum.ParameterState)

	if err := custom.CreateCustomDataPoints(dev, custom.DefaultRegistry()); err != nil {
		t.Fatalf("materialize custom data points: %v", err)
	}
	return climateCh, stateCh, level, partyMode, state
}

// TestIPThermostatGroupLevelStateAndPartyModeBindThroughSubscribe covers
// IPThermostatGroup.level, IPThermostatGroup.state (the group's own
// cross-channel case, offset 3 → absolute channel 4) and
// IPThermostatGroup.party_mode (the group side of the fix).
func TestIPThermostatGroupLevelStateAndPartyModeBindThroughSubscribe(t *testing.T) {
	t.Parallel()
	climateCh, _, level, partyMode, state := newIPThermostatGroupFixture(t)
	c := climateOnCh(t, climateCh)

	level.OnEvent(0)
	state.OnEvent(true)
	if got, observed := c.Activity(); !observed || got != ActivityHeating {
		t.Errorf("Activity() = (%v, %v), want (heating, true) after ch4 STATE=true", got, observed)
	}

	partyMode.OnEvent(true)
	if got, ok := c.PartyMode(); !ok || !got {
		t.Errorf("PartyMode() = (%v, %v), want (true, true) after PARTY_MODE=true", got, ok)
	}
}
