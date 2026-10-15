// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package adapter

import (
	"sync"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/central/events"
	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// collectDeviceRefreshed subscribes to DeviceRefreshedEvent on bus and
// returns a snapshot function; the bus dispatches synchronously by default,
// so the snapshot is complete once the publishing call returned.
func collectDeviceRefreshed(t *testing.T, bus *events.Bus) func() []hmevent.DeviceRefreshedEvent {
	t.Helper()
	var mu sync.Mutex
	var got []hmevent.DeviceRefreshedEvent
	unsub := bus.Subscribe(func(e hmevent.DeviceRefreshedEvent) {
		mu.Lock()
		got = append(got, e)
		mu.Unlock()
	})
	t.Cleanup(unsub)
	return func() []hmevent.DeviceRefreshedEvent {
		mu.Lock()
		defer mu.Unlock()
		return append([]hmevent.DeviceRefreshedEvent(nil), got...)
	}
}

// TestWireDeviceRefreshedToBus_PublishesOnObservationAndThrottles drives the
// bridge the device pipeline installs: an observation on any VALUES data
// point of the device becomes one DeviceRefreshedEvent, a burst inside the
// throttle window collapses into that one event, and the next observation
// past the window opens a new one — including an unchanged re-read, which
// is the case the value-changed stream cannot carry.
func TestWireDeviceRefreshedToBus_PublishesOnObservationAndThrottles(t *testing.T) {
	t.Parallel()
	bus := events.NewBus()
	dev, ch := buildDeviceWithDP(t, "main", "REFR001", "REFR001:1", "ACTUAL_TEMPERATURE")
	humKey, err := hmtypes.NewDataPointKey("HmIP-RF", "REFR001:1", hmenum.ParamsetKeyValues, "HUMIDITY")
	if err != nil {
		t.Fatalf("NewDataPointKey: %v", err)
	}
	hum := generic.NewIntegerSensor(generic.Spec{Key: humKey, Descriptor: hmproto.ParameterData{}, CentralName: "main"})
	ch.Put(hum)
	temp, ok := ch.Parameter("ACTUAL_TEMPERATURE").(*generic.Sensor[float64])
	if !ok {
		t.Fatalf("ACTUAL_TEMPERATURE is %T, want *generic.Sensor[float64]", ch.Parameter("ACTUAL_TEMPERATURE"))
	}
	snapshot := collectDeviceRefreshed(t, bus)

	wireDeviceRefreshedToBus(bus, "main", dev)

	t0 := time.Date(2030, 5, 1, 10, 0, 0, 0, time.UTC)
	temp.OnEventAt(21.0, t0)
	hum.OnEventAt(55, t0.Add(100*time.Millisecond))             // same window: second DP of the same device
	temp.OnEventAt(21.0, t0.Add(deviceRefreshThrottleWindow-1)) // same window: unchanged re-read
	got := snapshot()
	if len(got) != 1 {
		t.Fatalf("events inside one throttle window = %d, want 1: %+v", len(got), got)
	}
	e := got[0]
	if e.CentralName != "main" || e.InterfaceID != "HmIP-RF" || e.DeviceAddress != "REFR001" {
		t.Fatalf("event identity = %+v", e)
	}
	if !e.RefreshedAt.Equal(t0) || !e.Timestamp().Equal(t0) {
		t.Fatalf("RefreshedAt/Timestamp = %v/%v, want %v (the observation that opened the window)", e.RefreshedAt, e.Timestamp(), t0)
	}

	// Past the window: an unchanged re-read still announces the refresh.
	t1 := t0.Add(deviceRefreshThrottleWindow)
	temp.OnEventAt(21.0, t1)
	got = snapshot()
	if len(got) != 2 {
		t.Fatalf("events after the window elapsed = %d, want 2: %+v", len(got), got)
	}
	if !got[1].RefreshedAt.Equal(t1) {
		t.Fatalf("second event RefreshedAt = %v, want %v", got[1].RefreshedAt, t1)
	}
	if lr := dev.LastRefreshed(); !lr.Equal(t1) {
		t.Fatalf("Device.LastRefreshed = %v, want %v: the event must never run ahead of the REST aggregate", lr, t1)
	}
}

// TestWireDeviceRefreshedToBus_NilSafe mirrors the other bridges: a nil
// bus or device is a no-op, not a panic.
func TestWireDeviceRefreshedToBus_NilSafe(t *testing.T) {
	t.Parallel()
	dev, _ := buildDeviceWithDP(t, "main", "REFR002", "REFR002:1", "STATE")
	wireDeviceRefreshedToBus(nil, "main", dev)
	wireDeviceRefreshedToBus(events.NewBus(), "main", nil)
}
