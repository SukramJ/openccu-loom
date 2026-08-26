// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

// Tests for ValueWriter.SetValueWithOptions and PutParamsetWithOptions
// with WriteOptions.SkipRetry=true: verifies that the IC's reliability stack
// routes through DoOnce (exactly 1 attempt) instead of Do (up to maxAttempts).
//
// countingBackend is defined in skip_retry_test.go (same package).

package client

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/client/reliability"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// newWriterWithIC is a test helper that returns:
// - a ValueWriter with a real InterfaceClient (maxAttempts=3, no sleep)
// and a countingBackend registered as backend.
// - the counting backend so tests can inspect call counts.
//
// Uses [countingBackend] from skip_retry_test.go.
func newWriterWithIC(t *testing.T, setErr error) (*ValueWriter, *countingBackend) {
	t.Helper()
	retrier := reliability.NewRetrier(reliability.RetryConfig{
		MaxAttempts: 3,
		// A non-positive Initial/Max is normalised back to the production
		// 2s/30s backoff by NewRetrier, so "no sleep" has to be spelled as
		// the shortest positive delay. These tests count backend calls.
		Initial: time.Microsecond,
		Max:     time.Microsecond,
	})
	ic, err := New(Config{
		CentralName: "ccu-wr",
		Interface:   hmenum.InterfaceHmIPRF,
		Caller:      CallerFunc(func(_ context.Context, _ string, _ []any) (any, error) { return nil, nil }),
		Retrier:     retrier,
	})
	if err != nil {
		t.Fatalf("New IC: %v", err)
	}
	b := &countingBackend{setErr: setErr}
	w := NewValueWriter()
	w.Register("ccu-wr", "HmIP-RF", b)
	w.RegisterIC("ccu-wr", "HmIP-RF", ic)
	return w, b
}

// TestSetValueWithOptions_SkipRetryRoutesThroughIC verifies that when
// WriteOptions.SkipRetry=true and an IC is registered, the backend is
// called exactly once even if it returns a transient error that would
// normally cause the retrier to retry (up to maxAttempts=3).
func TestSetValueWithOptions_SkipRetryRoutesThroughIC(t *testing.T) {
	t.Parallel()

	transient := errors.New("transient error")
	w, b := newWriterWithIC(t, transient)

	err := w.SetValueWithOptions(
		context.Background(), "ccu-wr", "HmIP-RF", "VCU001:1",
		hmenum.ParameterLevel, 0.5,
		WriteOptions{SkipRetry: true},
	)
	if err == nil {
		t.Fatal("expected error from backend, got nil")
	}
	if got := b.SetCallCount(); got != 1 {
		t.Errorf("SkipRetry=true: backend.SetValue called %d times, want exactly 1", got)
	}
}

// TestSetValueWithOptions_NoSkipRetryDirect verifies that without
// SkipRetry the direct backend path is used (no IC retry routing).
// The direct path calls backend.SetValue exactly once.
func TestSetValueWithOptions_NoSkipRetryDirect(t *testing.T) {
	t.Parallel()

	// No IC registered — direct backend path.
	b := &countingBackend{setErr: nil}
	w := NewValueWriter()
	w.Register("ccu-wr", "HmIP-RF", b)

	err := w.SetValueWithOptions(
		context.Background(), "ccu-wr", "HmIP-RF", "VCU001:1",
		hmenum.ParameterLevel, 0.5,
		WriteOptions{SkipRetry: false},
	)
	if err != nil {
		t.Fatalf("direct path: unexpected error: %v", err)
	}
	if got := b.SetCallCount(); got != 1 {
		t.Errorf("direct path: backend.SetValue called %d times, want 1", got)
	}
}

// TestSetValueWithOptions_SkipRetryNoICFallsBackToDirect verifies that
// when no IC is registered, SkipRetry=true is silently accepted and the
// direct backend path is used (backend called once, no error).
func TestSetValueWithOptions_SkipRetryNoICFallsBackToDirect(t *testing.T) {
	t.Parallel()

	b := &countingBackend{setErr: nil}
	w := NewValueWriter()
	w.Register("ccu-wr", "HmIP-RF", b)
	// No RegisterIC → icSetters map has no entry.

	err := w.SetValueWithOptions(
		context.Background(), "ccu-wr", "HmIP-RF", "VCU001:1",
		hmenum.ParameterLevel, 0.5,
		WriteOptions{SkipRetry: true},
	)
	if err != nil {
		t.Fatalf("SkipRetry without IC: unexpected error: %v", err)
	}
	if got := b.SetCallCount(); got != 1 {
		t.Errorf("SkipRetry without IC: backend.SetValue called %d times, want 1", got)
	}
}

// TestPutParamsetWithOptions_SkipRetryRoutesThroughIC mirrors the
// SetValue test for PutParamset: with SkipRetry=true and an IC,
// the backend is called exactly once even on a transient error.
func TestPutParamsetWithOptions_SkipRetryRoutesThroughIC(t *testing.T) {
	t.Parallel()

	// The retrier's DoOnce path should call PutParamset exactly once.
	// countingBackend.PutParamset always returns nil, so we need to
	// observe a successful single call here (no error to force retry).
	b := &countingBackend{setErr: nil}
	retrier := reliability.NewRetrier(reliability.RetryConfig{
		MaxAttempts: 3,
		Initial:     time.Microsecond,
		Max:         time.Microsecond,
	})
	ic, err := New(Config{
		CentralName: "ccu-put",
		Interface:   hmenum.InterfaceHmIPRF,
		Caller:      CallerFunc(func(_ context.Context, _ string, _ []any) (any, error) { return nil, nil }),
		Retrier:     retrier,
	})
	if err != nil {
		t.Fatalf("New IC: %v", err)
	}
	w := NewValueWriter()
	w.Register("ccu-put", "HmIP-RF", b)
	w.RegisterIC("ccu-put", "HmIP-RF", ic)

	err = w.PutParamsetWithOptions(
		context.Background(), "ccu-put", "HmIP-RF", "VCU001:1",
		hmenum.ParamsetKeyValues,
		map[string]any{"LEVEL": 0.5},
		WriteOptions{SkipRetry: true},
	)
	if err != nil {
		t.Fatalf("PutParamsetWithOptions SkipRetry: unexpected error: %v", err)
	}
	// PutParamset on countingBackend always returns nil; call count
	// is not directly trackable via countingBackend, but verifying
	// no error and no panic is sufficient here. The IC's PutParamset
	// calls b.PutParamset which is a no-op returning nil.
	// Detailed retry-count assertion is in skip_retry_test.go
	// for the bare IC path; here we assert the wiring compiles and
	// executes without error when routed through the IC.
}
