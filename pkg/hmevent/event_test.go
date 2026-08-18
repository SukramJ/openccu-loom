// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package hmevent

import (
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// TestEventCatalogueTypesUniqueAndNonEmpty verifies every catalogued
// event carries a non-empty, catalogue-unique Type(). New events are
// added to the list when they land.
func TestEventCatalogueTypesUniqueAndNonEmpty(t *testing.T) {
	events := []Event{
		CentralStateChangedEvent{},
		ClientStateChangedEvent{},
		DataPointValueChangedEvent{},
		DataPointOptimisticRolledBackEvent{},
		DataPointSourceChangedEvent{},
		DeviceCreatedEvent{},
		DeviceRemovedEvent{},
		DeviceMetadataChangedEvent{},
		DeviceTriggerEvent{},
		LinkPeerChangedEvent{},
		ConnectionLostEvent{},
		CircuitBreakerStateChangedEvent{},
		HeartbeatTimerFiredEvent{},
		PingPongMismatchEvent{},
		RequestCoalescedEvent{},
		RecoveryStartedEvent{},
		RecoveryStageChangedEvent{},
		RecoveryCompletedEvent{},
		RecoveryFailedEvent{},
		ProgramExecutedEvent{},
		SysvarChangedEvent{},
		InstallModeChangedEvent{},
		SystemStatusChangedEvent{},
		ConnectivityChangedEvent{},
		DriftCorrectedEvent{},
		DataRefreshTriggeredEvent{},
		DataRefreshCompletedEvent{},
		// — additional event types
		DataFetchCompletedEvent{},
		RPCParameterReceivedEvent{},
		DeviceLifecycleEvent{},
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
