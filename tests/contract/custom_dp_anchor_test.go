// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package contract

import (
	"context"
	"encoding/json"
	"strconv"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/custom"
	// Side-effect import: register every built-in custom-DP
	// constructor against the DefaultRegistry so the materializer
	// has a non-empty constructor catalogue.
	_ "github.com/SukramJ/openccu-loom/internal/model/custom/builtins"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// =====================================================================
// Anchor contract tests for custom-DP materialisation (Wave-X E.15).
//
// Each test pins down the end-to-end behaviour for a representative
// real-world device:
//
// - HmIP-BWTH — HmIP wall thermostat with button lock
// - HmIP-eTRV — HmIP radiator thermostat (also has button lock)
// - HmIP-WGT — HmIP wall device with thermostat + dimmer + lock + switch
// - HmIP-BROLL — HmIP roller shutter (cover)
// - HmIP-PS — HmIP plug switch (anchor for the IPSwitch family;
// the originally-requested HM-LC-Sw1-Pl has no
// generated profile, see test comment)
//
// The tests construct a faithful in-memory device with the exact
// channel layout the CCU exposes and the generic data points the CCU
// paramset-description hydration would create. They then call
// custom.CreateCustomDataPoints against the process-wide
// DefaultRegistry (populated by the [builtins] blank import) and
// verify which channels carry custom DPs and how the materializer
// flipped per-parameter ForcedUsage.
//
// Anchors are deliberately black-box: they do not assert against the
// internal generated_profile_configs.go directly so that profile
// regeneration can adjust internal layout without breaking these
// tests as long as the visible channel-attachment behaviour is
// preserved.
// =====================================================================

// testWriter is a minimal generic.Writer for the anchor fixtures. It
// never actually contacts a backend — every helper here is read-only
// against the live materializer state.
type testWriter struct{}

func (testWriter) SetValue(
	_ context.Context,
	_ string,
	_ hmenum.Parameter,
	_ any,
	_ hmenum.CommandPriority,
) error {
	return nil
}

// =====================================================================
// helpers
// =====================================================================

// usageForcer is the minimal interface the materializer's per-Field
// visibility forcing leaves on each generic data point. We re-derive
// the contract here rather than importing materializer-internal types.
type usageForcer interface {
	ForcedUsage() (hmenum.DataPointUsage, bool)
}

// putBool drops a boolean writable VALUES data point on ch. Used for
// STATE / GLOBAL_BUTTON_LOCK / etc.
func putBool(ch *device.Channel, p hmenum.Parameter) device.ParameterDataPoint {
	dp := generic.NewSwitch(generic.Spec{
		Key: hmtypes.DataPointKey{
			InterfaceID:    "iface",
			ChannelAddress: ch.Address,
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(p),
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeBool,
			Operations: hmenum.OperationsRead | hmenum.OperationsWrite | hmenum.OperationsEvent,
		},
		Writer: testWriter{},
	})
	ch.Put(dp)
	return dp
}

// putFloat drops a float writable VALUES data point on ch (used for
// SET_POINT_TEMPERATURE / LEVEL / LEVEL_2 / ACTUAL_TEMPERATURE / …).
func putFloat(ch *device.Channel, p hmenum.Parameter) device.ParameterDataPoint {
	dp := generic.NewFloat(generic.Spec{
		Key: hmtypes.DataPointKey{
			InterfaceID:    "iface",
			ChannelAddress: ch.Address,
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(p),
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeFloat,
			Operations: hmenum.OperationsRead | hmenum.OperationsWrite | hmenum.OperationsEvent,
			Min:        json.RawMessage("0.0"),
			Max:        json.RawMessage("100.0"),
		},
		Writer: testWriter{},
	})
	ch.Put(dp)
	return dp
}

// putFloatSensor adds a read-only float to mimic ACTUAL_TEMPERATURE /
// HUMIDITY (the Visible-flagged sensor parameters). The materializer
// still flips ForcedUsage on those — the call shape is identical.
func putFloatSensor(ch *device.Channel, p hmenum.Parameter) device.ParameterDataPoint {
	dp := generic.NewFloatSensor(generic.Spec{
		Key: hmtypes.DataPointKey{
			InterfaceID:    "iface",
			ChannelAddress: ch.Address,
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(p),
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeFloat,
			Operations: hmenum.OperationsRead | hmenum.OperationsEvent,
		},
	})
	ch.Put(dp)
	return dp
}

// addChannel registers a channel under its CCU-style address and
// returns the channel pointer. Channel type is irrelevant for the
// anchor tests since the visibility gate (which keys off TYPE) is not
// active here.
func addChannel(d *device.Device, num int) *device.Channel {
	addr := d.Address + ":" + itoa(num)
	return d.AddChannel(addr, num, "X", hmenum.ParamsetKeyValues)
}

// newDevice constructs a HmIP/HM-style device and pre-populates the
// channels [0..maxChannel].
func newDevice(model string, maxChannel int) *device.Device {
	d := device.New(device.Config{
		InterfaceID:  "HmIP-RF",
		Interface:    hmenum.InterfaceHmIPRF,
		Address:      "0001ANCHR",
		Model:        model,
		Manufacturer: hmenum.ManufacturerEQ3,
		ProductGroup: hmenum.ProductGroupHmIP,
	})
	for i := 0; i <= maxChannel; i++ {
		addChannel(d, i)
	}
	return d
}

// hasForcedUsage returns the forced usage installed by the materializer
// on dp and whether one was set. dp is type-asserted to the [usageForcer]
// contract that every generic.DataPoint[T] satisfies.
func hasForcedUsage(t *testing.T, dp device.ParameterDataPoint) (hmenum.DataPointUsage, bool) {
	t.Helper()
	if dp == nil {
		return "", false
	}
	f, ok := dp.(usageForcer)
	if !ok {
		t.Fatalf("data point %T does not satisfy usageForcer", dp)
	}
	return f.ForcedUsage()
}

// itoa formats n for a guard's failure message.
//
// It was hand-rolled "to keep the import surface minimal" on the premise
// that its inputs were channel numbers in [0..15], and it was correct for
// [0..99] and silently wrong above: at n=197 it rendered "C7", because
// '0'+19 is 'C'. That premise stopped holding the moment a guard put a
// source line number in a message — and a message is the whole product of
// a failing guard, so a garbled one costs exactly what the guard was for.
func itoa(n int) string { return strconv.Itoa(n) }

// =====================================================================
// 1) HmIP-BWTH — IPThermostat (channel 1) + IPButtonLock (channel 0)
// =====================================================================

// TestCustomDP_HmIPBWTH verifies the HmIP-BWTH bug repro: after
// materialisation, channel 1 carries a Climate custom DP (IPThermostat),
// channel 0 carries a Lock custom DP (IPButtonLock), and the per-Field
// forced-usage flags are applied:
//
// - SET_POINT_TEMPERATURE on channel 1 stays default (Bare → no force)
// - HEATING_COOLING / HUMIDITY / ACTUAL_TEMPERATURE on channel 1 are
// flagged Visible → CDPVisible
// - GLOBAL_BUTTON_LOCK on channel 0 is Hidden → NoCreate
func TestCustomDP_HmIPBWTH(t *testing.T) {
	t.Parallel()

	dev := newDevice("HmIP-BWTH", 8)

	// Channel 0 — button lock: GLOBAL_BUTTON_LOCK + an unrelated
	// LOW_BAT to assert it stays untouched.
	ch0 := dev.Channel("0001ANCHR:0")
	gbl := putBool(ch0, hmenum.ParameterGlobalButtonLock)
	lowBat := putBool(ch0, hmenum.ParameterLowBat)

	// Channel 1 — primary thermostat channel.
	ch1 := dev.Channel("0001ANCHR:1")
	setPoint := putFloat(ch1, hmenum.ParameterSetPointTemperature)
	heatingCooling := putBool(ch1, hmenum.ParameterHeatingCooling)
	humidity := putFloatSensor(ch1, hmenum.ParameterHumidity)
	actualTemp := putFloatSensor(ch1, hmenum.ParameterActualTemperature)

	if err := custom.CreateCustomDataPoints(dev, custom.DefaultRegistry()); err != nil {
		t.Fatalf("CreateCustomDataPoints: %v", err)
	}

	// IPButtonLock landed on channel 0.
	if cdp := ch0.CustomDataPoint(); cdp == nil {
		t.Error("channel 0 has no custom DP — IPButtonLock did not materialise")
	}
	// IPThermostat landed on channel 1.
	if cdp := ch1.CustomDataPoint(); cdp == nil {
		t.Error("channel 1 has no custom DP — IPThermostat did not materialise")
	}

	// GLOBAL_BUTTON_LOCK is `Hidden` in the IPButtonLock profile → NoCreate.
	if u, ok := hasForcedUsage(t, gbl); !ok || u != hmenum.DataPointUsageNoCreate {
		t.Errorf("GLOBAL_BUTTON_LOCK ForcedUsage = (%q, %v), want (%q, true)",
			u, ok, hmenum.DataPointUsageNoCreate)
	}
	// LOW_BAT on channel 0 is in the global DEFAULT_DATA_POINTS map, which the
	// materializer marks DataPoint-usage when the profile has
	// IncludeDefaultDataPoints=true (the default).
	if u, ok := hasForcedUsage(t, lowBat); !ok || u != hmenum.DataPointUsageDataPoint {
		t.Errorf("LOW_BAT ForcedUsage = (%q, %v), want (%q, true)",
			u, ok, hmenum.DataPointUsageDataPoint)
	}
	// SET_POINT_TEMPERATURE is `Bare` in IPThermostat — no forcing.
	if _, ok := hasForcedUsage(t, setPoint); ok {
		t.Error("SET_POINT_TEMPERATURE (Bare field) must not have a forced usage")
	}
	// HEATING_COOLING is `Visible` → CDPVisible.
	if u, ok := hasForcedUsage(t, heatingCooling); !ok || u != hmenum.DataPointUsageCDPVisible {
		t.Errorf("HEATING_COOLING ForcedUsage = (%q, %v), want (%q, true)",
			u, ok, hmenum.DataPointUsageCDPVisible)
	}
	// HUMIDITY is `Visible` → CDPVisible.
	if u, ok := hasForcedUsage(t, humidity); !ok || u != hmenum.DataPointUsageCDPVisible {
		t.Errorf("HUMIDITY ForcedUsage = (%q, %v), want (%q, true)",
			u, ok, hmenum.DataPointUsageCDPVisible)
	}
	// ACTUAL_TEMPERATURE is `Visible` → CDPVisible.
	if u, ok := hasForcedUsage(t, actualTemp); !ok || u != hmenum.DataPointUsageCDPVisible {
		t.Errorf("ACTUAL_TEMPERATURE ForcedUsage = (%q, %v), want (%q, true)",
			u, ok, hmenum.DataPointUsageCDPVisible)
	}

	// No custom DP attached to bystander channels (2..8).
	for i := 2; i <= 8; i++ {
		ch := dev.Channel("0001ANCHR:" + itoa(i))
		if ch != nil && ch.CustomDataPoint() != nil {
			t.Errorf("channel %d unexpectedly carries a custom DP", i)
		}
	}
}

// =====================================================================
// 2) HmIP-eTRV — IPThermostat (channel 1) + IPButtonLock (channel 0)
// =====================================================================

// TestCustomDP_HmIPeTRV pins the radiator-thermostat layout. Per
// generated_profiles.go, HmIP-eTRV registers BOTH IPThermostat
// (Channel 1) and IPButtonLock (Channel 0) — confirming that even a
// device without a wall-style display still exposes the global
// button-lock visibility plumbing on its config channel.
func TestCustomDP_HmIPeTRV(t *testing.T) {
	t.Parallel()

	dev := newDevice("HmIP-eTRV", 8)

	ch0 := dev.Channel("0001ANCHR:0")
	gbl := putBool(ch0, hmenum.ParameterGlobalButtonLock)

	ch1 := dev.Channel("0001ANCHR:1")
	putFloat(ch1, hmenum.ParameterSetPointTemperature)
	putFloatSensor(ch1, hmenum.ParameterActualTemperature)

	if err := custom.CreateCustomDataPoints(dev, custom.DefaultRegistry()); err != nil {
		t.Fatalf("CreateCustomDataPoints: %v", err)
	}

	if cdp := ch0.CustomDataPoint(); cdp == nil {
		t.Error("HmIP-eTRV channel 0 has no custom DP — IPButtonLock did not materialise")
	}
	if cdp := ch1.CustomDataPoint(); cdp == nil {
		t.Error("HmIP-eTRV channel 1 has no custom DP — IPThermostat did not materialise")
	}
	// GLOBAL_BUTTON_LOCK on a button-lock channel is forced NoCreate.
	if u, ok := hasForcedUsage(t, gbl); !ok || u != hmenum.DataPointUsageNoCreate {
		t.Errorf("HmIP-eTRV GLOBAL_BUTTON_LOCK ForcedUsage = (%q, %v), want (%q, true)",
			u, ok, hmenum.DataPointUsageNoCreate)
	}
}

// =====================================================================
// 3) HmIP-WGT — IPThermostat (channel 8), IPDimmer (channel 2),
// IPButtonLock (channel 0), IPSwitch (channel 4)
// =====================================================================

// TestCustomDP_HmIPWGT exercises a multi-profile device. Per
// generated_profiles.go HmIP-WGT carries four profiles — the
// materializer must instantiate all four and attach each to its
// primary channel without cross-contamination.
func TestCustomDP_HmIPWGT(t *testing.T) {
	t.Parallel()

	dev := newDevice("HmIP-WGT", 12)

	ch0 := dev.Channel("0001ANCHR:0") // IPButtonLock primary
	putBool(ch0, hmenum.ParameterGlobalButtonLock)

	ch2 := dev.Channel("0001ANCHR:2") // IPDimmer primary
	putFloat(ch2, hmenum.ParameterLevel)

	ch4 := dev.Channel("0001ANCHR:4") // IPSwitch primary
	putBool(ch4, hmenum.ParameterState)

	ch8 := dev.Channel("0001ANCHR:8") // IPThermostat primary
	putFloat(ch8, hmenum.ParameterSetPointTemperature)
	putFloatSensor(ch8, hmenum.ParameterActualTemperature)

	if err := custom.CreateCustomDataPoints(dev, custom.DefaultRegistry()); err != nil {
		t.Fatalf("CreateCustomDataPoints: %v", err)
	}

	for label, num := range map[string]int{
		"IPButtonLock@0": 0,
		"IPDimmer@2":     2,
		"IPSwitch@4":     4,
		"IPThermostat@8": 8,
	} {
		ch := dev.Channel("0001ANCHR:" + itoa(num))
		if ch == nil {
			t.Fatalf("%s: missing channel %d", label, num)
		}
		if cdp := ch.CustomDataPoint(); cdp == nil {
			t.Errorf("%s: no custom DP attached on channel %d", label, num)
		}
	}
}

// =====================================================================
// 4) HmIP-BROLL — IPCover (primary channel 4)
// =====================================================================

// TestCustomDP_HmIPBROLL pins the cover-profile end-to-end. HmIP-BROLL
// registers IPCover with `Channels: [{4, Primary}]` and the IPCover
// profile config has `PrimaryChannel: 0` + `SecondaryChannels: [1, 2]`,
// so relevant absolute channels = {4, 5, 6}. The custom Cover DP
// attaches to the *primary* channel only (4).
func TestCustomDP_HmIPBROLL(t *testing.T) {
	t.Parallel()

	dev := newDevice("HmIP-BROLL", 8)

	// Primary channel 4: LEVEL writable. No LEVEL_2 → constructor
	// builds a plain Cover (not a Blind).
	ch4 := dev.Channel("0001ANCHR:4")
	level := putFloat(ch4, hmenum.ParameterLevel)

	if err := custom.CreateCustomDataPoints(dev, custom.DefaultRegistry()); err != nil {
		t.Fatalf("CreateCustomDataPoints: %v", err)
	}

	if cdp := ch4.CustomDataPoint(); cdp == nil {
		t.Fatal("HmIP-BROLL channel 4 has no custom DP — IPCover did not materialise")
	}
	// LEVEL is `Bare` on the IPCover profile — no forced usage.
	if _, ok := hasForcedUsage(t, level); ok {
		t.Error("LEVEL on IPCover primary channel must not be force-marked")
	}
	// Channels outside the relevant set (primary {4} ∪ secondary
	// {5, 6} = {4, 5, 6}) must remain DP-free. Channels 5 and 6 also
	// Land in the relevant set per
	// `_get_relevant_channels` (mirroring secondary-channel
	// promotion); they each receive a custom DP whose existence we
	// rely on for HA group-level rendering. Bystander channels
	// outside that set must stay empty.
	for _, num := range []int{0, 1, 2, 3, 7, 8} {
		ch := dev.Channel("0001ANCHR:" + itoa(num))
		if ch == nil {
			continue
		}
		if cdp := ch.CustomDataPoint(); cdp != nil {
			t.Errorf("HmIP-BROLL channel %d unexpectedly carries a custom DP", num)
		}
	}
}

// =====================================================================
// 5) HmIP-PS — IPSwitch (primary channel 3)
// =====================================================================

// TestCustomDP_HmIPPS_AsRfSwitchAnchor pins the Switch end-to-end on a
// real generated profile. The originally requested HM-LC-Sw1-Pl is the
// classic-RF analogue but is NOT present in the auto-generated profile
// catalogue (`generated_profiles.go` registers no `RfSwitch` entry —
// the RfSwitch enum value exists for symmetry but no device type maps
// to it yet). HmIP-PS gives us the same Switch constructor exercise
// against a profile the registry actually carries.
func TestCustomDP_HmIPPS_AsRfSwitchAnchor(t *testing.T) {
	t.Parallel()

	dev := newDevice("HmIP-PS", 8)

	// HmIP-PS has Channels: [{3, Primary}] for IPSwitch. With
	// PrimaryChannel:0, the primary absolute channel is 3.
	ch3 := dev.Channel("0001ANCHR:3")
	state := putBool(ch3, hmenum.ParameterState)

	if err := custom.CreateCustomDataPoints(dev, custom.DefaultRegistry()); err != nil {
		t.Fatalf("CreateCustomDataPoints: %v", err)
	}

	if cdp := ch3.CustomDataPoint(); cdp == nil {
		t.Fatal("HmIP-PS channel 3 has no custom DP — IPSwitch did not materialise")
	}
	// STATE is `Bare` on IPSwitch — no force.
	if _, ok := hasForcedUsage(t, state); ok {
		t.Error("STATE on IPSwitch primary channel must not be force-marked")
	}

	// IPSwitch profile has SecondaryChannels {1, 2}, rebased to
	// {4, 5} for primary 3. Those channels are part of the relevant
	// set and may carry secondary custom DPs. Channels truly outside
	// that set (0, 1, 2, 6, 7, 8) must remain DP-free.
	for _, num := range []int{0, 1, 2, 6, 7, 8} {
		ch := dev.Channel("0001ANCHR:" + itoa(num))
		if ch == nil {
			continue
		}
		if cdp := ch.CustomDataPoint(); cdp != nil {
			t.Errorf("HmIP-PS channel %d unexpectedly carries a custom DP", num)
		}
	}
}
