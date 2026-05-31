// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package adapter

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/central/events"
	"github.com/SukramJ/openccu-loom/internal/scheduler"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
)

// TestSchedulerEventsAreWired verifies that WireSchedulerEvents injects
// OnStart / OnComplete hooks that publish the expected domain events.
func TestSchedulerEventsAreWired(t *testing.T) {
	bus := events.NewBus()

	var (
		mu        sync.Mutex
		triggered []hmevent.DataRefreshTriggeredEvent
		completed []hmevent.DataRefreshCompletedEvent
	)

	_ = events.Subscribe(bus, func(e hmevent.DataRefreshTriggeredEvent) {
		mu.Lock()
		triggered = append(triggered, e)
		mu.Unlock()
	})
	_ = events.Subscribe(bus, func(e hmevent.DataRefreshCompletedEvent) {
		mu.Lock()
		completed = append(completed, e)
		mu.Unlock()
	})

	jobs := []scheduler.Job{
		{
			Name:     "hub.sysvar_refresh",
			Interval: time.Minute,
			Run:      func(ctx context.Context) error { return nil },
		},
	}

	wired := WireSchedulerEvents("my-ccu", bus, jobs)
	if len(wired) != 1 {
		t.Fatalf("expected 1 wired job, got %d", len(wired))
	}

	// Simulate job invocation by calling the hooks directly.
	if wired[0].OnStart != nil {
		wired[0].OnStart(wired[0].Name)
	}
	if wired[0].OnComplete != nil {
		wired[0].OnComplete(wired[0].Name, 42, true, nil)
	}

	// Allow event delivery (bus is synchronous but give it a moment).
	mu.Lock()
	gotTriggered := len(triggered)
	gotCompleted := len(completed)
	mu.Unlock()

	if gotTriggered != 1 {
		t.Fatalf("DataRefreshTriggeredEvent: got %d, want 1", gotTriggered)
	}
	if triggered[0].CentralName != "my-ccu" {
		t.Errorf("CentralName: got %q, want %q", triggered[0].CentralName, "my-ccu")
	}
	if triggered[0].JobName != "hub.sysvar_refresh" {
		t.Errorf("JobName: got %q, want %q", triggered[0].JobName, "hub.sysvar_refresh")
	}

	if gotCompleted != 1 {
		t.Fatalf("DataRefreshCompletedEvent: got %d, want 1", gotCompleted)
	}
	if completed[0].Duration != 42 {
		t.Errorf("Duration: got %d, want 42", completed[0].Duration)
	}
	if !completed[0].Success {
		t.Error("Success should be true")
	}
}

// TestWireSchedulerEventsPreservesExistingHooks verifies that pre-existing
// OnStart/OnComplete hooks on a job are still called after wiring.
func TestWireSchedulerEventsPreservesExistingHooks(t *testing.T) {
	bus := events.NewBus()

	var existingStartCalled bool
	var existingCompleteCalled bool

	jobs := []scheduler.Job{
		{
			Name:     "test.job",
			Interval: time.Minute,
			Run:      func(ctx context.Context) error { return nil },
			OnStart: func(name string) {
				existingStartCalled = true
			},
			OnComplete: func(name string, durationMs int64, success bool, err error) {
				existingCompleteCalled = true
			},
		},
	}

	wired := WireSchedulerEvents("ccu", bus, jobs)
	wired[0].OnStart(wired[0].Name)
	wired[0].OnComplete(wired[0].Name, 10, false, nil)

	if !existingStartCalled {
		t.Error("existing OnStart hook was not called after wiring")
	}
	if !existingCompleteCalled {
		t.Error("existing OnComplete hook was not called after wiring")
	}
}
