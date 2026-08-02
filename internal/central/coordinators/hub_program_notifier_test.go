// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package coordinators

import (
	"testing"

	"github.com/SukramJ/openccu-loom/internal/central/events"
	"github.com/SukramJ/openccu-loom/internal/model/hub"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// TestProgramNotifiersReachScanRegisteredPrograms is the regression tripwire
// for the wiring gap: the hub scan registers programs through
// [hub.Hub.PutProgram], not through [HubCoordinator.AddProgramDP], so the
// notifier fields stayed nil for every program a real daemon ever had. Neither
// an execution nor an activity change reached the bus, and no north-bound
// adapter could learn that a program had been deactivated.
func TestProgramNotifiersReachScanRegisteredPrograms(t *testing.T) {
	t.Parallel()

	bus := events.NewBus()
	hc := NewHubCoordinator("test-central", bus)
	m := hub.NewHub("test-central")

	// A program already present when the model is attached.
	early := hub.NewProgram("test-central", "1", "Early", "", false, nil)
	m.PutProgram(early)

	hc.SetHubModel(m)

	// …and one the scan registers afterwards.
	late := hub.NewProgram("test-central", "2", "Late", "", false, nil)
	m.PutProgram(late)

	var changed []hmevent.ProgramChangedEvent
	unsub := events.Subscribe(bus, func(e hmevent.ProgramChangedEvent) {
		changed = append(changed, e)
	})
	defer unsub()

	early.OnActive(true)
	late.OnActive(true)
	late.OnActive(false)

	if len(changed) != 3 {
		t.Fatalf("expected 3 program-changed events, got %d: %+v", len(changed), changed)
	}
	if changed[0].ProgramID != "1" || !changed[0].Active {
		t.Fatalf("first event = %+v, want program 1 active", changed[0])
	}
	if changed[2].ProgramID != "2" || changed[2].Active {
		t.Fatalf("third event = %+v, want program 2 inactive", changed[2])
	}
	for _, e := range changed {
		if e.CentralName != "test-central" {
			t.Fatalf("event not scoped to the central: %+v", e)
		}
	}

	// The same wiring pass arms the execution notifier, which had no
	// production caller at all before.
	if early.ExecuteNotifier == nil || late.ExecuteNotifier == nil {
		t.Fatal("ExecuteNotifier must be wired for scan-registered programs")
	}
	var executed []hmevent.ProgramExecutedEvent
	unsubExec := events.Subscribe(bus, func(e hmevent.ProgramExecutedEvent) {
		executed = append(executed, e)
	})
	defer unsubExec()
	early.ExecuteNotifier(t.Context(), early.ID, hmenum.ProgramTriggerAPI, true)
	if len(executed) != 1 || executed[0].ProgramID != "1" || !executed[0].Success {
		t.Fatalf("execution notifier did not reach the bus: %+v", executed)
	}
}

// TestSetHubModelDetachesPreviousProgramHook guards against a re-attached
// model leaving the previous registration hook behind, which would publish a
// duplicate event per activity change.
func TestSetHubModelDetachesPreviousProgramHook(t *testing.T) {
	t.Parallel()

	bus := events.NewBus()
	hc := NewHubCoordinator("test-central", bus)
	first := hub.NewHub("test-central")
	hc.SetHubModel(first)
	second := hub.NewHub("test-central")
	hc.SetHubModel(second)

	var changed []hmevent.ProgramChangedEvent
	unsub := events.Subscribe(bus, func(e hmevent.ProgramChangedEvent) {
		changed = append(changed, e)
	})
	defer unsub()

	// Registering on the detached model must not arm anything.
	orphan := hub.NewProgram("test-central", "9", "Orphan", "", false, nil)
	first.PutProgram(orphan)
	orphan.OnActive(true)
	if len(changed) != 0 {
		t.Fatalf("detached model still publishes: %+v", changed)
	}

	live := hub.NewProgram("test-central", "10", "Live", "", false, nil)
	second.PutProgram(live)
	live.OnActive(true)
	if len(changed) != 1 {
		t.Fatalf("expected exactly one event from the live model, got %d", len(changed))
	}
}

// TestSysvarNotifiersReachScanRegisteredSysvars is the same tripwire for
// system variables. The hub scan registers them through [hub.Hub.PutSysvar],
// and only the MQTT publisher subscribed to the model directly — so
// `hub.sysvar_changed` never fired for any bus-driven consumer, and a
// WebSocket client's sysvar values froze at whatever the bootstrap read.
func TestSysvarNotifiersReachScanRegisteredSysvars(t *testing.T) {
	t.Parallel()

	bus := events.NewBus()
	hc := NewHubCoordinator("test-central", bus)
	m := hub.NewHub("test-central")

	early := hub.NewSysvar("test-central", "Early", "", hmenum.HubValueTypeFloat, nil)
	m.PutSysvar(early)

	hc.SetHubModel(m)

	late := hub.NewSysvar("test-central", "Late", "", hmenum.HubValueTypeFloat, nil)
	m.PutSysvar(late)

	var changed []hmevent.SysvarChangedEvent
	unsub := events.Subscribe(bus, func(e hmevent.SysvarChangedEvent) {
		changed = append(changed, e)
	})
	defer unsub()

	early.OnValue(hmtypes.FloatValue(1))
	late.OnValue(hmtypes.FloatValue(2))
	late.OnValue(hmtypes.FloatValue(2)) // unchanged — the scan re-observes every cycle
	late.OnValue(hmtypes.FloatValue(3))

	if len(changed) != 3 {
		t.Fatalf("expected 3 sysvar-changed events, got %d: %+v", len(changed), changed)
	}
	if changed[0].Name != "Early" {
		t.Fatalf("first event = %+v, want Early", changed[0])
	}
	if changed[2].Name != "Late" || changed[2].ValueType != hmenum.HubValueTypeFloat {
		t.Fatalf("third event = %+v, want Late as FLOAT", changed[2])
	}
	if v := changed[2].NewValue.Unwrap(); v != 3.0 {
		t.Fatalf("third event value = %v, want 3", v)
	}
	for _, e := range changed {
		if e.CentralName != "test-central" {
			t.Fatalf("event not scoped to the central: %+v", e)
		}
	}
}
