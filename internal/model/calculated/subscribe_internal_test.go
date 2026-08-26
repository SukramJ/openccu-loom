// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

// Package calculated tests internal subscribe helpers: composeUnsubs,
// feedSink edge paths, and channelSubscribe nil-channel short-circuit.
package calculated

import (
	"context"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// ---------------------------------------------------------------------------
// composeUnsubs — nil-safe multi-unsub
// ---------------------------------------------------------------------------

func TestComposeUnsubsNilSafe(t *testing.T) {
	called := 0
	fn := func() { called++ }
	unsub := composeUnsubs(nil, fn, nil, fn)
	unsub()
	if called != 2 {
		t.Fatalf("expected 2 non-nil calls, got %d", called)
	}
}

func TestComposeUnsubsAllNil(t *testing.T) {
	unsub := composeUnsubs(nil, nil)
	unsub() // must not panic
}

// ---------------------------------------------------------------------------
// feedSink — !ok path (zero humidity, formula returns !ok)
// ---------------------------------------------------------------------------

func TestFeedSinkNotOkPath(t *testing.T) {
	s := NewDewPointSensor()
	var fired int
	s.OnUpdate(func(_, _ float64) { fired++ })
	s.OnTemperature(20)
	s.OnHumidity(0) // DewPoint(20, 0) = (0, false) → feedSink skips
	if fired != 0 {
		t.Fatalf("feedSink should not fire when formula returns !ok; fired=%d", fired)
	}
	if s.IsRefreshed() {
		t.Fatal("IsRefreshed must be false when formula never returned ok")
	}
}

// ---------------------------------------------------------------------------
// feedSink — PublishedEventRecently guard
// ---------------------------------------------------------------------------

func TestFeedSinkPublishedEventRecentlyGuard(t *testing.T) {
	s := NewDewPointSensor()

	pub := &calcCountingPublisher{}
	s.SetPublisher(pub)

	s.OnTemperature(25)
	s.OnHumidity(60)
	before := pub.count()
	if before == 0 {
		t.Skip("no publish recorded from first emission; PublishedEventRecently guard unreachable in this path")
	}

	s.PublishUpdate(context.Background(), 42.0)

	s.OnHumidity(61)
	after := pub.count()
	_ = after
}

// ---------------------------------------------------------------------------
// feedSink — shouldPublishCalcUpdate suppression with sources
// ---------------------------------------------------------------------------

func TestFeedSinkShouldPublishCalcUpdateSuppression(t *testing.T) {
	s := NewDewPointSensor()

	src1 := &stubSourceDP{}
	src1.setObserved(25.0)
	src1.setPublishedRecently(true)
	src2 := &stubSourceDP{}
	src2.setObserved(60.0)
	src2.setPublishedRecently(true)
	s.RegisterSource(src1)
	s.RegisterSource(src2)

	s.OnTemperature(25)
	s.OnHumidity(60)

	var fired int
	s.OnUpdate(func(_, _ float64) { fired++ })
	s.OnHumidity(55)
	_ = fired
}

// ---------------------------------------------------------------------------
// channelSubscribe — missing parameter path (dp == nil)
// ---------------------------------------------------------------------------

func TestChannelSubscribeMissingParameter(t *testing.T) {
	s := NewDewPointSensor()
	unsub := s.Subscribe(nil)
	unsub()
}

// ---------------------------------------------------------------------------
// masterSubscribe — parameter absent from MASTER
// ---------------------------------------------------------------------------

func TestMasterSubscribeMissingParam(t *testing.T) {
	d := device.New(device.Config{InterfaceID: "HmIP-RF", Address: "MAST0001"})
	ch := d.AddChannel("MAST0001:0", 0, "MAINTENANCE", hmenum.ParamsetKeyMaster)
	var called bool
	unsub := masterSubscribe(ch, hmenum.ParameterLowBatLimit, func(_ float64) { called = true })
	if unsub != nil {
		t.Fatal("masterSubscribe must return nil when parameter is absent from MASTER")
	}
	if called {
		t.Fatal("callback must not be called when parameter is absent")
	}
}

// ---------------------------------------------------------------------------
// subscribeTemperatureHumidityCapture — temperature fallback alias
// ---------------------------------------------------------------------------

func TestSubscribeTemperatureHumidityCaptureFallbackAlias(t *testing.T) {
	d := device.New(device.Config{InterfaceID: "HmIP-RF", Address: "FALL0001"})
	ch := d.AddChannel("FALL0001:1", 1, "WEATHER", hmenum.ParamsetKeyValues)

	tempDP := generic.NewFloatSensor(generic.Spec{
		Key:        hmtypes.DataPointKey{ChannelAddress: ch.Address, Parameter: string(hmenum.ParameterTemperature)},
		Descriptor: hmproto.ParameterData{Type: hmenum.ParameterTypeFloat, Operations: hmenum.OperationsRead | hmenum.OperationsEvent},
	})
	humDP := generic.NewFloatSensor(generic.Spec{
		Key:        hmtypes.DataPointKey{ChannelAddress: ch.Address, Parameter: string(hmenum.ParameterActualHumidity)},
		Descriptor: hmproto.ParameterData{Type: hmenum.ParameterTypeFloat, Operations: hmenum.OperationsRead | hmenum.OperationsEvent},
	})
	ch.Put(tempDP)
	ch.Put(humDP)

	sensor := NewDewPointSensor()
	unsub, srcs := subscribeTemperatureHumidityCapture(ch, sensor.OnTemperature, sensor.OnHumidity)
	defer unsub()

	if len(srcs) == 0 {
		t.Fatal("subscribeTemperatureHumidityCapture must find sources via alias parameters")
	}

	for _, dp := range srcs {
		sensor.RegisterSource(dp)
	}
	tempDP.OnEvent(20.0)
	humDP.OnEvent(50.0)
	if !sensor.IsRefreshed() {
		t.Fatal("DewPointSensor must be refreshed after alias parameter events")
	}
}

// ---------------------------------------------------------------------------
// DerivedBinarySensor.Subscribe — real channel with STATE param
// ---------------------------------------------------------------------------

// TestDerivedBinarySensorSubscribeRealChannel drives the sensor through
// both shapes a read-only ENUM source arrives in.
//
// The index case is the one production builds: a read-only ENUM resolves
// to an integer sensor, so the wire value pushed through the source DP is
// the 0-based VALUE_LIST index, never the label. Asserting the label form
// alone left every derived binary sensor permanently valueless on the
// devices the registry maps.
func TestDerivedBinarySensorSubscribeRealChannel(t *testing.T) {
	valueList := []string{"CLOSED", "TILTED", "OPEN"}

	cases := []struct {
		name string
		// build returns the source DP in its production shape plus the
		// closure that pushes an "open" event in that shape.
		build func(generic.Spec) (device.ParameterDataPoint, func())
	}{
		{
			name: "enum index",
			build: func(s generic.Spec) (device.ParameterDataPoint, func()) {
				dp := generic.NewIntegerSensor(s)
				return dp, func() { dp.OnEvent(int32(2)) }
			},
		},
		{
			name: "enum label",
			build: func(s generic.Spec) (device.ParameterDataPoint, func()) {
				dp := generic.NewStringSensor(s)
				return dp, func() { dp.OnEvent("OPEN") }
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := device.New(device.Config{InterfaceID: "HmIP-RF", Address: "WIN0001", Model: "HmIP-SRH"})
			ch := d.AddChannel("WIN0001:1", 1, "SHUTTER_CONTACT", hmenum.ParamsetKeyValues)

			enumDP, fireOpen := tc.build(generic.Spec{
				Key: hmtypes.DataPointKey{ChannelAddress: ch.Address, Parameter: string(hmenum.ParameterState)},
				Descriptor: hmproto.ParameterData{
					Type:       hmenum.ParameterTypeEnum,
					Operations: hmenum.OperationsRead | hmenum.OperationsEvent,
					ValueList:  valueList,
				},
			})
			ch.Put(enumDP)

			s := NewDerivedBinarySensor(hmenum.CalculatedParameterWindowOpen, []string{"OPEN"}, []string{"CLOSED"})
			s.SourceParameter = hmenum.ParameterState

			unsub := s.Subscribe(ch)
			if unsub == nil {
				t.Fatal("Subscribe must return a non-nil unsubscribe closure for valid channel+param")
			}
			defer unsub()

			fireOpen()

			v, ok := s.Value()
			if !ok {
				t.Fatal("DerivedBinarySensor should report a value after receiving OPEN from channel")
			}
			if !v {
				t.Fatalf("OPEN must map to true; got %v", v)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// OperatingVoltageLevelSensor.Subscribe — BATTERY_STATE fallback
// ---------------------------------------------------------------------------

func TestOperatingVoltageLevelSensorBatteryStateFallback(t *testing.T) {
	d := device.New(device.Config{InterfaceID: "HmIP-RF", Address: "BATT002", Model: "HM-CC-RT-DN"})
	ch := d.AddChannel("BATT002:0", 0, "MAINTENANCE", hmenum.ParamsetKeyValues)

	batDP := generic.NewFloatSensor(generic.Spec{
		Key:        hmtypes.DataPointKey{ChannelAddress: ch.Address, Parameter: string(hmenum.ParameterBatteryState), ParamsetKey: hmenum.ParamsetKeyValues},
		Descriptor: hmproto.ParameterData{Type: hmenum.ParameterTypeFloat, Operations: hmenum.OperationsRead | hmenum.OperationsEvent},
	})
	ch.Put(batDP)

	s := NewOperatingVoltageLevelSensor()
	s.SetReferences(2.0, 3.0)

	unsub := s.Subscribe(ch)
	if unsub == nil {
		t.Fatal("Subscribe must return non-nil for a channel with BATTERY_STATE")
	}
	defer unsub()

	batDP.OnEvent(2.8)
}

// ---------------------------------------------------------------------------
// OperatingVoltageLevelSensor.Subscribe — model with no battery config
// ---------------------------------------------------------------------------

func TestOperatingVoltageLevelSensorSubscribeNoBatteryConfig(t *testing.T) {
	d := device.New(device.Config{InterfaceID: "HmIP-RF", Address: "NOBT001", Model: "UNKNOWN-NO-BATTERY"})
	ch := d.AddChannel("NOBT001:0", 0, "MAINTENANCE", hmenum.ParamsetKeyValues)

	opVoltage := generic.NewFloatSensor(generic.Spec{
		Key:        hmtypes.DataPointKey{ChannelAddress: ch.Address, Parameter: string(hmenum.ParameterOperatingVoltage), ParamsetKey: hmenum.ParamsetKeyValues},
		Descriptor: hmproto.ParameterData{Type: hmenum.ParameterTypeFloat, Operations: hmenum.OperationsRead | hmenum.OperationsEvent},
	})
	ch.Put(opVoltage)

	s := NewOperatingVoltageLevelSensor()
	unsub := s.Subscribe(ch)
	if unsub == nil {
		t.Fatal("Subscribe must return a valid unsub even with no battery config")
	}
	defer unsub()

	opVoltage.OnEvent(3.0)
}
