// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package ws

import (
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/central/events"
	"github.com/SukramJ/openccu-loom/internal/model/hub"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

func TestSysvarTopicFormat(t *testing.T) {
	got := SysvarTopic("home", "EnergyCounter")
	if want := "hub.home.sysvars.EnergyCounter"; got != want {
		t.Fatalf("SysvarTopic = %q, want %q", got, want)
	}
}

func TestProgramTopicFormat(t *testing.T) {
	got := ProgramTopic("home", "1234")
	if want := "hub.home.programs.1234"; got != want {
		t.Fatalf("ProgramTopic = %q, want %q", got, want)
	}
}

func TestHubEventsSubscriberNilSafe(t *testing.T) {
	s := NewHubEventsSubscriber(nil, nil)
	s.Start()
	s.Stop()
}

func TestInstallModeTopicFormat(t *testing.T) {
	got := InstallModeTopic("home")
	if want := "hub.home.install_mode"; got != want {
		t.Fatalf("InstallModeTopic = %q, want %q", got, want)
	}
}

func TestInstallModeChangedPayloadShape(t *testing.T) {
	p := InstallModeChangedPayload{
		Central:    "home",
		Enabled:    true,
		RemainingS: 42,
	}
	if p.Central != "home" || !p.Enabled || p.RemainingS != 42 {
		t.Fatalf("payload field round-trip failed: %+v", p)
	}
}

func TestSysvarChangedPayloadShape(t *testing.T) {
	p := SysvarChangedPayload{
		Central:   "home",
		Name:      "EnergyCounter",
		ValueType: hmenum.HubValueType("FLOAT"),
		Value:     42.5,
		Previous:  41.0,
	}
	if p.Central != "home" || p.Name != "EnergyCounter" {
		t.Fatalf("payload field round-trip failed: %+v", p)
	}
}

func TestProgramExecutedPayloadShape(t *testing.T) {
	p := ProgramExecutedPayload{
		Central:   "home",
		ProgramID: "42",
		Trigger:   hmenum.ProgramTrigger("MANUAL"),
		Success:   true,
	}
	if p.ProgramID != "42" || !p.Success {
		t.Fatalf("payload field round-trip failed: %+v", p)
	}
}

// hubEventsRegistry builds a registry with one central whose serial is set to
// "3014F711A0001234" (suffix "11a0001234") for the hub-events end-to-end tests.
func hubEventsRegistry(t *testing.T) (*central.Registry, *central.Unit) {
	t.Helper()
	reg := central.NewRegistry()
	cu, err := central.New(central.Config{Name: "home"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	if err := reg.Register(cu); err != nil {
		t.Fatalf("reg.Register: %v", err)
	}
	cu.SetSystemInformation(central.SystemInfo{Serial: "3014F711A0001234"})
	return reg, cu
}

// pollHub waits up to 2 s for a hub event matching the given topic filter.
// It returns the first matching event or calls t.Fatal if none appears.
func pollHub(t *testing.T, h *Hub, filter func(string) bool) Event {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		res := h.Replay(0, filter)
		if len(res.Events) > 0 {
			return res.Events[0]
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("expected hub event did not appear within deadline")
	return Event{}
}

// TestHubEventsSubscriberSysvarUniqueID publishes a SysvarChangedEvent and
// verifies the payload carries the serial-prefixed unique_id for the sysvar.
func TestHubEventsSubscriberSysvarUniqueID(t *testing.T) {
	t.Parallel()

	h := NewHub()
	reg, cu := hubEventsRegistry(t)

	sub := NewHubEventsSubscriber(reg, h)
	sub.Start()
	t.Cleanup(sub.Stop)

	events.Publish(cu.EventBus, hmevent.SysvarChangedEvent{
		Base:        hmevent.NewBase(),
		CentralName: "home",
		Name:        "Außen Temperatur",
		ValueType:   hmenum.HubValueType("FLOAT"),
		NewValue:    hmtypes.FloatValue(5.5),
		OldValue:    hmtypes.FloatValue(4.0),
	})

	ev := pollHub(t, h, func(topic string) bool {
		return topic == SysvarTopic("home", "Außen Temperatur")
	})
	p, ok := ev.Payload.(SysvarChangedPayload)
	if !ok {
		t.Fatalf("payload type %T, want SysvarChangedPayload", ev.Payload)
	}
	if want := "loom_11a0001234_sysvar_aussen-temperatur"; p.UniqueID != want {
		t.Fatalf("unique_id = %q, want %q", p.UniqueID, want)
	}
}

// TestHubEventsSubscriberProgramUniqueIDResolvable registers a program in the
// hub model and verifies the executed-event payload carries the name-based slug.
func TestHubEventsSubscriberProgramUniqueIDResolvable(t *testing.T) {
	t.Parallel()

	h := NewHub()
	reg, cu := hubEventsRegistry(t)

	// Register the program in the central's hub model so the name can be resolved.
	cu.HubModel.PutProgram(&hub.Program{
		HubDataPoint: hub.HubDataPoint{Name: "Lights Off"},
		ID:           "P1",
	})

	sub := NewHubEventsSubscriber(reg, h)
	sub.Start()
	t.Cleanup(sub.Stop)

	events.Publish(cu.EventBus, hmevent.ProgramExecutedEvent{
		Base:        hmevent.NewBase(),
		CentralName: "home",
		ProgramID:   "P1",
		Trigger:     hmenum.ProgramTriggerUser,
		Success:     true,
	})

	ev := pollHub(t, h, func(topic string) bool {
		return topic == ProgramTopic("home", "P1")
	})
	p, ok := ev.Payload.(ProgramExecutedPayload)
	if !ok {
		t.Fatalf("payload type %T, want ProgramExecutedPayload", ev.Payload)
	}
	if want := "loom_11a0001234_program_lights-off"; p.UniqueID != want {
		t.Fatalf("unique_id = %q, want %q", p.UniqueID, want)
	}
}

// TestHubEventsSubscriberProgramUniqueIDUnresolvable verifies that an
// unresolvable program ID results in an empty unique_id.
func TestHubEventsSubscriberProgramUniqueIDUnresolvable(t *testing.T) {
	t.Parallel()

	h := NewHub()
	reg, cu := hubEventsRegistry(t)

	sub := NewHubEventsSubscriber(reg, h)
	sub.Start()
	t.Cleanup(sub.Stop)

	// No PutProgram call → ID "UNKNOWN" cannot be resolved.
	events.Publish(cu.EventBus, hmevent.ProgramExecutedEvent{
		Base:        hmevent.NewBase(),
		CentralName: "home",
		ProgramID:   "UNKNOWN",
		Trigger:     hmenum.ProgramTriggerUser,
		Success:     false,
	})

	ev := pollHub(t, h, func(topic string) bool {
		return topic == ProgramTopic("home", "UNKNOWN")
	})
	p, ok := ev.Payload.(ProgramExecutedPayload)
	if !ok {
		t.Fatalf("payload type %T, want ProgramExecutedPayload", ev.Payload)
	}
	if p.UniqueID != "" {
		t.Fatalf("unique_id = %q, want empty for unresolvable program", p.UniqueID)
	}
}
