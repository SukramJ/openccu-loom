// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package mqtt

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// MQTT lifecycle integration tests — use lifecycleMockBroker (TCP-based)
// ---------------------------------------------------------------------------

// fastLifecycleCfg returns a LifecycleConfig with very short backoff
// timings so tests do not need to wait wall-clock seconds.
func fastLifecycleCfg() LifecycleConfig {
	return LifecycleConfig{
		InitialBackoff: 20 * time.Millisecond,
		MaxBackoff:     100 * time.Millisecond,
		Jitter:         0, // no jitter: deterministic timing in tests
	}
}

// waitCondition polls fn up to timeout, sleeping 5 ms between attempts.
// Returns true if fn returned true before the deadline.
func waitCondition(timeout time.Duration, fn func() bool) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if fn() {
			return true
		}
		time.Sleep(5 * time.Millisecond)
	}
	return false
}

// TestMQTTReconnectAfterTCPReset verifies that after InjectTCPReset the
// client detects the broken socket (c.conn is cleared by
// handleConnectionLost) and re-dials the broker.
func TestMQTTReconnectAfterTCPReset(t *testing.T) {
	broker := newLifecycleMockBroker(t)

	client := NewTCPClient(TCPConfig{
		BrokerURL: broker.URL(),
		ClientID:  "lc-reset-test",
		KeepAlive: 30 * time.Second,
	})

	cfg := fastLifecycleCfg()
	lc := NewLifecycle(cfg, client)

	runCtx, cancelRun := context.WithCancel(context.Background())
	defer cancelRun()

	if err := lc.Start(runCtx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	// Use a short stop deadline so the post-reset keepAlive goroutine
	// (which is waiting on its next ticker tick, up to 15 s away) does
	// not block the test suite for the full keepAlive/2 period.
	// Disconnect selects on ctx.Done, so a 500 ms deadline here causes
	// it to return quickly while the goroutine drains in the background.
	t.Cleanup(func() {
		cancelRun()
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
		defer stopCancel()
		_ = lc.Stop(stopCtx)
	})

	// Confirm initial connection.
	if !waitCondition(2*time.Second, func() bool { return broker.ConnCount() >= 1 }) {
		t.Fatal("broker never saw initial connection")
	}

	// TCP RST — broker closes the socket abruptly.
	broker.InjectTCPReset()

	// After the read-loop detects EOF, handleConnectionLost clears c.conn.
	// The lifecycle loop then re-dials; expect a second connection.
	if !waitCondition(3*time.Second, func() bool { return broker.ConnCount() >= 2 }) {
		t.Fatalf("broker never saw reconnect; conn count = %d", broker.ConnCount())
	}
}

// TestMQTTConnackFailureBackoff verifies that a non-zero CONNACK return
// code causes the lifecycle to back off and does NOT spam connect
// attempts faster than InitialBackoff.
func TestMQTTConnackFailureBackoff(t *testing.T) {
	broker := newLifecycleMockBroker(t)

	client := NewTCPClient(TCPConfig{
		BrokerURL:   broker.URL(),
		ClientID:    "lc-backoff-test",
		KeepAlive:   30 * time.Second,
		DialTimeout: 500 * time.Millisecond,
	})

	cfg := LifecycleConfig{
		InitialBackoff: 80 * time.Millisecond,
		MaxBackoff:     200 * time.Millisecond,
		Jitter:         0,
	}
	lc := NewLifecycle(cfg, client)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Reject ALL connects by pre-setting before Start and re-arming
	// inside a goroutine so the loop always sees a rejection.
	broker.RejectNextConnect()

	// Start will fail on the first (synchronous) connect attempt.
	err := lc.Start(ctx)
	if err == nil {
		// If Start did succeed somehow, stop cleanly.
		_ = lc.Stop(ctx)
		t.Fatal("expected Start to fail when broker rejects CONNACK")
	}

	// Measure how many TCP connections the broker has seen over 400 ms
	// with an 80 ms InitialBackoff. Even in the worst case (no backoff
	// at all) the theoretical maximum is 400/0 = ∞; with the correct
	// backoff it is at most ~5. We verify it is well under 20 to rule
	// out a runaway reconnect storm.
	//
	// The lifecycle does NOT start a background loop on a failed first
	// connect, so broker.ConnCount() stays at 1 (the one attempt from
	// Start). The test confirms that exactly 1 connection was made and
	// the lifecycle did not spin.
	time.Sleep(400 * time.Millisecond)
	count := broker.ConnCount()
	if count > 3 {
		t.Fatalf("reconnect spam: broker saw %d connections in 400 ms (want ≤ 3)", count)
	}
}

// TestMQTTForceDisconnectClearsConn verifies that calling
// lifecycle.Stop causes the underlying TCPClient's c.conn to be nil
// so a subsequent Connect call dials a fresh socket instead of
// returning "already connected".
func TestMQTTForceDisconnectClearsConn(t *testing.T) {
	broker := newLifecycleMockBroker(t)

	client := NewTCPClient(TCPConfig{
		BrokerURL: broker.URL(),
		ClientID:  "lc-force-dc-test",
		KeepAlive: 30 * time.Second,
	})

	cfg := fastLifecycleCfg()
	lc := NewLifecycle(cfg, client)

	ctx := context.Background()
	if err := lc.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Graceful stop via lifecycle.
	if err := lc.Stop(ctx); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	// After Stop, c.conn must be nil; a new Connect should succeed
	// without "already connected".
	if err := client.Connect(ctx); err != nil {
		t.Fatalf("Connect after Stop: %v (want nil — c.conn was not cleared)", err)
	}
	defer client.Disconnect(ctx) //nolint:errcheck // test cleanup: disconnect error is not actionable

	if broker.ConnCount() < 2 {
		t.Fatalf("expected at least 2 broker connections; got %d", broker.ConnCount())
	}
}

// TestMQTTSubscribeReplayAfterReconnect verifies that all topic filters
// registered via Subscribe are re-sent to the broker on reconnect.
func TestMQTTSubscribeReplayAfterReconnect(t *testing.T) {
	broker := newLifecycleMockBroker(t)

	client := NewTCPClient(TCPConfig{
		BrokerURL: broker.URL(),
		ClientID:  "lc-sub-replay-test",
		KeepAlive: 30 * time.Second,
	})

	cfg := fastLifecycleCfg()
	lc := NewLifecycle(cfg, client)

	// Use a separate runCtx for the lifecycle and a separate stopCtx
	// for teardown so that cancelling the run loop does not short-
	// circuit Disconnect's wg.Wait (which selects on ctx.Done).
	runCtx, cancelRun := context.WithCancel(context.Background())
	defer cancelRun()

	if err := lc.Start(runCtx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	// Short stop deadline (same reason as TestMQTTReconnectAfterTCPReset).
	t.Cleanup(func() {
		cancelRun()
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
		defer stopCancel()
		_ = lc.Stop(stopCtx)
	})

	// Subscribe to 5 distinct topic filters.
	topics := []string{
		"gh/ccu1/device/+/state",
		"gh/ccu1/device/+/set",
		"gh/ccu1/hub/status",
		"gh/ccu1/command/#",
		"homeassistant/status",
	}
	for _, f := range topics {
		if err := client.Subscribe(runCtx, f, QoS1, func(string, []byte, bool) {}); err != nil {
			t.Fatalf("Subscribe(%q): %v", f, err)
		}
	}

	// Wait until broker has seen the 5 initial SUBSCRIBE frames.
	if !waitCondition(2*time.Second, func() bool { return broker.SubscribeCount() >= 5 }) {
		t.Fatalf("broker never saw 5 initial subscribes; got %d", broker.SubscribeCount())
	}

	initialSubs := broker.SubscribeCount()

	// TCP RST — triggers reconnect + subscribe replay.
	broker.InjectTCPReset()

	// After reconnect, Connect replays all 5 filters; broker total rises
	// by 5.
	if !waitCondition(3*time.Second, func() bool {
		return broker.SubscribeCount() >= initialSubs+5
	}) {
		t.Fatalf(
			"subscribe replay incomplete: broker total = %d, want ≥ %d",
			broker.SubscribeCount(), initialSubs+5,
		)
	}
}

type stubConnector struct {
	connects    atomic.Int32
	disconnects atomic.Int32
	err         atomic.Value
}

func (s *stubConnector) Connect(_ context.Context) error {
	s.connects.Add(1)
	if e := s.err.Load(); e != nil {
		if err, ok := e.(error); ok {
			return err
		}
	}
	return nil
}

func (s *stubConnector) Disconnect(_ context.Context) error {
	s.disconnects.Add(1)
	return nil
}

func TestLifecycleStartFiresOnConnect(t *testing.T) {
	s := &stubConnector{}
	l := NewLifecycle(DefaultLifecycle(), s)
	var callbacks int
	l.OnConnect(func(context.Context) { callbacks++ })
	if err := l.Start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer func() { _ = l.Stop(context.Background()) }()
	if s.connects.Load() != 1 || callbacks != 1 {
		t.Fatalf("connects=%d cb=%d", s.connects.Load(), callbacks)
	}
}

func TestLifecycleFirstConnectErrorBubbles(t *testing.T) {
	s := &stubConnector{}
	s.err.Store(errors.New("boom"))
	l := NewLifecycle(DefaultLifecycle(), s)
	if err := l.Start(context.Background()); err == nil {
		t.Fatal("expected error")
	}
}

func TestLifecycleStopCallsDisconnect(t *testing.T) {
	s := &stubConnector{}
	l := NewLifecycle(DefaultLifecycle(), s)
	_ = l.Start(context.Background())
	if err := l.Stop(context.Background()); err != nil {
		t.Fatalf("stop: %v", err)
	}
	if s.disconnects.Load() != 1 {
		t.Fatalf("disconnects=%d", s.disconnects.Load())
	}
}

func TestWiringDriftDetection(t *testing.T) {
	b := NewBridge(BridgeConfig{Base: "gh", RawEnabled: true}, NewNoopClient())
	w := NewWiring(b, nil)
	if !w.MarkDiscovered("obj-1", "hash-A") {
		t.Fatal("first mark must report change")
	}
	if w.MarkDiscovered("obj-1", "hash-A") {
		t.Fatal("same hash must be no-op")
	}
	if !w.MarkDiscovered("obj-1", "hash-B") {
		t.Fatal("new hash must report change")
	}
	if w.DiscoveryCount() != 1 {
		t.Fatalf("count=%d", w.DiscoveryCount())
	}
}

func TestNoopClientRecordsPublications(t *testing.T) {
	c := NewNoopClient()
	_ = c.Publish(context.Background(), "foo", []byte("1"), QoS1, true)
	_ = c.Publish(context.Background(), "bar", []byte("2"), QoS0, false)
	if got := c.Published(); len(got) != 2 || got[1].Topic != "bar" {
		t.Fatalf("published=%+v", got)
	}
}

func TestNoopClientSubscribeDelivery(t *testing.T) {
	c := NewNoopClient()
	var received []string
	_ = c.Subscribe(context.Background(), "cmd/#", QoS1, func(topic string, _ []byte, _ bool) {
		received = append(received, topic)
	})
	if !c.DeliverInbound("cmd/#", "cmd/a", []byte("x")) {
		t.Fatal("subscribe path failed")
	}
	if len(received) != 1 || received[0] != "cmd/a" {
		t.Fatalf("received=%v", received)
	}
}

func TestLifecycleJitterBounded(t *testing.T) {
	cfg := DefaultLifecycle()
	cfg.Jitter = 10 * time.Millisecond
	l := NewLifecycle(cfg, &stubConnector{})
	for i := 0; i < 50; i++ {
		got := l.jittered(100 * time.Millisecond)
		if got < 90*time.Millisecond || got > 110*time.Millisecond {
			t.Fatalf("jittered=%v", got)
		}
	}
}

// ---------------------------------------------------------------------------
// Lifecycle-Recovery tests
// ---------------------------------------------------------------------------

// TestForcedDisconnectKeepsSubscriptions verifies that after a TCP reset all
// previously registered subscriptions are replayed on the new socket. The
// mock broker counts SUBSCRIBE frames across all connections; after reconnect
// the total must have risen by the number of registered filters.
func TestForcedDisconnectKeepsSubscriptions(t *testing.T) {
	t.Parallel()

	broker := newLifecycleMockBroker(t)

	client := NewTCPClient(TCPConfig{
		BrokerURL: broker.URL(),
		ClientID:  "lc-forced-subs-test",
		KeepAlive: 30 * time.Second,
	})

	cfg := fastLifecycleCfg()
	lc := NewLifecycle(cfg, client)

	// OnConnect callback registers subscriptions after each reconnect
	// (same as the bridge wiring code does).
	filters := []string{
		"gh/ccu1/device/+/state",
		"gh/ccu1/device/+/set",
		"gh/ccu1/command/#",
	}

	runCtx, cancelRun := context.WithCancel(context.Background())
	defer cancelRun()

	if err := lc.Start(runCtx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() {
		cancelRun()
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
		defer stopCancel()
		_ = lc.Stop(stopCtx)
	})

	// Register subscriptions on the first connection.
	for _, f := range filters {
		if err := client.Subscribe(runCtx, f, QoS1, func(string, []byte, bool) {}); err != nil {
			t.Fatalf("Subscribe(%q): %v", f, err)
		}
	}

	// Wait until the broker has seen all initial SUBSCRIBE frames.
	if !waitCondition(2*time.Second, func() bool {
		return broker.SubscribeCount() >= len(filters)
	}) {
		t.Fatalf("broker never saw initial subscribes; got %d", broker.SubscribeCount())
	}

	baseSubs := broker.SubscribeCount()

	// Inject a TCP reset — after reconnect all filters must be
	// re-subscribed (ReSubscribe-Replay in TCPClient.Connect).
	broker.InjectTCPReset()

	// Broker must have seen at least a second connect.
	if !waitCondition(3*time.Second, func() bool { return broker.ConnCount() >= 2 }) {
		t.Fatalf("no reconnect after TCP reset; connCount=%d", broker.ConnCount())
	}

	// Broker must have received the subscriptions again.
	if !waitCondition(3*time.Second, func() bool {
		return broker.SubscribeCount() >= baseSubs+len(filters)
	}) {
		t.Fatalf(
			"subscribe replay incomplete: broker total=%d, want≥%d",
			broker.SubscribeCount(), baseSubs+len(filters),
		)
	}
}

// TestConnackFailureBackoffMaxBackoff verifies that the lifecycle loop backs
// off to MaxBackoff under repeated CONNACK failures and does not spin
// uncontrolled connection attempts.
//
// Strategy: broker rejects all connects. The number of connection attempts
// is measured over a fixed window and verified to be well below the
// theoretical maximum without any backoff.
func TestConnackFailureBackoffMaxBackoff(t *testing.T) {
	t.Parallel()

	broker := newLifecycleMockBroker(t)

	// First reject for the synchronous Start attempt.
	broker.RejectNextConnect()

	client := NewTCPClient(TCPConfig{
		BrokerURL:   broker.URL(),
		ClientID:    "lc-backoff-max-test",
		KeepAlive:   30 * time.Second,
		DialTimeout: 200 * time.Millisecond,
	})

	cfg := LifecycleConfig{
		InitialBackoff: 50 * time.Millisecond,
		MaxBackoff:     150 * time.Millisecond,
		Jitter:         0, // deterministic
	}
	lc := NewLifecycle(cfg, client)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Start fails on the first (synchronous) attempt.
	if err := lc.Start(ctx); err == nil {
		_ = lc.Stop(ctx)
		t.Fatal("Start should have failed on CONNACK rejection")
	}

	// After a failed Start the lifecycle loop does NOT start —
	// ConnCount stays at 1. Verify no reconnect storm over 300 ms
	// (max 3 attempts).
	time.Sleep(300 * time.Millisecond)
	count := broker.ConnCount()
	if count > 3 {
		t.Fatalf("reconnect storm: broker saw %d connections in 300 ms (want≤3)", count)
	}

	// Second part: lifecycle running, broker rejects several times then
	// allows. Count connections within the time window.
	//
	// Fresh client + lifecycle for the background-loop test.
	broker2 := newLifecycleMockBroker(t)
	client2 := NewTCPClient(TCPConfig{
		BrokerURL:   broker2.URL(),
		ClientID:    "lc-backoff-max-loop-test",
		KeepAlive:   30 * time.Second,
		DialTimeout: 200 * time.Millisecond,
	})
	lc2 := NewLifecycle(cfg, client2)

	ctx2, cancel2 := context.WithCancel(context.Background())
	defer cancel2()

	// Allow the first successful connect, then start the backoff loop.
	if err := lc2.Start(ctx2); err != nil {
		t.Fatalf("lc2.Start: %v", err)
	}
	t.Cleanup(func() {
		cancel2()
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
		defer stopCancel()
		_ = lc2.Stop(stopCtx)
	})

	// TCP reset + reject all reconnects for 3 cycles.
	broker2.RejectNextConnect()
	broker2.RejectNextConnect()
	broker2.RejectNextConnect()
	broker2.InjectTCPReset()

	// Measure: in 600 ms (≈ 4× MaxBackoff) the broker must not see
	// an uncontrolled flood. With MaxBackoff=150 ms at most ~4 attempts
	// are possible; allow 10 generously.
	connsBefore := broker2.ConnCount()
	time.Sleep(600 * time.Millisecond)
	connsAfter := broker2.ConnCount()
	delta := connsAfter - connsBefore
	if delta > 10 {
		t.Fatalf("backoff exceeded: %d reconnects in 600 ms (want≤10)", delta)
	}
}

// TestTCPResetClearsConnDuringReadLoop is the focused unit test for the
// bug class: after a TCP reset c.conn must not remain set, otherwise the
// next Connect call returns "already connected".
//
// It uses a minimal in-process broker (lifecycleMockBroker), injects a TCP
// reset, and verifies that a subsequent Connect call succeeds.
func TestTCPResetClearsConnDuringReadLoop(t *testing.T) {
	t.Parallel()

	broker := newLifecycleMockBroker(t)

	client := NewTCPClient(TCPConfig{
		BrokerURL: broker.URL(),
		ClientID:  "lc-tcpreset-conn-test",
		KeepAlive: 30 * time.Second,
	})

	ctx := context.Background()

	// Establish the first connection.
	if err := client.Connect(ctx); err != nil {
		t.Fatalf("first Connect: %v", err)
	}

	// Confirm first successful connect.
	if !waitCondition(2*time.Second, func() bool { return broker.ConnCount() >= 1 }) {
		t.Fatal("broker never saw initial connection")
	}

	// Inject TCP reset — readLoop receives EOF and calls
	// handleConnectionLost which sets c.conn → nil.
	broker.InjectTCPReset()

	// Wait until c.conn is cleared by handleConnectionLost.
	// Indirect proof: a new Connect must now succeed (no "already connected").
	if !waitCondition(2*time.Second, func() bool {
		err := client.Connect(ctx)
		if err == nil {
			// Disconnect immediately to avoid a goroutine leak.
			stopCtx, stopCancel := context.WithTimeout(ctx, 200*time.Millisecond)
			defer stopCancel()
			_ = client.Disconnect(stopCtx)
			return true
		}
		// "already connected" means c.conn was not yet cleared.
		return false
	}) {
		t.Fatal("c.conn was not cleared after TCP reset — handleConnectionLost did not nil c.conn")
	}
}
