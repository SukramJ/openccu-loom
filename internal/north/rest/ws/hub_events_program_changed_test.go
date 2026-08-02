// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package ws

import (
	"testing"

	"github.com/SukramJ/openccu-loom/internal/central/events"
	"github.com/SukramJ/openccu-loom/internal/model/hub"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// TestHubEventsSubscriberProgramChanged pins the broadcast a client needs to
// re-render a program's two controls. Until it existed, a deactivation — from
// the CCU WebUI or from a client's own write — reached no WebSocket consumer
// at all: the only program broadcast reported executions.
func TestHubEventsSubscriberProgramChanged(t *testing.T) {
	t.Parallel()

	h := NewHub()
	reg, cu := hubEventsRegistry(t)
	cu.HubModel.PutProgram(&hub.Program{
		HubDataPoint: hub.HubDataPoint{Name: "Lights Off"},
		ID:           "P1",
	})

	sub := NewHubEventsSubscriber(reg, h)
	sub.Start()
	t.Cleanup(sub.Stop)

	events.Publish(cu.EventBus, hmevent.ProgramChangedEvent{
		Base:        hmevent.NewBase(),
		CentralName: "home",
		ProgramID:   "P1",
		Active:      false,
	})

	ev := pollHub(t, h, func(topic string) bool {
		return topic == ProgramTopic("home", "P1")
	})
	if ev.Type != string(hmevent.EventTypeProgramChanged) {
		t.Fatalf("type = %q, want %q", ev.Type, string(hmevent.EventTypeProgramChanged))
	}
	p, ok := ev.Payload.(ProgramChangedPayload)
	if !ok {
		t.Fatalf("payload type %T, want ProgramChangedPayload", ev.Payload)
	}
	if p.Active {
		t.Fatal("active must mirror the event")
	}
	if p.ExecuteAvailable {
		t.Fatal("a deactivated program refuses to run — execute_available must be false")
	}
	if want := "loom_11a0001234_program_lights-off"; p.UniqueID != want {
		t.Fatalf("unique_id = %q, want %q", p.UniqueID, want)
	}
}

// TestHubEventsSubscriberProgramChangedActive covers the recovery direction:
// re-activating the program makes its execute control usable again.
func TestHubEventsSubscriberProgramChangedActive(t *testing.T) {
	t.Parallel()

	h := NewHub()
	reg, cu := hubEventsRegistry(t)
	cu.HubModel.PutProgram(&hub.Program{
		HubDataPoint: hub.HubDataPoint{Name: "Lights Off"},
		ID:           "P1",
	})

	sub := NewHubEventsSubscriber(reg, h)
	sub.Start()
	t.Cleanup(sub.Stop)

	events.Publish(cu.EventBus, hmevent.ProgramChangedEvent{
		Base:        hmevent.NewBase(),
		CentralName: "home",
		ProgramID:   "P1",
		Active:      true,
	})

	ev := pollHub(t, h, func(topic string) bool {
		return topic == ProgramTopic("home", "P1")
	})
	p, ok := ev.Payload.(ProgramChangedPayload)
	if !ok {
		t.Fatalf("payload type %T, want ProgramChangedPayload", ev.Payload)
	}
	if !p.Active || !p.ExecuteAvailable {
		t.Fatalf("expected an active, runnable program, got %+v", p)
	}
}

// TestSysvarChangeReachesTheBroadcast measures the whole chain the hub scan
// walks: a value observed on the model must arrive as a `hub.sysvar_changed`
// broadcast. Only the MQTT publisher subscribed to the model, and the bus
// event the WebSocket bridge listens for had no production publisher — so
// this broadcast was declared, bridged, contract-tested, and silent.
func TestSysvarChangeReachesTheBroadcast(t *testing.T) {
	t.Parallel()

	h := NewHub()
	reg, cu := hubEventsRegistry(t)
	sv := hub.NewSysvar("home", "Testvar", "", hmenum.HubValueTypeFloat, nil)
	cu.HubModel.PutSysvar(sv)

	// The coordinator owns the model→bus wiring; attaching it is what a real
	// central does at boot.
	cu.Hub.SetHubModel(cu.HubModel)

	sub := NewHubEventsSubscriber(reg, h)
	sub.Start()
	t.Cleanup(sub.Stop)

	sv.OnValue(hmtypes.FloatValue(42))

	ev := pollHub(t, h, func(topic string) bool {
		return topic == SysvarTopic("home", "Testvar")
	})
	if ev.Type != string(hmevent.EventTypeSysvarChanged) {
		t.Fatalf("type = %q, want %q", ev.Type, string(hmevent.EventTypeSysvarChanged))
	}
	p, ok := ev.Payload.(SysvarChangedPayload)
	if !ok {
		t.Fatalf("payload type %T, want SysvarChangedPayload", ev.Payload)
	}
	if p.Value != 42.0 {
		t.Fatalf("value = %v, want 42", p.Value)
	}
	if p.UniqueID == "" {
		t.Fatal("unique_id must be resolved for a sysvar")
	}
}

// TestDeviceTriggerReachesTheBroadcast measures the last leg: a raw CCU
// callback for a keypress must arrive as a `device.trigger` broadcast. This
// broadcast was declared, bridged and contract-tested while its only
// publisher had no production caller — so Home Assistant device triggers and
// keypress event entities never fired through this daemon.
func TestDeviceTriggerReachesTheBroadcast(t *testing.T) {
	t.Parallel()

	h := NewHub()
	reg, cu := hubEventsRegistry(t)

	sub := NewDeviceTriggerSubscriber(reg, h)
	sub.Start()
	t.Cleanup(sub.Stop)

	cu.Events.HandleRawEvent(t.Context(), "HmIP-RF", "0001ABCD:2", "PRESS_SHORT", hmtypes.BoolValue(true))

	ev := pollHub(t, h, func(topic string) bool {
		return topic == DeviceTriggerTopic("0001ABCD", 2)
	})
	if ev.Type != string(hmevent.EventTypeDeviceTrigger) {
		t.Fatalf("type = %q, want %q", ev.Type, string(hmevent.EventTypeDeviceTrigger))
	}
	p, ok := ev.Payload.(DeviceTriggerPayload)
	if !ok {
		t.Fatalf("payload type %T, want DeviceTriggerPayload", ev.Payload)
	}
	if p.Parameter != "PRESS_SHORT" || p.Channel != 2 {
		t.Fatalf("payload lost its coordinates: %+v", p)
	}
}
