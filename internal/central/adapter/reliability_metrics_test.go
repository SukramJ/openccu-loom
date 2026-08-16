// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package adapter

import (
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/client"
	"github.com/SukramJ/openccu-loom/internal/metrics"
)

// TestRPCOutcomeHookFillsTheRPCAndServiceSections pins that the outcomes
// a client reports arrive in the sections the diagnostics dump renders.
//
// Both sections used to read zero on every deployment: nothing fed the
// observer from the client side, so a daemon that had failed every single
// call reported no failures, no rejections and no service calls — the
// same numbers a daemon that had never been asked to do anything reports.
func TestRPCOutcomeHookFillsTheRPCAndServiceSections(t *testing.T) {
	t.Parallel()

	unit, err := central.New(central.Config{Name: "ccu1"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	hook := newRPCOutcomeHook(unit, "ccu1-HmIP-RF")
	if hook == nil {
		t.Fatal("newRPCOutcomeHook returned nil for a live central")
	}

	// The aggregator is attached AFTER the hook was built, as boot does:
	// the clients come up before the metrics wiring runs. A hook that
	// captured the observer instead of resolving it would be permanently
	// blind to everything from here on.
	unit.SetAggregator(metrics.NewAggregator("ccu1", metrics.NewObserver()))

	hook("getValue", 12*time.Millisecond, client.RPCOutcomeSuccess)
	hook("getValue", 20*time.Millisecond, client.RPCOutcomeFailed)
	hook("setValue", 5*time.Millisecond, client.RPCOutcomeRejected)

	rpc := unit.Aggregator.RPC()
	if rpc.FailedRequests != 1 {
		t.Errorf("failed_requests = %d, want 1", rpc.FailedRequests)
	}
	if rpc.RejectedRequests != 1 {
		t.Errorf("rejected_requests = %d, want 1", rpc.RejectedRequests)
	}

	svc := unit.Aggregator.Services()
	// The rejected call never reached the CCU, so it is not a service call.
	if svc.TotalCalls != 2 {
		t.Errorf("service total_calls = %d, want 2 (the rejection is not a call)", svc.TotalCalls)
	}
	if svc.TotalErrors != 1 {
		t.Errorf("service total_errors = %d, want 1", svc.TotalErrors)
	}
	if _, ok := svc.ByMethod["getValue"]; !ok {
		t.Errorf("service by_method is missing getValue: %+v", svc.ByMethod)
	}
	if _, ok := svc.ByMethod["setValue"]; ok {
		t.Errorf("service by_method recorded a rejected call as setValue: %+v", svc.ByMethod)
	}
}

// A central without an aggregator must absorb outcomes silently: the
// clients are built before the metrics wiring runs, and a call in that
// window must not panic.
func TestRPCOutcomeHookIsSilentBeforeTheAggregatorIsAttached(t *testing.T) {
	t.Parallel()

	unit, err := central.New(central.Config{Name: "ccu1"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	newRPCOutcomeHook(unit, "ccu1-HmIP-RF")("getValue", time.Millisecond, client.RPCOutcomeFailed)
}
