// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package custom

import (
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// TestSuppressUndefinedGenericDataPointsHmIPBWTHCh10To12 pins the HmIP-BWTH
// ch10-12 fix. The IP_THERMOSTAT profile (rebased with group_no=1) marks ch9
// STATE Visible — ch10/11/12 carry STATE too but receive no Visible mark, so
// the suppression pass force-marks them NoCreate.
func TestSuppressUndefinedGenericDataPointsHmIPBWTHCh10To12(t *testing.T) {
	dev := newHmIPBwthDevice()
	for _, n := range []int{9, 10, 11, 12} {
		dev.AddChannel("0001D7:"+itoaSmall(n), n, "SWITCH_VIRTUAL_RECEIVER", hmenum.ParamsetKeyValues)
	}
	stateCh9 := putBoolDP(dev.Channel("0001D7:9"), hmenum.ParameterState)
	stateCh10 := putBoolDP(dev.Channel("0001D7:10"), hmenum.ParameterState)
	stateCh11 := putBoolDP(dev.Channel("0001D7:11"), hmenum.ParameterState)
	stateCh12 := putBoolDP(dev.Channel("0001D7:12"), hmenum.ParameterState)

	registry := NewRegistry()
	profile := Profile{
		Name:       "IPThermostat",
		DeviceType: "hmip-bwth",
		Category:   hmenum.DataPointCategoryClimate,
		Channels:   []ChannelRoleAssignment{{Channel: 1, Role: ChannelRolePrimary}},
		Config: &ProfileConfig{
			ProfileType: "IPThermostat",
			ChannelGroup: ChannelGroupConfig{
				PrimaryChannel:                  0,
				PrimaryChannelSet:               true,
				AllowUndefinedGenericDataPoints: false,
				ChannelFields: map[int]map[hmenum.Field]FieldValue{
					8: {hmenum.FieldState: Visible(hmenum.ParameterState)},
				},
			},
			IncludeDefaultDataPoints: false, // not relevant for this assertion
		},
	}
	if err := registry.Register(profile); err != nil {
		t.Fatal(err)
	}
	ctor, _ := fakeCtor("IPThermostat")
	if err := registry.RegisterConstructor("IPThermostat", ctor); err != nil {
		t.Fatal(err)
	}

	// Materialise sets ch9 STATE Visible and attaches the custom DP.
	if err := CreateCustomDataPoints(dev, registry); err != nil {
		t.Fatalf("CreateCustomDataPoints: %v", err)
	}

	// At this point ch10/11/12 STATE have no forced usage.
	for _, dp := range []*generic.Switch{stateCh10, stateCh11, stateCh12} {
		if _, set := dp.ForcedUsage(); set {
			t.Fatalf("ch10/11/12 STATE prematurely marked: %v", dp)
		}
	}

	// Run the suppression pass against the local test registry so
	// `deviceAllowsUndefinedDPs` finds the IPThermostat profile.
	SuppressUndefinedGenericDataPointsWith(dev, registry)

	// ch9 STATE keeps its CDPVisible mark (Visible() from profile).
	if got, ok := stateCh9.ForcedUsage(); !ok || got != hmenum.DataPointUsageCDPVisible {
		t.Errorf("ch9 STATE forced usage = %q (set=%v); want CDPVisible (set=true)", got, ok)
	}

	// ch10/11/12 STATE are now NoCreate.
	for i, dp := range []*generic.Switch{stateCh10, stateCh11, stateCh12} {
		got, set := dp.ForcedUsage()
		if !set {
			t.Errorf("ch1%d STATE not suppressed; expected NoCreate", i)
			continue
		}
		if got != hmenum.DataPointUsageNoCreate {
			t.Errorf("ch1%d STATE usage = %q, want NoCreate", i, got)
		}
		if dp.Visible() {
			t.Errorf("ch1%d STATE must report Visible() == false after suppression", i)
		}
	}
}

// TestSuppressUndefinedGenericDataPointsAllowUndefinedSkips guards
// the opposite case: when every attached custom DP's profile has
// `AllowUndefinedGenericDataPoints=true`, the suppression must be a
// no-op so generic-only diagnostic flags survive.
func TestSuppressUndefinedGenericDataPointsAllowUndefinedSkips(t *testing.T) {
	dev := newHmIPBwthDevice()
	dev.AddChannel("0001D7:9", 9, "SWITCH_VIRTUAL_RECEIVER", hmenum.ParamsetKeyValues)
	stateCh9 := putBoolDP(dev.Channel("0001D7:9"), hmenum.ParameterState)

	registry := NewRegistry()
	profile := Profile{
		Name:       "IPThermostat",
		DeviceType: "hmip-bwth",
		Category:   hmenum.DataPointCategoryClimate,
		Channels:   []ChannelRoleAssignment{{Channel: 1, Role: ChannelRolePrimary}},
		Config: &ProfileConfig{
			ProfileType: "IPThermostat",
			ChannelGroup: ChannelGroupConfig{
				PrimaryChannel:                  0,
				PrimaryChannelSet:               true,
				AllowUndefinedGenericDataPoints: true, // permissive
			},
			IncludeDefaultDataPoints: false,
		},
	}
	_ = registry.Register(profile)
	ctor, _ := fakeCtor("IPThermostat")
	_ = registry.RegisterConstructor("IPThermostat", ctor)

	_ = CreateCustomDataPoints(dev, registry)
	SuppressUndefinedGenericDataPointsWith(dev, registry)

	if _, set := stateCh9.ForcedUsage(); set {
		t.Errorf("ch9 STATE must not be touched when AllowUndefined=true")
	}
}

// TestSuppressUndefinedGenericDataPointsNoCustomIsNoop pins that
// devices without any attached custom DP (pure-generic models) are
// left alone — no surprise NoCreate stamps.
func TestSuppressUndefinedGenericDataPointsNoCustomIsNoop(t *testing.T) {
	dev := newHmIPBwthDevice()
	dev.AddChannel("0001D7:9", 9, "SWITCH_VIRTUAL_RECEIVER", hmenum.ParamsetKeyValues)
	stateCh9 := putBoolDP(dev.Channel("0001D7:9"), hmenum.ParameterState)

	SuppressUndefinedGenericDataPoints(dev)

	if _, set := stateCh9.ForcedUsage(); set {
		t.Errorf("STATE on a pure-generic device must not be force-marked by the suppression pass")
	}
}
