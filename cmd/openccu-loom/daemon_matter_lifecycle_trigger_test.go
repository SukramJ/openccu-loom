// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package main

import (
	"testing"

	"github.com/SukramJ/openccu-loom/internal/central/events"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
)

// TestSubscribeMatterDeviceLifecycleTrigger_ReadyUnitFiresOnCreateAndRemove
// verifies the happy path: once the central has completed its southbound
// bring-up, both a DeviceCreatedEvent and a DeviceRemovedEvent invoke the
// trigger.
func TestSubscribeMatterDeviceLifecycleTrigger_ReadyUnitFiresOnCreateAndRemove(t *testing.T) {
	t.Parallel()
	reg := buildTestRegistry(t, "ccu-a")
	unit, _ := reg.Get("ccu-a")
	unit.MarkSouthboundReady()

	fired := 0
	unsubs := subscribeMatterDeviceLifecycleTrigger(unit, func() { fired++ })
	t.Cleanup(func() {
		for _, unsub := range unsubs {
			unsub()
		}
	})

	events.Publish(unit.EventBus, hmevent.DeviceCreatedEvent{
		Base:        hmevent.NewBase(),
		CentralName: "ccu-a",
	})
	if fired != 1 {
		t.Fatalf("fired = %d after DeviceCreatedEvent, want 1", fired)
	}

	events.Publish(unit.EventBus, hmevent.DeviceRemovedEvent{
		Base:        hmevent.NewBase(),
		CentralName: "ccu-a",
	})
	if fired != 2 {
		t.Fatalf("fired = %d after DeviceRemovedEvent, want 2", fired)
	}
}

// TestSubscribeMatterDeviceLifecycleTrigger_NotReadyUnitSkipsTrigger pins the
// boot-ingest exclusion: a central still waiting on its southbound bring-up
// must not fire the trigger for its device events — the ready trigger
// already covers that initial batch with a single reassemble.
func TestSubscribeMatterDeviceLifecycleTrigger_NotReadyUnitSkipsTrigger(t *testing.T) {
	t.Parallel()
	reg := buildTestRegistry(t, "ccu-a")
	unit, _ := reg.Get("ccu-a")

	fired := 0
	unsubs := subscribeMatterDeviceLifecycleTrigger(unit, func() { fired++ })
	t.Cleanup(func() {
		for _, unsub := range unsubs {
			unsub()
		}
	})

	events.Publish(unit.EventBus, hmevent.DeviceCreatedEvent{
		Base:        hmevent.NewBase(),
		CentralName: "ccu-a",
	})
	events.Publish(unit.EventBus, hmevent.DeviceRemovedEvent{
		Base:        hmevent.NewBase(),
		CentralName: "ccu-a",
	})
	if fired != 0 {
		t.Fatalf("fired = %d for a not-yet-ready central, want 0", fired)
	}
}

// TestSubscribeMatterDeviceLifecycleTrigger_UnsubscribeStopsFiring verifies
// teardown: the function returns exactly two unsubscribe closers (one per
// subscribed event type), and calling all of them stops any further trigger
// invocation.
func TestSubscribeMatterDeviceLifecycleTrigger_UnsubscribeStopsFiring(t *testing.T) {
	t.Parallel()
	reg := buildTestRegistry(t, "ccu-a")
	unit, _ := reg.Get("ccu-a")
	unit.MarkSouthboundReady()

	fired := 0
	unsubs := subscribeMatterDeviceLifecycleTrigger(unit, func() { fired++ })
	if len(unsubs) != 4 {
		t.Fatalf("unsubs = %d, want 4 (DeviceCreated + DeviceRemoved + DeviceMetadataChanged + DeviceReleased)", len(unsubs))
	}

	for _, unsub := range unsubs {
		unsub()
	}

	events.Publish(unit.EventBus, hmevent.DeviceCreatedEvent{
		Base:        hmevent.NewBase(),
		CentralName: "ccu-a",
	})
	events.Publish(unit.EventBus, hmevent.DeviceRemovedEvent{
		Base:        hmevent.NewBase(),
		CentralName: "ccu-a",
	})
	if fired != 0 {
		t.Fatalf("fired = %d after unsubscribe, want 0", fired)
	}
}

// TestSubscribeMatterDeviceLifecycleTrigger_NilInputsReturnNil covers both
// nil-safety branches: a nil unit and a nil trigger each return a nil slice
// without subscribing anything or panicking.
func TestSubscribeMatterDeviceLifecycleTrigger_NilInputsReturnNil(t *testing.T) {
	t.Parallel()

	if unsubs := subscribeMatterDeviceLifecycleTrigger(nil, func() {}); unsubs != nil {
		t.Errorf("unsubs = %v for nil unit, want nil", unsubs)
	}

	reg := buildTestRegistry(t, "ccu-a")
	unit, _ := reg.Get("ccu-a")
	if unsubs := subscribeMatterDeviceLifecycleTrigger(unit, nil); unsubs != nil {
		t.Errorf("unsubs = %v for nil trigger, want nil", unsubs)
	}
}

// TestSubscribeMatterDeviceLifecycleTrigger_FiresOnRename pins the rename arm.
//
// A rename changes no device's existence, so it is easy to read as "not a
// topology change" — but a Matter accessory's NodeLabel is built from the
// device's name, so without this subscription a device renamed in the CCU
// WebUI keeps its old label in Apple Home and Google Home until the daemon
// restarts. MQTT and the WebSocket both react to the same event; Matter was
// the plane left behind.
func TestSubscribeMatterDeviceLifecycleTrigger_FiresOnRename(t *testing.T) {
	t.Parallel()
	reg := buildTestRegistry(t, "ccu-a")
	unit, _ := reg.Get("ccu-a")
	unit.MarkSouthboundReady()

	fired := 0
	unsubs := subscribeMatterDeviceLifecycleTrigger(unit, func() { fired++ })
	t.Cleanup(func() {
		for _, unsub := range unsubs {
			unsub()
		}
	})

	events.Publish(unit.EventBus, hmevent.DeviceMetadataChangedEvent{
		Base:        hmevent.NewBase(),
		CentralName: "ccu-a",
		InterfaceID: "HmIP-RF",
		Address:     "0001ABCD",
	})
	if fired != 1 {
		t.Errorf("fired = %d after a rename, want 1 — a renamed device keeps its old "+
			"NodeLabel in every Matter controller until the daemon restarts", fired)
	}
}
