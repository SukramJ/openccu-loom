// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package adapter

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/central/events"
	"github.com/SukramJ/openccu-loom/internal/client/reliability"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
)

// fakeRecorder is a test double for reliability.IncidentRecorder.
// It records every IncidentRecord it receives and returns a configurable error.
type fakeRecorder struct {
	err     error
	records []reliability.IncidentRecord
}

func (f *fakeRecorder) RecordIncident(_ context.Context, inc reliability.IncidentRecord) error {
	f.records = append(f.records, inc)
	return f.err
}

// TestPublishingIncidentRecorderNilInnerReturnsNil verifies that passing a nil
// inner recorder returns a nil recorder (persistence disabled).
func TestPublishingIncidentRecorderNilInnerReturnsNil(t *testing.T) {
	t.Parallel()
	reg := central.NewRegistry()
	got := NewPublishingIncidentRecorder(nil, reg)
	if got != nil {
		t.Fatalf("expected nil recorder, got %T", got)
	}
}

// TestPublishingIncidentRecorderPublishesOnSuccess verifies that a successful
// inner RecordIncident also publishes exactly one IncidentRecordedEvent on the
// matching central's event bus with fields mirroring the IncidentRecord.
func TestPublishingIncidentRecorderPublishesOnSuccess(t *testing.T) {
	t.Parallel()

	reg, unit := registryWithUnit(t, "ccu-publish")
	inner := &fakeRecorder{}
	rec := NewPublishingIncidentRecorder(inner, reg)

	inc := reliability.IncidentRecord{
		CentralName: "ccu-publish",
		InterfaceID: "HmIP-RF",
		Type:        hmenum.IncidentTypeConnectionLost,
		Severity:    hmenum.IncidentSeverityError,
		Message:     "connection dropped",
		Details:     "tcp: dial refused",
	}

	// Subscribe before calling RecordIncident. The event bus dispatches
	// synchronously on the publisher's goroutine, so the handler fires before
	// RecordIncident returns.
	received := make(chan hmevent.IncidentRecordedEvent, 1)
	unsub := events.Subscribe(unit.EventBus, func(e hmevent.IncidentRecordedEvent) {
		received <- e
	})
	defer unsub()

	if err := rec.RecordIncident(context.Background(), inc); err != nil {
		t.Fatalf("RecordIncident: %v", err)
	}

	// The bus is synchronous so the event is already in the channel.
	select {
	case got := <-received:
		if got.CentralName != inc.CentralName {
			t.Errorf("CentralName: got %q, want %q", got.CentralName, inc.CentralName)
		}
		if got.InterfaceID != inc.InterfaceID {
			t.Errorf("InterfaceID: got %q, want %q", got.InterfaceID, inc.InterfaceID)
		}
		if got.IncidentType != inc.Type {
			t.Errorf("IncidentType: got %q, want %q", got.IncidentType, inc.Type)
		}
		if got.Severity != inc.Severity {
			t.Errorf("Severity: got %q, want %q", got.Severity, inc.Severity)
		}
		if got.Message != inc.Message {
			t.Errorf("Message: got %q, want %q", got.Message, inc.Message)
		}
		if got.Details != inc.Details {
			t.Errorf("Details: got %q, want %q", got.Details, inc.Details)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for IncidentRecordedEvent")
	}

	// Exactly one event: channel must be empty now.
	select {
	case extra := <-received:
		t.Fatalf("received unexpected second event: %+v", extra)
	default:
	}

	// Inner recorder must have been called.
	if len(inner.records) != 1 {
		t.Fatalf("inner.records: got %d, want 1", len(inner.records))
	}
}

// TestPublishingIncidentRecorderNoPublishOnInnerError verifies that when the
// inner recorder returns an error, RecordIncident propagates that error and
// does not publish any event.
func TestPublishingIncidentRecorderNoPublishOnInnerError(t *testing.T) {
	t.Parallel()

	reg, unit := registryWithUnit(t, "ccu-fail")
	innerErr := errors.New("db full")
	inner := &fakeRecorder{err: innerErr}
	rec := NewPublishingIncidentRecorder(inner, reg)

	inc := reliability.IncidentRecord{
		CentralName: "ccu-fail",
		InterfaceID: "BidCos-RF",
		Type:        hmenum.IncidentTypeRPCError,
		Severity:    hmenum.IncidentSeverityWarning,
		Message:     "rpc fault",
	}

	received := make(chan hmevent.IncidentRecordedEvent, 1)
	unsub := events.Subscribe(unit.EventBus, func(e hmevent.IncidentRecordedEvent) {
		received <- e
	})
	defer unsub()

	err := rec.RecordIncident(context.Background(), inc)
	if !errors.Is(err, innerErr) {
		t.Fatalf("RecordIncident error: got %v, want %v", err, innerErr)
	}

	// No event should have been published.
	select {
	case got := <-received:
		t.Fatalf("unexpected event published on error: %+v", got)
	default:
	}
}

// TestPublishingIncidentRecorderUnknownCentralNoPublish verifies that when the
// IncidentRecord names a central not in the registry, RecordIncident still
// returns nil (persist succeeded) and does not panic.
func TestPublishingIncidentRecorderUnknownCentralNoPublish(t *testing.T) {
	t.Parallel()

	reg, _ := registryWithUnit(t, "ccu-known")
	inner := &fakeRecorder{}
	rec := NewPublishingIncidentRecorder(inner, reg)

	inc := reliability.IncidentRecord{
		CentralName: "ccu-unknown",
		InterfaceID: "HmIP-RF",
		Type:        hmenum.IncidentTypePingPongMismatch,
		Severity:    hmenum.IncidentSeverityInfo,
		Message:     "mismatch",
	}

	if err := rec.RecordIncident(context.Background(), inc); err != nil {
		t.Fatalf("RecordIncident with unknown central: %v", err)
	}

	// Inner was still called.
	if len(inner.records) != 1 {
		t.Fatalf("inner.records: got %d, want 1", len(inner.records))
	}
}

// TestPublishingIncidentRecorderNilRegistryNoPanic verifies that a nil registry
// does not cause a panic: the inner recorder is still called and the result
// is returned without attempting to publish.
func TestPublishingIncidentRecorderNilRegistryNoPanic(t *testing.T) {
	t.Parallel()

	inner := &fakeRecorder{}
	rec := NewPublishingIncidentRecorder(inner, nil)

	inc := reliability.IncidentRecord{
		CentralName: "ccu-x",
		Type:        hmenum.IncidentTypeRetryExhausted,
		Severity:    hmenum.IncidentSeverityWarning,
		Message:     "all retries exhausted",
	}

	if err := rec.RecordIncident(context.Background(), inc); err != nil {
		t.Fatalf("RecordIncident with nil registry: %v", err)
	}

	if len(inner.records) != 1 {
		t.Fatalf("inner.records: got %d, want 1", len(inner.records))
	}
}
