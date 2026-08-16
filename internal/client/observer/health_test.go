// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package observer_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/client/observer"
	"github.com/SukramJ/openccu-loom/pkg/hmerr"
	"github.com/SukramJ/openccu-loom/pkg/interfaces"
)

// recordingTracker captures every RecordRequest call so the tests
// can assert on success / failure counts per component name. Thread-
// safe via the embedded mutex.
type recordingTracker struct {
	mu      sync.Mutex
	entries []trackEntry
}

type trackEntry struct {
	Name    string
	Success bool
}

func (r *recordingTracker) RecordRequest(name string, success bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.entries = append(r.entries, trackEntry{Name: name, Success: success})
}

func (r *recordingTracker) snapshot() []trackEntry {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]trackEntry, len(r.entries))
	copy(out, r.entries)
	return out
}

func TestHealth_HappyPath_RecordsSuccess(t *testing.T) {
	t.Parallel()
	rec := &recordingTracker{}
	h := observer.NewHealth(rec)

	span := h.OnRequestStart(context.Background(), interfaces.RequestInfo{
		Interface: "HmIP-RF",
		Method:    "listDevices",
	})
	h.OnRequestEnd(span, interfaces.RequestResult{Err: nil})

	got := rec.snapshot()
	if len(got) != 1 {
		t.Fatalf("entries=%d, want 1", len(got))
	}
	if got[0].Name != "HmIP-RF" || !got[0].Success {
		t.Fatalf("entry=%+v", got[0])
	}
}

func TestHealth_TransientFault_RecordsFailure(t *testing.T) {
	t.Parallel()
	rec := &recordingTracker{}
	h := observer.NewHealth(rec)

	// XMLRPCFaultGeneral (-1) IS retryable → counts as a real
	// transport failure on the interface health metric.
	transient := &hmerr.XMLRPCFault{Code: int(hmerr.XMLRPCFaultGeneral), Message: "Unreach"}
	span := h.OnRequestStart(context.Background(), interfaces.RequestInfo{Interface: "HmIP-RF"})
	h.OnRequestEnd(span, interfaces.RequestResult{Err: transient})

	got := rec.snapshot()
	if len(got) != 1 || got[0].Success {
		t.Fatalf("entry=%+v want success=false", got)
	}
}

func TestHealth_SemanticFault_RecordsSuccess(t *testing.T) {
	t.Parallel()
	rec := &recordingTracker{}
	h := observer.NewHealth(rec)

	// XMLRPCFaultInvalidParameter (-5) is NOT retryable — describes
	// the device, not the wire. The observer treats this as
	// success so a healthy CCU doesn't trip to "degraded" the
	// first time a write-only DP gets polled.
	semantic := &hmerr.XMLRPCFault{Code: int(hmerr.XMLRPCFaultInvalidParameter), Message: "Unknown Parameter"}
	span := h.OnRequestStart(context.Background(), interfaces.RequestInfo{Interface: "HmIP-RF"})
	h.OnRequestEnd(span, interfaces.RequestResult{Err: semantic})

	got := rec.snapshot()
	if len(got) != 1 || !got[0].Success {
		t.Fatalf("entry=%+v want success=true (semantic fault)", got)
	}
}

func TestHealth_NonXMLRPCError_RecordsFailure(t *testing.T) {
	t.Parallel()
	rec := &recordingTracker{}
	h := observer.NewHealth(rec)

	span := h.OnRequestStart(context.Background(), interfaces.RequestInfo{Interface: "HmIP-RF"})
	h.OnRequestEnd(span, interfaces.RequestResult{Err: errors.New("connection refused")})

	got := rec.snapshot()
	if len(got) != 1 || got[0].Success {
		t.Fatalf("entry=%+v want success=false (plain transport error)", got)
	}
}

func TestHealth_NilTracker_NoOp(t *testing.T) {
	t.Parallel()
	// nil tracker disables recording without panicking.
	h := observer.NewHealth(nil)
	span := h.OnRequestStart(context.Background(), interfaces.RequestInfo{Interface: "HmIP-RF"})
	h.OnRequestEnd(span, interfaces.RequestResult{Err: nil})
	// No panic = pass.
}

func TestHealth_WithComponentName_OverridesInterface(t *testing.T) {
	t.Parallel()
	rec := &recordingTracker{}
	h := observer.NewHealth(rec, observer.WithComponentName("hub-session"))

	span := h.OnRequestStart(context.Background(), interfaces.RequestInfo{
		Interface: "HmIP-RF", // overridden
		Method:    "listMethods",
	})
	h.OnRequestEnd(span, interfaces.RequestResult{Err: nil})

	got := rec.snapshot()
	if len(got) != 1 || got[0].Name != "hub-session" {
		t.Fatalf("entry=%+v want Name=hub-session", got)
	}
}

func TestHealth_EmptyInterfaceName_SkipsRecord(t *testing.T) {
	t.Parallel()
	rec := &recordingTracker{}
	h := observer.NewHealth(rec)

	// Empty Interface + no override → healthSpan.iface == "" →
	// OnRequestEnd short-circuits without calling RecordRequest.
	span := h.OnRequestStart(context.Background(), interfaces.RequestInfo{Method: "listMethods"})
	h.OnRequestEnd(span, interfaces.RequestResult{Err: nil})

	if got := rec.snapshot(); len(got) != 0 {
		t.Fatalf("expected no record for empty iface, got %+v", got)
	}
}

func TestHealth_NilOption_Tolerated(t *testing.T) {
	t.Parallel()
	rec := &recordingTracker{}
	// nil HealthOption in the variadic list must not panic — the
	// option-applier skips nils.
	h := observer.NewHealth(rec, nil, observer.WithComponentName("x"))

	span := h.OnRequestStart(context.Background(), interfaces.RequestInfo{Interface: "HmIP-RF"})
	h.OnRequestEnd(span, interfaces.RequestResult{Err: nil})

	got := rec.snapshot()
	if len(got) != 1 || got[0].Name != "x" {
		t.Fatalf("entry=%+v want Name=x", got)
	}
}

func TestHealth_MismatchedSpan_NoOp(t *testing.T) {
	t.Parallel()
	rec := &recordingTracker{}
	h := observer.NewHealth(rec)

	// A non-healthSpan (e.g. nil from a buggy multi observer) must
	// not panic and must not produce a record.
	h.OnRequestEnd(nil, interfaces.RequestResult{Err: nil})

	if got := rec.snapshot(); len(got) != 0 {
		t.Fatalf("expected no record from nil span, got %+v", got)
	}
}
