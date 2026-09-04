// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

// The wire bindings of the combined data points: Subscribe is what
// attaches each one to the generic data points it aggregates, and until
// it runs the combined value is unobserved no matter what the device
// reports.

package combined_test

import (
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/combined"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// newFloatParam adds a float VALUES parameter to ch.
func newFloatParam(ch *device.Channel, address string, parameter hmenum.Parameter) *generic.Float {
	dp := generic.NewFloat(generic.Spec{
		Key: hmtypes.DataPointKey{
			ChannelAddress: address,
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(parameter),
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeFloat,
			Operations: hmenum.OperationsRead | hmenum.OperationsWrite | hmenum.OperationsEvent,
		},
	})
	ch.Put(dp)
	return dp
}

// newIntParam adds an integer VALUES parameter to ch.
func newIntParam(ch *device.Channel, address string, parameter hmenum.Parameter) *generic.Sensor[int32] {
	dp := generic.NewIntegerSensor(generic.Spec{
		Key: hmtypes.DataPointKey{
			ChannelAddress: address,
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(parameter),
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeInteger,
			Operations: hmenum.OperationsRead | hmenum.OperationsWrite | hmenum.OperationsEvent,
		},
	})
	ch.Put(dp)
	return dp
}

func newChannel(t *testing.T, address string) *device.Channel {
	t.Helper()
	d := device.New(device.Config{InterfaceID: "HmIP-RF", Address: "SUB0001"})
	return d.AddChannel(address, 1, "TEST_CHANNEL", hmenum.ParamsetKeyValues)
}

// TestTimerSubscribeCombinesValueAndUnit pins the timer's wire binding.
//
// The seconds value is a product of two parameters, so neither alone is a
// duration: until both have been observed the timer reports nothing, and
// after both it reports their combination. A binding that fired on the
// value alone would publish a number in whatever unit happened to be
// stale.
func TestTimerSubscribeCombinesValueAndUnit(t *testing.T) {
	t.Parallel()
	const address = "SUB0001:1"
	ch := newChannel(t, address)
	value := newIntParam(ch, address, "DURATION_VALUE")
	unit := newIntParam(ch, address, "DURATION_UNIT")

	timer := combined.NewTimer(address, nil, "DURATION_VALUE", "DURATION_UNIT")
	unsub := timer.Subscribe(ch)
	if unsub == nil {
		t.Fatal("Subscribe returned nil for a channel carrying both parameters")
	}
	defer unsub()

	// Only one half observed: not a duration yet.
	value.OnEvent(5)
	if _, observed := timer.ValueSeconds(); observed {
		t.Fatal("the timer reported a duration with only the value observed")
	}

	unit.OnEvent(int32(hmenum.TimerUnitMinutes))
	seconds, observed := timer.ValueSeconds()
	if !observed {
		t.Fatal("the timer reported nothing with both halves observed")
	}
	if seconds != 300 {
		t.Fatalf("ValueSeconds() = %v, want 300 (5 minutes)", seconds)
	}
}

// TestTimerSubscribeDeclinesAnIncompleteChannel pins that a channel
// missing either half yields no binding — a timer bound to one parameter
// would report a number whose unit nothing supplies.
func TestTimerSubscribeDeclinesAnIncompleteChannel(t *testing.T) {
	t.Parallel()
	const address = "SUB0002:1"

	t.Run("unit missing", func(t *testing.T) {
		t.Parallel()
		ch := newChannel(t, address)
		newIntParam(ch, address, "DURATION_VALUE")
		timer := combined.NewTimer(address, nil, "DURATION_VALUE", "DURATION_UNIT")
		if unsub := timer.Subscribe(ch); unsub != nil {
			t.Error("Subscribe must decline a channel without the unit parameter")
		}
	})

	t.Run("nil channel", func(t *testing.T) {
		t.Parallel()
		timer := combined.NewTimer(address, nil, "DURATION_VALUE", "DURATION_UNIT")
		if unsub := timer.Subscribe(nil); unsub != nil {
			t.Error("Subscribe(nil) must decline")
		}
	})
}

// TestTimerSubscribeSeedsFromAlreadyObservedValues pins the hydration
// case: both wire values already carry a reading when Subscribe runs, and
// no further update is coming.
func TestTimerSubscribeSeedsFromAlreadyObservedValues(t *testing.T) {
	t.Parallel()
	const address = "SUB0003:1"
	ch := newChannel(t, address)
	value := newIntParam(ch, address, "DURATION_VALUE")
	unit := newIntParam(ch, address, "DURATION_UNIT")
	value.OnEvent(90)
	unit.OnEvent(int32(hmenum.TimerUnitSeconds))

	timer := combined.NewTimer(address, nil, "DURATION_VALUE", "DURATION_UNIT")
	unsub := timer.Subscribe(ch)
	if unsub != nil {
		defer unsub()
	}
	seconds, observed := timer.ValueSeconds()
	if !observed || seconds != 90 {
		t.Fatalf("ValueSeconds() = (%v, %v) after Subscribe, want (90, true)", seconds, observed)
	}
}

// TestHSColorSubscribeCombinesHueAndSaturation pins the colour binding.
// Like the timer, neither half alone is a value.
func TestHSColorSubscribeCombinesHueAndSaturation(t *testing.T) {
	t.Parallel()
	const address = "SUB0004:1"
	ch := newChannel(t, address)
	hue := newIntParam(ch, address, hmenum.ParameterHue)
	sat := newFloatParam(ch, address, hmenum.ParameterSaturation)

	hs := combined.NewHSColor(address, nil, hmenum.ParameterHue, hmenum.ParameterSaturation)
	unsub := hs.Subscribe(ch)
	if unsub == nil {
		t.Fatal("Subscribe returned nil for a channel carrying both parameters")
	}
	defer unsub()

	hue.OnEvent(120)
	if _, observed := hs.Value(); observed {
		t.Fatal("the colour reported a value with only hue observed")
	}

	sat.OnEvent(1.0)
	got, observed := hs.Value()
	if !observed {
		t.Fatal("the colour reported nothing with both halves observed")
	}
	// Saturation crosses the boundary as a 0..1 fraction and surfaces as
	// the 0..100 percentage HA-flavoured consumers expect.
	if got.Hue != 120 || got.Saturation != 100 {
		t.Fatalf("Value() = %+v, want hue 120 / saturation 100", got)
	}
}

// TestHSColorSubscribeDeclinesAnIncompleteChannel is the negative control
// for the binding above.
func TestHSColorSubscribeDeclinesAnIncompleteChannel(t *testing.T) {
	t.Parallel()
	const address = "SUB0005:1"
	ch := newChannel(t, address)
	newIntParam(ch, address, hmenum.ParameterHue)

	hs := combined.NewHSColor(address, nil, hmenum.ParameterHue, hmenum.ParameterSaturation)
	if unsub := hs.Subscribe(ch); unsub != nil {
		t.Error("Subscribe must decline a channel without the saturation parameter")
	}
	if unsub := hs.Subscribe(nil); unsub != nil {
		t.Error("Subscribe(nil) must decline")
	}
}

// TestLevelCombinedSubscribeCombinesBothAxes pins the blind binding:
// level and slat tilt travel together as one composite.
func TestLevelCombinedSubscribeCombinesBothAxes(t *testing.T) {
	t.Parallel()
	const address = "SUB0006:1"
	ch := newChannel(t, address)
	level := newFloatParam(ch, address, hmenum.ParameterLevel)
	level2 := newFloatParam(ch, address, hmenum.ParameterLevel2)

	lc := combined.NewLevelCombined(address, hmenum.ParameterLevel, hmenum.ParameterLevel2)
	unsub := lc.Subscribe(ch)
	if unsub == nil {
		t.Fatal("Subscribe returned nil for a channel carrying both axes")
	}
	defer unsub()

	level.OnEvent(0.5)
	level2.OnEvent(0.25)
	got, observed := lc.Value()
	if !observed {
		t.Fatal("the composite reported nothing with both axes observed")
	}
	if got.Level.Level() != 0.5 || got.SlatsLevel.Level() != 0.25 {
		t.Fatalf("Value() = %+v, want level 0.5 / slats 0.25", got)
	}
}

// TestLevelCombinedSubscribeDeclinesAnIncompleteChannel is the negative
// control for the binding above.
func TestLevelCombinedSubscribeDeclinesAnIncompleteChannel(t *testing.T) {
	t.Parallel()
	const address = "SUB0007:1"
	ch := newChannel(t, address)
	newFloatParam(ch, address, hmenum.ParameterLevel)

	lc := combined.NewLevelCombined(address, hmenum.ParameterLevel, hmenum.ParameterLevel2)
	if unsub := lc.Subscribe(ch); unsub != nil {
		t.Error("Subscribe must decline a channel without the slats parameter")
	}
	if unsub := lc.Subscribe(nil); unsub != nil {
		t.Error("Subscribe(nil) must decline")
	}
}

// TestTimerDataPointKeyIdentifiesTheCombinedParameter pins the synthetic
// identity: DURATION is not a CCU parameter, and naming the combined data
// point after either wire half would collide with it on the channel.
func TestTimerDataPointKeyIdentifiesTheCombinedParameter(t *testing.T) {
	t.Parallel()
	timer := combined.NewTimer("SUB0008:1", nil, "DURATION_VALUE", "DURATION_UNIT")
	key := timer.DataPointKey()
	if key.Parameter != string(combined.ParameterDuration) {
		t.Errorf("key.Parameter = %q, want %q", key.Parameter, combined.ParameterDuration)
	}
	if key.ChannelAddress != "SUB0008:1" {
		t.Errorf("key.ChannelAddress = %q", key.ChannelAddress)
	}
}
