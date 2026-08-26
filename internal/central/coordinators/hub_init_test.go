// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package coordinators

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/central/events"
)

// TestInitHubTriggersProgramsAndSysvarsRefresh verifies that InitHub
// calls the programs and sysvars refresh hooks once each after clearing
// stale state, so the hub model is populated as soon as the CCU
// connection comes up.
func TestInitHubTriggersProgramsAndSysvarsRefresh(t *testing.T) {
	t.Parallel()
	bus := events.NewBus()
	h := NewHubCoordinator("c1", bus)

	var programsCalls, sysvarsCalls atomic.Int32
	h.SetRefreshHooks(RefreshHooks{
		Programs: func(_ context.Context) error {
			programsCalls.Add(1)
			return nil
		},
		Sysvars: func(_ context.Context) error {
			sysvarsCalls.Add(1)
			return nil
		},
	})

	h.InitHub()

	if programsCalls.Load() != 1 {
		t.Errorf("Programs refresh called %d times, want 1", programsCalls.Load())
	}
	if sysvarsCalls.Load() != 1 {
		t.Errorf("Sysvars refresh called %d times, want 1", sysvarsCalls.Load())
	}
}

// TestInitHubClearsPreviousSysvarsBeforeRefresh verifies that InitHub first
// clears stale sysvars so that if a refresh hook is not wired, the model
// starts from a clean baseline.
func TestInitHubClearsPreviousSysvarsBeforeRefresh(t *testing.T) {
	t.Parallel()
	bus := events.NewBus()
	h := NewHubCoordinator("c1", bus)

	// Pre-populate a sysvar snapshot.
	h.mu.Lock()
	h.sysvars["OldVar"] = SysvarSnapshot{Name: "OldVar"}
	h.mu.Unlock()

	h.InitHub()

	sysvars := h.Sysvars()
	for _, s := range sysvars {
		if s.Name == "OldVar" {
			t.Fatalf("InitHub should have cleared stale sysvar OldVar")
		}
	}
}

// TestInitHubIgnoresRefreshErrors verifies that InitHub does not
// propagate errors from refresh hooks — a partial CCU response must
// not prevent the coordinator from entering operational state.
func TestInitHubIgnoresRefreshErrors(t *testing.T) {
	t.Parallel()
	bus := events.NewBus()
	h := NewHubCoordinator("c1", bus)

	var called atomic.Int32
	h.SetRefreshHooks(RefreshHooks{
		Programs: func(_ context.Context) error {
			called.Add(1)
			return errors.New("programs refresh failure")
		},
	})

	// Must not panic and must return normally.
	h.InitHub()
	if called.Load() != 1 {
		t.Errorf("Programs refresh hook called %d times, want 1", called.Load())
	}
}
