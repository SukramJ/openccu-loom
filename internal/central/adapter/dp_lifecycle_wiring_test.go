// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package adapter

import (
	"context"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/central/events"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/internal/model/weekprofile"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// helper builds a minimal device with one channel containing one VALUES DP.
func buildDeviceWithDP(t *testing.T, centralName, devAddr, chanAddr, param string) (*device.Device, *device.Channel) {
	t.Helper()
	dev := device.New(device.Config{
		Address:     devAddr,
		InterfaceID: "HmIP-RF",
		Interface:   hmenum.InterfaceHmIPRF,
		Model:       "HmIP-STH",
	})
	ch := dev.AddChannel(chanAddr, 1, "WEATHER_TRANSMIT", hmenum.ParamsetKeyValues)
	dpKey, err := hmtypes.NewDataPointKey("HmIP-RF", chanAddr, hmenum.ParamsetKeyValues, param)
	if err != nil {
		t.Fatalf("NewDataPointKey: %v", err)
	}
	dp := generic.NewFloatSensor(generic.Spec{
		Key:         dpKey,
		Descriptor:  hmproto.ParameterData{},
		CentralName: centralName,
	})
	ch.Put(dp)
	return dev, ch
}

// TestWireDataPointLifecycle_AvailabilityProvider verifies that after
// wireDataPointLifecycle, a VALUES DP's Available() mirrors its device's
// reachability rather than returning a hard-coded true.
func TestWireDataPointLifecycle_AvailabilityProvider(t *testing.T) {
	t.Parallel()

	bus := events.NewBus()
	c, err := central.New(central.Config{Name: "dp-lifecycle-avail"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	c.EventBus = bus

	dev, ch := buildDeviceWithDP(t, c.Name(), "AVAIL001", "AVAIL001:1", "ACTUAL_TEMPERATURE")
	c.ModelRegistry.Put(dev)

	rawDP := ch.Parameter("ACTUAL_TEMPERATURE")
	if rawDP == nil {
		t.Fatal("DP not found after Put")
	}
	type availabler interface{ Available() bool }
	dp, ok := rawDP.(availabler)
	if !ok {
		t.Fatal("DP does not implement Available()")
	}

	// Before wiring, Available() should return true (no provider → default true).
	if !dp.Available() {
		t.Fatal("before wiring: Available() must be true by default")
	}

	wireDataPointLifecycle(bus, c.Name(), dev)

	// Force the device to unavailable.
	dev.Availability().SetForced(hmenum.ForcedDeviceAvailabilityForceFalse)
	if dp.Available() {
		t.Fatal("after wiring + force-false: Available() must return false")
	}

	// Restore to available.
	dev.Availability().SetForced(hmenum.ForcedDeviceAvailabilityNotSet)
	if !dp.Available() {
		t.Fatal("after wiring + force-not-set: Available() must return true")
	}
}

// TestWireDataPointLifecycle_WeekProfilePublisher verifies that after
// wireDataPointLifecycle, a weekprofile ProfileDataPoint emits a
// WeekProfileChangedEvent on the event bus when FireScheduleUpdated is called.
func TestWireDataPointLifecycle_WeekProfilePublisher(t *testing.T) {
	t.Parallel()

	bus := events.NewBus()
	c, err := central.New(central.Config{Name: "dp-lifecycle-wp"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	c.EventBus = bus

	dev := device.New(device.Config{
		Address:     "WPDEV001",
		InterfaceID: "HmIP-RF",
		Interface:   hmenum.InterfaceHmIPRF,
		Model:       "HmIP-eTRV-2",
	})
	ch := dev.AddChannel("WPDEV001:1", 1, "CLIMATECONTROL_VENT_DRIVE", hmenum.ParamsetKeyValues)
	wp := weekprofile.NewProfileDataPoint(weekprofile.ProfileDataPointConfig{
		CentralName:    c.Name(),
		ChannelAddress: "WPDEV001:1",
		ScheduleType:   weekprofile.ScheduleTypeClimate,
	})
	ch.AttachWeekProfile(wp)
	c.ModelRegistry.Put(dev)

	// Subscribe to WeekProfileChangedEvent before wiring.
	received := make(chan hmevent.WeekProfileChangedEvent, 1)
	unsub := events.Subscribe(bus, func(e hmevent.WeekProfileChangedEvent) {
		received <- e
	})
	defer unsub()

	wireDataPointLifecycle(bus, c.Name(), dev)

	// Trigger the publisher via FireScheduleUpdated.
	wp.FireScheduleUpdated(context.Background(), 0)

	select {
	case ev := <-received:
		if ev.CentralName != c.Name() {
			t.Fatalf("CentralName=%q, want %q", ev.CentralName, c.Name())
		}
		if ev.ChannelAddress != "WPDEV001:1" {
			t.Fatalf("ChannelAddress=%q, want WPDEV001:1", ev.ChannelAddress)
		}
	default:
		t.Fatal("no WeekProfileChangedEvent received after FireScheduleUpdated")
	}
}

// TestWireDataPointLifecycle_NilSafe verifies that wireDataPointLifecycle
// does not panic when called with a nil bus or nil device.
func TestWireDataPointLifecycle_NilSafe(t *testing.T) {
	t.Parallel()
	bus := events.NewBus()
	dev := device.New(device.Config{
		Address:     "NILSAFE001",
		InterfaceID: "HmIP-RF",
		Interface:   hmenum.InterfaceHmIPRF,
		Model:       "HmIP-STH",
	})
	// Neither call should panic.
	wireDataPointLifecycle(nil, "c1", dev)
	wireDataPointLifecycle(bus, "c1", nil)
}
