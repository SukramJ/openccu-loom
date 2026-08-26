// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package hub

import (
	"testing"
)

// TestProgramOnActive_NotifiesOnTransition pins that a change of the activity
// flag reaches subscribers. It is the control that gates the program's other
// control — a deactivated program refuses to run — so a consumer offering
// "run now" has to be told when the answer flips. Before this, OnActive
// recorded the flag silently and the only path that ever fired was
// OnExecution, leaving execute-availability stale until the program next ran.
func TestProgramOnActive_NotifiesOnTransition(t *testing.T) {
	t.Parallel()
	p := NewProgram("main", "42", "TestProg", "", false, &stubProgramWriter{})

	var events []ProgramEvent
	p.OnUpdate(func(e ProgramEvent) { events = append(events, e) })

	var notified []bool
	var notifiedID string
	p.ActiveNotifier = func(id string, active bool) {
		notifiedID = id
		notified = append(notified, active)
	}

	p.OnActive(true) // first observation counts as a transition
	p.OnActive(true) // unchanged — the hub scan re-observes on every cycle
	p.OnActive(false)

	if len(events) != 2 {
		t.Fatalf("expected 2 subscriber calls (on → off), got %d: %+v", len(events), events)
	}
	if events[0].Kind != ProgramEventKindActivity || events[1].Kind != ProgramEventKindActivity {
		t.Fatalf("activity events must carry the activity kind, got %q / %q", events[0].Kind, events[1].Kind)
	}
	if !events[0].Active || events[1].Active {
		t.Fatalf("expected Active true then false, got %v / %v", events[0].Active, events[1].Active)
	}
	if events[0].Success || events[1].Success {
		t.Fatal("an activity change is not a run — Success must stay false")
	}
	if notifiedID != "42" {
		t.Fatalf("ActiveNotifier id = %q, want \"42\"", notifiedID)
	}
	if len(notified) != 2 || !notified[0] || notified[1] {
		t.Fatalf("ActiveNotifier calls = %v, want [true false]", notified)
	}
}

// TestProgramOnExecution_CarriesExecutionKind guards the other half of the
// discriminator: a run must not be mistaken for an activity change.
func TestProgramOnExecution_CarriesExecutionKind(t *testing.T) {
	t.Parallel()
	p := NewProgram("main", "42", "TestProg", "", false, &stubProgramWriter{})
	p.OnActive(true)

	var got ProgramEvent
	p.OnUpdate(func(e ProgramEvent) { got = e })
	p.OnExecution(true, "MANUAL")

	if got.Kind != ProgramEventKindExecution {
		t.Fatalf("Kind = %q, want execution", got.Kind)
	}
	if !got.Success || got.Trigger != "MANUAL" {
		t.Fatalf("execution event lost its payload: %+v", got)
	}
	if !got.Active {
		t.Fatal("an execution event still reports the current activity flag")
	}
}
