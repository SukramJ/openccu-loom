// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package reliability

import (
	"context"
	"errors"
	"testing"
)

// TestRetrier_IncidentSink_CalledOnExhaustion verifies that the configured
// IncidentSink receives exactly one call when the retry chain exhausts all
// attempts.
func TestRetrier_IncidentSink_CalledOnExhaustion(t *testing.T) {
	t.Parallel()

	var sinkErrors []error
	sink := IncidentSinkFunc(func(err error) {
		sinkErrors = append(sinkErrors, err)
	})

	r := NewRetrier(RetryConfig{
		MaxAttempts:  2,
		Initial:      0,
		IncidentSink: sink,
	})

	sentinel := errors.New("boom")
	_ = r.Do(context.Background(), func(_ context.Context, _ int) error {
		return sentinel
	})

	if len(sinkErrors) != 1 {
		t.Fatalf("expected 1 sink call, got %d", len(sinkErrors))
	}
	if !errors.Is(sinkErrors[0], sentinel) {
		t.Errorf("sink error = %v, want wrapped %v", sinkErrors[0], sentinel)
	}
}

// TestRetrier_IncidentSink_NotCalledOnSuccess verifies the sink is silent
// when the function eventually succeeds.
func TestRetrier_IncidentSink_NotCalledOnSuccess(t *testing.T) {
	t.Parallel()

	var calls int
	sink := IncidentSinkFunc(func(_ error) { calls++ })

	r := NewRetrier(RetryConfig{
		MaxAttempts:  3,
		Initial:      0,
		IncidentSink: sink,
	})

	attempt := 0
	_ = r.Do(context.Background(), func(_ context.Context, _ int) error {
		attempt++
		if attempt < 2 {
			return errors.New("transient")
		}
		return nil
	})

	if calls != 0 {
		t.Fatalf("sink called %d times on eventual success, want 0", calls)
	}
}

// TestRetrier_IncidentSink_NilSinkIsSafe confirms the Retrier works without
// a configured IncidentSink (the nil path must not panic).
func TestRetrier_IncidentSink_NilSinkIsSafe(t *testing.T) {
	t.Parallel()

	r := NewRetrier(RetryConfig{
		MaxAttempts: 2,
		Initial:     0,
	})
	err := r.Do(context.Background(), func(_ context.Context, _ int) error {
		return errors.New("always fails")
	})
	if err == nil {
		t.Fatal("expected error from exhausted retrier, got nil")
	}
}

// TestWireRetryIncidents_RecordsExhaustion checks that WireRetryIncidents
// forwards exhausted errors to the underlying IncidentRecorder.
func TestWireRetryIncidents_RecordsExhaustion(t *testing.T) {
	t.Parallel()

	var recorded []IncidentRecord
	rec := &fakeIncidentRecorder{records: &recorded}

	sink := WireRetryIncidents(rec, "central-1", "HmIP-RF")
	if sink == nil {
		t.Fatal("WireRetryIncidents returned nil for non-nil recorder")
	}
	sentinel := errors.New("rpc timeout")
	sink.ReportRetryExhausted(sentinel)

	if len(recorded) != 1 {
		t.Fatalf("expected 1 recorded incident, got %d", len(recorded))
	}
	got := recorded[0]
	if got.CentralName != "central-1" {
		t.Errorf("CentralName = %q, want %q", got.CentralName, "central-1")
	}
	if got.InterfaceID != "HmIP-RF" {
		t.Errorf("InterfaceID = %q, want %q", got.InterfaceID, "HmIP-RF")
	}
}

// TestWireRetryIncidents_NilRecorderReturnsNil verifies the nil-safe path.
func TestWireRetryIncidents_NilRecorderReturnsNil(t *testing.T) {
	t.Parallel()
	if WireRetryIncidents(nil, "c", "i") != nil {
		t.Fatal("WireRetryIncidents(nil, …) must return nil")
	}
}

// fakeIncidentRecorder implements IncidentRecorder for tests.
type fakeIncidentRecorder struct {
	records *[]IncidentRecord
}

func (f *fakeIncidentRecorder) RecordIncident(_ context.Context, inc IncidentRecord) error {
	*f.records = append(*f.records, inc)
	return nil
}
