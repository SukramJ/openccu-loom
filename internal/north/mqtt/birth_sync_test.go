// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package mqtt

import (
	"context"
	"testing"
	"time"
)

// slowBirthPublisher blocks every Publish call until release is closed,
// simulating a broker that has not yet sent the PUBACK for a QoS1
// discovery republish. In the real go-mqtt transport that PUBACK is
// processed by the very read-loop goroutine that invokes BirthSync.handle,
// so a synchronous RepublishDiscovery call would self-deadlock.
type slowBirthPublisher struct {
	release chan struct{}
	calls   chan struct{} // signalled once per Publish call, buffered
}

func newSlowBirthPublisher(buf int) *slowBirthPublisher {
	return &slowBirthPublisher{release: make(chan struct{}), calls: make(chan struct{}, buf)}
}

func (p *slowBirthPublisher) Publish(ctx context.Context, _ string, _ []byte, _ QoS, _ bool, _ ...PublishOption) error {
	p.calls <- struct{}{}
	select {
	case <-p.release:
	case <-ctx.Done():
		return ctx.Err()
	}
	return nil
}

// TestBirthSyncHandleReturnsPromptlyDuringSlowRepublish is the test-first
// reproducer for the self-deadlock: handle() must return well before the
// downstream Publish call (which only this same goroutine, in production,
// could ever unblock) completes.
func TestBirthSyncHandleReturnsPromptlyDuringSlowRepublish(t *testing.T) {
	t.Parallel()
	pub := newSlowBirthPublisher(1)
	bridge := NewBridge(BridgeConfig{Base: "gh", HADiscoveryEnabled: true}, pub)
	bridge.mu.Lock()
	bridge.declared["homeassistant/switch/gh/obj1/config"] = []byte(`{"x":1}`)
	bridge.mu.Unlock()

	bs := NewBirthSync(NewNoopClient(), bridge, nil)
	defer bs.Close()

	done := make(chan struct{})
	start := time.Now()
	go func() {
		bs.handle(HABirthTopic, []byte("online"), false)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("handle() did not return; it must dispatch RepublishDiscovery off the calling goroutine")
	}
	if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
		t.Fatalf("handle() took %v to return while Publish was still blocked; want near-instant return", elapsed)
	}

	// The work must still eventually run: the dispatcher already called
	// into Publish (it is parked on <-release right now).
	select {
	case <-pub.calls:
	case <-time.After(2 * time.Second):
		t.Fatal("RepublishDiscovery never reached the slow Publish call")
	}
	close(pub.release)
}

// TestBirthSyncCloseDrainsCleanly proves Close blocks until the in-flight
// republish finishes (no queued/running job is abandoned) and that the
// worker goroutine it owns actually exits — the documented lifecycle from
// NewBirthSync to Close.
func TestBirthSyncCloseDrainsCleanly(t *testing.T) {
	t.Parallel()
	pub := newSlowBirthPublisher(1)
	bridge := NewBridge(BridgeConfig{Base: "gh", HADiscoveryEnabled: true}, pub)
	bridge.mu.Lock()
	bridge.declared["homeassistant/switch/gh/obj1/config"] = []byte(`{"x":1}`)
	bridge.mu.Unlock()

	bs := NewBirthSync(NewNoopClient(), bridge, nil)
	bs.handle(HABirthTopic, []byte("online"), false)

	// Wait until the worker is actually inside Publish before racing Close
	// against it.
	select {
	case <-pub.calls:
	case <-time.After(2 * time.Second):
		t.Fatal("RepublishDiscovery never reached the slow Publish call")
	}

	closeDone := make(chan struct{})
	go func() {
		bs.Close()
		close(closeDone)
	}()

	select {
	case <-closeDone:
		t.Fatal("Close returned before the in-flight republish was released; it must drain, not abandon, running work")
	case <-time.After(100 * time.Millisecond):
	}

	close(pub.release)
	select {
	case <-closeDone:
	case <-time.After(2 * time.Second):
		t.Fatal("Close never returned after the blocked Publish was released — worker goroutine leaked")
	}

	// Close is idempotent.
	bs.Close()
}

// TestBirthSyncHandleOfflineDoesNotEnqueue keeps the existing "offline is a
// no-op" contract intact under the dispatcher refactor: an "offline"
// payload must never touch the dispatcher at all.
func TestBirthSyncHandleOfflineDoesNotEnqueue(t *testing.T) {
	t.Parallel()
	pub := &mockPublisher{}
	bridge := NewBridge(BridgeConfig{Base: "gh", HADiscoveryEnabled: true}, pub)
	bs := NewBirthSync(NewNoopClient(), bridge, nil)
	defer bs.Close()

	bs.handle(HABirthTopic, []byte("offline"), false)
	bs.dispatcher.flush()
	if got := len(pub.publications()); got != 0 {
		t.Fatalf("offline payload triggered %d publishes, want 0", got)
	}
}
