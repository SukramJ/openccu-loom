// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package hmreqctx_test

import (
	"context"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/pkg/hmreqctx"
)

func TestWithRequestContext_RoundTrip(t *testing.T) {
	rc := hmreqctx.RequestContext{
		RequestID: "test-id-123",
		Operation: "test_op",
		StartedAt: time.Now(),
	}
	ctx := hmreqctx.WithRequestContext(context.Background(), rc)
	got, ok := hmreqctx.FromContext(ctx)
	if !ok {
		t.Fatal("FromContext returned false, want true")
	}
	if got.RequestID != rc.RequestID {
		t.Errorf("RequestID = %q, want %q", got.RequestID, rc.RequestID)
	}
	if got.Operation != rc.Operation {
		t.Errorf("Operation = %q, want %q", got.Operation, rc.Operation)
	}
}

func TestFromContext_AbsentReturnsFalse(t *testing.T) {
	_, ok := hmreqctx.FromContext(context.Background())
	if ok {
		t.Error("FromContext on empty context should return false")
	}
}

func TestRequestIDFromContext(t *testing.T) {
	rc := hmreqctx.RequestContext{RequestID: "req-abc"}
	ctx := hmreqctx.WithRequestContext(context.Background(), rc)
	if got := hmreqctx.RequestIDFromContext(ctx); got != "req-abc" {
		t.Errorf("RequestIDFromContext = %q, want req-abc", got)
	}
}

func TestRequestIDFromContext_EmptyWhenAbsent(t *testing.T) {
	if got := hmreqctx.RequestIDFromContext(context.Background()); got != "" {
		t.Errorf("RequestIDFromContext on empty ctx = %q, want empty", got)
	}
}

func TestIsInService(t *testing.T) {
	if hmreqctx.IsInService(context.Background()) {
		t.Error("IsInService on empty ctx should be false")
	}
	rc := hmreqctx.RequestContext{RequestID: "svc-1"}
	ctx := hmreqctx.WithRequestContext(context.Background(), rc)
	if !hmreqctx.IsInService(ctx) {
		t.Error("IsInService with context should be true")
	}
}

func TestRequestContext_ElapsedMS(t *testing.T) {
	rc := hmreqctx.RequestContext{StartedAt: time.Now().Add(-100 * time.Millisecond)}
	ms := rc.ElapsedMS()
	if ms < 50 {
		t.Errorf("ElapsedMS = %.1f, want ≥ 50ms", ms)
	}
}

func TestRequestContext_WithDevice(t *testing.T) {
	rc := hmreqctx.RequestContext{RequestID: "x"}
	got := rc.WithDevice("HEQ0123456:1")
	if got.DeviceAddress != "HEQ0123456:1" {
		t.Errorf("WithDevice = %q, want HEQ0123456:1", got.DeviceAddress)
	}
	if got.RequestID != rc.RequestID {
		t.Error("WithDevice must not change RequestID")
	}
}

func TestRequestContext_WithOperation(t *testing.T) {
	rc := hmreqctx.RequestContext{Operation: "old"}
	got := rc.WithOperation("new_op")
	if got.Operation != "new_op" {
		t.Errorf("WithOperation = %q, want new_op", got.Operation)
	}
}

func TestRequestContext_WithExtra(t *testing.T) {
	rc := hmreqctx.RequestContext{Extra: map[string]any{"k1": "v1"}}
	got := rc.WithExtra(map[string]any{"k2": "v2"})
	if got.Extra["k1"] != "v1" || got.Extra["k2"] != "v2" {
		t.Errorf("WithExtra = %v, want both k1 and k2", got.Extra)
	}
}

func TestSetRequestContextForTesting(t *testing.T) {
	rc := hmreqctx.RequestContext{RequestID: "test-only"}
	ctx := hmreqctx.SetRequestContextForTesting(context.Background(), rc)
	got, ok := hmreqctx.FromContext(ctx)
	if !ok || got.RequestID != "test-only" {
		t.Error("SetRequestContextForTesting did not store context")
	}
}

func TestResetRequestContextForTesting(t *testing.T) {
	rc := hmreqctx.RequestContext{RequestID: "will-be-cleared"}
	ctx := hmreqctx.WithRequestContext(context.Background(), rc)
	ctx = hmreqctx.ResetRequestContextForTesting(ctx)
	_, ok := hmreqctx.FromContext(ctx)
	if ok {
		t.Error("After ResetRequestContextForTesting, FromContext should return false")
	}
}

// --------------------------------------------------------------------------
// InterfaceID field + WithInterfaceID (A7v4-01)
// --------------------------------------------------------------------------

func TestRequestContext_InterfaceIDField(t *testing.T) {
	rc := hmreqctx.RequestContext{
		RequestID:   "if-test",
		InterfaceID: "HmIP-RF",
	}
	if rc.InterfaceID != "HmIP-RF" {
		t.Errorf("InterfaceID = %q, want HmIP-RF", rc.InterfaceID)
	}
}

func TestRequestContext_WithInterfaceID(t *testing.T) {
	rc := hmreqctx.RequestContext{RequestID: "base"}
	got := rc.WithInterfaceID("CUxD")
	if got.InterfaceID != "CUxD" {
		t.Errorf("WithInterfaceID = %q, want CUxD", got.InterfaceID)
	}
	// Original must be unchanged.
	if rc.InterfaceID != "" {
		t.Errorf("original InterfaceID = %q, want empty (immutability)", rc.InterfaceID)
	}
}

func TestRequestContext_WithInterfaceID_PreservesOtherFields(t *testing.T) {
	rc := hmreqctx.RequestContext{
		RequestID: "preserve-me",
		Operation: "test_op",
		StartedAt: time.Now(),
	}
	got := rc.WithInterfaceID("BidCos-RF")
	if got.RequestID != rc.RequestID {
		t.Error("WithInterfaceID must not change RequestID")
	}
	if got.Operation != rc.Operation {
		t.Error("WithInterfaceID must not change Operation")
	}
}
