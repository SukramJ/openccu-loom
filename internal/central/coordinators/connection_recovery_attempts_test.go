// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package coordinators

import (
	"context"
	"errors"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/central/events"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

func TestRecoveryAttemptCounterIncrementsOnFailure(t *testing.T) {
	bus := events.NewBus()
	c := NewConnectionRecoveryCoordinatorWithLimit("ccu-01", bus, 3)

	for i := range 2 {
		res := c.Run(context.Background(), "HmIP-RF", []Pipeline{
			{Stage: hmenum.RecoveryStage("ping"), Run: func(_ context.Context) error { return errors.New("boom") }},
		})
		if res != hmenum.RecoveryResultFailed {
			t.Fatalf("attempt %d result = %s, want failed", i, res)
		}
	}
	if got := c.AttemptCount("HmIP-RF"); got != 2 {
		t.Fatalf("attempt count = %d, want 2", got)
	}
}

func TestRecoveryAttemptCounterResetsOnSuccess(t *testing.T) {
	bus := events.NewBus()
	c := NewConnectionRecoveryCoordinatorWithLimit("ccu-01", bus, 5)

	// Two failures …
	for range 2 {
		_ = c.Run(context.Background(), "HmIP-RF", []Pipeline{
			{Stage: hmenum.RecoveryStage("ping"), Run: func(_ context.Context) error { return errors.New("boom") }},
		})
	}
	if c.AttemptCount("HmIP-RF") != 2 {
		t.Fatalf("setup wrong: %d", c.AttemptCount("HmIP-RF"))
	}

	// … followed by a success resets the counter.
	res := c.Run(context.Background(), "HmIP-RF", []Pipeline{
		{Stage: hmenum.RecoveryStage("ping"), Run: func(_ context.Context) error { return nil }},
	})
	if res != hmenum.RecoveryResultSuccess {
		t.Fatalf("success run returned %s", res)
	}
	if got := c.AttemptCount("HmIP-RF"); got != 0 {
		t.Fatalf("counter not reset: %d", got)
	}
}

func TestRecoveryAttemptCapBlocksFurtherRuns(t *testing.T) {
	bus := events.NewBus()
	c := NewConnectionRecoveryCoordinatorWithLimit("ccu-01", bus, 2)

	// Burn through the cap.
	for range 2 {
		_ = c.Run(context.Background(), "HmIP-RF", []Pipeline{
			{Stage: hmenum.RecoveryStage("ping"), Run: func(_ context.Context) error { return errors.New("boom") }},
		})
	}

	// Next run must short-circuit without invoking the step.
	called := false
	res := c.Run(context.Background(), "HmIP-RF", []Pipeline{
		{Stage: hmenum.RecoveryStage("ping"), Run: func(_ context.Context) error {
			called = true
			return nil
		}},
	})
	if res != hmenum.RecoveryResultFailed {
		t.Fatalf("capped run returned %s, want failed", res)
	}
	if called {
		t.Fatal("step must not run when cap is reached")
	}
}

func TestRecoveryResetAttemptsReleasesCap(t *testing.T) {
	bus := events.NewBus()
	c := NewConnectionRecoveryCoordinatorWithLimit("ccu-01", bus, 2)

	for range 2 {
		_ = c.Run(context.Background(), "HmIP-RF", []Pipeline{
			{Stage: hmenum.RecoveryStage("ping"), Run: func(_ context.Context) error { return errors.New("boom") }},
		})
	}
	c.ResetAttempts("HmIP-RF")
	if c.AttemptCount("HmIP-RF") != 0 {
		t.Fatal("ResetAttempts should clear the counter")
	}

	called := false
	res := c.Run(context.Background(), "HmIP-RF", []Pipeline{
		{Stage: hmenum.RecoveryStage("ping"), Run: func(_ context.Context) error {
			called = true
			return nil
		}},
	})
	if res != hmenum.RecoveryResultSuccess || !called {
		t.Fatalf("after reset, run should proceed; called=%v res=%s", called, res)
	}
}

func TestRecoveryUnlimitedAttemptsWhenCapZero(t *testing.T) {
	bus := events.NewBus()
	c := NewConnectionRecoveryCoordinatorWithLimit("ccu-01", bus, 0)

	for range 50 {
		_ = c.Run(context.Background(), "HmIP-RF", []Pipeline{
			{Stage: hmenum.RecoveryStage("ping"), Run: func(_ context.Context) error { return errors.New("boom") }},
		})
	}
	// Counter still ticks for diagnostics, but Run is never refused.
	called := false
	res := c.Run(context.Background(), "HmIP-RF", []Pipeline{
		{Stage: hmenum.RecoveryStage("ping"), Run: func(_ context.Context) error {
			called = true
			return nil
		}},
	})
	if !called || res != hmenum.RecoveryResultSuccess {
		t.Fatalf("unlimited cap must not refuse runs; called=%v res=%s", called, res)
	}
}
