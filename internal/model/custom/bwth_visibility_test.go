// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package custom

import (
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// TestIPThermostatChannel9StateIsVisibleOnHmIPBWTH pins the parity fix for
// the HmIP-BWTH symptom: channel 9 STATE (the switch output) must be marked
// CDPVisible by the materializer when an `IPThermostat` profile attaches to
// channel 1.
//
// Without this rebase chain the wire-level switch DP on ch9 would stay at its
// default DataPointUsage and never surface in HA-Discovery as a visible
// switch.
func TestIPThermostatChannel9StateIsVisibleOnHmIPBWTH(t *testing.T) {
	dev := newHmIPBwthDevice()
	// Add channel 9 (the switch output) — newHmIPBwthDevice only goes
	// up to ch8 because the previous tests didn't need ch9.
	dev.AddChannel("0001D7:9", 9, "SWITCH_VIRTUAL_RECEIVER", hmenum.ParamsetKeyValues)
	ch9 := dev.Channel("0001D7:9")
	state := putBoolDP(ch9, hmenum.ParameterState)

	registry := NewRegistry()
	// Profile mirrors the generated IPThermostat shape but only with
	// the fields the assertion needs: PrimaryChannel=0,
	// channel_fields[8] = Visible(STATE). Base channel is 1 (the
	// CCU-reported primary BWTH thermostat channel).
	profile := Profile{
		Name:       "IPThermostat",
		DeviceType: "hmip-bwth",
		Category:   hmenum.DataPointCategoryClimate,
		Channels:   []ChannelRoleAssignment{{Channel: 1, Role: ChannelRolePrimary}},
		Config: &ProfileConfig{
			ProfileType: "IPThermostat",
			ChannelGroup: ChannelGroupConfig{
				PrimaryChannel:    0,
				PrimaryChannelSet: true,
				ChannelFields: map[int]map[hmenum.Field]FieldValue{
					// Relative offset 8; rebase with group_no=1 → absolute 9.
					8: {hmenum.FieldState: Visible(hmenum.ParameterState)},
				},
			},
		},
	}
	if err := registry.Register(profile); err != nil {
		t.Fatal(err)
	}
	ctor, _ := fakeCtor("IPThermostat")
	if err := registry.RegisterConstructor("IPThermostat", ctor); err != nil {
		t.Fatal(err)
	}

	if err := CreateCustomDataPoints(dev, registry); err != nil {
		t.Fatalf("CreateCustomDataPoints: %v", err)
	}

	got, ok := state.ForcedUsage()
	if !ok {
		t.Fatal("BWTH ch9 STATE should have been force-marked CDPVisible by IP_THERMOSTAT visibility rebase")
	}
	if got != hmenum.DataPointUsageCDPVisible {
		t.Fatalf("BWTH ch9 STATE forced usage = %q, want CDPVisible", got)
	}
	if !state.Visible() {
		t.Fatal("BWTH ch9 STATE must be Visible() after the materializer ran")
	}
}

// ---------------------------------------------------------------------------
// Category B: applyFieldValueToChannel must not promote a force-sensor DP
// to CDPVisible even when the profile field says Visible(LEVEL).
// Regression test for parity drift on HmIP-eTRV LEVEL.
// ---------------------------------------------------------------------------

// putFloatDP adds a float-typed VALUES parameter to the channel — used
// to represent LEVEL on thermostat channels.
func putFloatDP(ch *device.Channel, param hmenum.Parameter) *generic.Sensor[float64] {
	cfg := generic.Spec{
		Key: hmtypes.DataPointKey{
			InterfaceID:    "iface",
			ChannelAddress: ch.Address,
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(param),
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeFloat,
			Operations: hmenum.OperationsRead | hmenum.OperationsWrite | hmenum.OperationsEvent,
		},
	}
	dp := generic.NewSensor[float64](cfg)
	ch.Put(dp)
	return dp
}

// TestApplyFieldVisibilitySkipsCDPVisibleOnForceSensorParam verifies that// when a profile field is Visible(LEVEL) and the parameter is in the
// _SWITCH_DP_TO_SENSOR table for the device model (HmIP-eTRV), the
// materializer must NOT promote LEVEL to CDPVisible.
//
// Python behaviour: `_add_data_point` in data_point.py:243 has the guard
// `if is_visible is True and data_point.is_forced_sensor is False:` —
// forced-sensor DPs are skipped. Without this guard openccu-loom's snapshot
// shows `forced_usage=ce_visible` while Python shows no forced_usage at all.
//
// Because `ApplyForceSensorMarks` runs AFTER the custom-DP materialisation
// pass, the fix uses the static `IsForceSensorParameter(model, param)` table
// lookup rather than the instance-method `IsForcedSensor()`.
func TestApplyFieldVisibilitySkipsCDPVisibleOnForceSensorParam(t *testing.T) {
	dev := device.New(device.Config{
		InterfaceID:  "HmIP-RF",
		Interface:    hmenum.InterfaceHmIPRF,
		Address:      "ETRV001",
		Model:        "HmIP-eTRV",
		ProductGroup: hmenum.ProductGroupHmIP,
	})
	ch := dev.AddChannel("ETRV001:1", 1, "HEATING_CLIMATECONTROL_RECEIVER", hmenum.ParamsetKeyValues)
	levelDP := putFloatDP(ch, hmenum.ParameterLevel)

	registry := NewRegistry()
	// Profile mirrors the IPThermostat shape for HmIP-eTRV:
	// Fields: {FieldLevel: Visible(ParameterLevel)}.
	profile := Profile{
		Name:       "IPThermostat",
		DeviceType: "hmip-etrv",
		Category:   hmenum.DataPointCategoryClimate,
		Channels:   []ChannelRoleAssignment{{Channel: 1, Role: ChannelRolePrimary}},
		Config: &ProfileConfig{
			ProfileType: "IPThermostat",
			ChannelGroup: ChannelGroupConfig{
				PrimaryChannel:    0,
				PrimaryChannelSet: true,
				Fields: map[hmenum.Field]FieldValue{
					hmenum.FieldLevel: Visible(hmenum.ParameterLevel),
				},
			},
		},
	}
	if err := registry.Register(profile); err != nil {
		t.Fatal(err)
	}
	ctor, _ := fakeCtor("IPThermostat")
	if err := registry.RegisterConstructor("IPThermostat", ctor); err != nil {
		t.Fatal(err)
	}

	if err := CreateCustomDataPoints(dev, registry); err != nil {
		t.Fatalf("CreateCustomDataPoints: %v", err)
	}

	// LEVEL must NOT have received CDPVisible because it is a force-sensor
	// parameter on HmIP-eTRV. The forced_usage must remain unset.
	if u, set := levelDP.ForcedUsage(); set {
		t.Errorf("HmIP-eTRV LEVEL ForcedUsage = %q (set=%v); want unset (force-sensor must skip CDPVisible)", u, set)
	}
}

// TestApplyFieldVisibilityStillAppliesCDPVisibleOnNonForceSensorParam verifies
// that the force-sensor guard does NOT prevent CDPVisible from being applied
// to parameters that are NOT in the _SWITCH_DP_TO_SENSOR table.
// This is the complement of : STATE on a switch channel IS promoted.
func TestApplyFieldVisibilityStillAppliesCDPVisibleOnNonForceSensorParam(t *testing.T) {
	dev := newHmIPBwthDevice()
	dev.AddChannel("0001D7:9", 9, "SWITCH_VIRTUAL_RECEIVER", hmenum.ParamsetKeyValues)
	ch9 := dev.Channel("0001D7:9")
	state := putBoolDP(ch9, hmenum.ParameterState)

	registry := NewRegistry()
	profile := Profile{
		Name:       "IPThermostat",
		DeviceType: "hmip-bwth",
		Category:   hmenum.DataPointCategoryClimate,
		Channels:   []ChannelRoleAssignment{{Channel: 1, Role: ChannelRolePrimary}},
		Config: &ProfileConfig{
			ProfileType: "IPThermostat",
			ChannelGroup: ChannelGroupConfig{
				PrimaryChannel:    0,
				PrimaryChannelSet: true,
				ChannelFields: map[int]map[hmenum.Field]FieldValue{
					8: {hmenum.FieldState: Visible(hmenum.ParameterState)},
				},
			},
		},
	}
	if err := registry.Register(profile); err != nil {
		t.Fatal(err)
	}
	ctor, _ := fakeCtor("IPThermostat")
	if err := registry.RegisterConstructor("IPThermostat", ctor); err != nil {
		t.Fatal(err)
	}
	if err := CreateCustomDataPoints(dev, registry); err != nil {
		t.Fatalf("CreateCustomDataPoints: %v", err)
	}

	// STATE on HmIP-BWTH ch9 is NOT a force-sensor parameter → CDPVisible must apply.
	if got, ok := state.ForcedUsage(); !ok || got != hmenum.DataPointUsageCDPVisible {
		t.Errorf("HmIP-BWTH ch9 STATE ForcedUsage = %q (set=%v); want CDPVisible", got, ok)
	}
}
