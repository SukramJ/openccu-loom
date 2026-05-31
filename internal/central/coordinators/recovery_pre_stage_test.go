// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package coordinators

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/central/events"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
)

// stubJSONRPCSessionClearer records how many times ClearJSONRPCSession is called.
type stubJSONRPCSessionClearer struct {
	calls atomic.Int32
}

func (s *stubJSONRPCSessionClearer) ClearJSONRPCSession() {
	s.calls.Add(1)
}

// TestClearJSONRPCSessionStepInvokesHookOnRun verifies that a recovery
// pipeline whose first stage uses ClearJSONRPCSessionBeforeRecovery invokes
// the registered clearer exactly once per Run call.
func TestClearJSONRPCSessionStepInvokesHookOnRun(t *testing.T) {
	t.Parallel()
	bus := events.NewBus()
	coord := NewConnectionRecoveryCoordinator("central1", bus)
	clearer := &stubJSONRPCSessionClearer{}
	coord.WithJSONRPCSessionClearer(clearer)

	pipeline := []Pipeline{
		{Stage: hmenum.RecoveryStageDetecting, Run: coord.ClearJSONRPCSessionBeforeRecovery()},
		{Stage: hmenum.RecoveryStageReconnecting, Run: func(_ context.Context) error { return nil }},
	}
	result := coord.Run(context.Background(), "iface1", pipeline)
	if result != hmenum.RecoveryResultSuccess {
		t.Fatalf("expected success, got %v", result)
	}
	if got := clearer.calls.Load(); got != 1 {
		t.Fatalf("expected ClearJSONRPCSession called 1 time, got %d", got)
	}
}

// TestClearJSONRPCSessionStepNilClearerIsNoop verifies that the step does not
// panic when no clearer has been wired.
func TestClearJSONRPCSessionStepNilClearerIsNoop(t *testing.T) {
	t.Parallel()
	bus := events.NewBus()
	coord := NewConnectionRecoveryCoordinator("central1", bus)
	// No clearer wired.

	pipeline := []Pipeline{
		{Stage: hmenum.RecoveryStageDetecting, Run: coord.ClearJSONRPCSessionBeforeRecovery()},
	}
	result := coord.Run(context.Background(), "iface1", pipeline)
	if result != hmenum.RecoveryResultSuccess {
		t.Fatalf("expected success, got %v", result)
	}
}

// TestRecoveryAttemptedEventEmittedOnSuccess verifies that a successful
// recovery run emits a RecoveryAttemptedEvent with Success=true.
func TestRecoveryAttemptedEventEmittedOnSuccess(t *testing.T) {
	t.Parallel()
	bus := events.NewBus()
	var received []hmevent.RecoveryAttemptedEvent
	unsubscribe := events.Subscribe(bus, func(e hmevent.RecoveryAttemptedEvent) {
		received = append(received, e)
	})
	defer unsubscribe()

	coord := NewConnectionRecoveryCoordinator("central1", bus)
	pipeline := []Pipeline{
		{Stage: hmenum.RecoveryStageReconnecting, Run: func(_ context.Context) error { return nil }},
	}
	result := coord.Run(context.Background(), "iface1", pipeline)
	if result != hmenum.RecoveryResultSuccess {
		t.Fatalf("expected success, got %v", result)
	}
	if len(received) != 1 {
		t.Fatalf("expected 1 RecoveryAttemptedEvent, got %d", len(received))
	}
	ev := received[0]
	if ev.CentralName != "central1" {
		t.Errorf("CentralName = %q, want %q", ev.CentralName, "central1")
	}
	if ev.InterfaceID != "iface1" {
		t.Errorf("InterfaceID = %q, want %q", ev.InterfaceID, "iface1")
	}
	if !ev.Success {
		t.Error("expected Success=true")
	}
	if ev.AttemptNumber < 1 {
		t.Errorf("AttemptNumber = %d, want >= 1", ev.AttemptNumber)
	}
}

// TestRecoveryAttemptedEventEmittedOnFailure verifies that a failed recovery
// run emits a RecoveryAttemptedEvent with Success=false and a non-empty
// ErrorMessage.
func TestRecoveryAttemptedEventEmittedOnFailure(t *testing.T) {
	t.Parallel()
	bus := events.NewBus()
	var received []hmevent.RecoveryAttemptedEvent
	unsubscribe := events.Subscribe(bus, func(e hmevent.RecoveryAttemptedEvent) {
		received = append(received, e)
	})
	defer unsubscribe()

	coord := NewConnectionRecoveryCoordinator("central1", bus)
	pipeline := []Pipeline{
		{Stage: hmenum.RecoveryStageReconnecting, Run: func(_ context.Context) error {
			return errors.New("forced failure")
		}},
	}
	result := coord.Run(context.Background(), "iface1", pipeline)
	if result != hmenum.RecoveryResultFailed {
		t.Fatalf("expected failed, got %v", result)
	}
	if len(received) != 1 {
		t.Fatalf("expected 1 RecoveryAttemptedEvent, got %d", len(received))
	}
	ev := received[0]
	if ev.Success {
		t.Error("expected Success=false")
	}
	if ev.ErrorMessage == "" {
		t.Error("expected non-empty ErrorMessage on failure")
	}
}
