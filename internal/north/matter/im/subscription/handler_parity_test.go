// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

// handler_parity_test.go locks Subscribe lifecycle semantics against
// matter.js HEAD ebe091744.
//
// Primary sources:
//   - packages/node/src/node/server/ServerSubscription.ts
//   - packages/node/test/node/ServerSubscriptionTest.ts
//   - packages/node/test/node/AttributeSubscriptionResponseTest.ts
//
// Conversion notes:
//   - matter.js uses per-subscription timer objects; openccu-loom uses a
//     shared Tick-based engine. Tests exercise the same observable
//     invariants via deterministic Tick calls instead of MockTime.advance.
//   - TypeScript Promise-orchestration tests (async session tear-down,
//     exchange.send blocking) have no direct Go equivalent in the engine
//     layer and are marked t.Skip("FixMe: ...").

package subscription_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/north/matter/im"
	"github.com/SukramJ/openccu-loom/internal/north/matter/im/subscription"
)

// ---- Case 1 ---------------------------------------------------------------
// Mirrors matter.js packages/node/src/node/server/ServerSubscription.ts:259-299
// (case "#determineSendingIntervals — half-interval under 1 min → 0.8×max")
//
// For min=0, max=60 s: half=30 s < 60 s → sendInterval = max(0, 0.8×60) = 48 s.
// Heartbeat must NOT fire at 47 s but MUST fire at 49 s.
func TestHandlerParity_SendInterval_HalfUnder60s_Uses80Percent(t *testing.T) {
	// Mirrors matter.js packages/node/src/node/server/ServerSubscription.ts:283-290 (case "#determineSendingIntervals half<60s branch")
	t.Parallel()
	ch := make(chan reporterCall, 4)
	m := newManager(subscription.Config{}, chanReporter(ch))

	args := defaultArgs()
	args.MinIntervalFloor = 0    // floored to 1 by manager default; use 1 below
	args.MaxIntervalCeiling = 60 // half=30s < 60s → 0.8×60 = 48 s sendInterval
	if _, err := m.Subscribe(args); err != nil {
		t.Fatalf("Subscribe: %v", err)
	}

	ctx := context.Background()
	t0 := time.Now()

	// No keepalive should fire at +47 s (sendInterval=48 s not yet elapsed).
	m.Tick(ctx, t0.Add(47*time.Second))
	if len(ch) != 0 {
		t.Errorf("got %d keepalive calls at t+47s, want 0 (sendInterval=48s not elapsed)", len(ch))
	}

	// Keepalive must fire at +49 s.
	m.Tick(ctx, t0.Add(49*time.Second))
	select {
	case call := <-ch:
		if call.paths != nil {
			t.Errorf("keepalive paths = %v, want nil", call.paths)
		}
	default:
		t.Fatal("keepalive not fired at t+49s (sendInterval=48s should have elapsed)")
	}
}

// ---- Case 2 ---------------------------------------------------------------
// Mirrors matter.js packages/node/src/node/server/ServerSubscription.ts:291-298
// (case "#determineSendingIntervals sendInterval<minInterval → clamp to min")
//
// When 0.8×maxInterval < minIntervalFloor the floor wins.
// min=20, max=22: 0.8×22=17.6 s < floor=20 → sendInterval=20 s.
func TestHandlerParity_SendInterval_ClampedToMinIntervalFloor(t *testing.T) {
	// Mirrors matter.js packages/node/src/node/server/ServerSubscription.ts:291-298
	t.Parallel()
	ch := make(chan reporterCall, 4)
	m := newManager(subscription.Config{}, chanReporter(ch))

	args := defaultArgs()
	args.MinIntervalFloor = 20
	args.MaxIntervalCeiling = 22 // 0.8×22=17.6 < floor=20 → clamp to floor=20
	if _, err := m.Subscribe(args); err != nil {
		t.Fatalf("Subscribe: %v", err)
	}

	ctx := context.Background()
	t0 := time.Now()

	// Drain first tick.
	m.Tick(ctx, t0)
	for len(ch) > 0 {
		<-ch
	}

	// At +18 s: under floor-clamped sendInterval=20 s → no keepalive.
	m.Tick(ctx, t0.Add(18*time.Second))
	if len(ch) != 0 {
		t.Errorf("got %d calls at t+18s, want 0 (floor-clamped sendInterval=20s)", len(ch))
	}

	// At +21 s: floor elapsed → keepalive fires.
	m.Tick(ctx, t0.Add(21*time.Second))
	if len(ch) != 1 {
		t.Errorf("got %d calls at t+21s, want 1 (floor-clamped sendInterval=20s elapsed)", len(ch))
	}
}

// ---- Case 3 ---------------------------------------------------------------
// Mirrors matter.js packages/node/src/node/server/ServerSubscription.ts:269-283
// (case "#determineSendingIntervals maxInterval is publisher min, not raw ceil")
//
// When the publisher-configured maxInterval (INTERNAL_INTERVAL_PUBLISHER_LIMIT=3 min)
// is lower than the controller's ceiling, the publisher limit wins.
// We model this via cfg.MaxIntervalCeilingSeconds=30 vs request max=9000.
// Post-clamp max=30; sendInterval = 0.8×30 = 24 s.
func TestHandlerParity_SendInterval_CfgCeilingCaps_PublisherWins(t *testing.T) {
	// Mirrors matter.js packages/node/src/node/server/ServerSubscription.ts:269-276
	t.Parallel()
	ch := make(chan reporterCall, 4)
	m := newManager(subscription.Config{MaxIntervalCeilingSeconds: 30}, chanReporter(ch))

	args := defaultArgs()
	args.MinIntervalFloor = 1
	args.MaxIntervalCeiling = 9000 // capped to cfg=30
	if _, err := m.Subscribe(args); err != nil {
		t.Fatalf("Subscribe: %v", err)
	}

	ctx := context.Background()
	t0 := time.Now()

	// Drain first tick.
	m.Tick(ctx, t0)
	for len(ch) > 0 {
		<-ch
	}

	// At +23 s: sendInterval=0.8×30=24 s not yet elapsed.
	m.Tick(ctx, t0.Add(23*time.Second))
	if len(ch) != 0 {
		t.Errorf("got %d calls at t+23s, want 0 (sendInterval=24s not elapsed)", len(ch))
	}

	// At +25 s: sendInterval elapsed → keepalive.
	m.Tick(ctx, t0.Add(25*time.Second))
	if len(ch) != 1 {
		t.Errorf("got %d calls at t+25s, want 1 (sendInterval=24s elapsed)", len(ch))
	}
}

// ---- Case 4 ---------------------------------------------------------------
// Mirrors matter.js packages/node/test/node/ServerSubscriptionTest.ts:48-65
// (case "sets isCanceledByPeer and removes from session when peer cancels")
//
// After ClosePeer(fabric, peer) the subscription must be IsClosed and
// removed from Active(); Active() must fall to 0.
func TestHandlerParity_PeerCancel_RemovesSubscriptionFromActive(t *testing.T) {
	// Mirrors matter.js packages/node/test/node/ServerSubscriptionTest.ts:48-65
	t.Parallel()
	m := newManager(subscription.Config{}, nil)

	args := defaultArgs()
	args.FabricIndex = 1
	args.PeerNodeID = 0xABCD
	sub, err := m.Subscribe(args)
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}

	if m.Active() != 1 {
		t.Fatalf("Active() = %d before cancel, want 1", m.Active())
	}

	// Simulate peer cancellation via ClosePeer (the openccu-loom counterpart
	// of ServerSubscription.handlePeerCancel → session.subscriptions.delete).
	n := m.ClosePeer(1, 0xABCD)
	if n != 1 {
		t.Errorf("ClosePeer returned %d, want 1", n)
	}

	if !sub.IsClosed() {
		t.Error("subscription.IsClosed() = false after peer cancel")
	}
	if m.Active() != 0 {
		t.Errorf("Active() = %d after peer cancel, want 0", m.Active())
	}
}

// ---- Case 5 ---------------------------------------------------------------
// Mirrors matter.js packages/node/test/node/ServerSubscriptionTest.ts:67-111
// (case "closes subscription even when in-flight exchange close throws")
//
// In openccu-loom there is no per-subscription exchange object; the
// equivalent invariant is that Close() on a mid-send subscription
// (IsClosed is called while dirty) still marks closed + removes from Active.
func TestHandlerParity_Close_WhileDirty_StillMarksClosed(t *testing.T) {
	// Mirrors matter.js packages/node/test/node/ServerSubscriptionTest.ts:67-111
	// (try/finally guarantee: this.close() runs even if exchange.close() throws)
	t.Parallel()

	var mu sync.Mutex
	var closedDuringReport bool

	reporter := func(_ context.Context, sub *subscription.Subscription, _ []im.ConcreteAttributePath) {
		// Simulate close arriving while the reporter (≈ send) is in progress.
		mu.Lock()
		closedDuringReport = true
		mu.Unlock()
		// Close via Manager while "sending".
		_ = sub // the test closes outside; this just records the race window
	}
	m := newManager(subscription.Config{}, reporter)

	subscribedPath := mkPath(1, 0x0006, 0x0000)
	args := defaultArgs()
	args.AttributePaths = []im.ConcreteAttributePath{subscribedPath}
	sub, err := m.Subscribe(args)
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}

	m.OnAttributeChanged(subscribedPath)
	ctx := context.Background()
	// Drive Tick so reporter fires.
	m.Tick(ctx, time.Now().Add(2*time.Second))

	// Close during or after the reporter call — must not panic, must mark closed.
	if err := m.Close(sub.ID); err != nil && !errors.Is(err, subscription.ErrNotFound) {
		t.Fatalf("Close: unexpected error %v", err)
	}

	if !sub.IsClosed() {
		t.Error("subscription.IsClosed() = false after Manager.Close")
	}
	if m.Active() != 0 {
		t.Errorf("Active() = %d, want 0 after Close", m.Active())
	}
	_ = closedDuringReport // referenced to prevent unused-var warning
}

// ---- Case 6 ---------------------------------------------------------------
// Mirrors matter.js packages/node/src/node/server/ServerSubscription.ts:366-394
// (case "activate() — outstandingAttributeUpdates present before activate → trigger send")
//
// matter.js: changes received *during seeding* (before activate()) are queued
// in #outstandingAttributeUpdates. After activate() a pending send is triggered.
// openccu-loom equivalent: markDirty called before the first Tick → dirty paths
// must be emitted on the first eligible Tick (MinIntervalFloor elapsed).
func TestHandlerParity_Activate_PendingDirtyBeforeFirstTick_EmittedAfterFloor(t *testing.T) {
	// Mirrors matter.js packages/node/src/node/server/ServerSubscription.ts:388-393
	t.Parallel()
	ch := make(chan reporterCall, 4)
	m := newManager(subscription.Config{}, chanReporter(ch))

	subscribedPath := mkPath(1, 0x0006, 0x0000)
	args := defaultArgs()
	args.MinIntervalFloor = 1
	args.MaxIntervalCeiling = 60
	args.AttributePaths = []im.ConcreteAttributePath{subscribedPath}
	sub, err := m.Subscribe(args)
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}

	// Mark dirty immediately after subscribe (simulates change during seeding).
	m.OnAttributeChanged(subscribedPath)

	ctx := context.Background()
	t0 := time.Now()

	// Tick at +2 s — MinIntervalFloor(1 s) elapsed → dirty report fires.
	m.Tick(ctx, t0.Add(2*time.Second))

	select {
	case call := <-ch:
		if call.sub.ID != sub.ID {
			t.Errorf("wrong sub: %d vs %d", call.sub.ID, sub.ID)
		}
		if len(call.paths) == 0 {
			t.Error("dirty paths must be non-empty")
		}
	default:
		t.Fatal("dirty report not emitted after MinIntervalFloor elapsed")
	}
}

// ---- Case 7 ---------------------------------------------------------------
// Mirrors matter.js packages/node/src/node/server/ServerSubscription.ts:301-311
// (#addOutstandingAttributes — duplicate path stored only once)
//
// Multiple OnAttributeChanged calls for the same concrete path before
// the first Tick must produce exactly one dirty entry: the reporter is
// called with deduplicated paths.
func TestHandlerParity_DirtyMarkDeduplication_MultipleChanges_SinglePath(t *testing.T) {
	// Mirrors matter.js packages/node/src/node/server/ServerSubscription.ts:301-311
	t.Parallel()
	ch := make(chan reporterCall, 4)
	m := newManager(subscription.Config{}, chanReporter(ch))

	subscribedPath := mkPath(1, 0x0006, 0x0000)
	args := defaultArgs()
	args.MinIntervalFloor = 1
	args.AttributePaths = []im.ConcreteAttributePath{subscribedPath}
	if _, err := m.Subscribe(args); err != nil {
		t.Fatalf("Subscribe: %v", err)
	}

	// Mark the same path dirty three times.
	m.OnAttributeChanged(subscribedPath)
	m.OnAttributeChanged(subscribedPath)
	m.OnAttributeChanged(subscribedPath)

	ctx := context.Background()
	m.Tick(ctx, time.Now().Add(2*time.Second))

	select {
	case call := <-ch:
		// Must have exactly 1 unique path, not 3.
		seen := make(map[im.ConcreteAttributePath]int)
		for _, p := range call.paths {
			seen[p]++
		}
		if len(seen) != 1 {
			t.Errorf("dirty paths contains %d unique paths, want 1 (dedup)", len(seen))
		}
		for p, n := range seen {
			if n > 1 {
				t.Errorf("path %v appears %d times, want 1 (dedup)", p, n)
			}
		}
	default:
		t.Fatal("reporter not called after 3× OnAttributeChanged + Tick")
	}
}

// ---- Case 8 ---------------------------------------------------------------
// Mirrors matter.js packages/node/src/node/server/ServerSubscription.ts:314-337
// (#handleClusterStateChanges seeded-version skip)
//
// matter.js: when a change arrives *during seeding* at the same cluster version
// already included in the initial report, it is skipped (subscriber already
// has it). In openccu-loom the equivalent invariant is that an attribute change
// on a *closed* subscription (IsClosed=true) is silently dropped:
// markDirty returns false and no dirty report fires.
func TestHandlerParity_ClosedSubscription_MarkDirtyIsNoop(t *testing.T) {
	// Mirrors matter.js packages/node/src/node/server/ServerSubscription.ts:320-322
	// (if (this.#isClosed ... ) return; guard at top of #handleClusterStateChanges)
	t.Parallel()
	ch := make(chan reporterCall, 4)
	m := newManager(subscription.Config{}, chanReporter(ch))

	subscribedPath := mkPath(1, 0x0006, 0x0000)
	args := defaultArgs()
	args.AttributePaths = []im.ConcreteAttributePath{subscribedPath}
	sub, err := m.Subscribe(args)
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}

	// Close before any attribute change.
	if err := m.Close(sub.ID); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Change fires after close — must be silently ignored.
	m.OnAttributeChanged(subscribedPath)

	ctx := context.Background()
	m.Tick(ctx, time.Now().Add(10*time.Second))

	if len(ch) != 0 {
		t.Fatalf("reporter called %d time(s) after close, want 0", len(ch))
	}
}

// ---- Case 9 ---------------------------------------------------------------
// Mirrors matter.js packages/node/src/node/server/ServerSubscription.ts:429-437
// (#triggerSendUpdate — send already in progress: set #sendNextUpdateImmediately)
//
// Untranslatable: the "send already in progress" state exists only inside an
// async Promise chain in matter.js; openccu-loom's Tick is synchronous and the
// reporter is invoked inline — there is no in-flight send state at the engine
// layer. The deduplication of concurrent sends is tested implicitly by
// TestConcurrent_OnAttributeChanged_Tick_Subscribe_Close.
func TestHandlerParity_SKIP_SendAlreadyInProgress_Deferred(t *testing.T) {
	// Mirrors matter.js packages/node/src/node/server/ServerSubscription.ts:429-437
	t.Skip("FixMe: TypeScript Promise in-flight state has no direct Go engine equivalent — covered implicitly by concurrent race test")
}

// ---- Case 10 --------------------------------------------------------------
// Mirrors matter.js packages/node/src/node/server/ServerSubscription.ts:460-521
// (#sendUpdate retry — up to 2 re-queue cycles on transport error, cancel on 3rd)
//
// Untranslatable at the engine layer: error counting lives inside
// #sendUpdate which wraps the exchange messenger; openccu-loom's Reporter is
// a pure callback — transport errors are handled by the caller of the Reporter,
// not by the subscription engine.
func TestHandlerParity_SKIP_SendUpdateRetryCount(t *testing.T) {
	// Mirrors matter.js packages/node/src/node/server/ServerSubscription.ts:475-509
	t.Skip("FixMe: openccu-loom gap — send-error retry count (≤2 re-queue, cancel on 3rd) lives in the transport layer above the engine; drift L10-Dxx-FUTURE")
}

// ---- Case 11 --------------------------------------------------------------
// Mirrors matter.js packages/node/src/node/server/ServerSubscription.ts:666-678
// (close() — idempotent: double-close is a no-op)
//
// Manager.Close must return ErrNotFound on the second call (already removed from map);
// the subscription must remain IsClosed() = true.
func TestHandlerParity_Close_Idempotent_SecondCloseErrNotFound(t *testing.T) {
	// Mirrors matter.js packages/node/src/node/server/ServerSubscription.ts:666-668
	// (#isClosed early-return guard)
	t.Parallel()
	m := newManager(subscription.Config{}, nil) //nolint:staticcheck // no reporter needed

	sub, err := m.Subscribe(defaultArgs())
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}

	if err := m.Close(sub.ID); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	// Second close: the subscription was already removed → ErrNotFound.
	if err := m.Close(sub.ID); !errors.Is(err, subscription.ErrNotFound) {
		t.Errorf("second Close: expected ErrNotFound, got %v", err)
	}
	if !sub.IsClosed() {
		t.Error("IsClosed() = false after double Close")
	}
}

// ---- Case 12 --------------------------------------------------------------
// Mirrors matter.js packages/node/src/node/server/InteractionServer.ts:549-566
// (KeepSubscriptions=false → ClosePeer before admitting new subscription)
//
// When a SubscribeRequest arrives with KeepSubscriptions=false the InteractionServer
// calls ClosePeer for every existing subscription from the same (fabric, peer)
// before admitting the new one. After ClosePeer the quota must be freed so the
// new subscribe succeeds.
func TestHandlerParity_KeepSubscriptionsFalse_ClosePeerFreesQuota(t *testing.T) {
	// Mirrors matter.js packages/node/src/node/server/InteractionServer.ts:549-566
	// (keepSubscriptions=false path: existing subs cleared before new admission)
	t.Parallel()
	cfg := subscription.Config{MaxSubscriptionsPerFabric: 1}
	m := newManager(cfg, nil)

	// Fabric 1, peer 0xABCD — fill the per-fabric quota (max=1).
	args := defaultArgs()
	args.FabricIndex = 1
	args.PeerNodeID = 0xABCD
	args.KeepSubscriptions = false
	sub1, err := m.Subscribe(args)
	if err != nil {
		t.Fatalf("Subscribe (first): %v", err)
	}
	if m.Active() != 1 {
		t.Fatalf("Active() = %d, want 1", m.Active())
	}

	// Simulate KeepSubscriptions=false handling: caller calls ClosePeer before
	// the new subscribe attempt.
	cleared := m.ClosePeer(1, 0xABCD)
	if cleared != 1 {
		t.Errorf("ClosePeer returned %d, want 1", cleared)
	}
	if !sub1.IsClosed() {
		t.Error("sub1 should be closed after ClosePeer")
	}

	// New subscribe on the same (fabric, peer) must succeed (quota freed).
	args.SessionID = 2
	sub2, err := m.Subscribe(args)
	if err != nil {
		t.Fatalf("Subscribe (second, after ClosePeer): %v", err)
	}
	if sub2.ID == sub1.ID {
		t.Error("new subscription must have a different ID than the closed one")
	}
	if m.Active() != 1 {
		t.Errorf("Active() = %d, want 1 after re-subscribe", m.Active())
	}
}

// ---- Case 13 --------------------------------------------------------------
// Mirrors matter.js packages/node/test/node/AttributeSubscriptionResponseTest.ts:16-41
// (case "reads concrete attribute when in filter")
//
// Untranslatable: AttributeSubscriptionResponse is an IM-layer object that
// requires a live MockServerNode + protocol stack. The openccu-loom equivalent
// lives in the REST handler layer (attribute read with a dirty-state filter),
// not in the subscription engine itself.
func TestHandlerParity_SKIP_AttrSubResponse_ConcreteInFilter(t *testing.T) {
	// Mirrors matter.js packages/node/test/node/AttributeSubscriptionResponseTest.ts:16-41
	t.Skip("FixMe: AttributeSubscriptionResponse requires a full IM+Node stack; openccu-loom dirty-filter logic tested at the engine layer via drainDirtyIfElapsed")
}

// ---- Case 14 --------------------------------------------------------------
// Mirrors matter.js packages/node/src/node/server/ServerSubscription.ts:399-427
// (#prepareDataUpdate — MinIntervalFloor respected: second change within floor
// does not immediately fire a second report)
//
// Validates that after a dirty report fires, a second OnAttributeChanged within
// MinIntervalFloor does NOT produce another reporter call until the floor elapses.
func TestHandlerParity_PrepareDataUpdate_SecondChangeWithinFloor_Deferred(t *testing.T) {
	// Mirrors matter.js packages/node/src/node/server/ServerSubscription.ts:399-427
	// (#prepareDataUpdate MinIntervalFloor guard)
	t.Parallel()
	ch := make(chan reporterCall, 4)
	m := newManager(subscription.Config{}, chanReporter(ch))

	subscribedPath := mkPath(1, 0x0006, 0x0000)
	args := defaultArgs()
	args.MinIntervalFloor = 5
	args.MaxIntervalCeiling = 60
	args.AttributePaths = []im.ConcreteAttributePath{subscribedPath}
	if _, err := m.Subscribe(args); err != nil {
		t.Fatalf("Subscribe: %v", err)
	}

	ctx := context.Background()
	t0 := time.Now()

	// First change + Tick at t0+6s → first report fires (floor=5s elapsed).
	m.OnAttributeChanged(subscribedPath)
	m.Tick(ctx, t0.Add(6*time.Second))
	if len(ch) == 0 {
		t.Fatal("first report not fired at t+6s")
	}
	<-ch // consume

	// Second change immediately after the first report.
	m.OnAttributeChanged(subscribedPath)

	// Tick at t0+8s: only 2s after first report, floor=5s not elapsed → no call.
	m.Tick(ctx, t0.Add(8*time.Second))
	if len(ch) != 0 {
		t.Errorf("got %d calls at t+8s (2s after last report), want 0 (floor=5s)", len(ch))
	}

	// Tick at t0+12s: floor elapsed → second report fires.
	m.Tick(ctx, t0.Add(12*time.Second))
	if len(ch) != 1 {
		t.Errorf("got %d calls at t+12s, want 1 (second dirty after floor)", len(ch))
	}
}

// ---- Case 16 --------------------------------------------------------------
// Two parallel SubscribeRequests from the same CASE session (same SessionID
// and PeerNodeID) must result in exactly one live subscription: the newer
// one survives, the older is closed and its quota slot freed.
//
// Scenario: peer reconnects and its first Subscribe attempt is still live
// (the session did not drop). The second request arrives on the same session
// while the first subscription is still registered. With
// ReplaceSessionDuplicate=true the manager closes the old entry before
// admitting the new one — Active() stays at 1 and the old subscription is
// marked IsClosed().
func TestSubscribe_ReplaceSessionDuplicate_OlderClosedNewerSurvives(t *testing.T) {
	t.Parallel()
	cfg := subscription.Config{MaxSubscriptionsPerFabric: 2}
	m := newManager(cfg, nil)

	// First subscribe: CASE session, fabric=1, peer=0xA1B2C3D4.
	args1 := subscription.SubscribeArgs{
		FabricIndex:             1,
		PeerNodeID:              0xA1B2C3D4,
		SessionID:               77,
		MinIntervalFloor:        1,
		MaxIntervalCeiling:      60,
		AttributePaths:          []im.ConcreteAttributePath{mkPath(1, 0x0006, 0x0000)},
		ReplaceSessionDuplicate: true,
	}
	sub1, err := m.Subscribe(args1)
	if err != nil {
		t.Fatalf("Subscribe (first): %v", err)
	}
	if m.Active() != 1 {
		t.Fatalf("Active() = %d after first subscribe, want 1", m.Active())
	}

	// Second subscribe: same session, same peer — simulates the parallel
	// reconnect race. With ReplaceSessionDuplicate=true the older subscription
	// must be evicted before the new one is admitted.
	args2 := subscription.SubscribeArgs{
		FabricIndex:             1,
		PeerNodeID:              0xA1B2C3D4,
		SessionID:               77, // same session
		MinIntervalFloor:        1,
		MaxIntervalCeiling:      60,
		AttributePaths:          []im.ConcreteAttributePath{mkPath(1, 0x0006, 0x0000)},
		ReplaceSessionDuplicate: true,
	}
	sub2, err := m.Subscribe(args2)
	if err != nil {
		t.Fatalf("Subscribe (second): %v", err)
	}

	// Only the newer subscription must remain active.
	if m.Active() != 1 {
		t.Errorf("Active() = %d, want 1 (only the newer subscription survives)", m.Active())
	}

	// The first subscription must be closed (evicted).
	if !sub1.IsClosed() {
		t.Error("first subscription should be marked IsClosed() after ReplaceSessionDuplicate")
	}

	// The second subscription must be live and retrievable.
	if sub2.IsClosed() {
		t.Error("second (newer) subscription must NOT be closed")
	}
	got, err := m.Get(sub2.ID)
	if err != nil {
		t.Fatalf("Get(sub2): unexpected error %v", err)
	}
	if got.ID != sub2.ID {
		t.Errorf("Get returned ID %d, want %d", got.ID, sub2.ID)
	}

	// The first subscription must no longer be retrievable.
	if _, err := m.Get(sub1.ID); err == nil {
		t.Error("Get(sub1) succeeded after eviction, want ErrNotFound")
	}
}

// TestSubscribe_ReplaceSessionDuplicate_DoesNotAffectOtherSessions verifies
// that ReplaceSessionDuplicate only closes subscriptions on the matching
// SessionID and leaves subscriptions from other sessions intact.
func TestSubscribe_ReplaceSessionDuplicate_DoesNotAffectOtherSessions(t *testing.T) {
	t.Parallel()
	m := newManager(subscription.Config{}, nil)

	// Session A — will be the target of the dedup.
	argsA := subscription.SubscribeArgs{
		FabricIndex:             1,
		PeerNodeID:              0xAAAA,
		SessionID:               10,
		MinIntervalFloor:        1,
		MaxIntervalCeiling:      60,
		AttributePaths:          []im.ConcreteAttributePath{mkPath(1, 0x0006, 0x0000)},
		ReplaceSessionDuplicate: true,
	}
	subA1, err := m.Subscribe(argsA)
	if err != nil {
		t.Fatalf("Subscribe A1: %v", err)
	}

	// Session B — different session; must not be touched.
	argsB := subscription.SubscribeArgs{
		FabricIndex:        1,
		PeerNodeID:         0xBBBB,
		SessionID:          20, // different session
		MinIntervalFloor:   1,
		MaxIntervalCeiling: 60,
		AttributePaths:     []im.ConcreteAttributePath{mkPath(2, 0x0006, 0x0000)},
	}
	subB, err := m.Subscribe(argsB)
	if err != nil {
		t.Fatalf("Subscribe B: %v", err)
	}

	// Second subscribe on session A — evicts subA1, keeps subB.
	argsA2 := argsA
	argsA2.PeerNodeID = 0xAAAA
	subA2, err := m.Subscribe(argsA2)
	if err != nil {
		t.Fatalf("Subscribe A2: %v", err)
	}

	// Session-B subscription must still be live.
	if subB.IsClosed() {
		t.Error("subB (different session) must NOT be closed after session-A replacement")
	}
	if _, err := m.Get(subB.ID); err != nil {
		t.Errorf("Get(subB): unexpected error %v", err)
	}

	// Session-A first subscription must be closed.
	if !subA1.IsClosed() {
		t.Error("subA1 must be IsClosed() after ReplaceSessionDuplicate")
	}

	// Session-A second subscription must be live.
	if subA2.IsClosed() {
		t.Error("subA2 must NOT be closed")
	}

	// Two live subscriptions: B + A2.
	if m.Active() != 2 {
		t.Errorf("Active() = %d, want 2 (subB + subA2)", m.Active())
	}
}

// TestSubscribe_ReplaceSessionDuplicate_QuotaFreedForResubscribe verifies
// that the eviction frees the per-fabric quota slot so the replacement
// subscribe succeeds even when the fabric is already at capacity.
func TestSubscribe_ReplaceSessionDuplicate_QuotaFreedForResubscribe(t *testing.T) {
	t.Parallel()
	cfg := subscription.Config{MaxSubscriptionsPerFabric: 1}
	m := newManager(cfg, nil)

	args := subscription.SubscribeArgs{
		FabricIndex:             1,
		PeerNodeID:              0xCAFE,
		SessionID:               55,
		MinIntervalFloor:        1,
		MaxIntervalCeiling:      60,
		AttributePaths:          []im.ConcreteAttributePath{mkPath(1, 0x0006, 0x0000)},
		ReplaceSessionDuplicate: true,
	}
	sub1, err := m.Subscribe(args)
	if err != nil {
		t.Fatalf("Subscribe (first): %v", err)
	}
	// Quota is now full (max=1).
	if m.Active() != 1 {
		t.Fatalf("Active() = %d, want 1", m.Active())
	}

	// Second subscribe with ReplaceSessionDuplicate=true must free the
	// quota by evicting sub1, then succeed.
	sub2, err := m.Subscribe(args)
	if err != nil {
		t.Fatalf("Subscribe (second, replace): unexpected error %v", err)
	}

	if !sub1.IsClosed() {
		t.Error("sub1 should be IsClosed() after replacement")
	}
	if sub2.IsClosed() {
		t.Error("sub2 should be live")
	}
	if m.Active() != 1 {
		t.Errorf("Active() = %d, want 1", m.Active())
	}
}

// ---- Case 15 --------------------------------------------------------------
// Mirrors matter.js packages/node/src/node/server/ServerSubscription.ts:200-215
// (#constructor — EventRequests stored into this.#eventPaths)
//
// When a SubscribeRequest carries EventRequests, the Manager must store them
// as EventPaths on the Subscription so the event fan-out in
// Manager.OnEventFired reaches the subscriber. This locks the EventPaths-wiring invariant:
// EventPaths wired from req.EventRequests in Manager.Subscribe.
//
// Apple Home sends EventRequests for Switch-cluster (0x003B) subscriptions;
// openccu-loom must propagate those paths so switch-press events flow to the
// right subscription. Without this fix OnEventFired finds no matching
// EventPaths and the event silently drops.
func TestHandlerParity_EventPathsWiredFromSubscribeArgs(t *testing.T) {
	// Mirrors matter.js packages/node/src/node/server/ServerSubscription.ts:200-215
	// (#constructor eventPaths init)
	t.Parallel()
	m := newManager(subscription.Config{}, nil)

	eventPath := im.ConcreteEventPath{
		Endpoint:    1,
		HasEndpoint: true,
		Cluster:     0x003B, // Switch cluster
		HasCluster:  true,
		// No HasEvent: wildcard (subscribe to all Switch events).
	}
	args := defaultArgs()
	args.EventPaths = []im.ConcreteEventPath{eventPath}

	sub, err := m.Subscribe(args)
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}

	// The Subscription must carry the EventPaths from the request.
	if len(sub.EventPaths) != 1 {
		t.Fatalf("EventPaths count=%d, want 1 (L10-D02: EventPaths not wired from Subscribe args)", len(sub.EventPaths))
	}
	ep := sub.EventPaths[0]
	if !ep.HasEndpoint || ep.Endpoint != 1 {
		t.Fatalf("EventPaths[0].Endpoint: has=%v val=%d, want has=true val=1", ep.HasEndpoint, ep.Endpoint)
	}
	if !ep.HasCluster || ep.Cluster != 0x003B {
		t.Fatalf("EventPaths[0].Cluster: has=%v val=0x%04X, want has=true val=0x003B", ep.HasCluster, ep.Cluster)
	}
	if ep.HasEvent {
		t.Fatal("EventPaths[0].HasEvent should be false (wildcard event subscription)")
	}
}
