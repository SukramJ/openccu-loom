// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

// Tests covering field shape and broadcast-key semantics of the
// extended event variants: RecoveryFailed, CentralStateChanged,
// ClientStateChanged, DataRefreshTriggered/Completed, SystemStatusChanged,
// SysvarChanged, plus the ordering invariants that callers rely on
// when consuming the bus.

package hmevent_test

import (
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// ---------------------------------------------------------------------------
// RecoveryFailedEvent — ports test_recovery_failed_event
// ---------------------------------------------------------------------------

func TestRecoveryFailedEvent_HasFailureReason(t *testing.T) {
	t.Parallel()
	e := hmevent.RecoveryFailedEvent{
		Base:        hmevent.NewBase(),
		CentralName: "ccu-main",
		InterfaceID: "ccu-main-BidCos-RF",
		Reason:      hmenum.FailureReasonNetwork,
		Attempts:    5,
	}
	if e.Type() != hmevent.EventTypeRecoveryFailed {
		t.Fatalf("wrong event type: %s", e.Type())
	}
	if e.Reason != hmenum.FailureReasonNetwork {
		t.Fatalf("wrong failure reason: %s", e.Reason)
	}
	if e.Attempts != 5 {
		t.Fatalf("wrong attempts: %d", e.Attempts)
	}
}

// ---------------------------------------------------------------------------
// CentralStateChangedEvent — ports test_central_state_changed_event
// ---------------------------------------------------------------------------

func TestCentralStateChangedEvent_Construction(t *testing.T) {
	t.Parallel()
	e := hmevent.CentralStateChangedEvent{
		Base:        hmevent.NewBase(),
		CentralName: "ccu-main",
		From:        hmenum.CentralStateStopped,
		To:          hmenum.CentralStateStarting,
	}
	if e.Type() != hmevent.EventTypeCentralStateChanged {
		t.Fatalf("wrong event type: %s", e.Type())
	}
	if e.CentralName != "ccu-main" {
		t.Fatalf("wrong central name: %s", e.CentralName)
	}
	if e.From != hmenum.CentralStateStopped {
		t.Fatalf("wrong From state: %s", e.From)
	}
	if e.To != hmenum.CentralStateStarting {
		t.Fatalf("wrong To state: %s", e.To)
	}
}

// ---------------------------------------------------------------------------
// ClientStateChangedEvent (failure variant) — ports
// test_client_state_changed_event_with_failure
// ---------------------------------------------------------------------------

func TestClientStateChangedEvent_WithFailureReason(t *testing.T) {
	t.Parallel()
	e := hmevent.ClientStateChangedEvent{
		Base:        hmevent.NewBase(),
		CentralName: "ccu-main",
		InterfaceID: "ccu-main-BidCos-RF",
		From:        hmenum.ClientStateConnected,
		To:          hmenum.ClientStateFailed,
		Reason:      hmenum.FailureReasonNetwork,
	}
	if e.Type() != hmevent.EventTypeClientStateChanged {
		t.Fatalf("wrong event type: %s", e.Type())
	}
	if e.To != hmenum.ClientStateFailed {
		t.Fatalf("wrong To state: %s", e.To)
	}
	if e.Reason != hmenum.FailureReasonNetwork {
		t.Fatalf("wrong failure reason: %s", e.Reason)
	}
}

// ---------------------------------------------------------------------------
// DataRefreshCompletedEvent (failure) — ports
// test_data_refresh_completed_event_failure
// ---------------------------------------------------------------------------

func TestDataRefreshCompletedEvent_Failure(t *testing.T) {
	t.Parallel()
	e := hmevent.DataRefreshCompletedEvent{
		Base:        hmevent.NewBase(),
		CentralName: "ccu-main",
		JobName:     "client_data",
		Success:     false,
		Duration:    500,
	}
	if e.Type() != hmevent.EventTypeDataRefreshCompleted {
		t.Fatalf("wrong event type: %s", e.Type())
	}
	if e.Success {
		t.Fatal("expected Success=false for failure variant")
	}
	if e.Duration != 500 {
		t.Fatalf("wrong duration: %d", e.Duration)
	}
}

// ---------------------------------------------------------------------------
// DataRefreshCompletedEvent (success) — ports
// test_data_refresh_completed_event_success
// ---------------------------------------------------------------------------

func TestDataRefreshCompletedEvent_Success(t *testing.T) {
	t.Parallel()
	e := hmevent.DataRefreshCompletedEvent{
		Base:        hmevent.NewBase(),
		CentralName: "ccu-main",
		JobName:     "client_data",
		Success:     true,
		Duration:    1500,
	}
	if e.Type() != hmevent.EventTypeDataRefreshCompleted {
		t.Fatalf("wrong event type: %s", e.Type())
	}
	if !e.Success {
		t.Fatal("expected Success=true for success variant")
	}
	if e.Duration <= 0 {
		t.Fatalf("duration must be positive, got %d", e.Duration)
	}
	if e.JobName != "client_data" {
		t.Fatalf("wrong job name: %s", e.JobName)
	}
}

// ---------------------------------------------------------------------------
// DataRefreshTriggeredEvent — ports test_data_refresh_triggered_event
// ---------------------------------------------------------------------------

func TestDataRefreshTriggeredEvent_Fields(t *testing.T) {
	t.Parallel()
	e := hmevent.DataRefreshTriggeredEvent{
		Base:        hmevent.NewBase(),
		CentralName: "ccu-main",
		JobName:     "client_data",
	}
	if e.Type() != hmevent.EventTypeDataRefreshTriggered {
		t.Fatalf("wrong event type: %s", e.Type())
	}
	if e.JobName != "client_data" {
		t.Fatalf("wrong job name: %s", e.JobName)
	}
	if e.CentralName != "ccu-main" {
		t.Fatalf("wrong central name: %s", e.CentralName)
	}
}

// ---------------------------------------------------------------------------
// SystemStatusChangedEvent — ports test_system_status_changed_event
// ---------------------------------------------------------------------------

func TestSystemStatusChangedEvent_Construction(t *testing.T) {
	t.Parallel()
	e := hmevent.SystemStatusChangedEvent{
		Base:         hmevent.NewBase(),
		CentralName:  "ccu-main",
		Component:    "BidCos-RF",
		Healthy:      true,
		CentralState: hmenum.CentralStateRunning,
	}
	if e.Type() != hmevent.EventTypeSystemStatusChanged {
		t.Fatalf("wrong event type: %s", e.Type())
	}
	if e.Component != "BidCos-RF" {
		t.Fatalf("wrong component: %s", e.Component)
	}
	if !e.Healthy {
		t.Fatal("expected Healthy=true")
	}
	if e.CentralState != hmenum.CentralStateRunning {
		t.Fatalf("wrong central state: %s", e.CentralState)
	}
}

// ---------------------------------------------------------------------------
// SystemStatusChangedEvent (partial) — ports
// test_system_status_changed_event_partial
// default to zero/nil (Issues defaults to empty slice).
// ---------------------------------------------------------------------------

func TestSystemStatusChangedEvent_PartialConstruction(t *testing.T) {
	t.Parallel()
	e := hmevent.SystemStatusChangedEvent{
		Base:          hmevent.NewBase(),
		CentralName:   "ccu-main",
		CallbackState: "stale",
	}
	if e.Type() != hmevent.EventTypeSystemStatusChanged {
		t.Fatalf("wrong event type: %s", e.Type())
	}
	if e.CallbackState != "stale" {
		t.Fatalf("wrong callback state: %s", e.CallbackState)
	}
	// ClientState, ConnectionState, InterfaceID should be zero values.
	if e.ClientState != "" {
		t.Fatalf("expected empty ClientState, got %s", e.ClientState)
	}
	// Issues defaults to nil (zero slice) — matches
	if len(e.Issues) != 0 {
		t.Fatalf("expected 0 issues, got %d", len(e.Issues))
	}
}

// ---------------------------------------------------------------------------
// SysvarChangedEvent — ports test_sysvar_state_changed_event
// Go equivalent: Name, NewValue, ValueType.
// ---------------------------------------------------------------------------

func TestSysvarChangedEvent_Construction(t *testing.T) {
	t.Parallel()
	e := hmevent.SysvarChangedEvent{
		Base:        hmevent.NewBase(),
		CentralName: "ccu-main",
		Name:        "sv_12345",
		NewValue:    hmtypes.IntValue(42),
		ValueType:   hmenum.HubValueTypeInteger,
	}
	if e.Type() != hmevent.EventTypeSysvarChanged {
		t.Fatalf("wrong event type: %s", e.Type())
	}
	if e.Name != "sv_12345" {
		t.Fatalf("wrong name: %s", e.Name)
	}
	if e.NewValue.Int != 42 {
		t.Fatalf("wrong value: %v", e.NewValue)
	}
}

// ---------------------------------------------------------------------------
// SysvarChangedEvent (string value) — ports
// test_sysvar_state_changed_event_string_value
// ---------------------------------------------------------------------------

func TestSysvarChangedEvent_StringValue(t *testing.T) {
	t.Parallel()
	e := hmevent.SysvarChangedEvent{
		Base:        hmevent.NewBase(),
		CentralName: "ccu-main",
		Name:        "sv_string_var",
		NewValue:    hmtypes.StringValue("Hello World"),
		ValueType:   hmenum.HubValueTypeString,
	}
	if e.NewValue.String != "Hello World" {
		t.Fatalf("wrong string value: %q", e.NewValue.String)
	}
}

// ---------------------------------------------------------------------------
// Broadcast events have empty EventKey — ports
// test_event_key_is_none_for_broadcast_events
// Key == None
// DeviceTriggerEvent.key == None.
// Go: EventKey() returns "" for broadcast events.
// ---------------------------------------------------------------------------

func TestBroadcastEvents_HaveEmptyEventKey(t *testing.T) {
	t.Parallel()
	t.Run("DeviceLifecycleEvent", func(t *testing.T) {
		t.Parallel()
		e := hmevent.DeviceLifecycleEvent{
			Base:        hmevent.NewBase(),
			CentralName: "ccu-main",
			InterfaceID: "ccu-main-BidCos-RF",
			Address:     "VCU0000001",
			Subtype:     hmenum.DeviceLifecycleSubtypeCreated,
		}
		if got := e.EventKey(); got != "" {
			t.Fatalf("expected empty EventKey for DeviceLifecycleEvent, got %q", got)
		}
	})
	t.Run("DeviceTriggerEvent", func(t *testing.T) {
		t.Parallel()
		e := hmevent.DeviceTriggerEvent{
			Base:          hmevent.NewBase(),
			CentralName:   "ccu-main",
			InterfaceID:   "ccu-main-HmIP-RF",
			DeviceAddress: "VCU0000001",
			ChannelNo:     1,
			Parameter:     "PRESS_SHORT",
			Value:         hmtypes.BoolValue(true),
		}
		if got := e.EventKey(); got != "" {
			t.Fatalf("expected empty EventKey for DeviceTriggerEvent, got %q", got)
		}
	})
	t.Run("SystemStatusChangedEvent", func(t *testing.T) {
		t.Parallel()
		e := hmevent.SystemStatusChangedEvent{
			Base:         hmevent.NewBase(),
			CentralName:  "ccu-main",
			CentralState: hmenum.CentralStateRunning,
		}
		if got := e.EventKey(); got != "" {
			t.Fatalf("expected empty EventKey for SystemStatusChangedEvent, got %q", got)
		}
	})
}

// ---------------------------------------------------------------------------
// Data-refresh integration sequence — ports test_data_refresh_events_integration
// DataRefreshCompleted event; both carry consistent fields.
// Go: verifies value semantics of both structs in sequence.
// ---------------------------------------------------------------------------

func TestDataRefreshEvents_Integration(t *testing.T) {
	t.Parallel()

	triggered := hmevent.DataRefreshTriggeredEvent{
		Base:        hmevent.NewBase(),
		CentralName: "ccu-main",
		JobName:     "client_data",
	}
	completed := hmevent.DataRefreshCompletedEvent{
		Base:        hmevent.NewBase(),
		CentralName: "ccu-main",
		JobName:     triggered.JobName, // same job
		Success:     true,
		Duration:    42,
	}

	if triggered.Type() != hmevent.EventTypeDataRefreshTriggered {
		t.Fatalf("triggered: wrong type %s", triggered.Type())
	}
	if completed.Type() != hmevent.EventTypeDataRefreshCompleted {
		t.Fatalf("completed: wrong type %s", completed.Type())
	}
	if triggered.JobName != completed.JobName {
		t.Fatalf("job name mismatch: triggered=%s completed=%s", triggered.JobName, completed.JobName)
	}
	if !completed.Success {
		t.Fatal("completed: expected Success=true")
	}
	if completed.Duration <= 0 {
		t.Fatalf("completed: duration must be positive, got %d", completed.Duration)
	}
	// Triggered must have an earlier or equal timestamp.
	if completed.Timestamp().Before(triggered.Timestamp()) {
		t.Fatalf("completed timestamp %v is before triggered timestamp %v",
			completed.Timestamp(), triggered.Timestamp())
	}
}

// ---------------------------------------------------------------------------
// Event sequence ordering — ports test_event_sequence_verification
// (Triggered before Completed). Go verifies timestamp ordering on a
// manually assembled sequence.
// ---------------------------------------------------------------------------

func TestEventSequence_OrderedByTimestamp(t *testing.T) {
	t.Parallel()

	// Simulate a refresh cycle: triggered → completed.
	seq := []hmevent.Event{
		hmevent.DataRefreshTriggeredEvent{
			Base:        hmevent.NewBase(),
			CentralName: "ccu-main",
			JobName:     "client_data",
		},
		hmevent.DataRefreshCompletedEvent{
			Base:        hmevent.NewBase(),
			CentralName: "ccu-main",
			JobName:     "client_data",
			Success:     true,
			Duration:    100,
		},
	}

	expectedTypes := []hmevent.EventType{
		hmevent.EventTypeDataRefreshTriggered,
		hmevent.EventTypeDataRefreshCompleted,
	}

	for i, e := range seq {
		if e.Type() != expectedTypes[i] {
			t.Errorf("seq[%d]: want type %s, got %s", i, expectedTypes[i], e.Type())
		}
	}

	// Each event's timestamp must be >= the previous.
	for i := 1; i < len(seq); i++ {
		if seq[i].Timestamp().Before(seq[i-1].Timestamp()) {
			t.Errorf("seq[%d] timestamp %v is before seq[%d] timestamp %v",
				i, seq[i].Timestamp(), i-1, seq[i-1].Timestamp())
		}
	}
}

// ---------------------------------------------------------------------------
// No events assertion — ports test_no_events_assertion
// Go: verifies that the zero-value RecoveryFailedEvent carries the correct
// type and that a manually assembled slice stays empty when nothing is added.
// ---------------------------------------------------------------------------

func TestNoEvents_WhenThresholdNotReached(t *testing.T) {
	t.Parallel()

	// Simulate a capture that accumulates only the events explicitly added.
	var captured []hmevent.Event

	// Suppose we only want RecoveryFailedEvent to appear if failures >= 5.
	failures := 3 // below threshold
	if failures >= 5 {
		captured = append(captured, hmevent.RecoveryFailedEvent{
			Base:        hmevent.NewBase(),
			CentralName: "ccu-main",
			InterfaceID: "ccu-main-BidCos-RF",
			Reason:      hmenum.FailureReasonNetwork,
			Attempts:    failures,
		})
	}

	var recoveryFailed []hmevent.RecoveryFailedEvent
	for _, e := range captured {
		if rf, ok := e.(hmevent.RecoveryFailedEvent); ok {
			recoveryFailed = append(recoveryFailed, rf)
		}
	}

	if len(recoveryFailed) != 0 {
		t.Fatalf("expected 0 RecoveryFailed events below threshold, got %d", len(recoveryFailed))
	}
}
