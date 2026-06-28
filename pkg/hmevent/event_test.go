// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package hmevent

import (
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// TestAll35EventTypes locks the event catalogue to the current count.
// Extensions must bump the count and add the new event to the list.
func TestAll35EventTypes(t *testing.T) {
	events := []Event{
		CentralStateChangedEvent{},
		ClientStateChangedEvent{},
		DataPointValueChangedEvent{},
		DataPointOptimisticRolledBackEvent{},
		DataPointStatusChangedEvent{},
		DataPointSourceChangedEvent{},
		DeviceCreatedEvent{},
		DeviceRemovedEvent{},
		DeviceTriggerEvent{},
		FirmwareStateChangedEvent{},
		LinkPeerChangedEvent{},
		ConnectionLostEvent{},
		CircuitBreakerStateChangedEvent{},
		CircuitBreakerTrippedEvent{},
		HeartbeatTimerFiredEvent{},
		PingPongMismatchEvent{},
		RequestCoalescedEvent{},
		RecoveryStartedEvent{},
		RecoveryStageChangedEvent{},
		RecoveryCompletedEvent{},
		RecoveryFailedEvent{},
		HealthRecordedEvent{},
		ProgramExecutedEvent{},
		SysvarChangedEvent{},
		InstallModeChangedEvent{},
		SystemStatusChangedEvent{},
		AlarmMessageEvent{},
		ServiceMessageEvent{},
		ConnectivityChangedEvent{},
		DriftCorrectedEvent{},
		DataRefreshTriggeredEvent{},
		DataRefreshCompletedEvent{},
		// — additional event types
		DataFetchCompletedEvent{},
		RPCParameterReceivedEvent{},
		DeviceLifecycleEvent{},
	}
	if len(events) != 35 {
		t.Fatalf("catalogue has %d events, want 35", len(events))
	}
	seen := make(map[EventType]struct{})
	for _, e := range events {
		if e.Type() == "" {
			t.Errorf("event %T returned empty Type()", e)
		}
		if _, dup := seen[e.Type()]; dup {
			t.Errorf("duplicate EventType %s", e.Type())
		}
		seen[e.Type()] = struct{}{}
	}
}

func TestNewBaseCarriesWallClock(t *testing.T) {
	before := time.Now()
	e := CentralStateChangedEvent{Base: NewBase()}
	after := time.Now()
	if e.Timestamp().Before(before) || e.Timestamp().After(after) {
		t.Fatalf("timestamp out of range: got=%v before=%v after=%v", e.Timestamp(), before, after)
	}
}

func TestNewBaseAtUsesFixedTime(t *testing.T) {
	ts := time.Unix(1_700_000_000, 0).UTC()
	e := CentralStateChangedEvent{Base: NewBaseAt(ts)}
	if !e.Timestamp().Equal(ts) {
		t.Fatalf("timestamp=%v, want %v", e.Timestamp(), ts)
	}
}

func TestDataPointValueChangedCarriesTypedValue(t *testing.T) {
	e := DataPointValueChangedEvent{
		Base:     NewBaseAt(time.Unix(1, 0)),
		Key:      hmtypes.DataPointKey{InterfaceID: "iface", ChannelAddress: "ABC:1", ParamsetKey: hmenum.ParamsetKeyValues, Parameter: "LEVEL"},
		OldValue: hmtypes.FloatValue(0.0),
		NewValue: hmtypes.FloatValue(0.5),
	}
	if e.NewValue.Float != 0.5 {
		t.Fatal("typed value lost")
	}
}
