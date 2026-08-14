// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package main

import (
	"strings"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/audit"
	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/central/events"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
)

// TestWireProgramExecuteAudit records a program run so an operator reporting a
// double execution can tell a run the daemon triggered from one the CCU
// produced on its own — the CCU's own log does not say who asked.
func TestWireProgramExecuteAudit(t *testing.T) {
	t.Parallel()

	unit, err := central.New(central.Config{Name: "GoOtto"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	reg := central.NewRegistry()
	if err := reg.Register(unit); err != nil {
		t.Fatalf("register: %v", err)
	}
	buf := audit.NewBuffer(10)

	_, teardown := wireProgramExecuteAudit(reg, buf, nil)
	defer teardown()

	events.Publish(unit.EventBus, hmevent.ProgramExecutedEvent{
		Base:        hmevent.NewBase(),
		CentralName: "GoOtto",
		ProgramID:   "4711",
		Trigger:     hmenum.ProgramTriggerAPI,
		Success:     true,
		Source:      "mqtt:program-trigger",
	})

	entries := buf.List(10)
	if len(entries) != 1 {
		t.Fatalf("expected exactly one audit entry, got %d", len(entries))
	}
	if entries[0].Action != audit.ActionProgramExecute {
		t.Errorf("action = %q, want %q", entries[0].Action, audit.ActionProgramExecute)
	}
	for _, want := range []string{"central=GoOtto", "program=4711", "success=true", "source=mqtt:program-trigger"} {
		if !strings.Contains(entries[0].Note, want) {
			t.Errorf("note %q must carry %q", entries[0].Note, want)
		}
	}

	// An event without a stamped source must never render a blank —
	// "unknown" is the honest answer and keeps the note grep-stable.
	events.Publish(unit.EventBus, hmevent.ProgramExecutedEvent{
		Base:        hmevent.NewBase(),
		CentralName: "GoOtto",
		ProgramID:   "4711",
		Trigger:     hmenum.ProgramTriggerAPI,
		Success:     true,
	})
	entries = buf.List(10)
	if len(entries) != 2 {
		t.Fatalf("expected two audit entries, got %d", len(entries))
	}
	// The buffer lists newest-first; the unstamped event is entries[0].
	if !strings.Contains(entries[0].Note, "source=unknown") {
		t.Errorf("note %q must carry source=unknown for an unstamped event", entries[0].Note)
	}
}

// TestWireProgramExecuteAuditTeardownStops verifies the returned teardown
// removes the subscription: a leaked subscriber would keep recording against a
// torn-down central after a live remove.
func TestWireProgramExecuteAuditTeardownStops(t *testing.T) {
	t.Parallel()

	unit, err := central.New(central.Config{Name: "GoOtto"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	reg := central.NewRegistry()
	if err := reg.Register(unit); err != nil {
		t.Fatalf("register: %v", err)
	}
	buf := audit.NewBuffer(10)

	_, teardown := wireProgramExecuteAudit(reg, buf, nil)
	teardown()

	events.Publish(unit.EventBus, hmevent.ProgramExecutedEvent{
		Base:        hmevent.NewBase(),
		CentralName: "GoOtto",
		ProgramID:   "4711",
		Trigger:     hmenum.ProgramTriggerAPI,
	})
	if got := len(buf.List(10)); got != 0 {
		t.Errorf("teardown must remove the subscription; got %d entries", got)
	}
}

// TestWireProgramExecuteAuditNilInputs pins the degraded path: no registry or
// no recorder yields a usable no-op teardown rather than a panic at boot.
func TestWireProgramExecuteAuditNilInputs(t *testing.T) {
	t.Parallel()
	hook, teardown := wireProgramExecuteAudit(nil, audit.NewBuffer(1), nil)
	teardown()
	if hook != nil {
		t.Error("a nil registry must yield no per-central hook")
	}
	hook, teardown = wireProgramExecuteAudit(central.NewRegistry(), nil, nil)
	teardown()
	if hook != nil {
		t.Error("a nil recorder must yield no per-central hook")
	}
}
