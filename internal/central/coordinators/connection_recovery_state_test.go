// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package coordinators

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/central/events"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// P0-2: ConnectionRecoveryCoordinator depth — per-interface state,
// recovery history, exponential backoff, and Pipeline.Classify wiring.

func newRecCoord(t *testing.T) *ConnectionRecoveryCoordinator {
	t.Helper()
	bus := events.NewBus()
	return NewConnectionRecoveryCoordinator("c1", bus)
}

func TestStateZeroValueForUnknownInterface(t *testing.T) {
	t.Parallel()
	c := newRecCoord(t)
	got := c.State("never-seen")
	if got.InterfaceID != "never-seen" {
		t.Fatalf("InterfaceID=%q", got.InterfaceID)
	}
	if got.Attempts != 0 || got.ConsecutiveFailures != 0 {
		t.Fatalf("zero state expected, got %+v", got)
	}
}

func TestSuccessfulRunUpdatesStateAndHistory(t *testing.T) {
	t.Parallel()
	c := newRecCoord(t)
	pipeline := []Pipeline{
		{Stage: hmenum.RecoveryStageDetecting, Run: func(_ context.Context) error { return nil }},
	}
	if got := c.Run(context.Background(), "iface", pipeline); got != hmenum.RecoveryResultSuccess {
		t.Fatalf("res=%v", got)
	}
	state := c.State("iface")
	if state.Attempts != 1 {
		t.Fatalf("attempts=%d", state.Attempts)
	}
	if state.ConsecutiveFailures != 0 {
		t.Fatalf("consecutive failures must reset on success")
	}
	if state.LastSuccess.IsZero() {
		t.Fatal("LastSuccess not stamped")
	}
	hist := c.History("iface")
	if len(hist) != 1 || hist[0].Result != hmenum.RecoveryResultSuccess {
		t.Fatalf("history=%+v", hist)
	}
}

func TestFailedRunBumpsConsecutiveFailures(t *testing.T) {
	t.Parallel()
	c := newRecCoord(t)
	pipeline := []Pipeline{
		{Stage: hmenum.RecoveryStageDetecting, Run: func(_ context.Context) error { return errors.New("offline") }},
	}
	for range 3 {
		c.Run(context.Background(), "iface", pipeline)
	}
	state := c.State("iface")
	if state.ConsecutiveFailures != 3 {
		t.Fatalf("consecutive failures=%d, want 3", state.ConsecutiveFailures)
	}
	if state.Attempts != 3 {
		t.Fatalf("attempts=%d", state.Attempts)
	}
	hist := c.History("iface")
	if len(hist) != 3 {
		t.Fatalf("history len=%d", len(hist))
	}
	for _, h := range hist {
		if h.Result != hmenum.RecoveryResultFailed {
			t.Fatalf("expected failed, got %v", h.Result)
		}
	}
}

func TestSuccessAfterFailuresResetsConsecutive(t *testing.T) {
	t.Parallel()
	c := newRecCoord(t)
	failing := []Pipeline{
		{Stage: hmenum.RecoveryStageDetecting, Run: func(_ context.Context) error { return errors.New("nope") }},
	}
	working := []Pipeline{
		{Stage: hmenum.RecoveryStageDetecting, Run: func(_ context.Context) error { return nil }},
	}
	c.Run(context.Background(), "iface", failing)
	c.Run(context.Background(), "iface", failing)
	c.Run(context.Background(), "iface", working)

	state := c.State("iface")
	if state.ConsecutiveFailures != 0 {
		t.Fatalf("consecutive failures must reset on success, got %d", state.ConsecutiveFailures)
	}
	if state.Attempts != 3 {
		t.Fatalf("attempts=%d (every run still increments)", state.Attempts)
	}
}

func TestExponentialBackoff(t *testing.T) {
	t.Parallel()
	c := newRecCoord(t)
	c.SetBackoff(time.Second, 30*time.Second)

	// No failures yet → base delay.
	if got := c.NextRetryDelay("iface"); got != time.Second {
		t.Fatalf("base delay=%v", got)
	}
	failing := []Pipeline{
		{Stage: hmenum.RecoveryStageDetecting, Run: func(_ context.Context) error { return errors.New("x") }},
	}
	// 1 failure → base
	c.Run(context.Background(), "iface", failing)
	if got := c.NextRetryDelay("iface"); got != time.Second {
		t.Fatalf("after 1 failure delay=%v want 1s", got)
	}
	// 2 failures → 2*base
	c.Run(context.Background(), "iface", failing)
	if got := c.NextRetryDelay("iface"); got != 2*time.Second {
		t.Fatalf("after 2 failures delay=%v want 2s", got)
	}
	// 3 failures → 4*base
	c.Run(context.Background(), "iface", failing)
	if got := c.NextRetryDelay("iface"); got != 4*time.Second {
		t.Fatalf("after 3 failures delay=%v want 4s", got)
	}
}

func TestExponentialBackoffSaturates(t *testing.T) {
	t.Parallel()
	c := newRecCoord(t)
	c.SetBackoff(time.Second, 4*time.Second)
	failing := []Pipeline{
		{Stage: hmenum.RecoveryStageDetecting, Run: func(_ context.Context) error { return errors.New("x") }},
	}
	for range 8 {
		c.Run(context.Background(), "iface", failing)
	}
	if got := c.NextRetryDelay("iface"); got != 4*time.Second {
		t.Fatalf("saturated delay=%v want 4s", got)
	}
}

func TestPipelineClassifyOverridesFailureReason(t *testing.T) {
	t.Parallel()
	c := newRecCoord(t)
	pipeline := []Pipeline{{
		Stage:    hmenum.RecoveryStageDetecting,
		Run:      func(_ context.Context) error { return errors.New("auth blew up") },
		Classify: func(_ error) *hmenum.FailureReason { return new(hmenum.FailureReasonAuth) },
	}}
	c.Run(context.Background(), "iface", pipeline)

	hist := c.History("iface")
	if len(hist) != 1 {
		t.Fatalf("history len=%d", len(hist))
	}
	if hist[0].Reason != hmenum.FailureReasonAuth {
		t.Fatalf("classify ignored, reason=%v", hist[0].Reason)
	}
}

func TestHistoryRingCaps(t *testing.T) {
	t.Parallel()
	c := newRecCoord(t)
	failing := []Pipeline{
		{Stage: hmenum.RecoveryStageDetecting, Run: func(_ context.Context) error { return errors.New("x") }},
	}
	for range historySize + 5 {
		c.Run(context.Background(), "iface", failing)
	}
	if got := len(c.History("iface")); got != historySize {
		t.Fatalf("history capped to %d, got %d", historySize, got)
	}
}

func TestNextRetryAfterIsZeroBeforeFirstAttempt(t *testing.T) {
	t.Parallel()
	c := newRecCoord(t)
	if got := c.State("never-seen").NextRetryAfter; !got.IsZero() {
		t.Fatalf("NextRetryAfter must be zero pre-attempt, got %v", got)
	}
}

func TestResetAttemptsZeroesConsecutive(t *testing.T) {
	t.Parallel()
	c := newRecCoord(t)
	failing := []Pipeline{
		{Stage: hmenum.RecoveryStageDetecting, Run: func(_ context.Context) error { return errors.New("x") }},
	}
	c.Run(context.Background(), "iface", failing)
	c.Run(context.Background(), "iface", failing)
	if c.State("iface").ConsecutiveFailures != 2 {
		t.Fatal("setup failed")
	}
	c.ResetAttempts("iface")
	if got := c.State("iface").ConsecutiveFailures; got != 0 {
		t.Fatalf("ResetAttempts did not zero ConsecutiveFailures: %d", got)
	}
}
