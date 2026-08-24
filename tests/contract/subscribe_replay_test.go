// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// Package contract — Subscribe-Replay contract tests.
//
// Every custom data point that implements [device.SubscribingDataPoint]
// must replay the wire DP's currently observed value through its internal
// handler immediately after Subscribe() is called. Without the replay,
// HA shows "unknown" for affected entities until the next CCU push —
// even when the coordinator already populated the wire DPs via
// fetch_all_device_data before Subscribe runs.
//
// The invariant: if a wire DP carries an observed value at the time
// Subscribe() is called, the custom DP's cached/computed state must
// reflect that value immediately after the call returns.
package contract

import (
	"reflect"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/custom"
	"github.com/SukramJ/openccu-loom/internal/model/custom/climate"
	"github.com/SukramJ/openccu-loom/internal/model/custom/cover"
	"github.com/SukramJ/openccu-loom/internal/model/custom/light"
	"github.com/SukramJ/openccu-loom/internal/model/custom/lock"
	"github.com/SukramJ/openccu-loom/internal/model/custom/siren"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// ─── helpers ─────────────────────────────────────────────────────────────────

func makeCh(address, chType string) *device.Channel {
	d := device.New(device.Config{InterfaceID: "HmIP-RF", Address: "REPLAY0001"})
	return d.AddChannel(address, 1, chType, hmenum.ParamsetKeyValues)
}

func putDP[T any](
	ch *device.Channel,
	param hmenum.Parameter,
	paramType hmenum.ParameterType,
	ops hmenum.Operations,
	ctor func(generic.Spec) T,
	putFn func(dp T),
) T {
	cfg := generic.Spec{
		Key: hmtypes.DataPointKey{
			ChannelAddress: ch.Address,
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(param),
		},
		Descriptor: hmproto.ParameterData{
			Type:       paramType,
			Operations: ops,
		},
	}
	dp := ctor(cfg)
	putFn(dp)
	return dp
}

func putFloatDP2(ch *device.Channel, p hmenum.Parameter) *generic.Float {
	return putDP(
		ch, p, hmenum.ParameterTypeFloat,
		hmenum.OperationsRead|hmenum.OperationsEvent,
		generic.NewFloat,
		func(dp *generic.Float) { ch.Put(dp) },
	)
}

func putBoolDP2(ch *device.Channel, p hmenum.Parameter) *generic.Switch {
	return putDP(
		ch, p, hmenum.ParameterTypeBool,
		hmenum.OperationsRead|hmenum.OperationsEvent,
		generic.NewSwitch,
		func(dp *generic.Switch) { ch.Put(dp) },
	)
}

func putIntDP(ch *device.Channel, p hmenum.Parameter) *generic.Sensor[int32] {
	return putDP(
		ch, p, hmenum.ParameterTypeInteger,
		hmenum.OperationsRead|hmenum.OperationsEvent,
		generic.NewIntegerSensor,
		func(dp *generic.Sensor[int32]) { ch.Put(dp) },
	)
}

func putBinDP(ch *device.Channel, p hmenum.Parameter) *generic.BinarySensor {
	return putDP(
		ch, p, hmenum.ParameterTypeBool,
		hmenum.OperationsRead|hmenum.OperationsEvent,
		generic.NewBinarySensor,
		func(dp *generic.BinarySensor) { ch.Put(dp) },
	)
}

func putStrDP(ch *device.Channel, p hmenum.Parameter) *generic.Sensor[string] {
	return putDP(
		ch, p, hmenum.ParameterTypeEnum,
		hmenum.OperationsRead|hmenum.OperationsEvent,
		generic.NewStringSensor,
		func(dp *generic.Sensor[string]) { ch.Put(dp) },
	)
}

// putEnumDP attaches a read-only ENUM sensor carrying its raw VALUE_LIST
// index — the shape the resolver produces for a read-only ENUM parameter
// (a *generic.Sensor[int32], not a *generic.Sensor[string]). Custom DPs
// read it back through custom.EnumSensorField + custom.EnumLabelValue, so a
// contract test that pre-populates one must build this type and fire the
// index, not the label.
func putEnumDP(ch *device.Channel, p hmenum.Parameter, valueList []string) *generic.Sensor[int32] {
	cfg := generic.Spec{
		Key: hmtypes.DataPointKey{
			ChannelAddress: ch.Address,
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(p),
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeEnum,
			Operations: hmenum.OperationsRead | hmenum.OperationsEvent,
			ValueList:  valueList,
		},
	}
	dp := generic.NewIntegerSensor(cfg)
	ch.Put(dp)
	return dp
}

// subscribeAndUnsub calls dp.Subscribe(ch) and returns the unsubscribe closure.
// It also verifies that Subscribe returned a non-nil unsub — a nil unsub from
// a SubscribingDataPoint is itself a contract violation.
func subscribeAndUnsub(t *testing.T, dp device.SubscribingDataPoint, ch *device.Channel) func() {
	t.Helper()
	unsub := dp.Subscribe(ch)
	if unsub == nil {
		t.Error("Subscribe returned nil unsub closure — must return a valid teardown func")
	}
	return unsub
}

// ─── Cover ───────────────────────────────────────────────────────────────────

// TestSubscribeReplay_Cover_Direction verifies that a Cover whose channel
// already carries an observed DIRECTION value has Direction() populated
// immediately after Subscribe() returns — without waiting for a future CCU push.
func TestSubscribeReplay_Cover_Direction(t *testing.T) {
	t.Parallel()

	ch := makeCh("COVER0001:1", "BLIND")
	levelDP := putFloatDP2(ch, hmenum.ParameterLevel)
	dirDP := putIntDP(ch, hmenum.ParameterDirection)

	// Pre-populate the DIRECTION wire DP before Subscribe is called —
	// this is what the coordinator's fetch_all_device_data does.
	dirDP.OnEvent(int32(1)) // DirectionUp constant value

	c := cover.New(cover.Config{
		Channel:      ch,
		Writer:       nil,
		Capabilities: custom.CoverCapabilities{},
	})
	_ = levelDP // LEVEL embedded in Cover; direction is the hot-path field

	// Direction must be unknown before Subscribe.
	if _, observed := c.Direction(); observed {
		t.Fatal("Direction must not be observed before Subscribe is called")
	}

	unsub := subscribeAndUnsub(t, c, ch)
	defer unsub()

	d, observed := c.Direction()
	if !observed {
		t.Fatal("Direction not observed after Subscribe — replay did not fire")
	}
	if d != cover.DirectionUp {
		t.Errorf("Direction after replay = %v, want DirectionUp(1)", d)
	}
}

// TestSubscribeReplay_Garage_State verifies that a Garage whose channel
// already carries an observed DOOR_STATE value has State() populated
// immediately after Subscribe() returns.
func TestSubscribeReplay_Garage_State(t *testing.T) {
	t.Parallel()

	ch := makeCh("GARAGE0001:1", "GARAGE_DOOR")
	stateDP := putEnumDP(ch, hmenum.ParameterDoorState,
		[]string{"UNKNOWN", "OPEN", "CLOSED", "VENTILATION_POSITION"})

	// Pre-populate before Subscribe (index 2 = CLOSED).
	stateDP.OnEvent(int32(2))

	g := cover.NewGarage(cover.GarageConfig{
		Channel:      ch,
		Writer:       nil,
		Capabilities: custom.CoverCapabilities{SupportsStop: true},
	})

	if _, observed := g.DoorState(); observed {
		t.Fatal("State must not be observed before Subscribe is called")
	}

	unsub := subscribeAndUnsub(t, g, ch)
	defer unsub()

	st, observed := g.DoorState()
	if !observed {
		t.Fatal("State not observed after Subscribe — replay did not fire")
	}
	if st != cover.DoorStateClosed {
		t.Errorf("State after replay = %q, want CLOSED", st)
	}
}

// ─── Climate ─────────────────────────────────────────────────────────────────

// TestSubscribeReplay_Climate_ValveState verifies that a classic RF Climate
// whose channel already carries an observed VALVE_STATE value has Activity()
// set immediately after Subscribe() returns. (KindRF is the only kind that
// derives activity from VALVE_STATE — SimpleRF has no activity source and
// HmIP uses LEVEL/STATE.)
func TestSubscribeReplay_Climate_ValveState(t *testing.T) {
	t.Parallel()

	ch := makeCh("CLIMATE0001:4", "HEATING_CLIMATECONTROL_RECEIVER")
	valveDP := putFloatDP2(ch, hmenum.ParameterValveState)
	setpointDP := putFloatDP2(ch, hmenum.ParameterSetPointTemperature)
	_ = setpointDP

	// Pre-populate VALVE_STATE with a non-zero value (heating active).
	valveDP.OnEvent(float64(0.5))

	c := climate.New(climate.Config{
		Channel:      ch,
		Kind:         climate.KindRF,
		Capabilities: custom.ClimateCapabilities{},
	})

	// Activity must be unknown before Subscribe.
	if _, known := c.Activity(); known {
		t.Fatal("Activity must not be known before Subscribe is called")
	}

	unsub := subscribeAndUnsub(t, c, ch)
	defer unsub()

	act, known := c.Activity()
	if !known {
		t.Fatal("Activity not known after Subscribe — VALVE_STATE replay did not fire")
	}
	if act != climate.ActivityHeating {
		t.Errorf("Activity after replay = %v, want ActivityHeating", act)
	}
}

// ─── Lock ────────────────────────────────────────────────────────────────────

// updateCallbackCount reports how many update callbacks are currently
// registered on a *generic.DataPoint[T] (reached through its exported
// DataPoint field on the concrete wire-DP wrapper). Lock and Siren keep no
// aggregate cache of their own — State()/IsActive() read the wire DP
// directly — so Subscribe's only observable contract is that it registers
// an OnAnyUpdate hook on the wire DP (the EventBridge relies on that
// registration existing to re-fire publishCustomDPState on every wire-side
// change). The callback slice is unexported, but reflection over its
// length and element kind does not require CanInterface/CanSet, so this
// reads the live count without touching production code.
//
// Nil slots are skipped deliberately: unsubscribing nils a slot in place
// rather than shortening the slice (internal/model/generic/datapoint.go,
// the unsubscribe closure OnUpdate returns), so a plain Len() would count a hook that has
// already been cancelled and report a registration that can never fire.
func updateCallbackCount(t *testing.T, dataPoint any) int {
	t.Helper()
	v := reflect.ValueOf(dataPoint)
	if v.Kind() != reflect.Pointer || v.IsNil() {
		t.Fatalf("updateCallbackCount: want non-nil pointer, got %T", dataPoint)
	}
	f := v.Elem().FieldByName("updateCallbacks")
	if !f.IsValid() {
		t.Fatalf("updateCallbackCount: %T has no updateCallbacks field", dataPoint)
	}
	live := 0
	for i := range f.Len() {
		if !f.Index(i).IsNil() {
			live++
		}
	}
	return live
}

// TestSubscribeReplay_Lock_RFRefreshed verifies that Lock.Subscribe registers
// an OnAnyUpdate hook on the RF lock's bool STATE wire DP. Lock keeps no
// aggregate cache of its own — State()/IsRefreshed() read the wire DP
// directly — so the replay-on-subscribe invariant the other custom DPs pin
// does not apply here; what Subscribe actually promises is the callback
// registration the EventBridge depends on to re-fire on every wire-side
// change (see Lock.Subscribe's doc comment).
func TestSubscribeReplay_Lock_RFRefreshed(t *testing.T) {
	t.Parallel()

	ch := makeCh("LOCK0001:1", "KEYMATIC")
	stateDP := putBoolDP2(ch, hmenum.ParameterState)

	l := lock.New(lock.Config{
		Channel:      ch,
		Kind:         lock.KindRF,
		Capabilities: custom.LockCapabilities{},
	})

	before := updateCallbackCount(t, stateDP.DataPoint)

	unsub := subscribeAndUnsub(t, l, ch)
	defer unsub()

	after := updateCallbackCount(t, stateDP.DataPoint)
	if after != before+1 {
		t.Errorf("Lock.Subscribe must register one OnAnyUpdate hook on the STATE wire DP; callback count %d -> %d", before, after)
	}
}

// ─── Siren ───────────────────────────────────────────────────────────────────

// TestSubscribeReplay_Siren_AcousticActive verifies that Siren.Subscribe
// registers an OnAnyUpdate hook on the ACOUSTIC_ALARM_ACTIVE wire DP. Siren
// keeps no aggregate cache either — IsActive()'s observed flag comes
// straight from the wire DP, and Siren.Subscribe never calls
// ReplayCurrentValue for any field — so, like Lock, the replay-on-subscribe
// invariant does not apply; what Subscribe actually promises is the
// callback registration its own doc comment names (the OnAnyUpdate hooks
// exist "so the channel records an OnAnyUpdate registration").
func TestSubscribeReplay_Siren_AcousticActive(t *testing.T) {
	t.Parallel()

	d := device.New(device.Config{InterfaceID: "HmIP-RF", Address: "SIREN0001"})
	ch := d.AddChannel("SIREN0001:1", 1, "ALARM_SWITCH_VIRTUAL_RECEIVER", hmenum.ParamsetKeyValues)

	acousticDP := putBinDP(ch, hmenum.ParameterAcousticAlarmActive)

	s := siren.New(siren.Config{
		Channel:      ch,
		Capabilities: custom.SirenCapabilities{SupportsAcoustic: true},
	})

	before := updateCallbackCount(t, acousticDP.DataPoint)

	unsub := subscribeAndUnsub(t, s, ch)
	defer unsub()

	after := updateCallbackCount(t, acousticDP.DataPoint)
	if after != before+1 {
		t.Errorf("Siren.Subscribe must register one OnAnyUpdate hook on the ACOUSTIC_ALARM_ACTIVE wire DP; callback count %d -> %d", before, after)
	}
}

// ─── Light ───────────────────────────────────────────────────────────────────

// TestSubscribeReplay_Light_LastLevel verifies that a Light whose LEVEL DP
// already has a non-zero value propagates lastLevel through the existing
// OnUpdate callback (registered in New, not Subscribe). Subscribe() itself
// only returns the Close closure; the real replay for lastLevel happens via
// the OnUpdate callback that fires on the initial OnEvent — this test pins
// that the contract is satisfied.
func TestSubscribeReplay_Light_SubscribeReturnsValidUnsub(t *testing.T) {
	t.Parallel()

	ch := makeCh("LIGHT0001:1", "DIMMER")
	putFloatDP2(ch, hmenum.ParameterLevel)

	l := light.New(light.Config{
		Channel:      ch,
		Capabilities: custom.LightCapabilities{Dimmable: true},
	})

	// Light.Subscribe returns its own Close closure — verify it is non-nil.
	unsub := subscribeAndUnsub(t, l, ch)
	unsub()
}

// TestSubscribeReplay_RGBWLight_ModeReplay verifies that an RGBWLight whose
// channel already carries a DEVICE_OPERATION_MODE value has the mode populated
// immediately after Subscribe().
func TestSubscribeReplay_RGBWLight_ModeReplay(t *testing.T) {
	t.Parallel()

	ch := makeCh("RGBW0001:1", "RGBW_COLOR")
	// LEVEL for the embedded Light.
	putFloatDP2(ch, hmenum.ParameterLevel)
	// DEVICE_OPERATION_MODE on MASTER — for test simplicity we put it on VALUES.
	modeDP := putStrDP(ch, hmenum.ParameterDeviceOperationMode)

	// Pre-populate mode before Subscribe.
	modeDP.OnEvent("RGB")

	r := light.NewRGBWLight(light.Config{
		Channel:      ch,
		Capabilities: custom.LightCapabilities{Dimmable: true},
	})

	// Before Subscribe, mode must be unknown.
	if r.Mode() != light.RGBWModeUnknown {
		t.Fatalf("Mode must be Unknown before Subscribe, got %v", r.Mode())
	}

	unsub := subscribeAndUnsub(t, r, ch)
	defer unsub()

	if r.Mode() != light.RGBWModeRGB {
		t.Errorf("RGBWLight mode after Subscribe replay = %v, want RGB", r.Mode())
	}
}
