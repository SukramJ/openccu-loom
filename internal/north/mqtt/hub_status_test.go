// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package mqtt

import (
	"context"
	"sync"
	"testing"
	"time"
)

// fakeDwell is a [hubStatusGate.after] replacement that hands the pending
// callback to the test instead of to the clock.
//
// The dwell is fifteen real seconds. A test that waits it out is a test
// nobody runs; a test that shortens the production constant exercises a
// different debounce than the daemon ships. So the CLOCK is replaced and the
// constant is left alone: `armed` is what the gate would have waited for,
// and `fire` is the window elapsing.
type fakeDwell struct {
	mu      sync.Mutex
	pending []*fakeTimer
	waited  []time.Duration
}

type fakeTimer struct {
	fn      func()
	stopped bool
}

func (f *fakeTimer) Stop() bool {
	f.stopped = true
	return true
}

func (f *fakeDwell) after(d time.Duration, fn func()) hubStatusTimer {
	f.mu.Lock()
	defer f.mu.Unlock()
	t := &fakeTimer{fn: fn}
	f.pending = append(f.pending, t)
	f.waited = append(f.waited, d)
	return t
}

// fireAll runs every armed callback that was not stopped, in arm order.
func (f *fakeDwell) fireAll() {
	f.mu.Lock()
	pending := f.pending
	f.pending = nil
	f.mu.Unlock()
	for _, t := range pending {
		if !t.stopped {
			t.fn()
		}
	}
}

// arms is the TOTAL number of times the dwell was armed, live or stopped.
func (f *fakeDwell) arms() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.waited)
}

func (f *fakeDwell) armed() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, t := range f.pending {
		if !t.stopped {
			n++
		}
	}
	return n
}

// writeLog records what reached the broker, in order.
type writeLog struct {
	mu     sync.Mutex
	levels []bool
}

func (w *writeLog) write(_ context.Context, _ string, online bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.levels = append(w.levels, online)
}

func (w *writeLog) seen() []bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]bool(nil), w.levels...)
}

func newFakeGate() (*hubStatusGate, *fakeDwell) {
	g := newHubStatusGate(hubStatusDwell)
	d := &fakeDwell{}
	g.after = d.after
	return g, d
}

// TestHubStatusGateSeedsTheFirstLevelImmediately pins the one write that is
// never debounced.
//
// Home Assistant holds an entity unavailable until EVERY topic in its
// `availability` list has reported a payload it recognises. Every CCU-scoped
// hub entity now lists this gate, so debouncing its first byte would grey
// out the whole hub plane for the dwell on every daemon start — against no
// previous level to flap with.
func TestHubStatusGateSeedsTheFirstLevelImmediately(t *testing.T) {
	t.Parallel()
	g, dwell := newFakeGate()
	log := &writeLog{}

	if !g.observe(context.Background(), "ccu-01", true, log.write) {
		t.Fatal("the first observation did not write synchronously")
	}
	if got := log.seen(); len(got) != 1 || !got[0] {
		t.Fatalf("seeded %v, want exactly one online", got)
	}
	if dwell.armed() != 0 {
		t.Fatal("the seed armed the dwell — the first level must not wait")
	}
}

// TestHubStatusGateAbsorbsAFlapEntirely is the debounce's whole point: a
// level that does not survive the window is never written at all.
//
// The failure it prevents is not broker load. It is an operator watching
// Home Assistant while a radio module restarts and seeing every sysvar,
// program and system score of a working CCU blink out and come back — and
// every automation with an `unavailable` trigger firing on the blink.
func TestHubStatusGateAbsorbsAFlapEntirely(t *testing.T) {
	t.Parallel()
	g, dwell := newFakeGate()
	log := &writeLog{}
	ctx := context.Background()

	g.observe(ctx, "ccu-01", true, log.write) // seed
	g.observe(ctx, "ccu-01", false, log.write)
	if dwell.armed() != 1 {
		t.Fatalf("armed %d dwells for the downward edge, want 1", dwell.armed())
	}
	// Back before the window elapses.
	g.observe(ctx, "ccu-01", true, log.write)
	dwell.fireAll()

	if got := log.seen(); len(got) != 1 || !got[0] {
		t.Fatalf("the flap reached the broker as %v, want only the seeded [true] — a level "+
			"that does not survive the dwell must not be written", got)
	}
}

// TestHubStatusGateWritesALevelThatHoldsTheWindow is the other side of the
// same coin: absorbing a flap must not absorb a real outage.
func TestHubStatusGateWritesALevelThatHoldsTheWindow(t *testing.T) {
	t.Parallel()
	g, dwell := newFakeGate()
	log := &writeLog{}
	ctx := context.Background()

	g.observe(ctx, "ccu-01", true, log.write)
	g.observe(ctx, "ccu-01", false, log.write)
	if waited := dwell.waited[0]; waited != hubStatusDwell {
		t.Fatalf("waited %v, want the shipped dwell %v", waited, hubStatusDwell)
	}
	dwell.fireAll()

	if got := log.seen(); len(got) != 2 || got[1] {
		t.Fatalf("wrote %v, want [true false]", got)
	}
	// And the written level is now the one the gate debounces against.
	g.observe(ctx, "ccu-01", false, log.write)
	if n := len(log.seen()); n != 2 {
		t.Fatalf("re-observing the written level wrote again (%d writes)", n)
	}
}

// TestHubStatusGateDoesNotRestartTheWindowOnRepeatedEdges pins that the
// window starts at the FIRST edge, not the last.
//
// A restarting window is a livelock: a CCU whose interfaces keep reporting
// the same new level faster than the dwell would never have the level
// written, so a genuinely dead CCU could stay `online` indefinitely.
func TestHubStatusGateDoesNotRestartTheWindowOnRepeatedEdges(t *testing.T) {
	t.Parallel()
	g, dwell := newFakeGate()
	log := &writeLog{}
	ctx := context.Background()

	g.observe(ctx, "ccu-01", true, log.write)
	for range 5 {
		g.observe(ctx, "ccu-01", false, log.write)
	}
	// Total arms, not live ones: a restart stops the old timer and arms a
	// new one, which leaves the live count at 1 and says nothing.
	if n := dwell.arms(); n != 1 {
		t.Fatalf("armed the dwell %d times, want 1 — the window starts at the first edge, "+
			"not the last, or a CCU whose interfaces keep re-reporting the same new level "+
			"could stay `online` while it is dead", n)
	}
	dwell.fireAll()
	if got := log.seen(); len(got) != 2 || got[1] {
		t.Fatalf("wrote %v, want [true false]", got)
	}
}

// TestHubStatusGateIsPerCentral: two CCUs must not debounce against each
// other's level.
func TestHubStatusGateIsPerCentral(t *testing.T) {
	t.Parallel()
	g, _ := newFakeGate()
	log := &writeLog{}
	ctx := context.Background()

	if !g.observe(ctx, "ccu-01", true, log.write) {
		t.Fatal("ccu-01 did not seed")
	}
	if !g.observe(ctx, "ccu-02", false, log.write) {
		t.Fatal("ccu-02 did not seed — it debounced against ccu-01's level")
	}
	if got := log.seen(); len(got) != 2 || !got[0] || got[1] {
		t.Fatalf("wrote %v, want [true false]", got)
	}
}

// TestHubStatusGateForgetRestoresTheSeed pins the retraction path: after a
// removal the broker holds no byte, so the next level must be seeded rather
// than debounced against a level nothing remembers.
func TestHubStatusGateForgetRestoresTheSeed(t *testing.T) {
	t.Parallel()
	g, _ := newFakeGate()
	log := &writeLog{}
	ctx := context.Background()

	g.observe(ctx, "ccu-01", true, log.write)
	g.forget("ccu-01")
	if !g.observe(ctx, "ccu-01", false, log.write) {
		t.Fatal("after forget the gate still debounced — a retracted topic has no level to " +
			"debounce against")
	}
}

// TestHubStatusGateCancellationBeatsAFiringTimer covers the race
// [hubStatusGate.fire] re-checks for. [time.Timer.Stop] returns false for a
// timer whose callback has already started, so a cancelling observe in that
// window cannot stop the goroutine — only make its write a no-op.
func TestHubStatusGateCancellationBeatsAFiringTimer(t *testing.T) {
	t.Parallel()
	g, dwell := newFakeGate()
	log := &writeLog{}
	ctx := context.Background()

	g.observe(ctx, "ccu-01", true, log.write)
	g.observe(ctx, "ccu-01", false, log.write)
	// The cancelling observe lands first; the timer's goroutine runs anyway.
	g.observe(ctx, "ccu-01", true, log.write)
	g.mu.Lock()
	g.entries["ccu-01"].timer = nil // Stop() already lost the race
	g.mu.Unlock()
	for _, ft := range dwell.pending {
		ft.fn()
	}

	if got := log.seen(); len(got) != 1 {
		t.Fatalf("wrote %v, want only the seed — a cancelled write must not land late", got)
	}
}

// TestPublishHubReachabilityWritesTheDocumentedTopicAtQoS1 reads the topic
// and the delivery level back off the transport.
//
// QoS 1 for the same reason PR #803 pinned it for every other availability
// flip and retraction: a level is written only on a transition, so a lost
// one is not repaired by a later publish. A lost `offline` here IS the
// defect this topic exists to fix, left unfixed.
func TestPublishHubReachabilityWritesTheDocumentedTopicAtQoS1(t *testing.T) {
	t.Parallel()
	rec := &recordingPublisher{}
	b := newDeepBridge(t, rec, func(c *BridgeConfig) { c.Base = "gh" })

	if err := b.PublishHubReachability(context.Background(), "ccu-01", true); err != nil {
		t.Fatalf("publish: %v", err)
	}
	got, ok := rec.findTopic("gh/ccu-01/hub/status")
	if !ok {
		t.Fatalf("nothing published to gh/ccu-01/hub/status; got %v", rec.records())
	}
	if got.payload != "online" {
		t.Errorf("payload %q, want online", got.payload)
	}
	if !got.retain {
		t.Error("not retained — Home Assistant reads this gate on every restart")
	}
	if got.qos != QoS1 {
		t.Errorf("QoS %v, want QoS1", got.qos)
	}
}

// TestAnnounceOfflineWritesTheCCUGatesBeforeTheBridgeMarker is the LWT
// ordering pin.
//
// MQTT allows one will per connection and this daemon's is spent on
// `bridge/status`, so the per-CCU gate cannot have one. On a graceful stop
// the broker discards the will, and this is the only path that ever writes
// the counterpart. The gates go FIRST so no instant exists in which the
// daemon has declared itself gone while its CCU gates still claim
// reachability.
func TestAnnounceOfflineWritesTheCCUGatesBeforeTheBridgeMarker(t *testing.T) {
	t.Parallel()
	rec := &recordingPublisher{}
	b := newDeepBridge(t, rec, func(c *BridgeConfig) { c.Base = "gh" })
	ctx := context.Background()

	if err := b.PublishHubReachability(ctx, "ccu-01", true); err != nil {
		t.Fatalf("seed: %v", err)
	}
	rec.clear()
	if err := b.AnnounceOffline(ctx); err != nil {
		t.Fatalf("announce offline: %v", err)
	}

	gate, bridge := -1, -1
	for i, r := range rec.records() {
		switch r.topic {
		case "gh/ccu-01/hub/status":
			if r.payload != "offline" {
				t.Errorf("gate payload %q, want offline", r.payload)
			}
			gate = i
		case "gh/bridge/status":
			bridge = i
		}
	}
	if gate < 0 {
		t.Fatalf("the per-CCU gate was never written offline; got %v", rec.records())
	}
	if bridge < 0 {
		t.Fatalf("the bridge marker was never written; got %v", rec.records())
	}
	if gate > bridge {
		t.Errorf("bridge marker at %d, CCU gate at %d — the gates must go first, so the "+
			"daemon never declares itself gone while a CCU gate still claims reachability",
			bridge, gate)
	}
}

// TestRetractHubStatusClearsTheTopicAndTheLevel pins the removal path: a
// removed CCU gets its claim deleted, not set to `offline`, and the gate
// forgets the level so a later re-wire seeds again.
func TestRetractHubStatusClearsTheTopicAndTheLevel(t *testing.T) {
	t.Parallel()
	rec := &recordingPublisher{}
	b := newDeepBridge(t, rec, func(c *BridgeConfig) { c.Base = "gh" })
	ctx := context.Background()

	if err := b.PublishHubReachability(ctx, "ccu-01", true); err != nil {
		t.Fatalf("seed: %v", err)
	}
	rec.clear()
	if err := b.RetractHubStatus(ctx, "ccu-01"); err != nil {
		t.Fatalf("retract: %v", err)
	}
	got, ok := rec.findTopic("gh/ccu-01/hub/status")
	if !ok {
		t.Fatalf("nothing published to the gate topic; got %v", rec.records())
	}
	if got.payload != "" {
		t.Errorf("payload %q, want empty — a removed CCU's claim is deleted, not set offline",
			got.payload)
	}
	if len(b.hubStatus.centrals()) != 0 {
		t.Error("the gate still remembers a level for a retracted topic")
	}
}
