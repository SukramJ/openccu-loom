// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package mqtt

import (
	"context"
	"sync"
	"testing"
	"time"
)

// newTestClient returns a *TCPClient pointed at the supplied broker
// URL. KeepAlive is set via cfg, but NewTCPClient floors anything below
// 30 s — the test mutates c.cfg.KeepAlive after construction to bypass
// the floor when the test wants fast keep-alive ticks.
func newTestClient(brokerURL string, fastKeepAlive time.Duration) *TCPClient {
	c := NewTCPClient(TCPConfig{
		BrokerURL:    brokerURL,
		ClientID:     "test-client",
		KeepAlive:    30 * time.Second,
		DialTimeout:  2 * time.Second,
		AckTimeout:   2 * time.Second,
		CleanSession: true,
	})
	if fastKeepAlive > 0 {
		c.cfg.KeepAlive = fastKeepAlive // sidestep the 30-s floor for tests
	}
	return c
}

func TestTCPClient_IsConnected_LifecyclePhases(t *testing.T) {
	t.Parallel()
	b := newFakeBroker(t)
	b.Start()

	c := newTestClient(b.URL(), 0)
	if c.IsConnected() {
		t.Fatal("IsConnected must be false before Connect")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := c.Connect(ctx); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	if !c.IsConnected() {
		t.Fatal("IsConnected must be true after Connect")
	}

	if err := c.Disconnect(ctx); err != nil {
		t.Fatalf("Disconnect: %v", err)
	}
	if c.IsConnected() {
		t.Fatal("IsConnected must be false after Disconnect")
	}
}

func TestTCPClient_LastConnectedAt_PopulatedAfterConnect(t *testing.T) {
	t.Parallel()
	b := newFakeBroker(t)
	b.Start()

	c := newTestClient(b.URL(), 0)
	if !c.LastConnectedAt().IsZero() {
		t.Fatal("LastConnectedAt must be zero pre-Connect")
	}
	before := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := c.Connect(ctx); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer c.Disconnect(ctx) //nolint:errcheck // test cleanup; disconnect errors are irrelevant after the assertion

	got := c.LastConnectedAt()
	if got.IsZero() {
		t.Fatal("LastConnectedAt must be populated after Connect")
	}
	if got.Before(before) {
		t.Fatalf("LastConnectedAt = %v, want >= %v", got, before)
	}
}

func TestTCPClient_KeepAlivePing_FiresPingreq(t *testing.T) {
	t.Parallel()
	b := newFakeBroker(t)
	b.Start()

	// Keep-alive = 200ms → ticker = 100ms. Test waits up to ~1s
	// for the first ping to arrive at the broker.
	c := newTestClient(b.URL(), 200*time.Millisecond)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := c.Connect(ctx); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer c.Disconnect(ctx) //nolint:errcheck // test cleanup; disconnect errors are irrelevant after the assertion

	if !b.WaitPing(time.Second) {
		t.Fatal("broker did not receive a PINGREQ within 1 s")
	}
}

func TestTCPClient_SubscribeAndReceive_Message(t *testing.T) {
	t.Parallel()
	b := newFakeBroker(t)
	b.Start()

	c := newTestClient(b.URL(), 0)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := c.Connect(ctx); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer c.Disconnect(ctx) //nolint:errcheck // test cleanup; disconnect errors are irrelevant after the assertion

	var (
		mu    sync.Mutex
		got   []byte
		gotCh = make(chan struct{}, 1)
	)
	if err := c.Subscribe(ctx, "test/topic", QoS1, func(_ string, payload []byte, _ bool) {
		mu.Lock()
		got = append([]byte(nil), payload...)
		mu.Unlock()
		select {
		case gotCh <- struct{}{}:
		default:
		}
	}); err != nil {
		t.Fatalf("Subscribe: %v", err)
	}

	// Give the broker a beat to register the SUBSCRIBE before we push.
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if len(b.Subscribes()) > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if len(b.Subscribes()) != 1 || b.Subscribes()[0] != "test/topic" {
		t.Fatalf("broker subscribes = %v", b.Subscribes())
	}

	if err := b.PublishToClient("test/topic", []byte("hello")); err != nil {
		t.Fatalf("PublishToClient: %v", err)
	}
	select {
	case <-gotCh:
		mu.Lock()
		defer mu.Unlock()
		if string(got) != "hello" {
			t.Fatalf("payload = %q, want %q", got, "hello")
		}
	case <-time.After(time.Second):
		t.Fatal("handler did not fire within 1 s")
	}
}

func TestTCPClient_Unsubscribe_ReachesBroker(t *testing.T) {
	t.Parallel()
	b := newFakeBroker(t)
	b.Start()

	c := newTestClient(b.URL(), 0)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := c.Connect(ctx); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer c.Disconnect(ctx) //nolint:errcheck // test cleanup; disconnect errors are irrelevant after the assertion

	if err := c.Subscribe(ctx, "test/topic", QoS1, func(string, []byte, bool) {}); err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	if err := c.Unsubscribe(ctx, "test/topic"); err != nil {
		t.Fatalf("Unsubscribe: %v", err)
	}
	// Brief settle so the broker drains the UNSUBSCRIBE frame.
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		b.mu.Lock()
		n := len(b.unsubscribes)
		b.mu.Unlock()
		if n > 0 {
			return // success
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("broker did not receive UNSUBSCRIBE within 1 s")
}

func TestTCPClient_Disconnect_SendsDisconnectFrame(t *testing.T) {
	t.Parallel()
	b := newFakeBroker(t)
	b.Start()

	c := newTestClient(b.URL(), 0)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := c.Connect(ctx); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	if err := c.Disconnect(ctx); err != nil {
		t.Fatalf("Disconnect: %v", err)
	}

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		b.mu.Lock()
		got := b.disconnect
		b.mu.Unlock()
		if got {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("broker did not see DISCONNECT within 1 s")
}

func TestTCPClient_Connack_NonZeroReturnCode_FailsConnect(t *testing.T) {
	t.Parallel()
	b := newFakeBroker(t)
	b.connackReturnCode = 5 // "Not authorized"
	b.Start()

	c := newTestClient(b.URL(), 0)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	err := c.Connect(ctx)
	if err == nil {
		t.Fatal("Connect must fail when CONNACK return code is non-zero")
	}
}

func TestTCPClient_PublishQoS0_HappyPath(t *testing.T) {
	t.Parallel()
	b := newFakeBroker(t)
	b.Start()

	c := newTestClient(b.URL(), 0)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := c.Connect(ctx); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer c.Disconnect(ctx) //nolint:errcheck // test cleanup; disconnect errors are irrelevant after the assertion

	if err := c.Publish(ctx, "data/sensor", []byte("42"), QoS0, false); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	pub, ok := b.WaitPublish(time.Second)
	if !ok {
		t.Fatal("broker did not receive PUBLISH within 1 s")
	}
	if pub.Topic != "data/sensor" || string(pub.Payload) != "42" || pub.QoS != 0 {
		t.Fatalf("captured publish = %+v", pub)
	}
}

func TestTCPClient_PublishQoS1_PuBackUnblocksCaller(t *testing.T) {
	t.Parallel()
	b := newFakeBroker(t)
	b.Start()

	c := newTestClient(b.URL(), 0)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := c.Connect(ctx); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer c.Disconnect(ctx) //nolint:errcheck // test cleanup; disconnect errors are irrelevant after the assertion

	if err := c.Publish(ctx, "data/sensor", []byte("17"), QoS1, false); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	// At this point the broker has both received the PUBLISH and
	// responded with PUBACK (the client returns only after PUBACK).
	pub, ok := b.WaitPublish(time.Second)
	if !ok {
		t.Fatal("broker did not receive PUBLISH within 1 s")
	}
	if pub.QoS != 1 {
		t.Fatalf("QoS = %d, want 1", pub.QoS)
	}
}
