// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

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

// ---- helpers ----

// mkPath builds a fully-concrete ConcreteAttributePath (all Has* = true).
func mkPath(endpoint uint16, cluster, attribute uint32) im.ConcreteAttributePath {
	return im.ConcreteAttributePath{
		Endpoint:     endpoint,
		Cluster:      cluster,
		Attribute:    attribute,
		HasEndpoint:  true,
		HasCluster:   true,
		HasAttribute: true,
	}
}

// mkWildcardPath builds a path where only the supplied dimensions are
// constrained. Pass 0 for a dimension to leave it as a wildcard
// (HasX = false). Use the named helpers below for clarity.
func mkWildcardCluster(cluster uint32) im.ConcreteAttributePath {
	return im.ConcreteAttributePath{
		Cluster:    cluster,
		HasCluster: true,
	}
}

func mkFullWildcard() im.ConcreteAttributePath {
	return im.ConcreteAttributePath{}
}

// reporterCall captures one Reporter invocation.
type reporterCall struct {
	sub   *subscription.Subscription
	paths []im.ConcreteAttributePath
}

// chanReporter returns a Reporter that sends to ch (buffered).
func chanReporter(ch chan reporterCall) subscription.Reporter {
	return func(_ context.Context, sub *subscription.Subscription, paths []im.ConcreteAttributePath) {
		ch <- reporterCall{sub: sub, paths: paths}
	}
}

// defaultArgs returns SubscribeArgs with sensible defaults.
func defaultArgs() subscription.SubscribeArgs {
	return subscription.SubscribeArgs{
		FabricIndex:        1,
		PeerNodeID:         0xDEAD,
		SessionID:          1,
		MinIntervalFloor:   1,
		MaxIntervalCeiling: 60,
		AttributePaths:     []im.ConcreteAttributePath{mkPath(1, 0x0006, 0x0000)},
	}
}

func newManager(cfg subscription.Config, reporter subscription.Reporter) *subscription.Manager {
	return subscription.NewManager(cfg, reporter, nil)
}

// ---- NewManager defaults ----

func TestNewManager_ZeroConfig_AppliesDefaults(t *testing.T) {
	t.Parallel()
	// Subscribe with a valid cadence to confirm the manager accepted floor/ceiling defaults.
	m := newManager(subscription.Config{}, nil)
	// MinIntervalFloor default = 1; MinInterval < 1 must be floored, not rejected.
	args := defaultArgs()
	args.MinIntervalFloor = 0 // below default floor
	args.MaxIntervalCeiling = 60
	sub, err := m.Subscribe(args)
	if err != nil {
		t.Fatalf("Subscribe with zeroed MinInterval: %v", err)
	}
	// The manager should have floored MinIntervalFloor to 1 (default).
	if sub.MinIntervalFloor != 1 {
		t.Errorf("MinIntervalFloor = %d, want 1 (floored to default)", sub.MinIntervalFloor)
	}
	// Active should be 1.
	if n := m.Active(); n != 1 {
		t.Fatalf("Active() = %d, want 1", n)
	}
}

func TestNewManager_NilLogger_NilReporter(t *testing.T) {
	t.Parallel()
	// Both nil: must not panic on construction or basic operations.
	m := subscription.NewManager(subscription.Config{}, nil, nil)
	sub, err := m.Subscribe(defaultArgs())
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	if sub == nil {
		t.Fatal("Subscribe returned nil sub with nil reporter/logger")
	}
}

// ---- Subscribe success ----

func TestSubscribe_Success_UniqueID_ActiveOne(t *testing.T) {
	t.Parallel()
	m := newManager(subscription.Config{}, nil)
	sub, err := m.Subscribe(defaultArgs())
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	if sub.ID == 0 {
		t.Error("Subscribe returned zero ID")
	}
	if m.Active() != 1 {
		t.Fatalf("Active() = %d, want 1", m.Active())
	}
}

// TestSubscribe_DoesNotEmitImmediateKeepAlive verifies that the engine
// does NOT fire an empty keep-alive ReportData on the very first tick
// after Subscribe(). Before the fix the manager left
// [Subscription.lastReport] at its zero value at admission; the engine's
// 250 ms tick then observed [Subscription.lastReport.IsZero], treated
// it as "heartbeat elapsed", and emitted an empty ReportData. The
// chip-tool subscribe-pump received this 52-byte report before it had
// piggyback-acked the bridge's initial report and dropped it with
// `Dropping message without piggyback ack when we are waiting for an
// ack` — eventually timing out the subscription.
//
// The fix stamps lastReport=now at admission inside [Manager.Subscribe]
// so the very first tick — even one that fires before the bridge's
// follow-up [Subscription.TouchLastReport] — sees a fresh stamp and
// skips the keep-alive path.
func TestSubscribe_DoesNotEmitImmediateKeepAlive(t *testing.T) {
	t.Parallel()

	var (
		mu    sync.Mutex
		calls int
	)
	reporter := func(_ context.Context, _ *subscription.Subscription, _ []im.ConcreteAttributePath) {
		mu.Lock()
		calls++
		mu.Unlock()
	}

	// Tick aggressively (10 ms) so any race-window keep-alive fires
	// quickly. MaxIntervalCeiling=60 s, MinIntervalFloor=1 s — the
	// engine's sendInterval clamps to ~48 s, so a properly-stamped
	// subscription must NOT report in the 100 ms test window.
	m := newManager(subscription.Config{TickInterval: 10 * time.Millisecond}, reporter)
	_, err := m.Subscribe(defaultArgs())
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	m.Start(ctx)
	t.Cleanup(m.Stop)

	// Run the engine for ~100 ms — ten ticks. Without the fix every
	// tick saw lastReport.IsZero() and emitted a keep-alive; with the
	// fix no keep-alive fires until the sendInterval (~48 s) elapses.
	time.Sleep(100 * time.Millisecond)

	mu.Lock()
	got := calls
	mu.Unlock()

	if got != 0 {
		t.Errorf("reporter calls = %d, want 0 (no keep-alive should fire in the first 100 ms after Subscribe)", got)
	}
}

func TestSubscribe_MultipleIDs_AreUnique(t *testing.T) {
	t.Parallel()
	m := newManager(subscription.Config{}, nil)
	seen := make(map[uint32]bool)
	for i := range 5 {
		args := defaultArgs()
		args.SessionID = uint16(i + 1) //nolint:gosec // G115: i bounded by loop constant
		sub, err := m.Subscribe(args)
		if err != nil {
			t.Fatalf("Subscribe #%d: %v", i, err)
		}
		if seen[sub.ID] {
			t.Fatalf("Duplicate subscription ID %d", sub.ID)
		}
		seen[sub.ID] = true
	}
}

// ---- Cadence validation ----

func TestSubscribe_MinGtMax_ErrCadenceOutOfRange(t *testing.T) {
	t.Parallel()
	m := newManager(subscription.Config{}, nil)
	args := defaultArgs()
	args.MinIntervalFloor = 60
	args.MaxIntervalCeiling = 30 // min > max
	_, err := m.Subscribe(args)
	if !errors.Is(err, subscription.ErrCadenceOutOfRange) {
		t.Fatalf("expected ErrCadenceOutOfRange, got %v", err)
	}
}

func TestSubscribe_MinBelowFloor_IsClamped(t *testing.T) {
	t.Parallel()
	cfg := subscription.Config{MinIntervalFloorSeconds: 5}
	m := newManager(cfg, nil)
	args := defaultArgs()
	args.MinIntervalFloor = 2 // below floor
	args.MaxIntervalCeiling = 60
	sub, err := m.Subscribe(args)
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	if sub.MinIntervalFloor != 5 {
		t.Errorf("MinIntervalFloor = %d, want 5 (clamped to cfg floor)", sub.MinIntervalFloor)
	}
}

func TestSubscribe_MaxAboveCeiling_IsCapped(t *testing.T) {
	t.Parallel()
	cfg := subscription.Config{MaxIntervalCeilingSeconds: 120}
	m := newManager(cfg, nil)
	args := defaultArgs()
	args.MinIntervalFloor = 1
	args.MaxIntervalCeiling = 9000 // above ceiling
	sub, err := m.Subscribe(args)
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	if sub.MaxIntervalCeiling != 120 {
		t.Errorf("MaxIntervalCeiling = %d, want 120 (capped to cfg ceiling)", sub.MaxIntervalCeiling)
	}
}

// ---- Fabric quota ----

func TestSubscribe_FabricQuotaExceeded(t *testing.T) {
	t.Parallel()
	cfg := subscription.Config{MaxSubscriptionsPerFabric: 2}
	m := newManager(cfg, nil)
	for i := range 2 {
		args := defaultArgs()
		args.SessionID = uint16(i + 1) //nolint:gosec // G115: i bounded by loop constant
		if _, err := m.Subscribe(args); err != nil {
			t.Fatalf("Subscribe #%d: %v", i, err)
		}
	}
	args := defaultArgs()
	args.SessionID = 99
	_, err := m.Subscribe(args)
	if !errors.Is(err, subscription.ErrFabricQuotaExceeded) {
		t.Fatalf("expected ErrFabricQuotaExceeded, got %v", err)
	}
}

// ---- Get ----

func TestGet_Hit(t *testing.T) {
	t.Parallel()
	m := newManager(subscription.Config{}, nil)
	sub, err := m.Subscribe(defaultArgs())
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	got, err := m.Get(sub.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.ID != sub.ID {
		t.Errorf("Get returned ID %d, want %d", got.ID, sub.ID)
	}
}

func TestGet_Miss_ErrNotFound(t *testing.T) {
	t.Parallel()
	m := newManager(subscription.Config{}, nil)
	_, err := m.Get(0xDEADBEEF)
	if !errors.Is(err, subscription.ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

// ---- Close ----

func TestClose_Hit_RemovesAndMarksClosed(t *testing.T) {
	t.Parallel()
	m := newManager(subscription.Config{}, nil)
	sub, err := m.Subscribe(defaultArgs())
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	if err := m.Close(sub.ID); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if !sub.IsClosed() {
		t.Error("Subscription.IsClosed() = false after Manager.Close")
	}
	if m.Active() != 0 {
		t.Fatalf("Active() = %d, want 0 after Close", m.Active())
	}
}

func TestClose_Miss_ErrNotFound(t *testing.T) {
	t.Parallel()
	m := newManager(subscription.Config{}, nil)
	err := m.Close(0xCAFE)
	if !errors.Is(err, subscription.ErrNotFound) {
		t.Fatalf("expected ErrNotFound for Close miss, got %v", err)
	}
}

func TestClose_AfterClose_GetMiss(t *testing.T) {
	t.Parallel()
	m := newManager(subscription.Config{}, nil)
	sub, _ := m.Subscribe(defaultArgs())
	_ = m.Close(sub.ID)
	_, err := m.Get(sub.ID)
	if !errors.Is(err, subscription.ErrNotFound) {
		t.Fatalf("expected ErrNotFound after Close, got %v", err)
	}
}

// ---- CloseSession ----

func TestCloseSession_OnlyAffectsMatchingSession(t *testing.T) {
	t.Parallel()
	m := newManager(subscription.Config{}, nil)

	// Session 1: two subs.
	args1 := defaultArgs()
	args1.SessionID = 1
	sub1a, _ := m.Subscribe(args1)
	sub1b, _ := m.Subscribe(args1)

	// Session 2: one sub.
	args2 := defaultArgs()
	args2.SessionID = 2
	sub2, _ := m.Subscribe(args2)

	m.CloseSession(1)

	if !sub1a.IsClosed() {
		t.Error("sub1a (session 1) should be closed")
	}
	if !sub1b.IsClosed() {
		t.Error("sub1b (session 1) should be closed")
	}
	if sub2.IsClosed() {
		t.Error("sub2 (session 2) should NOT be closed")
	}
	if m.Active() != 1 {
		t.Fatalf("Active() = %d, want 1 after CloseSession(1)", m.Active())
	}
}

// ---- CloseFabric ----

func TestCloseFabric_OnlyAffectsTargetFabric(t *testing.T) {
	t.Parallel()
	m := newManager(subscription.Config{}, nil)

	// Fabric 1: two subs.
	args1 := defaultArgs()
	args1.FabricIndex = 1
	args1.SessionID = 1
	f1a, _ := m.Subscribe(args1)
	args1.SessionID = 2
	f1b, _ := m.Subscribe(args1)

	// Fabric 2: one sub.
	args2 := defaultArgs()
	args2.FabricIndex = 2
	args2.SessionID = 3
	f2, _ := m.Subscribe(args2)

	m.CloseFabric(1)

	if !f1a.IsClosed() {
		t.Error("f1a (fabric 1) should be closed")
	}
	if !f1b.IsClosed() {
		t.Error("f1b (fabric 1) should be closed")
	}
	if f2.IsClosed() {
		t.Error("f2 (fabric 2) should NOT be closed")
	}
	if m.Active() != 1 {
		t.Fatalf("Active() = %d, want 1 after CloseFabric(1)", m.Active())
	}
}

// ---- OnAttributeChanged ----

func TestOnAttributeChanged_NoMatchingPath_NoEffect(t *testing.T) {
	t.Parallel()
	ch := make(chan reporterCall, 4)
	m := newManager(subscription.Config{}, chanReporter(ch))

	args := defaultArgs()
	args.MinIntervalFloor = 1
	args.MaxIntervalCeiling = 60
	args.AttributePaths = []im.ConcreteAttributePath{mkPath(1, 0x0006, 0x0000)}
	_, err := m.Subscribe(args)
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}

	ctx := context.Background()
	t0 := time.Now()

	// First Tick: lastReport is zero so MaxInterval fires immediately as a
	// keepalive. Drain it so subsequent checks are clean.
	m.Tick(ctx, t0)
	for len(ch) > 0 {
		<-ch
	}

	// Changed path does NOT match the subscribed path (endpoint 2, cluster 0x0300).
	m.OnAttributeChanged(mkPath(2, 0x0300, 0x0001))

	// Tick well within MaxIntervalCeiling (60 s): no keepalive, no dirty match.
	m.Tick(ctx, t0.Add(2*time.Second))

	if len(ch) != 0 {
		t.Fatalf("got %d reporter calls, want 0 (non-matching path)", len(ch))
	}
}

func TestOnAttributeChanged_MatchingPath_MarksDirty(t *testing.T) {
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

	m.OnAttributeChanged(subscribedPath)

	ctx := context.Background()
	t0 := time.Now()
	// First tick: MinInterval hasn't elapsed (lastReport = zero → elapsed immediately).
	m.Tick(ctx, t0.Add(2*time.Second))

	select {
	case call := <-ch:
		if call.sub.ID != sub.ID {
			t.Errorf("Reporter called for wrong sub: got ID %d, want %d", call.sub.ID, sub.ID)
		}
		if len(call.paths) == 0 {
			t.Error("Reporter called with no dirty paths, expected at least 1")
		}
	default:
		t.Fatal("Reporter not called after OnAttributeChanged + Tick with elapsed MinInterval")
	}
}

// ---- Wildcard path matching ----

func TestWildcardPath_NoEndpoint_MatchesAllEndpoints(t *testing.T) {
	t.Parallel()
	ch := make(chan reporterCall, 4)
	m := newManager(subscription.Config{}, chanReporter(ch))

	// Subscribe with a cluster-only wildcard (no Endpoint constraint).
	args := defaultArgs()
	args.AttributePaths = []im.ConcreteAttributePath{mkWildcardCluster(0x0006)}
	sub, err := m.Subscribe(args)
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}

	// Change on endpoint 5, cluster 0x0006 — should match.
	m.OnAttributeChanged(mkPath(5, 0x0006, 0x0000))

	ctx := context.Background()
	m.Tick(ctx, time.Now().Add(2*time.Second))

	select {
	case call := <-ch:
		if call.sub.ID != sub.ID {
			t.Errorf("Reporter wrong sub: %d vs %d", call.sub.ID, sub.ID)
		}
	default:
		t.Fatal("Reporter not called for wildcard-endpoint subscription")
	}
}

func TestFullWildcard_MatchesAnyPath(t *testing.T) {
	t.Parallel()
	ch := make(chan reporterCall, 4)
	m := newManager(subscription.Config{}, chanReporter(ch))

	args := defaultArgs()
	args.AttributePaths = []im.ConcreteAttributePath{mkFullWildcard()}
	_, err := m.Subscribe(args)
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}

	m.OnAttributeChanged(mkPath(99, 0xFFFF, 0xAAAA))

	ctx := context.Background()
	m.Tick(ctx, time.Now().Add(2*time.Second))

	if len(ch) == 0 {
		t.Fatal("Reporter not called for full-wildcard subscription")
	}
}

// ---- Tick: MinInterval not elapsed ----

func TestTick_MinIntervalNotElapsed_DirtyPathsRetained(t *testing.T) {
	t.Parallel()
	ch := make(chan reporterCall, 4)
	cfg := subscription.Config{MinIntervalFloorSeconds: 5}
	m := newManager(cfg, chanReporter(ch))

	subscribedPath := mkPath(1, 0x0006, 0x0000)
	args := defaultArgs()
	args.MinIntervalFloor = 5
	args.MaxIntervalCeiling = 60
	args.AttributePaths = []im.ConcreteAttributePath{subscribedPath}
	_, err := m.Subscribe(args)
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}

	ctx := context.Background()
	// First Tick sets lastReport (via keepalive because MaxInterval elapsed on a fresh sub).
	t0 := time.Now()
	m.Tick(ctx, t0)
	// Drain the keepalive call if any.
	for len(ch) > 0 {
		<-ch
	}

	// Now mark dirty and tick before MinInterval elapses.
	m.OnAttributeChanged(subscribedPath)
	m.Tick(ctx, t0.Add(1*time.Second)) // only 1 s elapsed, floor is 5 s

	if len(ch) != 0 {
		t.Fatalf("Reporter called prematurely: got %d calls, want 0", len(ch))
	}
}

// ---- Tick: MinInterval elapsed ----

func TestTick_MinIntervalElapsed_ReporterCalledWithDirtyPaths(t *testing.T) {
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

	ctx := context.Background()
	t0 := time.Now()

	m.OnAttributeChanged(subscribedPath)
	m.Tick(ctx, t0.Add(2*time.Second)) // 2 s > MinIntervalFloor(1 s)

	select {
	case call := <-ch:
		if call.sub.ID != sub.ID {
			t.Errorf("wrong sub: %d vs %d", call.sub.ID, sub.ID)
		}
		if len(call.paths) == 0 {
			t.Error("Reporter called with empty paths")
		}
	default:
		t.Fatal("Reporter not called after MinInterval elapsed")
	}
}

func TestTick_AfterDirtyReport_PendingDirtyEmpty(t *testing.T) {
	t.Parallel()
	ch := make(chan reporterCall, 4)
	m := newManager(subscription.Config{}, chanReporter(ch))

	subscribedPath := mkPath(1, 0x0006, 0x0000)
	args := defaultArgs()
	args.MinIntervalFloor = 1
	args.MaxIntervalCeiling = 60
	args.AttributePaths = []im.ConcreteAttributePath{subscribedPath}
	_, err := m.Subscribe(args)
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}

	ctx := context.Background()
	t0 := time.Now()

	m.OnAttributeChanged(subscribedPath)
	m.Tick(ctx, t0.Add(2*time.Second))

	// Consume reporter call.
	<-ch

	// Second Tick shortly after: no new dirty paths, MaxInterval not elapsed → no call.
	m.Tick(ctx, t0.Add(3*time.Second))
	if len(ch) != 0 {
		t.Fatalf("Reporter called again unexpectedly: %d calls", len(ch))
	}
}

// ---- Tick: MaxInterval keep-alive ----

func TestTick_MaxIntervalElapsed_KeepAlive_NilPaths(t *testing.T) {
	t.Parallel()
	ch := make(chan reporterCall, 4)
	m := newManager(subscription.Config{}, chanReporter(ch))

	args := defaultArgs()
	args.MinIntervalFloor = 1
	args.MaxIntervalCeiling = 5
	args.AttributePaths = []im.ConcreteAttributePath{mkPath(1, 0x0006, 0x0000)}
	sub, err := m.Subscribe(args)
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}

	ctx := context.Background()
	// Tick far past MaxIntervalCeiling (5 s), no dirty paths.
	m.Tick(ctx, time.Now().Add(10*time.Second))

	select {
	case call := <-ch:
		if call.sub.ID != sub.ID {
			t.Errorf("Reporter wrong sub: %d vs %d", call.sub.ID, sub.ID)
		}
		if call.paths != nil {
			t.Errorf("Keep-alive should pass nil paths, got %v", call.paths)
		}
	default:
		t.Fatal("Reporter not called for keep-alive (MaxInterval elapsed)")
	}
}

// ---- Tick: heartbeat sendInterval cadence ----

// TestTick_Heartbeat_FiresAtSendInterval verifies the keep-alive
// gate is the matter.js-style sendInterval formula, NOT a fixed
// hard cap. For min=1, max=10 the formula is
// `max(floor, maxInterval × 0.8) = max(1s, 8s) = 8s`.
// Mirrors matter.js `ServerSubscription#determineSendingIntervals`.
func TestTick_Heartbeat_FiresAtSendInterval(t *testing.T) {
	t.Parallel()
	ch := make(chan reporterCall, 4)
	m := newManager(subscription.Config{}, chanReporter(ch))

	args := defaultArgs()
	args.MinIntervalFloor = 1
	args.MaxIntervalCeiling = 10 // half=5s under 60s → 0.8×10 = 8 s sendInterval
	args.AttributePaths = []im.ConcreteAttributePath{mkPath(1, 0x0006, 0x0000)}
	if _, err := m.Subscribe(args); err != nil {
		t.Fatalf("Subscribe: %v", err)
	}

	ctx := context.Background()
	t0 := time.Now()

	// First Tick: subscription was admitted at ~t0 with lastReport
	// already stamped (see [Manager.Subscribe]). No immediate keep-alive
	// — would arrive at chip-tool
	// before the bridge's initial-report-stream completes and trip
	// the MRP "Dropping message without piggyback ack" race.
	m.Tick(ctx, t0)
	if len(ch) != 0 {
		t.Errorf("got %d reporter calls at t0, want 0 (lastReport pre-stamped by Subscribe)", len(ch))
	}

	// Tick at +7 s: under 8 s sendInterval → no keep-alive.
	m.Tick(ctx, t0.Add(7*time.Second))
	if len(ch) != 0 {
		t.Errorf("got %d reporter calls at t+7s, want 0 (under sendInterval=8s)", len(ch))
	}

	// Tick at +9 s: sendInterval has elapsed → keep-alive must fire.
	m.Tick(ctx, t0.Add(9*time.Second))
	select {
	case call := <-ch:
		if call.paths != nil {
			t.Errorf("keep-alive paths = %v, want nil", call.paths)
		}
	default:
		t.Fatal("expected keep-alive at t+9s, sendInterval elapsed")
	}
}

// TestTick_Heartbeat_HonorsMinIntervalFloor verifies the matter.js
// formula's floor clamp: when `0.8 × maxInterval` falls below
// `MinIntervalFloor`, the floor wins. With min=10, max=12 the half
// is 6 s, 0.8× is 9.6 s, both below floor=10 s → sendInterval = 10 s.
func TestTick_Heartbeat_HonorsMinIntervalFloor(t *testing.T) {
	t.Parallel()
	ch := make(chan reporterCall, 4)
	m := newManager(subscription.Config{}, chanReporter(ch))

	args := defaultArgs()
	args.MinIntervalFloor = 10
	args.MaxIntervalCeiling = 12 // 0.8×12 = 9.6 s < floor=10 → floor wins
	args.AttributePaths = []im.ConcreteAttributePath{mkPath(1, 0x0006, 0x0000)}
	if _, err := m.Subscribe(args); err != nil {
		t.Fatalf("Subscribe: %v", err)
	}

	ctx := context.Background()
	t0 := time.Now()

	// Drain initial keep-alive.
	m.Tick(ctx, t0)
	for len(ch) > 0 {
		<-ch
	}

	// At +8 s: 8 < floor=10 → no keep-alive.
	m.Tick(ctx, t0.Add(8*time.Second))
	if len(ch) != 0 {
		t.Errorf("got %d calls at t+8s, want 0 (under floor=10s)", len(ch))
	}

	// At +11 s: floor has elapsed → keep-alive must fire.
	m.Tick(ctx, t0.Add(11*time.Second))
	if len(ch) != 1 {
		t.Errorf("got %d calls at t+11s, want 1 (floor=10s elapsed)", len(ch))
	}
}

// TestTick_Heartbeat_CappedAtMaxIntervalCeiling verifies the
// degenerate path: sub-second / sub-cap MaxInterval (e.g. tests
// that subscribe with max=2) clamps the heartbeat to that
// MaxInterval rather than the 5 s default cap. Without this clamp
// a max=2 subscription would sit silent for 5 s on every gap and
// trigger MaxInterval-based timeouts on the commissioner side.
func TestTick_Heartbeat_CappedAtMaxIntervalCeiling(t *testing.T) {
	t.Parallel()
	ch := make(chan reporterCall, 4)
	m := newManager(subscription.Config{}, chanReporter(ch))

	args := defaultArgs()
	args.MinIntervalFloor = 1
	args.MaxIntervalCeiling = 2
	args.AttributePaths = []im.ConcreteAttributePath{mkPath(1, 0x0006, 0x0000)}
	if _, err := m.Subscribe(args); err != nil {
		t.Fatalf("Subscribe: %v", err)
	}

	ctx := context.Background()
	t0 := time.Now()

	// Drain initial.
	m.Tick(ctx, t0)
	for len(ch) > 0 {
		<-ch
	}

	// At +1 s: under MaxIntervalCeiling=2 → no keep-alive.
	m.Tick(ctx, t0.Add(1*time.Second))
	if len(ch) != 0 {
		t.Errorf("got %d calls at t+1s, want 0 (under maxInterval=2s)", len(ch))
	}

	// At +3 s: maxInterval elapsed → keep-alive.
	m.Tick(ctx, t0.Add(3*time.Second))
	if len(ch) != 1 {
		t.Errorf("got %d calls at t+3s, want 1 (maxInterval=2s elapsed)", len(ch))
	}
}

// ---- Tick with nil Reporter ----

func TestTick_NilReporter_NoPanic(t *testing.T) {
	t.Parallel()
	m := newManager(subscription.Config{}, nil)

	args := defaultArgs()
	args.MinIntervalFloor = 1
	args.MaxIntervalCeiling = 5
	_, err := m.Subscribe(args)
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}

	ctx := context.Background()
	// Should not panic even though there is no reporter and MaxInterval elapsed.
	m.Tick(ctx, time.Now().Add(10*time.Second))
}

// ---- Tick on closed subscription ----

func TestTick_ClosedSubscription_Skipped(t *testing.T) {
	t.Parallel()
	ch := make(chan reporterCall, 4)
	m := newManager(subscription.Config{}, chanReporter(ch))

	subscribedPath := mkPath(1, 0x0006, 0x0000)
	args := defaultArgs()
	args.MinIntervalFloor = 1
	args.MaxIntervalCeiling = 5
	args.AttributePaths = []im.ConcreteAttributePath{subscribedPath}
	sub, err := m.Subscribe(args)
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}

	// Close through Manager so it is removed from the map.
	_ = m.Close(sub.ID)

	m.OnAttributeChanged(subscribedPath)
	m.Tick(context.Background(), time.Now().Add(10*time.Second))

	if len(ch) != 0 {
		t.Fatalf("Reporter called for closed sub: %d calls", len(ch))
	}
}

// ---- Concurrent safety ----

func TestConcurrent_OnAttributeChanged_Tick_Subscribe_Close(t *testing.T) {
	// Not t.Parallel() — the goroutines themselves provide parallelism
	// and this test is designed as a race detector target.
	ch := make(chan reporterCall, 256)
	m := newManager(subscription.Config{MaxSubscriptionsPerFabric: 16}, chanReporter(ch))
	ctx := context.Background()

	var wg sync.WaitGroup
	subscribedPath := mkPath(1, 0x0006, 0x0000)

	// Open 8 subscriptions upfront (under quota) so there are targets.
	subs := make([]*subscription.Subscription, 8)
	for i := range 8 {
		args := defaultArgs()
		args.SessionID = uint16(i + 1) //nolint:gosec // G115: i bounded by 8
		sub, err := m.Subscribe(args)
		if err != nil {
			t.Fatalf("pre-populate Subscribe #%d: %v", i, err)
		}
		subs[i] = sub
	}

	// Concurrent OnAttributeChanged.
	for range 4 {
		wg.Go(func() {
			for range 20 {
				m.OnAttributeChanged(subscribedPath)
			}
		})
	}

	// Concurrent Tick.
	for range 4 {
		wg.Go(func() {
			for range 20 {
				m.Tick(ctx, time.Now().Add(5*time.Second))
			}
		})
	}

	// Concurrent Subscribe (new fabric to avoid quota).
	for i := range 4 {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			args := defaultArgs()
			args.FabricIndex = uint8(idx + 2)  //nolint:gosec // G115: idx bounded by 4
			args.SessionID = uint16(100 + idx) //nolint:gosec // G115: idx bounded by 4
			_, _ = m.Subscribe(args)
		}(i)
	}

	// Concurrent Close.
	for i := range 4 {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			_ = m.Close(subs[idx].ID)
		}(i)
	}

	wg.Wait()
	// Drain reporter channel to avoid goroutine leak.
	for len(ch) > 0 {
		<-ch
	}
}

// ---- ClosePeer ----

// TestManager_ClosePeer_RemovesMatchingSubs verifies that ClosePeer(fabric,
// peer) removes exactly the subscriptions that match both dimensions and
// leaves non-matching subscriptions intact.
func TestManager_ClosePeer_RemovesMatchingSubs(t *testing.T) {
	t.Parallel()
	m := newManager(subscription.Config{}, nil)

	// sub1 + sub2: target (fabric=2, peer=0xABCD)
	args1 := defaultArgs()
	args1.FabricIndex = 2
	args1.PeerNodeID = 0xABCD
	args1.SessionID = 1
	sub1, err := m.Subscribe(args1)
	if err != nil {
		t.Fatalf("Subscribe sub1: %v", err)
	}
	args1.SessionID = 2
	sub2, err := m.Subscribe(args1)
	if err != nil {
		t.Fatalf("Subscribe sub2: %v", err)
	}

	// sub3: different fabric
	args3 := defaultArgs()
	args3.FabricIndex = 3
	args3.PeerNodeID = 0xABCD
	args3.SessionID = 3
	sub3, err := m.Subscribe(args3)
	if err != nil {
		t.Fatalf("Subscribe sub3: %v", err)
	}

	// sub4: different peer
	args4 := defaultArgs()
	args4.FabricIndex = 2
	args4.PeerNodeID = 0xBEEF
	args4.SessionID = 4
	sub4, err := m.Subscribe(args4)
	if err != nil {
		t.Fatalf("Subscribe sub4: %v", err)
	}

	if m.Active() != 4 {
		t.Fatalf("Active() = %d before ClosePeer, want 4", m.Active())
	}

	cleared := m.ClosePeer(2, 0xABCD)
	if cleared != 2 {
		t.Errorf("ClosePeer returned %d, want 2", cleared)
	}
	if m.Active() != 2 {
		t.Errorf("Active() = %d after ClosePeer, want 2", m.Active())
	}

	// sub1 and sub2 must be gone and marked closed.
	if _, err := m.Get(sub1.ID); !errors.Is(err, subscription.ErrNotFound) {
		t.Errorf("Get(sub1): expected ErrNotFound, got %v", err)
	}
	if !sub1.IsClosed() {
		t.Error("sub1 should be marked closed after ClosePeer")
	}
	if _, err := m.Get(sub2.ID); !errors.Is(err, subscription.ErrNotFound) {
		t.Errorf("Get(sub2): expected ErrNotFound, got %v", err)
	}
	if !sub2.IsClosed() {
		t.Error("sub2 should be marked closed after ClosePeer")
	}

	// sub3 and sub4 must still resolve.
	if _, err := m.Get(sub3.ID); err != nil {
		t.Errorf("Get(sub3): unexpected error %v (should survive)", err)
	}
	if _, err := m.Get(sub4.ID); err != nil {
		t.Errorf("Get(sub4): unexpected error %v (should survive)", err)
	}
}

// TestManager_ClosePeer_NoMatch_ReturnsZero verifies that ClosePeer returns 0
// and leaves the existing subscription intact when no (fabric, peer) pair matches.
func TestManager_ClosePeer_NoMatch_ReturnsZero(t *testing.T) {
	t.Parallel()
	m := newManager(subscription.Config{}, nil)

	args := defaultArgs()
	args.FabricIndex = 2
	args.PeerNodeID = 0xABCD
	sub, err := m.Subscribe(args)
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}

	cleared := m.ClosePeer(2, 0xCAFE) // different peer
	if cleared != 0 {
		t.Errorf("ClosePeer returned %d, want 0 (no match)", cleared)
	}
	if m.Active() != 1 {
		t.Errorf("Active() = %d, want 1 (sub should survive)", m.Active())
	}
	if sub.IsClosed() {
		t.Error("sub should NOT be closed after a non-matching ClosePeer")
	}
}

// TestManager_ClosePeer_DecrementsFabricQuota verifies that ClosePeer frees
// per-fabric quota so fresh subscribes can succeed after the cleared slots
// are released.
func TestManager_ClosePeer_DecrementsFabricQuota(t *testing.T) {
	t.Parallel()
	cfg := subscription.Config{MaxSubscriptionsPerFabric: 2}
	m := newManager(cfg, nil)

	// Fill fabric=1 to capacity.
	args := defaultArgs()
	args.FabricIndex = 1
	args.PeerNodeID = 0xABCD
	args.SessionID = 1
	if _, err := m.Subscribe(args); err != nil {
		t.Fatalf("Subscribe #1: %v", err)
	}
	args.SessionID = 2
	if _, err := m.Subscribe(args); err != nil {
		t.Fatalf("Subscribe #2: %v", err)
	}

	// Third subscribe must fail with ErrFabricQuotaExceeded.
	args.SessionID = 3
	_, err := m.Subscribe(args)
	if !errors.Is(err, subscription.ErrFabricQuotaExceeded) {
		t.Fatalf("expected ErrFabricQuotaExceeded, got %v", err)
	}

	// ClosePeer frees both slots.
	cleared := m.ClosePeer(1, 0xABCD)
	if cleared != 2 {
		t.Errorf("ClosePeer returned %d, want 2", cleared)
	}
	if m.Active() != 0 {
		t.Errorf("Active() = %d after ClosePeer, want 0", m.Active())
	}

	// Fresh subscribe under the same fabric must now succeed.
	args.SessionID = 4
	if _, err := m.Subscribe(args); err != nil {
		t.Errorf("Subscribe after ClosePeer: unexpected error %v", err)
	}
}

// ---- MinIntervalFloor enforced for event-triggered reports ----

// TestTick_CriticalEventAndDirty_OnlyOneReporterCallPerFloor verifies that a
// Critical event and a dirty attribute coexisting within the MinIntervalFloor
// window produce exactly ONE reporter call, not two. Apple Home's
// duplicate-suppression heuristic rejects two consecutive ReportData frames
// in the same floor window.
func TestTick_CriticalEventAndDirty_OnlyOneReporterCallPerFloor(t *testing.T) {
	t.Parallel()
	// Reporter counts how many attribute-report calls arrive.
	var mu sync.Mutex
	reporterCalls := 0
	reporter := func(_ context.Context, _ *subscription.Subscription, paths []im.ConcreteAttributePath) {
		if paths != nil { // ignore keep-alive (nil paths)
			mu.Lock()
			reporterCalls++
			mu.Unlock()
		}
	}

	// EventReporter counts how many event-drain calls arrive.
	var evMu sync.Mutex
	eventCalls := 0
	eventReporter := subscription.EventReporter(func(_ context.Context, _ *subscription.Subscription, _ []im.EventReport) {
		evMu.Lock()
		eventCalls++
		evMu.Unlock()
	})

	m := newManager(subscription.Config{}, reporter)
	m.SetEventReporter(eventReporter)

	eventPath := im.ConcreteEventPath{
		HasEndpoint: true, Endpoint: 1,
		HasCluster: true, Cluster: 0x0006,
		HasEvent: true, Event: 0x0001,
	}
	attrPath := mkPath(1, 0x0006, 0x0000)
	args := defaultArgs()
	args.MinIntervalFloor = 2
	args.MaxIntervalCeiling = 60
	args.AttributePaths = []im.ConcreteAttributePath{attrPath}
	args.EventPaths = []im.ConcreteEventPath{eventPath}
	sub, err := m.Subscribe(args)
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	sub.TouchLastReport(time.Now()) // prime lastReport so keepalive doesn't immediately fire

	ctx := context.Background()
	t0 := time.Now()

	// Fire a Critical event at t0.
	m.OnEventFired(subscription.EventFiring{
		Path:     eventPath,
		Number:   1,
		Priority: im.EventPriorityCritical,
	})

	// Mark dirty at t0 + 100 ms (within the 2 s floor).
	m.OnAttributeChanged(attrPath)

	// Tick at t0 + 100 ms — event drains (Critical, bypasses floor);
	// dirty must NOT drain in the same tick.
	m.Tick(ctx, t0.Add(100*time.Millisecond))

	mu.Lock()
	gotReporter := reporterCalls
	mu.Unlock()
	evMu.Lock()
	gotEvent := eventCalls
	evMu.Unlock()

	if gotEvent != 1 {
		t.Errorf("eventCalls = %d, want 1 (critical event drained)", gotEvent)
	}
	if gotReporter != 0 {
		t.Errorf("reporterCalls = %d, want 0 (dirty must be blocked within floor)", gotReporter)
	}

	// After the floor elapses (t0 + 3 s > 2 s floor), dirty drains.
	m.Tick(ctx, t0.Add(3*time.Second))

	mu.Lock()
	gotReporter2 := reporterCalls
	mu.Unlock()
	if gotReporter2 != 1 {
		t.Errorf("reporterCalls after floor = %d, want 1 (dirty should drain after floor)", gotReporter2)
	}
}

// ---- MinIntervalFloor > MaxIntervalCeiling not re-validated post-clamp ----

// TestSubscribe_PostClampInversion_ErrCadenceInvertedAfterClamp verifies that
// a subscribe request whose cadence inverts after the manager's ceiling clamp
// is rejected with ErrCadenceInvertedAfterClamp. Without this check a
// min=10, max=30 request against a cfg.MaxIntervalCeiling=5 manager silently
// accepted min=10, max=5, causing drainDirtyIfElapsed to never fire.
func TestSubscribe_PostClampInversion_ErrCadenceInvertedAfterClamp(t *testing.T) {
	t.Parallel()
	cfg := subscription.Config{MaxIntervalCeilingSeconds: 5}
	m := newManager(cfg, nil)

	args := defaultArgs()
	args.MinIntervalFloor = 10   // valid: 10 <= 30
	args.MaxIntervalCeiling = 30 // but cfg caps max to 5 → post-clamp min=10 > max=5

	_, err := m.Subscribe(args)
	if !errors.Is(err, subscription.ErrCadenceInvertedAfterClamp) {
		t.Fatalf("expected ErrCadenceInvertedAfterClamp, got %v", err)
	}
}

// TestSubscribe_PostClampNoInversion_Succeeds verifies that a request that is
// valid both before and after clamping succeeds — ensuring the post-clamp check
// does not reject legitimate subscriptions.
func TestSubscribe_PostClampNoInversion_Succeeds(t *testing.T) {
	t.Parallel()
	cfg := subscription.Config{MaxIntervalCeilingSeconds: 30}
	m := newManager(cfg, nil)

	args := defaultArgs()
	args.MinIntervalFloor = 5
	args.MaxIntervalCeiling = 60 // capped to 30 → post-clamp min=5 ≤ max=30

	sub, err := m.Subscribe(args)
	if err != nil {
		t.Fatalf("Subscribe: unexpected error %v (post-clamp should be valid)", err)
	}
	if sub.MaxIntervalCeiling != 30 {
		t.Errorf("MaxIntervalCeiling = %d, want 30 (capped)", sub.MaxIntervalCeiling)
	}
}

// ---- Start / Stop lifecycle ----

func TestStartStop_Idempotent(t *testing.T) {
	t.Parallel()
	m := newManager(subscription.Config{}, nil)
	ctx := t.Context()

	m.Start(ctx)
	// Double Stop must not panic (sync.Once).
	m.Stop()
	m.Stop()
}

func TestStart_ContextCancel_RunGoroutineEnds(t *testing.T) {
	t.Parallel()
	m := newManager(subscription.Config{TickInterval: 10 * time.Millisecond}, nil)
	ctx, cancel := context.WithCancel(context.Background())

	m.Start(ctx)
	cancel() // signal goroutine to stop via ctx.Done()
	// Stop waits for the goroutine to drain; must return in finite time.
	done := make(chan struct{})
	go func() {
		m.Stop()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Stop() did not return within 2 s after ctx cancel")
	}
}

func TestStart_TickLoop_ReporterFires(t *testing.T) {
	// Not t.Parallel() — depends on real timer behaviour; keep isolated.
	ch := make(chan reporterCall, 8)
	cfg := subscription.Config{
		TickInterval:              10 * time.Millisecond,
		MinIntervalFloorSeconds:   1,
		MaxIntervalCeilingSeconds: 0, // will be defaulted to 3600 — override explicitly
	}
	// Use a very low MaxIntervalCeiling so the keep-alive fires quickly.
	// Override via MaxIntervalCeilingSeconds requires a non-zero value;
	// set it to 1 (minimum meaningful value) via a custom config.
	cfg2 := subscription.Config{
		TickInterval:              10 * time.Millisecond,
		MaxIntervalCeilingSeconds: 1,
		MinIntervalFloorSeconds:   1,
	}
	_ = cfg // unused; use cfg2 below
	m := newManager(cfg2, chanReporter(ch))

	args := defaultArgs()
	args.MinIntervalFloor = 1
	args.MaxIntervalCeiling = 1
	_, err := m.Subscribe(args)
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}

	ctx := context.Background()
	m.Start(ctx)
	defer m.Stop()

	// Wait for at least one keep-alive to fire (MaxIntervalCeiling = 1 s,
	// TickInterval = 10 ms → should fire well within 3 s).
	select {
	case <-ch:
		// success
	case <-time.After(3 * time.Second):
		t.Fatal("Reporter not called via Start tick loop within 3 s")
	}
}

// ---- CloseEndpoint ----

// TestCloseEndpoint_ClosesMatchingSubscriptions verifies that CloseEndpoint
// tears down every subscription whose AttributePaths contain the removed
// endpoint. A subscription that does NOT reference the removed endpoint must
// survive.
func TestCloseEndpoint_ClosesMatchingSubscriptions(t *testing.T) {
	t.Parallel()
	m := newManager(subscription.Config{}, nil)

	// sub1 targets endpoint 5.
	args1 := defaultArgs()
	args1.AttributePaths = []im.ConcreteAttributePath{mkPath(5, 0x0006, 0x0000)}
	sub1, err := m.Subscribe(args1)
	if err != nil {
		t.Fatalf("Subscribe sub1: %v", err)
	}

	// sub2 targets endpoint 7 — must survive.
	args2 := defaultArgs()
	args2.PeerNodeID = 0xBEEF
	args2.SessionID = 2
	args2.AttributePaths = []im.ConcreteAttributePath{mkPath(7, 0x0006, 0x0000)}
	_, err = m.Subscribe(args2)
	if err != nil {
		t.Fatalf("Subscribe sub2: %v", err)
	}

	// Remove endpoint 5.
	reaped := m.CloseEndpoint(5)
	if reaped != 1 {
		t.Errorf("CloseEndpoint(5): reaped %d, want 1", reaped)
	}
	if m.Active() != 1 {
		t.Errorf("Active() = %d, want 1 (sub2 must survive)", m.Active())
	}
	// sub1 must be closed.
	if !sub1.IsClosed() {
		t.Error("sub1 (endpoint=5) should be closed after CloseEndpoint(5)")
	}
}

// TestCloseEndpoint_WildcardEndpointPath considers a subscription whose
// AttributePaths have HasEndpoint=false (wildcard). A wildcard path matches
// every endpoint by definition, so it must also be reaped when an endpoint
// is removed — the subscription can no longer be guaranteed valid.
//
// NOTE: The implementation skips wildcard-endpoint paths (HasEndpoint=false)
// because wildcard subscribers survive endpoint removal; only explicit-
// endpoint subscriptions are closed. This test documents that expectation.
func TestCloseEndpoint_WildcardPathSurvives(t *testing.T) {
	t.Parallel()
	m := newManager(subscription.Config{}, nil)

	// Subscribe with a wildcard path (HasEndpoint=false).
	args := defaultArgs()
	args.AttributePaths = []im.ConcreteAttributePath{mkFullWildcard()}
	_, err := m.Subscribe(args)
	if err != nil {
		t.Fatalf("Subscribe wildcard: %v", err)
	}

	// Removing endpoint 5 must not affect the wildcard subscription.
	reaped := m.CloseEndpoint(5)
	if reaped != 0 {
		t.Errorf("CloseEndpoint(5) on wildcard subscriber: reaped %d, want 0", reaped)
	}
	if m.Active() != 1 {
		t.Errorf("Active() = %d, want 1 (wildcard sub must survive)", m.Active())
	}
}

// TestCloseEndpoint_EventPathAlsoMatches verifies that CloseEndpoint
// also reaped subscriptions where the removed endpoint is referenced
// only via EventPaths (not AttributePaths).
func TestCloseEndpoint_EventPathAlsoMatches(t *testing.T) {
	t.Parallel()
	m := newManager(subscription.Config{}, nil)

	args := defaultArgs()
	args.AttributePaths = nil // no attribute path
	args.EventPaths = []im.ConcreteEventPath{
		{Endpoint: 9, HasEndpoint: true, Cluster: 0x003B, HasCluster: true, Event: 0x03, HasEvent: true},
	}
	sub, err := m.Subscribe(args)
	if err != nil {
		t.Fatalf("Subscribe event-only: %v", err)
	}

	reaped := m.CloseEndpoint(9)
	if reaped != 1 {
		t.Errorf("CloseEndpoint(9): reaped %d, want 1", reaped)
	}
	if !sub.IsClosed() {
		t.Error("event-path subscription to endpoint=9 should be closed")
	}
}

// ---- CloseSession for PASE path ----

// TestCloseSession_ClosesMatchingSessionSubscription verifies that
// CloseSession tears down subscriptions on the given session.
// This is the mechanism used by the KeepSubscriptions=false PASE path.
func TestCloseSession_ClosesMatchingSessionSubscription(t *testing.T) {
	t.Parallel()
	m := newManager(subscription.Config{}, nil)

	// Two subscriptions on different sessions.
	args1 := defaultArgs()
	args1.SessionID = 100
	args1.FabricIndex = 0 // PASE
	sub1, err := m.Subscribe(args1)
	if err != nil {
		t.Fatalf("Subscribe sub1: %v", err)
	}

	args2 := defaultArgs()
	args2.SessionID = 200
	args2.FabricIndex = 0
	args2.PeerNodeID = 0xBEEF
	sub2, err := m.Subscribe(args2)
	if err != nil {
		t.Fatalf("Subscribe sub2: %v", err)
	}

	// Close session 100: only sub1 must be torn down.
	m.CloseSession(100)
	if !sub1.IsClosed() {
		t.Error("sub1 (session=100) should be closed after CloseSession(100)")
	}
	if sub2.IsClosed() {
		t.Error("sub2 (session=200) must survive CloseSession(100)")
	}
	if m.Active() != 1 {
		t.Errorf("Active() = %d, want 1", m.Active())
	}
}

// TestSubjectHasActiveSubscription verifies the diagnostic helper that
// reports whether a (fabric, peer) pair holds any active subscription.
func TestSubjectHasActiveSubscription(t *testing.T) {
	t.Parallel()
	m := subscription.NewManager(subscription.Config{MaxSubscriptionsPerFabric: 4}, nil, nil)

	if m.SubjectHasActiveSubscription(1, 0xABCD) {
		t.Fatal("no subscriptions yet — must return false")
	}

	args := defaultArgs()
	args.FabricIndex = 1
	args.PeerNodeID = 0xABCD
	_, err := m.Subscribe(args)
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}

	if !m.SubjectHasActiveSubscription(1, 0xABCD) {
		t.Error("should have active subscription for (fabric=1, peer=0xABCD)")
	}
	if m.SubjectHasActiveSubscription(1, 0x0000) {
		t.Error("different peer should have no subscription")
	}
	if m.SubjectHasActiveSubscription(2, 0xABCD) {
		t.Error("different fabric should have no subscription")
	}
}

// TestCloseFabricExcept_PreservesExceptSession verifies that
// CloseFabricExcept closes every subscription on the target fabric whose
// SessionID != exceptSessionID while leaving the excepted subscription
// alive. The per-fabric counter must reflect the surviving subscription
// (count==1), not 0. This mirrors the UpdateNOC invariant: the invoking
// session's subscriptions must stay alive so the NOCResponse reaches the
// wire before the commissioner re-CASEs.
func TestCloseFabricExcept_PreservesExceptSession(t *testing.T) {
	t.Parallel()
	cfg := subscription.Config{MaxSubscriptionsPerFabric: 4}
	m := newManager(cfg, nil)

	// Two fabric-1 subscriptions on different sessions.
	args10 := defaultArgs()
	args10.FabricIndex = 1
	args10.SessionID = 10
	sub10, err := m.Subscribe(args10)
	if err != nil {
		t.Fatalf("Subscribe sub10: %v", err)
	}
	args20 := defaultArgs()
	args20.FabricIndex = 1
	args20.SessionID = 20
	args20.PeerNodeID = 0xBEEF
	sub20, err := m.Subscribe(args20)
	if err != nil {
		t.Fatalf("Subscribe sub20: %v", err)
	}

	// One fabric-2 subscription — must survive regardless.
	args2 := defaultArgs()
	args2.FabricIndex = 2
	args2.SessionID = 30
	args2.PeerNodeID = 0xCAFE
	subF2, err := m.Subscribe(args2)
	if err != nil {
		t.Fatalf("Subscribe subF2: %v", err)
	}

	// Preserve sub20 (session=20); close sub10.
	m.CloseFabricExcept(1, 20)

	// sub10 (session=10) must be closed and unreachable.
	if !sub10.IsClosed() {
		t.Error("sub10 (session=10) must be closed after CloseFabricExcept(1,20)")
	}
	if _, err := m.Get(sub10.ID); !errors.Is(err, subscription.ErrNotFound) {
		t.Errorf("Get(sub10): expected ErrNotFound, got %v", err)
	}

	// sub20 (session=20) must survive and still be findable.
	if sub20.IsClosed() {
		t.Error("sub20 (session=20, excepted) must NOT be closed")
	}
	if _, err := m.Get(sub20.ID); err != nil {
		t.Errorf("Get(sub20): expected hit, got %v", err)
	}

	// Fabric-2 subscription must be untouched.
	if subF2.IsClosed() {
		t.Error("subF2 (fabric=2) must NOT be closed")
	}
	if _, err := m.Get(subF2.ID); err != nil {
		t.Errorf("Get(subF2): expected hit, got %v", err)
	}

	// Total active must be 2 (sub20 + subF2).
	if m.Active() != 2 {
		t.Fatalf("Active() = %d, want 2 (sub20 + subF2)", m.Active())
	}

	// Fabric-1 still has one surviving subscription — a fresh subscribe under
	// the same fabric must succeed (quota allows it), proving the per-fabric
	// counter was decremented for the closed sub only.
	argsNew := defaultArgs()
	argsNew.FabricIndex = 1
	argsNew.SessionID = 20
	argsNew.PeerNodeID = 0xAAAA
	if _, err := m.Subscribe(argsNew); err != nil {
		t.Errorf("Subscribe after CloseFabricExcept: unexpected error %v (per-fabric counter must reflect 1 remaining)", err)
	}
}

// TestCloseFabricExcept_ZeroExcept_ClosesAll verifies that
// exceptSessionID==0 terminates every subscription on the fabric because
// no subscription carries SessionID==0.
func TestCloseFabricExcept_ZeroExcept_ClosesAll(t *testing.T) {
	t.Parallel()
	m := newManager(subscription.Config{}, nil)

	args1 := defaultArgs()
	args1.FabricIndex = 1
	args1.SessionID = 10
	sub1, _ := m.Subscribe(args1)

	args2 := defaultArgs()
	args2.FabricIndex = 1
	args2.SessionID = 20
	args2.PeerNodeID = 0xBEEF
	sub2, _ := m.Subscribe(args2)

	argsF2 := defaultArgs()
	argsF2.FabricIndex = 2
	argsF2.SessionID = 30
	argsF2.PeerNodeID = 0xCAFE
	subF2, _ := m.Subscribe(argsF2)

	m.CloseFabricExcept(1, 0) // 0 matches nothing

	if !sub1.IsClosed() {
		t.Error("sub1 must be closed (exceptSessionID=0 matches nothing)")
	}
	if !sub2.IsClosed() {
		t.Error("sub2 must be closed (exceptSessionID=0 matches nothing)")
	}
	if subF2.IsClosed() {
		t.Error("subF2 (fabric=2) must survive")
	}
	if m.Active() != 1 {
		t.Fatalf("Active() = %d, want 1 (only subF2 survives)", m.Active())
	}
}

// TestFabricHasAtLeastOneActiveSubscription verifies the diagnostic
// helper that reports whether a fabric holds any active subscription.
func TestFabricHasAtLeastOneActiveSubscription(t *testing.T) {
	t.Parallel()
	m := subscription.NewManager(subscription.Config{MaxSubscriptionsPerFabric: 4}, nil, nil)

	if m.FabricHasAtLeastOneActiveSubscription(1) {
		t.Fatal("no subscriptions yet — must return false")
	}

	args := defaultArgs()
	args.FabricIndex = 1
	sub, err := m.Subscribe(args)
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}

	if !m.FabricHasAtLeastOneActiveSubscription(1) {
		t.Error("fabric 1 should have at least one subscription")
	}
	if m.FabricHasAtLeastOneActiveSubscription(2) {
		t.Error("fabric 2 should have no subscription")
	}

	// After closing, fabric should report no active subscription.
	_ = m.Close(sub.ID)
	if m.FabricHasAtLeastOneActiveSubscription(1) {
		t.Error("after Close, fabric 1 should have no subscription")
	}
}
