// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package mqtt

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// birthBroker is a client that captures the handler installed on each
// filter and can block every Publish on demand.
//
// Blocking is the point of these tests. In the real go-mqtt transport the
// PUBACK of a QoS 1 discovery republish is processed by the very read-loop
// goroutine that delivers the birth message, so a replay performed inline
// in the handler would wait on an acknowledgement only it could deliver —
// a self-deadlock on the first birth message Home Assistant ever sends.
type birthBroker struct {
	mu       sync.Mutex
	handlers map[string]MessageHandler
	sent     []publishedMsg

	blocking atomic.Bool
	release  chan struct{}
	calls    chan struct{} // signalled once per blocked Publish, buffered
}

func newBirthBroker(buf int) *birthBroker {
	return &birthBroker{
		handlers: map[string]MessageHandler{},
		release:  make(chan struct{}),
		calls:    make(chan struct{}, buf),
	}
}

func (b *birthBroker) Publish(ctx context.Context, topic string, payload []byte, _ QoS, retain bool, _ ...PublishOption) error {
	b.mu.Lock()
	b.sent = append(b.sent, publishedMsg{topic: topic, payload: payload, retain: retain})
	b.mu.Unlock()
	if !b.blocking.Load() {
		return nil
	}
	b.calls <- struct{}{}
	select {
	case <-b.release:
	case <-ctx.Done():
		return ctx.Err()
	}
	return nil
}

func (b *birthBroker) Subscribe(_ context.Context, filter string, _ QoS, handler MessageHandler, _ ...SubscribeOption) (SubscribeResult, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.handlers[filter] = handler
	return SubscribeResult{}, nil
}

func (b *birthBroker) Unsubscribe(_ context.Context, filter string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.handlers, filter)
	return nil
}

// deliver hands one message to the handler registered for filter, the way
// the client's read loop would.
func (b *birthBroker) deliver(t *testing.T, filter, payload string) {
	t.Helper()
	b.mu.Lock()
	h, ok := b.handlers[filter]
	b.mu.Unlock()
	if !ok {
		t.Fatalf("nothing subscribed to %q", filter)
	}
	h(&Message{Topic: filter, Payload: []byte(payload), Retain: true})
}

func (b *birthBroker) publications() []publishedMsg {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]publishedMsg, len(b.sent))
	copy(out, b.sent)
	return out
}

// newBirthFixture wires a bridge and a started BirthSync over one broker,
// with a single discovery config already declared so a replay has
// something to send.
func newBirthFixture(t *testing.T, broker *birthBroker) (*Bridge, *BirthSync) {
	t.Helper()
	bridge := NewBridge(BridgeConfig{Base: "gh", HADiscoveryEnabled: true}, broker).WithSubscriber(broker)
	seedDeclared(t, bridge, "homeassistant/switch/gh/obj1/config", []byte(`{"x":1}`))

	bs := NewBirthSync(broker, bridge, nil)
	if err := bs.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	return bridge, bs
}

// TestBirthOnlineReplaysOffTheReadLoop is the reproducer for the
// self-deadlock, restated against the shared runtime's watch: the delivery
// must return well before the republish it triggered completes.
//
// The subscription is the runtime's now, not this package's, so the test
// drives it the way the broker does — through the handler the runtime
// installed — instead of calling a method on BirthSync.
func TestBirthOnlineReplaysOffTheReadLoop(t *testing.T) {
	t.Parallel()
	broker := newBirthBroker(1)
	_, bs := newBirthFixture(t, broker)
	defer func() {
		close(broker.release)
		bs.Close()
	}()

	broker.blocking.Store(true)
	done := make(chan struct{})
	start := time.Now()
	go func() {
		broker.deliver(t, HABirthTopic, "online")
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("the birth delivery did not return; the replay must run off the read loop")
	}
	if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
		t.Fatalf("the delivery took %v while Publish was blocked; want a near-instant return", elapsed)
	}

	// The work must still eventually run: the worker is parked inside
	// Publish right now.
	select {
	case <-broker.calls:
	case <-time.After(2 * time.Second):
		t.Fatal("the replay never reached the blocked Publish call")
	}
}

// TestBirthSyncCloseDrainsCleanly pins that Close blocks until the
// in-flight replay finishes — no queued or running job is abandoned — and
// that the worker behind it actually exits.
func TestBirthSyncCloseDrainsCleanly(t *testing.T) {
	t.Parallel()
	broker := newBirthBroker(1)
	_, bs := newBirthFixture(t, broker)

	broker.blocking.Store(true)
	broker.deliver(t, HABirthTopic, "online")

	select {
	case <-broker.calls:
	case <-time.After(2 * time.Second):
		t.Fatal("the replay never reached the blocked Publish call")
	}

	closeDone := make(chan struct{})
	go func() {
		bs.Close()
		close(closeDone)
	}()

	select {
	case <-closeDone:
		t.Fatal("Close returned while a replay was still in flight; it must drain, not abandon, running work")
	case <-time.After(100 * time.Millisecond):
	}

	close(broker.release)
	select {
	case <-closeDone:
	case <-time.After(2 * time.Second):
		t.Fatal("Close never returned after the blocked Publish was released — the worker leaked")
	}

	// Close is idempotent: a shutdown path reached from two places is the
	// normal case, not a bug to punish with a panic.
	bs.Close()
}

// TestBirthOfflineDoesNotReplay keeps the existing contract intact: Home
// Assistant emits "offline" before its own restart, and there is nothing to
// do on that edge — the configs it will re-read are already retained, and
// the replay belongs on the way back up.
func TestBirthOfflineDoesNotReplay(t *testing.T) {
	t.Parallel()
	broker := newBirthBroker(1)
	_, bs := newBirthFixture(t, broker)
	defer bs.Close()

	before := len(broker.publications())
	broker.deliver(t, HABirthTopic, "offline")
	time.Sleep(50 * time.Millisecond)
	if got := len(broker.publications()); got != before {
		t.Fatalf("an offline payload triggered %d publishes, want 0", got-before)
	}
}

// TestBirthBurstCollapsesOntoOnePendingReplay pins the one behaviour the
// move changed on purpose.
//
// This layer used to run a depth-4 queue in front of a single worker, so a
// burst of four birth messages produced four full replays. The runtime
// collapses a burst onto one pending job instead: every job is an
// idempotent replay of the same declared set, so running the second after
// the first changes nothing, and a queue that filled up would push the
// blocking back onto the read loop the dispatcher exists to keep free.
//
// With one replay in flight and three more deliveries behind it, exactly
// one further replay may follow.
func TestBirthBurstCollapsesOntoOnePendingReplay(t *testing.T) {
	t.Parallel()
	broker := newBirthBroker(8)
	_, bs := newBirthFixture(t, broker)

	broker.blocking.Store(true)
	for range 4 {
		broker.deliver(t, HABirthTopic, "online")
	}
	// The first replay is parked inside Publish.
	select {
	case <-broker.calls:
	case <-time.After(2 * time.Second):
		t.Fatal("the first replay never reached the blocked Publish call")
	}

	broker.blocking.Store(false)
	close(broker.release)
	bs.Close()

	// One declared config, so one publish per replay. Four deliveries
	// against a busy worker must collapse to the running one plus at most
	// one pending.
	replays := 0
	for _, p := range broker.publications() {
		if p.topic == "homeassistant/switch/gh/obj1/config" {
			replays++
		}
	}
	// The seed publish counts as one write of that topic.
	if replays > 3 {
		t.Fatalf("%d writes of the declared config; a burst must collapse onto one pending replay", replays)
	}
	if replays < 2 {
		t.Fatalf("%d writes of the declared config; the burst produced no replay at all", replays)
	}
}
