// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package main

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	mochi "github.com/mochi-mqtt/server/v2"
	"github.com/mochi-mqtt/server/v2/hooks/auth"
	"github.com/mochi-mqtt/server/v2/listeners"
	"github.com/mochi-mqtt/server/v2/packets"

	"github.com/SukramJ/openccu-loom/internal/config"
	"github.com/SukramJ/openccu-loom/internal/health"
	"github.com/SukramJ/openccu-loom/internal/model/hub"
	"github.com/SukramJ/openccu-loom/internal/north/mqtt"
)

func supervisorLogger() *slog.Logger {
	return slog.New(slog.DiscardHandler)
}

func mqttCfg(enabled bool) *config.Config {
	c := config.Default()
	c.North.MQTT.Enabled = enabled
	c.North.MQTT.BrokerURL = "" // forces NoopClient path
	c.North.MQTT.TopicBase = "test"
	return c
}

func newSup(t *testing.T) *mqttSupervisor {
	t.Helper()
	return newMQTTSupervisor(supervisorLogger(), health.NewTracker(), nil)
}

func TestSupervisor_StartDisabled_NoStack(t *testing.T) {
	t.Parallel()
	s := newSup(t)
	ctx := context.Background()

	if err := s.Start(ctx, mqttCfg(false)); err != nil {
		t.Fatalf("Start: %v", err)
	}
	// No stack, but the stable Wiring exists: consumers bind it at
	// composition time and a runtime enable has to reach them.
	if s.Wiring() == nil {
		t.Fatal("Wiring() must be non-nil even when MQTT is disabled")
	}
	if s.Wiring().Bridge() != nil {
		t.Fatal("Wiring().Bridge() must be nil when MQTT is disabled, so publishes are dropped")
	}
	if s.CurrentClient() != nil {
		t.Fatal("CurrentClient() must be nil when MQTT is disabled")
	}
}

func TestSupervisor_StartEnabled_NoopClient_HasWiring(t *testing.T) {
	t.Parallel()
	s := newSup(t)
	ctx := context.Background()

	if err := s.Start(ctx, mqttCfg(true)); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if s.Wiring() == nil {
		t.Fatal("Wiring() must be non-nil after Start with MQTT enabled")
	}
	if s.CurrentClient() == nil {
		t.Fatal("CurrentClient() must be non-nil after Start")
	}
	if _, ok := s.CurrentClient().(*mqtt.NoopClient); !ok {
		t.Fatalf("expected *mqtt.NoopClient, got %T", s.CurrentClient())
	}
	if s.CurrentBridge() == nil {
		t.Fatal("CurrentBridge() must be non-nil after Start")
	}
}

func TestSupervisor_AttachSubscribers_NoBuilder_NoOp(t *testing.T) {
	t.Parallel()
	s := newSup(t)
	ctx := context.Background()

	if err := s.Start(ctx, mqttCfg(true)); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := s.AttachSubscribers(ctx); err != nil {
		t.Fatalf("AttachSubscribers without builder: %v", err)
	}
}

func TestSupervisor_AttachSubscribers_BuilderInvoked(t *testing.T) {
	t.Parallel()
	s := newSup(t)
	ctx := context.Background()

	var buildCalls, stopCalls atomic.Int32
	s.SetSubscriberBuilder(func(_ context.Context, _ mqtt.Client, _ *mqtt.Bridge) (func(), error) {
		buildCalls.Add(1)
		return func() { stopCalls.Add(1) }, nil
	})

	if err := s.Start(ctx, mqttCfg(true)); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Start already invoked the builder if one was set; AttachSubscribers
	// must be idempotent (see idempotency test). We verify that the builder
	// was called exactly once in total after Start.
	_ = s.AttachSubscribers(ctx) // no-op: stopSubs already set by Start

	if buildCalls.Load() != 1 {
		t.Fatalf("builder called %d times, want 1", buildCalls.Load())
	}

	s.Shutdown(ctx)
	if stopCalls.Load() != 1 {
		t.Fatalf("stop func called %d times after Shutdown, want 1", stopCalls.Load())
	}
}

func TestSupervisor_AttachSubscribers_BuilderError_NoOp(t *testing.T) {
	t.Parallel()
	s := newSup(t)
	ctx := context.Background()

	buildErr := errors.New("broker not ready")
	var stopCalls atomic.Int32

	// Install the error builder before Start so it is also invoked by Start.
	// Start logs the error but does not abort (comment in Start confirms this).
	s.SetSubscriberBuilder(func(_ context.Context, _ mqtt.Client, _ *mqtt.Bridge) (func(), error) {
		return nil, buildErr
	})

	if err := s.Start(ctx, mqttCfg(true)); err != nil {
		t.Fatalf("Start must not fail on subscriber builder error: %v", err)
	}

	// Replace builder for the explicit AttachSubscribers call.
	s.SetSubscriberBuilder(func(_ context.Context, _ mqtt.Client, _ *mqtt.Bridge) (func(), error) {
		return nil, buildErr
	})

	// stopSubs is nil because the builder errored during Start; AttachSubscribers
	// will invoke the builder and get the error again.
	if err := s.AttachSubscribers(ctx); err == nil {
		t.Fatal("AttachSubscribers must return error when builder fails")
	}

	s.Shutdown(ctx)
	if stopCalls.Load() != 0 {
		t.Fatalf("stop func must not be called when builder returned error, got %d calls", stopCalls.Load())
	}
}

func TestSupervisor_AttachSubscribers_Idempotent(t *testing.T) {
	t.Parallel()
	s := newSup(t)
	ctx := context.Background()

	var buildCalls atomic.Int32
	s.SetSubscriberBuilder(func(_ context.Context, _ mqtt.Client, _ *mqtt.Bridge) (func(), error) {
		buildCalls.Add(1)
		return func() {}, nil
	})

	if err := s.Start(ctx, mqttCfg(true)); err != nil {
		t.Fatalf("Start: %v", err)
	}
	// Start already wired subscribers; AttachSubscribers must be a no-op.
	_ = s.AttachSubscribers(ctx)
	_ = s.AttachSubscribers(ctx)

	if buildCalls.Load() != 1 {
		t.Fatalf("builder called %d times, want exactly 1", buildCalls.Load())
	}
}

// TestSupervisor_Swap_DisabledToEnabled pins the whole runtime-enable path,
// not just the swap: a consumer binds the Wiring once, at composition time,
// while MQTT is still disabled, and must publish through it after the operator
// enables MQTT and reloads. Handing out no Wiring at boot — or a second one at
// swap time — leaves every publisher built at boot permanently MQTT-dead with
// a connected broker to look at.
func TestSupervisor_Swap_DisabledToEnabled(t *testing.T) {
	t.Parallel()
	s := newSup(t)
	ctx := context.Background()

	if err := s.Start(ctx, mqttCfg(false)); err != nil {
		t.Fatalf("Start: %v", err)
	}
	// What EventBridge / HubMQTTPublisher / the system-status publishers
	// capture when the daemon composes them.
	bound := s.Wiring()
	if bound == nil {
		t.Fatal("Wiring() must be non-nil after a disabled Start; consumers bind it once and never re-read it")
	}
	if bound.Bridge() != nil {
		t.Fatal("the disabled Wiring must carry no bridge, so publishes are dropped rather than panicking")
	}

	enabled := mqttCfg(true)
	enabled.North.MQTT.RawEnabled = true // the plane the assertion below observes
	if err := s.Swap(ctx, enabled); err != nil {
		t.Fatalf("Swap to enabled: %v", err)
	}
	if s.Wiring() != bound {
		t.Fatal("Swap handed out a different Wiring; every consumer still holds the boot-time pointer")
	}
	client, ok := s.CurrentClient().(*mqtt.NoopClient)
	if !ok {
		t.Fatalf("expected *mqtt.NoopClient after swap, got %T", s.CurrentClient())
	}

	// A hub aggregate is the smallest real consumer payload: it addresses its
	// own retained topic, exactly as HubMQTTPublisher publishes it.
	bound.PublishSysvar(ctx, "ccu", hub.NewServiceMessages(nil), 3)
	if len(client.Published()) == 0 {
		t.Fatal("a consumer publishing through the boot-time Wiring reached nothing after the runtime enable")
	}
}

func TestSupervisor_Swap_EnabledToDisabled(t *testing.T) {
	t.Parallel()
	s := newSup(t)
	ctx := context.Background()

	var stopCalls atomic.Int32
	s.SetSubscriberBuilder(func(_ context.Context, _ mqtt.Client, _ *mqtt.Bridge) (func(), error) {
		return func() { stopCalls.Add(1) }, nil
	})

	if err := s.Start(ctx, mqttCfg(true)); err != nil {
		t.Fatalf("Start: %v", err)
	}
	_ = s.AttachSubscribers(ctx)

	if err := s.Swap(ctx, mqttCfg(false)); err != nil {
		t.Fatalf("Swap to disabled: %v", err)
	}

	if s.CurrentClient() != nil {
		t.Fatal("CurrentClient() must be nil after swap to disabled")
	}
	// The stable Wiring pointer survives across swaps.
	if s.Wiring() == nil {
		t.Fatal("Wiring() must survive swap-to-disabled")
	}
	// The bridge behind Wiring is nil after disabling.
	if s.Wiring().Bridge() != nil {
		t.Fatalf("Wiring().Bridge() must be nil after swap to disabled, got %p", s.Wiring().Bridge())
	}
	// Stop was called during teardown.
	if stopCalls.Load() != 1 {
		t.Fatalf("stop func called %d times after swap to disabled, want 1", stopCalls.Load())
	}
}

func TestSupervisor_Swap_NilConfig_ReturnsError(t *testing.T) {
	t.Parallel()
	s := newSup(t)
	ctx := context.Background()
	if err := s.Swap(ctx, nil); err == nil {
		t.Fatal("Swap(nil) must return an error")
	}
}

func TestSupervisor_Shutdown_Idempotent(t *testing.T) {
	t.Parallel()
	s := newSup(t)
	ctx := context.Background()

	if err := s.Start(ctx, mqttCfg(true)); err != nil {
		t.Fatalf("Start: %v", err)
	}
	s.Shutdown(ctx)
	s.Shutdown(ctx) // idempotent: second call is safe
}

func TestSupervisor_Shutdown_NoStart_NoOp(t *testing.T) {
	t.Parallel()
	s := newSup(t)
	ctx := context.Background()
	s.Shutdown(ctx) // safe to call without a prior Start
}

func TestSupervisor_RedactBrokerURL(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   string
		want string
	}{
		{"tcp://user:pass@host:1883", "tcp://***@host:1883"},
		{"tcp://host:1883", "tcp://host:1883"},
		{"", ""},
		{"host:1883", "host:1883"},
	}
	for _, c := range cases {
		got := redactBrokerURL(c.in)
		if got != c.want {
			t.Errorf("redactBrokerURL(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestSupervisorSurvivesABrokerThatIsDownAtBoot pins the two halves of the
// boot-time recovery: the Wiring every north-bound consumer binds to must
// exist even though the first connect failed, and the supervisor must keep
// retrying so the link comes up without a daemon restart.
//
// Both mattered because Lifecycle.Start performs the first connect
// synchronously and only starts its reconnect loop afterwards — a refused
// CONNECT at boot left no bridge, no Wiring and nothing that would ever try
// again, so the whole MQTT plane stayed dark for the process lifetime.
func TestSupervisorSurvivesABrokerThatIsDownAtBoot(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	// A listener that accepts and immediately closes is a broker that is
	// reachable but not serving — every CONNECT fails, and each accept counts
	// one connect attempt.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	var dials atomic.Int64
	go func() {
		for {
			conn, aErr := ln.Accept()
			if aErr != nil {
				return
			}
			dials.Add(1)
			_ = conn.Close()
		}
	}()

	cfg := mqttCfg(true)
	cfg.North.MQTT.BrokerURL = "tcp://" + ln.Addr().String()

	s := newSup(t)
	s.retryDelay = 10 * time.Millisecond
	s.retryMaxDelay = 10 * time.Millisecond
	t.Cleanup(func() { s.Shutdown(context.Background()) })

	if err := s.Start(ctx, cfg); err == nil {
		t.Fatal("Start against a broker that refuses every CONNECT returned nil, want an error")
	}
	if s.Wiring() == nil {
		t.Fatal("Wiring() is nil after a failed boot connect; every consumer binds this pointer " +
			"once at composition time, so a nil here disables MQTT for the process lifetime")
	}

	deadline := time.Now().Add(5 * time.Second)
	for dials.Load() < 3 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if got := dials.Load(); got < 3 {
		t.Fatalf("connect attempts = %d, want >= 3; the supervisor must keep retrying "+
			"because the lifecycle's own reconnect loop never started", got)
	}
}

// TestSupervisorSwapAfterAFailedBootConnectReachesBoundConsumers pins that the
// recovery actually reaches the consumers: they hold the Wiring pointer from
// boot, so a stack built later has to be installed INTO that pointer rather
// than into a fresh one.
func TestSupervisorSwapAfterAFailedBootConnectReachesBoundConsumers(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	// A closed port: the dial itself fails, so no retry can succeed until the
	// config is swapped.
	probe, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := probe.Addr().String()
	_ = probe.Close()

	cfg := mqttCfg(true)
	cfg.North.MQTT.BrokerURL = "tcp://" + addr

	s := newSup(t)
	s.retryDelay = time.Hour // the retry loop must not race the explicit Swap
	s.retryMaxDelay = time.Hour
	t.Cleanup(func() { s.Shutdown(context.Background()) })

	if err := s.Start(ctx, cfg); err == nil {
		t.Fatal("Start against a closed port returned nil, want an error")
	}
	// What a consumer captured at composition time.
	bound := s.Wiring()
	if bound == nil {
		t.Fatal("Wiring() is nil after a failed boot connect")
	}
	if bound.Bridge() != nil {
		t.Fatal("the boot-failed Wiring must carry no bridge, so publishes are dropped rather than panicking")
	}

	if err := s.Swap(ctx, mqttCfg(true)); err != nil {
		t.Fatalf("Swap after a failed boot connect: %v", err)
	}
	if bound.Bridge() == nil {
		t.Fatal("the Wiring captured at boot still has no bridge after a successful Swap; " +
			"the recovered stack never reaches the publishers built at boot")
	}
}

// TestSupervisorConcurrentSwapsRetireEveryStackButTheLiveOne pins that a stack
// transition is atomic end to end.
//
// Swap snapshots the live stack, releases the lock to build and connect the
// replacement, then re-takes it to commit. Two callers exist in production and
// nothing else serialises them — the config watcher's tick and
// POST /api/v1/admin/mqtt/reload — so without a lifecycle lock both capture the
// same predecessor, both commit, and the loser's fully-built stack is
// referenced by nothing that could tear it down: a connected client and its
// subscribers leak until the process exits.
func TestSupervisorConcurrentSwapsRetireEveryStackButTheLiveOne(t *testing.T) {
	t.Parallel()
	s := newSup(t)
	ctx := context.Background()

	var built, stopped atomic.Int64
	s.SetSubscriberBuilder(func(_ context.Context, _ mqtt.Client, _ *mqtt.Bridge) (func(), error) {
		built.Add(1)
		return func() { stopped.Add(1) }, nil
	})

	if err := s.Start(ctx, mqttCfg(true)); err != nil {
		t.Fatalf("Start: %v", err)
	}

	const (
		swappers = 8
		rounds   = 20
	)
	var wg sync.WaitGroup
	for range swappers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range rounds {
				if err := s.Swap(ctx, mqttCfg(true)); err != nil {
					t.Errorf("Swap: %v", err)
					return
				}
			}
		}()
	}
	wg.Wait()

	// Exactly one generation — the live one — may still hold its subscribers.
	if live := built.Load() - stopped.Load(); live != 1 {
		t.Fatalf("%d subscriber sets are still live after %d swaps, want exactly 1 (built=%d stopped=%d)",
			live, swappers*rounds, built.Load(), stopped.Load())
	}

	s.Shutdown(ctx)
	if live := built.Load() - stopped.Load(); live != 0 {
		t.Fatalf("%d subscriber sets survived shutdown, want 0", live)
	}
}

// TestSupervisorSwapDuringShutdownStopsTheOrphanedSubscribers pins the other
// half of the same invariant: a stack whose commit loses to Shutdown must be
// torn down rather than left running behind a cleared s.current.
func TestSupervisorSwapDuringShutdownStopsTheOrphanedSubscribers(t *testing.T) {
	t.Parallel()
	s := newSup(t)
	ctx := context.Background()

	var built, stopped atomic.Int64
	s.SetSubscriberBuilder(func(_ context.Context, _ mqtt.Client, _ *mqtt.Bridge) (func(), error) {
		built.Add(1)
		return func() { stopped.Add(1) }, nil
	})
	if err := s.Start(ctx, mqttCfg(true)); err != nil {
		t.Fatalf("Start: %v", err)
	}

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		_ = s.Swap(ctx, mqttCfg(true))
	}()
	go func() {
		defer wg.Done()
		s.Shutdown(ctx)
	}()
	wg.Wait()
	// Whichever order the two ran in, a stack built after the shutdown must
	// still be torn down by the next Shutdown call.
	s.Shutdown(ctx)

	if live := built.Load() - stopped.Load(); live != 0 {
		t.Fatalf("%d subscriber sets outlived shutdown (built=%d stopped=%d)",
			live, built.Load(), stopped.Load())
	}
}

// instantConnector is a broker adapter whose connect always succeeds
// immediately, so a lifecycle can be driven without a socket.
type instantConnector struct{}

func (instantConnector) Connect(context.Context) error { return nil }

func (instantConnector) Disconnect(context.Context) error { return nil }

// dropConnector is a broker adapter whose Connect always succeeds. Its
// ConnectionLost channel lets a test force a drop, and the lifecycle it drives
// uses a long idle backoff so the only reconnect within the test window is the
// forced one — making the connect count a clean signal of "the reconnect loop
// is still alive".
type dropConnector struct {
	connects atomic.Int64
	lost     chan struct{}
}

func (c *dropConnector) Connect(context.Context) error    { c.connects.Add(1); return nil }
func (c *dropConnector) Disconnect(context.Context) error { return nil }
func (c *dropConnector) ConnectionLost() <-chan struct{}  { return c.lost }

func waitForCond(t *testing.T, d time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("condition not met within deadline")
}

// TestSupervisorRecoveredLifecycleSurvivesTheRetryGoroutineReturn pins the fix
// for the boot-recovery context bug: retryInitialConnect used to rebuild the
// stack on retryCtx, whose deferred cancel() fires the instant the one-shot
// retry goroutine returns — cancelling the context the recovered lifecycle's
// whole reconnect loop runs on. The first broker drop after a late boot connect
// was then permanent and silent.
//
// The recovered lifecycle must run on the daemon-lifetime context instead, so
// it is still live after the retry goroutine returns AND still reconnects on a
// later drop. The swapFn seam lets the recovered lifecycle be observed without a
// real broker; production wires it to Swap.
func TestSupervisorRecoveredLifecycleSurvivesTheRetryGoroutineReturn(t *testing.T) {
	t.Parallel()
	daemonCtx, cancelDaemon := context.WithCancel(context.Background())
	t.Cleanup(cancelDaemon)

	conn := &dropConnector{lost: make(chan struct{}, 1)}
	var lc *mqtt.Lifecycle
	ctxCh := make(chan context.Context, 1)

	s := newSup(t)
	s.retryDelay = time.Millisecond
	s.retryMaxDelay = time.Millisecond
	s.swapFn = func(swapCtx context.Context, _ *config.Config) error {
		// A long idle backoff so the reconnect loop's timer never fires within
		// the test window: the only reconnect is the drop forced below.
		l := mqtt.NewLifecycle(mqtt.LifecycleConfig{
			InitialBackoff: 30 * time.Second,
			MaxBackoff:     30 * time.Second,
			FlapWindow:     -1, // every detected drop reconnects immediately
		}, conn)
		if err := l.Start(swapCtx); err != nil {
			return err
		}
		lc = l
		ctxCh <- swapCtx
		return nil
	}
	t.Cleanup(func() {
		if lc != nil {
			_ = lc.Stop(context.Background())
		}
	})

	s.retryInitialConnect(daemonCtx, mqttCfg(true))

	var recoveredCtx context.Context
	select {
	case recoveredCtx = <-ctxCh:
	case <-time.After(5 * time.Second):
		t.Fatal("connect-retry never recovered the stack")
	}

	// Wait until the retry goroutine has returned: its defer cancels retryCtx and
	// clears retryCancel. A lifecycle bound to retryCtx dies exactly here.
	waitForCond(t, 5*time.Second, func() bool {
		s.mu.Lock()
		defer s.mu.Unlock()
		return s.retryCancel == nil
	})

	if err := recoveredCtx.Err(); err != nil {
		t.Fatalf("the recovered lifecycle's context was cancelled when the retry goroutine returned: %v — "+
			"it was bound to the retry ctx, not the daemon ctx", err)
	}

	// It still reconnects on a later drop, which it can only do while its
	// reconnect loop's context is alive.
	if got := conn.connects.Load(); got != 1 {
		t.Fatalf("connects = %d before the drop, want 1 (only the boot-recovery connect)", got)
	}
	conn.lost <- struct{}{}
	waitForCond(t, 5*time.Second, func() bool { return conn.connects.Load() >= 2 })
}

// TestSupervisorRegistersEachConnectHookExactlyOnce pins the registration of
// the connect callbacks against the window between publishing a stack and
// handing it those callbacks.
//
// Start and Swap publish s.current first and call announceConnected after, and
// announceConnected forwards the whole callback list to the new lifecycle. A
// callback registered in between is in that list AND sees a published stack,
// so registering it on the spot as well makes it fire twice on every later
// reconnect: the initial raw-plane snapshot is republished twice and the hub
// publisher is re-Started twice per broker drop. The boot goroutine registers
// hooks while the connect-retry goroutine may be running a Swap, so the window
// is reachable in production.
func TestSupervisorRegistersEachConnectHookExactlyOnce(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	s := newSup(t)
	lifecycle := mqtt.NewLifecycle(mqtt.DefaultLifecycle(), instantConnector{})
	sw := &mqttSwap{lifecycle: lifecycle}

	// The published-but-not-yet-announced state Start and Swap pass through.
	s.mu.Lock()
	s.current = sw
	s.mu.Unlock()

	var fired atomic.Int64
	s.OnConnect(func(context.Context) { fired.Add(1) })
	s.announceConnected(ctx, sw)
	if got := fired.Load(); got != 1 {
		t.Fatalf("connect hook fired %d times for the connect the stack already performed, want 1", got)
	}

	// One reconnect: every callback the lifecycle holds runs once.
	if err := lifecycle.Start(ctx); err != nil {
		t.Fatalf("lifecycle.Start: %v", err)
	}
	t.Cleanup(func() { _ = lifecycle.Stop(context.Background()) })
	if got := fired.Load(); got != 2 {
		t.Errorf("connect hook fired %d times over one announce plus one connect, want 2; "+
			"a hook registered while the stack was published but not yet announced is registered twice", got)
	}
}

// brokerConnectCounter is a mochi hook that counts CONNECT packets per client
// id. A second CONNECT under the same id is the only evidence from outside the
// daemon that a lifecycle's reconnect loop is still alive.
type brokerConnectCounter struct {
	mochi.HookBase
	mu     sync.Mutex
	counts map[string]int
}

func (h *brokerConnectCounter) ID() string { return "connect-counter" }

func (h *brokerConnectCounter) Provides(b byte) bool { return b == mochi.OnConnect }

func (h *brokerConnectCounter) OnConnect(cl *mochi.Client, _ packets.Packet) error {
	h.mu.Lock()
	if h.counts == nil {
		h.counts = make(map[string]int)
	}
	h.counts[cl.ID]++
	h.mu.Unlock()
	return nil
}

func (h *brokerConnectCounter) count(id string) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.counts[id]
}

// startTestBroker brings up a real in-process MQTT broker on an OS-assigned
// loopback port and returns its URL plus the CONNECT counter. A real broker is
// what makes the assertion meaningful: the reconnect loop lives inside
// go-mqtt's Lifecycle, and the only way to observe it from the supervisor's
// public surface is to drop the socket and watch the daemon dial again.
func startTestBroker(t *testing.T) (url string, connects *brokerConnectCounter, srv *mochi.Server) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	srv = mochi.New(&mochi.Options{InlineClient: true, Logger: supervisorLogger()})
	connects = &brokerConnectCounter{counts: make(map[string]int)}
	if err := srv.AddHook(new(auth.AllowHook), nil); err != nil {
		t.Fatalf("broker: add auth hook: %v", err)
	}
	if err := srv.AddHook(connects, nil); err != nil {
		t.Fatalf("broker: add connect counter: %v", err)
	}
	if err := srv.AddListener(listeners.NewNet("supervisor-test", ln)); err != nil {
		t.Fatalf("broker: add listener: %v", err)
	}
	go func() { _ = srv.Serve() }()
	t.Cleanup(func() { _ = srv.Close() })
	return "tcp://" + ln.Addr().String(), connects, srv
}

// brokerMQTTCfg is [mqttCfg] pointed at a real broker under clientID.
func brokerMQTTCfg(brokerURL, clientID string) *config.Config {
	c := mqttCfg(true)
	c.North.MQTT.BrokerURL = brokerURL
	c.North.MQTT.ClientID = clientID
	return c
}

// TestSupervisorSwappedStackReconnectsAfterTheCallersContextIsCancelled pins
// the context a swapped-in MQTT stack's reconnect loop runs on.
//
// go-mqtt's Lifecycle.Start binds the WHOLE reconnect loop to the context it
// receives, and both operator-triggered Swap callers hand over a short-lived
// one — the REST reload handler passes r.Context(), which net/http cancels the
// instant the response is written, and the config watcher passes a 30 s timeout
// it cancels as soon as Swap returns. A stack built on either keeps whatever
// TCP session it happens to have and never reconnects again: the next broker
// restart drops every Home Assistant entity to `unavailable` via the LWT and
// leaves it there, with no log line, until the daemon process is restarted.
//
// The test drives the production entry points in the production order — Start
// with the daemon context, then Swap with a request-scoped one that is
// cancelled the way net/http cancels it — against a real broker, then closes
// the session server-side. A second CONNECT can only happen while the
// reconnect loop's context is alive.
func TestSupervisorSwappedStackReconnectsAfterTheCallersContextIsCancelled(t *testing.T) {
	t.Parallel()
	brokerURL, connects, srv := startTestBroker(t)

	daemonCtx, cancelDaemon := context.WithCancel(context.Background())
	t.Cleanup(cancelDaemon)

	s := newSup(t)
	t.Cleanup(func() { s.Shutdown(context.Background()) })

	// Boot: the composition root hands Start the daemon-lifetime context.
	const bootID = "supervisor-test-boot"
	if err := s.Start(daemonCtx, brokerMQTTCfg(brokerURL, bootID)); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Reload: the operator presses "Reload MQTT" in the SPA. The handler's
	// context lives exactly as long as the request.
	const reloadID = "supervisor-test-reload"
	reqCtx, cancelReq := context.WithCancel(context.Background())
	if err := s.Swap(reqCtx, brokerMQTTCfg(brokerURL, reloadID)); err != nil {
		cancelReq()
		t.Fatalf("Swap: %v", err)
	}
	// ServeHTTP returned.
	cancelReq()

	waitForCond(t, 5*time.Second, func() bool { return connects.count(reloadID) == 1 })

	// The broker restarts / the link blips: the session is closed underneath
	// the daemon.
	cl, ok := srv.Clients.Get(reloadID)
	if !ok {
		t.Fatalf("the swapped-in stack never connected as %q", reloadID)
	}
	cl.Stop(errors.New("broker dropped the session"))

	deadline := time.Now().Add(15 * time.Second)
	for connects.count(reloadID) < 2 {
		if time.Now().After(deadline) {
			t.Fatalf("the swapped-in MQTT stack never reconnected after the session dropped "+
				"(CONNECTs as %q: %d, want 2) — its reconnect loop was bound to the reload "+
				"caller's context and died with the HTTP response, so the bridge stays down "+
				"until the daemon is restarted", reloadID, connects.count(reloadID))
		}
		time.Sleep(25 * time.Millisecond)
	}
}

// TestSupervisorConnectHooksRunOnALiveContextAfterAReloadRequest pins the
// context handed to the supervisor's connect callbacks.
//
// A connect callback is not pure republish work. The hub publisher's callback
// re-Starts its fan-out worker on the context it receives, and that worker is
// what drains every hub publish afterwards — programs, system variables,
// connectivity, install mode, service and alarm messages. Handed the reload
// handler's request context, the worker exits with the HTTP response and every
// later hub publish is queued for a drain loop that is already gone: no error,
// no log line, and Home Assistant simply stops seeing the CCU's hub entities
// change.
//
// The callback the test registers stands in for that worker: it captures the
// context it is given, and the assertion is that the context is still alive
// once the request that triggered the swap has completed.
func TestSupervisorConnectHooksRunOnALiveContextAfterAReloadRequest(t *testing.T) {
	t.Parallel()
	brokerURL, _, _ := startTestBroker(t)

	daemonCtx, cancelDaemon := context.WithCancel(context.Background())
	t.Cleanup(cancelDaemon)

	s := newSup(t)
	t.Cleanup(func() { s.Shutdown(context.Background()) })

	hookCtx := make(chan context.Context, 4)
	s.OnConnect(func(ctx context.Context) { hookCtx <- ctx })

	if err := s.Start(daemonCtx, brokerMQTTCfg(brokerURL, "hook-ctx-boot")); err != nil {
		t.Fatalf("Start: %v", err)
	}
	select {
	case <-hookCtx:
	case <-time.After(5 * time.Second):
		t.Fatal("the connect hook never ran for the boot stack")
	}

	reqCtx, cancelReq := context.WithCancel(context.Background())
	if err := s.Swap(reqCtx, brokerMQTTCfg(brokerURL, "hook-ctx-reload")); err != nil {
		cancelReq()
		t.Fatalf("Swap: %v", err)
	}
	var swapHookCtx context.Context
	select {
	case swapHookCtx = <-hookCtx:
	case <-time.After(5 * time.Second):
		cancelReq()
		t.Fatal("the connect hook never ran for the swapped-in stack")
	}
	// ServeHTTP returned.
	cancelReq()

	if err := swapHookCtx.Err(); err != nil {
		t.Fatalf("the connect hook's context was cancelled when the reload request finished: %v — "+
			"anything the hook started on it (the hub publisher's fan-out worker above all) is "+
			"dead, so every later hub publish is queued for a drain loop that has exited", err)
	}
}
