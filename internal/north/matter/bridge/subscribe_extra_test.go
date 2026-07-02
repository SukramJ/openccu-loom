// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package bridge

// White-box tests for resolveSessionFabric, SubscriptionReporter,
// and SubscriptionEventReporter. Lives in package bridge to access
// unexported methods.

import (
	"context"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/north/matter/im"
	"github.com/SukramJ/openccu-loom/internal/north/matter/im/subscription"
	"github.com/SukramJ/openccu-loom/internal/north/matter/secure/channel"
)

// fabricResolvingLookup implements both SessionLookup and SessionFabricResolver.
type fabricResolvingLookup struct {
	fabricMap map[uint16]uint8
}

func (f fabricResolvingLookup) Lookup(_ uint16) (*channel.Session, bool) { return nil, false }
func (f fabricResolvingLookup) FabricFor(sessionID uint16) (uint8, bool) {
	idx, ok := f.fabricMap[sessionID]
	return idx, ok
}

// ─── resolveSessionFabric ────────────────────────────────────────────────────

func TestResolveSessionFabric_ZeroSessionID_ReturnsZero(t *testing.T) {
	t.Parallel()
	b := newStartedBridge(t)
	if idx := b.resolveSessionFabric(0); idx != 0 {
		t.Errorf("sessionID=0: want 0, got %d", idx)
	}
}

func TestResolveSessionFabric_NoResolver_ReturnsZero(t *testing.T) {
	t.Parallel()
	b := newStartedBridge(t)
	// noopSessionLookup does not implement SessionFabricResolver.
	b.AttachSessionLookup(nil) // revert to noop
	if idx := b.resolveSessionFabric(7); idx != 0 {
		t.Errorf("noop lookup: want 0, got %d", idx)
	}
}

func TestResolveSessionFabric_WithResolver_ReturnsCorrectFabric(t *testing.T) {
	t.Parallel()
	b := newStartedBridge(t)
	b.AttachSessionLookup(fabricResolvingLookup{
		fabricMap: map[uint16]uint8{5: 3},
	})
	if idx := b.resolveSessionFabric(5); idx != 3 {
		t.Errorf("sessionID=5: want fabricIndex=3, got %d", idx)
	}
}

func TestResolveSessionFabric_WithResolver_UnknownSession_ReturnsZero(t *testing.T) {
	t.Parallel()
	b := newStartedBridge(t)
	b.AttachSessionLookup(fabricResolvingLookup{
		fabricMap: map[uint16]uint8{5: 3},
	})
	if idx := b.resolveSessionFabric(99); idx != 0 {
		t.Errorf("unknown sessionID: want 0, got %d", idx)
	}
}

// ─── SubscriptionReporter / SubscriptionEventReporter ────────────────────────

func TestBridge_SubscriptionReporter_NotNil(t *testing.T) {
	t.Parallel()
	b := newStartedBridge(t)
	if r := b.SubscriptionReporter(); r == nil {
		t.Error("SubscriptionReporter() returned nil")
	}
}

func TestBridge_SubscriptionEventReporter_NotNil(t *testing.T) {
	t.Parallel()
	b := newStartedBridge(t)
	if r := b.SubscriptionEventReporter(); r == nil {
		t.Error("SubscriptionEventReporter() returned nil")
	}
}

// ─── reportSubscriptionEvents ────────────────────────────────────────────────

// TestReportSubscriptionEvents_NilSub_IsNoop verifies that calling
// reportSubscriptionEvents with a nil subscription returns without panicking.
func TestReportSubscriptionEvents_NilSub_IsNoop(t *testing.T) {
	t.Parallel()
	b := newStartedBridge(t)
	// nil sub → early return
	b.reportSubscriptionEvents(context.Background(), nil, nil)
}

// TestReportSubscriptionEvents_EmptyEvents_IsNoop verifies that an empty
// event list returns early without panicking.
func TestReportSubscriptionEvents_EmptyEvents_IsNoop(t *testing.T) {
	t.Parallel()
	b := newStartedBridge(t)
	// non-nil sub, empty events → early return
	b.reportSubscriptionEvents(context.Background(), &subscription.Subscription{}, nil)
}

// TestReportSubscriptionEvents_UnknownSubID_IsNoop verifies that when the
// subTarget is not registered, the function returns silently.
func TestReportSubscriptionEvents_UnknownSubID_IsNoop(t *testing.T) {
	t.Parallel()
	b := newStartedBridge(t)
	// Sub ID not in subTargets → Load returns ok=false → return
	b.reportSubscriptionEvents(context.Background(), &subscription.Subscription{ID: 99999},
		[]im.EventReport{{Path: im.ConcreteEventPath{Endpoint: 1}}})
}

// ─── releaseReportCounter ───────────────────────────────────────────────────

// ─── reportSubscription ──────────────────────────────────────────────────────

// TestReportSubscription_NilSub_IsNoop verifies that nil sub returns early.
func TestReportSubscription_NilSub_IsNoop(t *testing.T) {
	t.Parallel()
	b := newStartedBridge(t)
	b.reportSubscription(context.Background(), nil, nil)
}

// TestReportSubscription_UnknownSubID_IsNoop verifies that missing
// subTarget returns early.
func TestReportSubscription_UnknownSubID_IsNoop(t *testing.T) {
	t.Parallel()
	b := newStartedBridge(t)
	b.reportSubscription(context.Background(), &subscription.Subscription{ID: 77777},
		[]im.ConcreteAttributePath{{Endpoint: 0, HasEndpoint: true}})
}

// ─── closeSubscriptionByCounter ─────────────────────────────────────────────

// TestCloseSubscriptionByCounter_WrongType_IsNoop verifies that if the
// stored value is not a uint32 (wrong type assertion), the function returns
// without panicking.
func TestCloseSubscriptionByCounter_WrongType_IsNoop(t *testing.T) {
	t.Parallel()
	b := newStartedBridge(t)
	// Store a string instead of uint32 to force the type-assertion failure.
	b.reportCounterOwner.Store(reportCounterKey(0, 55), "not-a-uint32")
	b.closeSubscriptionByCounter(0, 55)
}

// TestCloseSubscriptionByCounter_ZeroSubID_IsNoop verifies that a stored
// subID of 0 does not proceed to delete or close anything.
func TestCloseSubscriptionByCounter_ZeroSubID_IsNoop(t *testing.T) {
	t.Parallel()
	b := newStartedBridge(t)
	b.reportCounterOwner.Store(reportCounterKey(0, 66), uint32(0))
	b.closeSubscriptionByCounter(0, 66)
	// entry must be gone
	if _, ok := b.reportCounterOwner.Load(reportCounterKey(0, 66)); ok {
		t.Error("reportCounterOwner still has entry after closeSubscriptionByCounter with subID=0")
	}
}

// TestReleaseReportCounter_ZeroIsNoop verifies that calling
// releaseReportCounter(0) is a no-op and does not panic.
func TestReleaseReportCounter_ZeroIsNoop(t *testing.T) {
	t.Parallel()
	b := newStartedBridge(t)
	// Pre-store something to confirm nothing is deleted for counter=0.
	b.reportCounterOwner.Store(reportCounterKey(0, 1), uint32(42))
	b.releaseReportCounter(0, 0) // must not panic, must not touch counter=1
	if _, ok := b.reportCounterOwner.Load(reportCounterKey(0, 1)); !ok {
		t.Error("releaseReportCounter(0) deleted an unrelated counter entry")
	}
}

// TestReleaseReportCounter_NonZeroDeletesEntry verifies that a non-zero
// counter is deleted from the map.
func TestReleaseReportCounter_NonZeroDeletesEntry(t *testing.T) {
	t.Parallel()
	b := newStartedBridge(t)
	b.reportCounterOwner.Store(reportCounterKey(0, 99), uint32(7))
	b.releaseReportCounter(0, 99)
	if _, ok := b.reportCounterOwner.Load(reportCounterKey(0, 99)); ok {
		t.Error("releaseReportCounter(99): entry still present, expected deleted")
	}
}
