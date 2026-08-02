// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package ws

import (
	"testing"

	"github.com/SukramJ/openccu-loom/internal/central/events"
	"github.com/SukramJ/openccu-loom/internal/model/hub"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
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
