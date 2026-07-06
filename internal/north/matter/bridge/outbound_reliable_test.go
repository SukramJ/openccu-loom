// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package bridge

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/north/matter/im"
	"github.com/SukramJ/openccu-loom/internal/north/matter/im/subscription"
	"github.com/SukramJ/openccu-loom/internal/north/matter/transport/mrp"
)

// TestOutboundReliable_TrackAck verifies the happy path: Track a
// counter, Ack it, Pending drops to zero.
func TestOutboundReliable_TrackAck(t *testing.T) {
	t.Parallel()
	tr := newOutboundReliableTracker(nil)
	dest := &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 4242}
	tr.Track(101, 0, 0, []byte{0xDE, 0xAD}, dest, time.Now())
	if tr.Pending() != 1 {
		t.Errorf("Pending after Track: got %d, want 1", tr.Pending())
	}
	if !tr.Ack(0, 101) {
		t.Error("Ack: returned false on a tracked counter")
	}
	if tr.Pending() != 0 {
		t.Errorf("Pending after Ack: got %d, want 0", tr.Pending())
	}
	// Stray Ack on a counter we never tracked.
	if tr.Ack(0, 999) {
		t.Error("Ack on stray counter: returned true, want false")
	}
}

// TestOutboundReliable_TickRetransmits verifies that an unacked
// pending entry is re-emitted by Tick once nextSendAt has elapsed,
// the retry counter advances, and the next Tick uses a wider
// backoff window.
//
// Session 0 (unknown / PASE) resolves no per-session base interval, so
// the tracker falls back to [mrp.SessionIdleIntervalDefault] (500 ms)
// per matter.js SessionIntervals.ts:45-49 — the deadline window here
// must clear that base's jitter ceiling
// (500ms × 1.1 × 1.25 = 687.5ms), not the old flat 300 ms.
func TestOutboundReliable_TickRetransmits(t *testing.T) {
	t.Parallel()
	tr := newOutboundReliableTracker(nil)
	dest := &net.UDPAddr{IP: net.IPv4(127, 0, 0, 2), Port: 5151}
	now := time.Unix(0, 0)
	tr.Track(7, 0, 0, []byte{0xAA}, dest, now)

	calls := 0
	send := func(addr *net.UDPAddr, payload []byte) error {
		calls++
		if addr != dest {
			t.Errorf("send: dest = %v, want %v", addr, dest)
		}
		if len(payload) == 0 || payload[0] != 0xAA {
			t.Errorf("send: payload = %x, want 0xAA…", payload)
		}
		return nil
	}

	// Tick before deadline → no work.
	if got := tr.Tick(now, send); len(got) != 0 {
		t.Errorf("Tick before deadline: %d results, want 0", len(got))
	}
	if calls != 0 {
		t.Errorf("send calls before deadline: %d, want 0", calls)
	}

	// Tick after deadline → re-emit + advance retry counter. 700ms
	// clears the 687.5ms jitter ceiling of the 500ms idle-default base.
	due := now.Add(700 * time.Millisecond)
	if got := tr.Tick(due, send); len(got) != 1 || got[0].Err != nil {
		t.Errorf("Tick at deadline: %+v, want 1 success", got)
	}
	if calls != 1 {
		t.Errorf("send calls after Tick: %d, want 1", calls)
	}
	if tr.Pending() != 1 {
		t.Errorf("Pending after re-emit: %d, want 1 (entry stays until Ack)", tr.Pending())
	}
}

// TestOutboundReliable_TickGivesUp verifies that Tick abandons an
// entry after [mrp.MaxRetransmissions] retries and reports it via
// [mrp.ErrMaxRetransmissionsReached] so the caller can drop the
// associated subscription.
func TestOutboundReliable_TickGivesUp(t *testing.T) {
	t.Parallel()
	tr := newOutboundReliableTracker(nil)
	tr.Track(3, 0, 0, []byte{0x01}, &net.UDPAddr{}, time.Unix(0, 0))

	// Drive past every retry: keep advancing time and ticking.
	now := time.Unix(0, 0)
	send := func(*net.UDPAddr, []byte) error { return nil }
	for i := range mrp.MaxRetransmissions {
		now = now.Add(10 * time.Second) // way past any backoff window
		results := tr.Tick(now, send)
		if len(results) != 1 || results[0].Err != nil {
			t.Fatalf("retry %d: results = %+v", i, results)
		}
	}
	// One more tick after the cap → abandonment.
	now = now.Add(10 * time.Second)
	results := tr.Tick(now, send)
	if len(results) != 1 {
		t.Fatalf("final Tick: %d results, want 1", len(results))
	}
	if !errors.Is(results[0].Err, mrp.ErrMaxRetransmissionsReached) {
		t.Errorf("final Err = %v, want ErrMaxRetransmissionsReached", results[0].Err)
	}
	if tr.Pending() != 0 {
		t.Errorf("Pending after abandonment: %d, want 0", tr.Pending())
	}
}

// TestOutboundReliable_AbandonExchange verifies that AbandonExchange
// drops every pending entry tagged with the matching exchangeID,
// leaves untagged / different-exchange entries in place, returns the
// drop count, and wakes a WaitForAck caller blocked on a dropped
// counter with ErrAckWaitTimeout. Mirrors the Sigma3-receive cleanup
// path: once the commissioner has progressed past Sigma2 the bridge
// must stop retransmitting the now-superseded Sigma2 datagrams.
func TestOutboundReliable_AbandonExchange(t *testing.T) {
	t.Parallel()
	tr := newOutboundReliableTracker(nil)
	dest := &net.UDPAddr{IP: net.IPv4(127, 0, 0, 3), Port: 5353}
	const targetExchange = uint16(0xABCD)
	const otherExchange = uint16(0x1234)

	// Three entries on the target exchange, two on a different one.
	tr.Track(1, 0, targetExchange, []byte{0x01}, dest, time.Now())
	tr.Track(2, 0, targetExchange, []byte{0x02}, dest, time.Now())
	tr.Track(3, 0, otherExchange, []byte{0x03}, dest, time.Now())
	tr.Track(4, 0, targetExchange, []byte{0x04}, dest, time.Now())
	tr.Track(5, 0, otherExchange, []byte{0x05}, dest, time.Now())

	if tr.Pending() != 5 {
		t.Fatalf("Pending before abandon: got %d, want 5", tr.Pending())
	}

	// Block a waiter on counter=2 (target exchange) to verify it gets
	// unblocked when AbandonExchange runs.
	waitErrCh := make(chan error, 1)
	go func() {
		waitErrCh <- tr.WaitForAck(t.Context(), 0, 2)
	}()
	// Ensure the goroutine has installed its waiter channel.
	time.Sleep(10 * time.Millisecond)

	cleared := tr.AbandonExchange(targetExchange)
	if cleared != 3 {
		t.Errorf("AbandonExchange: cleared %d, want 3", cleared)
	}
	if tr.Pending() != 2 {
		t.Errorf("Pending after abandon: got %d, want 2", tr.Pending())
	}
	// Confirm survivors are the otherExchange counters.
	if !tr.Ack(0, 3) {
		t.Error("Ack(3) after abandon: returned false, want survivor on otherExchange")
	}
	if !tr.Ack(0, 5) {
		t.Error("Ack(5) after abandon: returned false, want survivor on otherExchange")
	}
	if tr.Pending() != 0 {
		t.Errorf("Pending after acking survivors: got %d, want 0", tr.Pending())
	}

	// The blocked waiter must have been released. ErrAckWaitTimeout is
	// the abandon marker; a nil-return is also acceptable (channel
	// close on best-effort path) since both signal "stop waiting".
	select {
	case err := <-waitErrCh:
		if err != nil && !errors.Is(err, ErrAckWaitTimeout) {
			t.Errorf("waiter resolved with unexpected err: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("AbandonExchange did not release the WaitForAck waiter within 1s")
	}

	// Abandoning an unknown exchange is a no-op.
	if got := tr.AbandonExchange(0xBEEF); got != 0 {
		t.Errorf("AbandonExchange on unknown exchange: cleared %d, want 0", got)
	}
}

// TestOutboundReliable_PerSessionBaseInterval verifies that Track /
// Tick honour a per-session base-interval resolver: a session with a
// short resolved base (100ms) becomes due well before a session that
// falls back to the spec idle default (500ms, resolver returns 0).
// Bounds are computed from the deterministic min/max of
// [mrp.BackoffDuration] at transmission 0 (jitter ∈ [0, 0.25)) so the
// due/not-due boundary is asserted without sleeping or depending on
// the jitter draw. Mirrors matter.js MRP.ts:129
// retransmissionIntervalOf's per-peer base-interval selection.
func TestOutboundReliable_PerSessionBaseInterval(t *testing.T) {
	t.Parallel()
	const sessionFast = uint16(7)
	const sessionSlow = uint16(9)
	baseFor := func(sessionID uint16, _ time.Time) time.Duration {
		if sessionID == sessionFast {
			return 100 * time.Millisecond
		}
		return 0 // unresolved → tracker falls back to the spec idle default (500ms)
	}
	tr := newOutboundReliableTracker(baseFor)
	dest := &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 4040}
	t0 := time.Unix(0, 0)

	tr.Track(701, sessionFast, 0, []byte{0x01}, dest, t0)
	tr.Track(702, sessionSlow, 0, []byte{0x02}, dest, t0)

	var fired []uint32
	send := func(_ *net.UDPAddr, payload []byte) error {
		fired = append(fired, uint32(payload[0]))
		return nil
	}

	// Deterministic min/max for transmission 0: base × MARGIN (jitter=0)
	// .. base × MARGIN × (1 + JITTER_FACTOR) (jitter→1, exclusive).
	minFast := 100 * time.Millisecond * 11 / 10         // 110ms
	maxFast := 100 * time.Millisecond * 11 / 10 * 5 / 4 // 137.5ms
	minSlow := 500 * time.Millisecond * 11 / 10         // 550ms
	maxSlow := 500 * time.Millisecond * 11 / 10 * 5 / 4 // 687.5ms

	// Before session7's minimum bound: neither entry can be due.
	if got := tr.Tick(t0.Add(minFast-time.Millisecond), send); len(got) != 0 {
		t.Fatalf("Tick before session7 min bound: %d results, want 0 (got %+v)", len(got), got)
	}

	// Past session7's maximum bound: session7 MUST be due, session9
	// (min bound 550ms) must NOT.
	got := tr.Tick(t0.Add(maxFast+time.Millisecond), send)
	if len(got) != 1 || got[0].Counter != 701 {
		t.Fatalf("Tick past session7 max bound: got %+v, want exactly counter=701", got)
	}
	if len(fired) != 1 || fired[0] != 0x01 {
		t.Fatalf("fired payloads = %v, want [0x01]", fired)
	}
	// Clear session7's entry so it cannot re-fire and confuse the
	// session9 assertions below.
	tr.Ack(sessionFast, 701)

	// Before session9's minimum bound: nothing left to fire.
	if got := tr.Tick(t0.Add(minSlow-time.Millisecond), send); len(got) != 0 {
		t.Fatalf("Tick before session9 min bound: %d results, want 0 (got %+v)", len(got), got)
	}

	// Past session9's maximum bound: session9 MUST be due.
	got = tr.Tick(t0.Add(maxSlow+time.Millisecond), send)
	if len(got) != 1 || got[0].Counter != 702 {
		t.Fatalf("Tick past session9 max bound: got %+v, want exactly counter=702", got)
	}
}

// TestOutboundReliable_AckIsSessionScoped verifies the H4 fix: two
// entries tracked under the SAME MessageCounter value but DIFFERENT
// SessionIDs are independent pending entries. Ack for session A must
// clear only session A's entry — leaving session B's still pending —
// and a subsequent Ack for session B then clears B's. Before the
// (sessionID, counter) keying this was a cross-session counter
// collision: acking session A's message could also (or instead) clear
// session B's still-in-flight entry whenever their independently
// seeded MRP counters (Matter §4.5.4) happened to coincide.
func TestOutboundReliable_AckIsSessionScoped(t *testing.T) {
	t.Parallel()
	tr := newOutboundReliableTracker(nil)
	dest := &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 4141}
	const sessionA = uint16(11)
	const sessionB = uint16(22)
	const sharedCounter = uint32(555)

	tr.Track(sharedCounter, sessionA, 0, []byte{0xA1}, dest, time.Now())
	tr.Track(sharedCounter, sessionB, 0, []byte{0xB2}, dest, time.Now())
	if tr.Pending() != 2 {
		t.Fatalf("Pending after tracking both sessions: got %d, want 2", tr.Pending())
	}

	// Acking session A's counter must not touch session B's entry that
	// shares the same bare counter value.
	if !tr.Ack(sessionA, sharedCounter) {
		t.Error("Ack(sessionA, sharedCounter): returned false, want true")
	}
	if tr.Pending() != 1 {
		t.Errorf("Pending after acking session A: got %d, want 1 (session B must survive)", tr.Pending())
	}
	// Acking the same counter again under session A must now be a
	// stray (already cleared) — proves the first Ack did not
	// accidentally clear both entries.
	if tr.Ack(sessionA, sharedCounter) {
		t.Error("second Ack(sessionA, sharedCounter): returned true, want false (already cleared)")
	}
	if tr.Pending() != 1 {
		t.Errorf("Pending after stray re-Ack: got %d, want 1", tr.Pending())
	}

	// Acking session B's counter now clears the remaining entry.
	if !tr.Ack(sessionB, sharedCounter) {
		t.Error("Ack(sessionB, sharedCounter): returned false, want true")
	}
	if tr.Pending() != 0 {
		t.Errorf("Pending after acking session B: got %d, want 0", tr.Pending())
	}
}

// TestSubscription_AutoCloseOnMaxRetries verifies that when an
// outbound report counter hits the retransmit cap and the bridge has
// recorded its counter→subscription mapping, the bookkeeping is
// cleared. Pure white-box — operates on the maps directly because
// constructing a real subscription.Manager requires more wiring than
// the assertion needs.
func TestSubscription_AutoCloseOnMaxRetries(t *testing.T) {
	t.Parallel()
	b := &Bridge{}
	const sessionID = uint16(0)
	const counter = uint32(99)
	const subID = uint32(7)
	b.reportCounterOwner.Store(reportCounterKey(sessionID, counter), subID)
	b.routing.subTargets.Store(subID, subTarget{})

	b.closeSubscriptionByCounter(sessionID, counter)

	if _, ok := b.reportCounterOwner.Load(reportCounterKey(sessionID, counter)); ok {
		t.Error("counter→subID mapping still present after close")
	}
	if _, ok := b.routing.subTargets.Load(subID); ok {
		t.Error("subTarget still present after close")
	}
}

// TestCloseSubscriptionByCounter_IsSessionScoped verifies the H4 fix
// on the bridge's reportCounterOwner bookkeeping: two subscriptions
// whose ongoing reports happen to reuse the same bare MessageCounter
// value under different SessionIDs are independent. Reaping session
// A's counter (retransmit-cap eviction) must not also reap session
// B's still-healthy subscription that shares the counter value.
func TestCloseSubscriptionByCounter_IsSessionScoped(t *testing.T) {
	t.Parallel()
	b := &Bridge{}
	const sessionA = uint16(11)
	const sessionB = uint16(22)
	const sharedCounter = uint32(321)
	const subA = uint32(101)
	const subB = uint32(202)

	b.reportCounterOwner.Store(reportCounterKey(sessionA, sharedCounter), subA)
	b.reportCounterOwner.Store(reportCounterKey(sessionB, sharedCounter), subB)
	b.routing.subTargets.Store(subA, subTarget{})
	b.routing.subTargets.Store(subB, subTarget{})

	b.closeSubscriptionByCounter(sessionA, sharedCounter)

	if _, ok := b.reportCounterOwner.Load(reportCounterKey(sessionA, sharedCounter)); ok {
		t.Error("session A counter→subID mapping still present after close")
	}
	if _, ok := b.routing.subTargets.Load(subA); ok {
		t.Error("session A subTarget still present after close")
	}
	// Session B's entries — sharing the bare counter value — must
	// survive untouched.
	if _, ok := b.reportCounterOwner.Load(reportCounterKey(sessionB, sharedCounter)); !ok {
		t.Error("session B counter→subID mapping was reaped by session A's close")
	}
	if _, ok := b.routing.subTargets.Load(subB); !ok {
		t.Error("session B subTarget was reaped by session A's close")
	}
}

// TestSubscription_ReleaseCounterOnPeerAck verifies that a
// peer-piggybacked ACK clears the counter→subID mapping so a later
// max-retries event on a different counter doesn't accidentally
// reap the same subscription.
func TestSubscription_ReleaseCounterOnPeerAck(t *testing.T) {
	t.Parallel()
	b := &Bridge{}
	const sessionID = uint16(0)
	const counter = uint32(0xCAFE)
	b.reportCounterOwner.Store(reportCounterKey(sessionID, counter), uint32(42))

	b.releaseReportCounter(sessionID, counter)

	if _, ok := b.reportCounterOwner.Load(reportCounterKey(sessionID, counter)); ok {
		t.Error("counter mapping still present after release")
	}
}

// TestOutboundReliable_AckStrayWithWaiter verifies that a stray Ack on a
// counter that has no pending entry but does have a waiter closes the channel
// and returns false.
func TestOutboundReliable_AckStrayWithWaiter(t *testing.T) {
	t.Parallel()
	tr := newOutboundReliableTracker(nil)
	// Install a waiter directly without a matching pending entry.
	ch := make(chan error, 1)
	const sessionID = uint16(0)
	tr.mu.Lock()
	tr.waiters[outboundKey{sessionID: sessionID, counter: 888}] = ch
	tr.mu.Unlock()

	// Ack the counter — no pending entry, but waiter exists.
	if got := tr.Ack(sessionID, 888); got {
		t.Error("Ack returned true for stray counter; want false")
	}
	// Channel must be closed.
	select {
	case _, ok := <-ch:
		if ok {
			t.Error("channel should be closed (receive would block on non-closed)")
		}
	default:
		t.Error("channel was not closed after stray Ack with waiter")
	}
}

// TestOutboundReliable_TickSendError verifies that a send error is reported
// in the result slice but the entry is NOT removed from the pending map
// (it will be retried on the next tick).
func TestOutboundReliable_TickSendError(t *testing.T) {
	t.Parallel()
	tr := newOutboundReliableTracker(nil)
	dest := &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 5353}
	now := time.Unix(0, 0)
	tr.Track(42, 0, 0, []byte{0xAA}, dest, now)

	sendErr := errors.New("simulated send failure")
	failSend := func(*net.UDPAddr, []byte) error { return sendErr }

	// 700ms clears the 687.5ms jitter ceiling of the 500ms idle-default
	// base a session-0 (unknown) entry falls back to.
	due := now.Add(700 * time.Millisecond)
	results := tr.Tick(due, failSend)
	if len(results) != 1 {
		t.Fatalf("Tick: %d results, want 1", len(results))
	}
	if !errors.Is(results[0].Err, sendErr) {
		t.Errorf("Err = %v, want %v", results[0].Err, sendErr)
	}
	// Entry must stay in the pending map for the next retry.
	if tr.Pending() != 1 {
		t.Errorf("Pending after send error: %d, want 1 (entry must stay)", tr.Pending())
	}
}

// TestWaitForAck_NotPending_ReturnsNilImmediately verifies that calling
// WaitForAck on a counter that was never tracked (or already acked)
// returns nil immediately without blocking.
func TestWaitForAck_NotPending_ReturnsNilImmediately(t *testing.T) {
	t.Parallel()
	tr := newOutboundReliableTracker(nil)
	// Counter 999 was never tracked.
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := tr.WaitForAck(ctx, 0, 999); err != nil {
		t.Errorf("not-pending counter: want nil, got %v", err)
	}
}

// TestWaitForAck_ContextCancelled verifies that WaitForAck returns
// ctx.Err() when the context is cancelled before the ACK arrives.
func TestWaitForAck_ContextCancelled(t *testing.T) {
	t.Parallel()
	tr := newOutboundReliableTracker(nil)
	dest := &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 1234}
	tr.Track(77, 0, 0, []byte{0x01}, dest, time.Now())

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		errCh <- tr.WaitForAck(ctx, 0, 77)
	}()
	// Cancel the context — unblocks WaitForAck.
	cancel()

	select {
	case err := <-errCh:
		if err == nil {
			t.Error("WaitForAck after cancel: want non-nil, got nil")
		}
	case <-time.After(time.Second):
		t.Fatal("WaitForAck did not return after context cancel")
	}
}

// TestWaitForAck_AckClosesChannel verifies that WaitForAck returns nil
// when the ACK arrives (channel closed by Ack) while the goroutine is
// blocked on the wait.
func TestWaitForAck_AckClosesChannel(t *testing.T) {
	t.Parallel()
	tr := newOutboundReliableTracker(nil)
	dest := &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 5678}
	const counter = uint32(88)
	tr.Track(counter, 0, 0, []byte{0xAA}, dest, time.Now())

	errCh := make(chan error, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	go func() {
		errCh <- tr.WaitForAck(ctx, 0, counter)
	}()
	// Give the goroutine time to install its waiter then Ack.
	time.Sleep(5 * time.Millisecond)
	tr.Ack(0, counter)

	select {
	case err := <-errCh:
		if err != nil {
			t.Errorf("WaitForAck after Ack: want nil, got %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("WaitForAck did not return after Ack")
	}
}

// TestArmStatusResponseWait_OrphanPreviousWaiter verifies that arming a
// second waiter on the same exchange gracefully closes the orphaned channel.
func TestArmStatusResponseWait_OrphanPreviousWaiter(t *testing.T) {
	t.Parallel()
	b := newStartedBridge(t)
	const exch = uint16(13)

	ch1 := b.armStatusResponseWait(0, exch)
	ch2 := b.armStatusResponseWait(0, exch) // second arm → ch1 is orphaned

	// ch1 must be closed by the second arm (orphan path).
	select {
	case <-ch1:
		// OK — orphaned channel was closed.
	case <-time.After(200 * time.Millisecond):
		t.Error("orphaned channel was not closed by second armStatusResponseWait")
	}

	// ch2 should still be live.
	select {
	case <-ch2:
		t.Error("second channel should still be open")
	default:
		// OK — live channel not yet closed.
	}

	// Clean up.
	b.disarmStatusResponseWait(0, exch)
}

// TestSubscription_CloseUnknownCounterNoOp verifies that the close
// path is safe to call with an unknown counter (race: peer ACKed
// before our Tick saw the abandonment).
func TestSubscription_CloseUnknownCounterNoOp(t *testing.T) {
	t.Parallel()
	b := &Bridge{}
	// Must not panic; Pending stays empty.
	b.closeSubscriptionByCounter(0, uint32(0xDEAD))
	count := 0
	b.routing.subTargets.Range(func(_, _ any) bool { count++; return true })
	if count != 0 {
		t.Errorf("subTargets unexpectedly populated: %d", count)
	}
}

// TestReport_PeerUnreachable_ClosesSubscriptionWithoutMRP verifies the
// send-error eviction path: once the consecutive send-error counter
// exceeds the matter.js-aligned retry cap (2), reportSubscription must
// drop the subTarget AND call m.Close(sub.ID) so the manager stops
// ticking a dead subscription.
//
// Uses newStartedBridge to get a real dispatcher and a running UDP
// listener; the send fails on every attempt because the session
// (SessionID=55) is not registered in the bridge's operational session
// manager.
func TestReport_PeerUnreachable_ClosesSubscriptionWithoutMRP(t *testing.T) {
	t.Parallel()

	b := newStartedBridge(t)
	mgr := subscription.NewManager(subscription.Config{}, nil, nil)

	b.mu.Lock()
	b.subManager = mgr
	b.mu.Unlock()

	// Admit a subscription.
	sub, err := mgr.Subscribe(subscription.SubscribeArgs{
		FabricIndex:        0,
		SessionID:          55,
		MinIntervalFloor:   1,
		MaxIntervalCeiling: 60,
		AttributePaths: []im.ConcreteAttributePath{
			{Endpoint: 2, Cluster: 0x0006, Attribute: 0x0000, HasEndpoint: true, HasCluster: true, HasAttribute: true},
		},
	})
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}

	// Plant a subTarget whose SessionID (55) is not in the session manager —
	// sendUnsolicitedIM will return ErrUnsolicitedSessionMissing.
	b.routing.subTargets.Store(sub.ID, subTarget{
		src:       &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 5540},
		sessionID: 55,
	})

	paths := []im.ConcreteAttributePath{
		{Endpoint: 2, Cluster: 0x0006, Attribute: 0x0000, HasEndpoint: true, HasCluster: true, HasAttribute: true},
	}

	// Drive reportSubscription past the retry cap (2): the first 2
	// failures must NOT evict (peer might be transiently unreachable);
	// the 3rd failure MUST evict.
	for i := 1; i <= 3; i++ {
		b.reportSubscription(context.Background(), sub, paths)
	}

	// subTarget must be deleted after the cap-exceeding call.
	if _, ok := b.routing.subTargets.Load(sub.ID); ok {
		t.Error("subTarget still present after 3 send failures — should have been deleted")
	}
	// Manager must have evicted the subscription.
	if mgr.Active() != 0 {
		t.Errorf("manager.Active() = %d after 3 send failures, want 0 (subscription must be closed)", mgr.Active())
	}
}

// TestReport_PeerUnreachable_RetriesBeforeEviction verifies the
// matter.js-aligned retry-before-cancel pattern: the first two
// consecutive send failures MUST keep the subscription alive (so a
// transient socket hiccup or fabric-reload race does not reap an
// otherwise-healthy subscription). Only the 3rd consecutive failure
// crosses the cap and triggers eviction.
//
// Mirrors matter.js ServerSubscription.ts retry-up-to-2-then-cancel
// behaviour.
func TestReport_PeerUnreachable_RetriesBeforeEviction(t *testing.T) {
	t.Parallel()

	b := newStartedBridge(t)
	mgr := subscription.NewManager(subscription.Config{}, nil, nil)

	b.mu.Lock()
	b.subManager = mgr
	b.mu.Unlock()

	sub, err := mgr.Subscribe(subscription.SubscribeArgs{
		FabricIndex:        0,
		SessionID:          55,
		MinIntervalFloor:   1,
		MaxIntervalCeiling: 60,
		AttributePaths: []im.ConcreteAttributePath{
			{Endpoint: 2, Cluster: 0x0006, Attribute: 0x0000, HasEndpoint: true, HasCluster: true, HasAttribute: true},
		},
	})
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	b.routing.subTargets.Store(sub.ID, subTarget{
		src:       &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 5540},
		sessionID: 55,
	})
	paths := []im.ConcreteAttributePath{
		{Endpoint: 2, Cluster: 0x0006, Attribute: 0x0000, HasEndpoint: true, HasCluster: true, HasAttribute: true},
	}

	// First failure: counter=1, subscription stays alive.
	b.reportSubscription(context.Background(), sub, paths)
	if _, ok := b.routing.subTargets.Load(sub.ID); !ok {
		t.Fatal("subscription evicted after 1 send error; want still active (retry-cap is 2)")
	}
	if mgr.Active() != 1 {
		t.Fatalf("manager.Active()=%d after 1 send error, want 1", mgr.Active())
	}

	// Second failure: counter=2, subscription stays alive.
	b.reportSubscription(context.Background(), sub, paths)
	if _, ok := b.routing.subTargets.Load(sub.ID); !ok {
		t.Fatal("subscription evicted after 2 send errors; want still active (retry-cap is 2)")
	}
	if mgr.Active() != 1 {
		t.Fatalf("manager.Active()=%d after 2 send errors, want 1", mgr.Active())
	}

	// Third failure: counter=3 exceeds cap → eviction.
	b.reportSubscription(context.Background(), sub, paths)
	if _, ok := b.routing.subTargets.Load(sub.ID); ok {
		t.Error("subscription not evicted after 3 send errors; want closed")
	}
	if mgr.Active() != 0 {
		t.Errorf("manager.Active()=%d after 3 send errors, want 0", mgr.Active())
	}
}
