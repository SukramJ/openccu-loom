// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

//go:build integration

package integration

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/north/mqtt"
)

// TestMQTTRoundTripAgainstRealBroker proves that the TCPClient can
// publish and receive against a genuine Mosquitto container, verifying
// bidirectional control without relying on the in-process mock broker.
func TestMQTTRoundTripAgainstRealBroker(t *testing.T) {
	mb := startMosquitto(t)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	pub := mqtt.NewTCPClient(mqtt.TCPConfig{
		BrokerURL: mb.URL(), ClientID: "pub", KeepAlive: 30 * time.Second,
	})
	if err := pub.Connect(ctx); err != nil {
		t.Fatalf("publisher connect: %v", err)
	}
	defer pub.Disconnect(ctx) //nolint:errcheck // teardown

	sub := mqtt.NewTCPClient(mqtt.TCPConfig{
		BrokerURL: mb.URL(), ClientID: "sub", KeepAlive: 30 * time.Second,
	})
	if err := sub.Connect(ctx); err != nil {
		t.Fatalf("subscriber connect: %v", err)
	}
	defer sub.Disconnect(ctx) //nolint:errcheck

	var mu sync.Mutex
	var received []string
	ready := make(chan struct{})
	if err := sub.Subscribe(ctx, "gh/test/#", mqtt.QoS1, func(topic string, _ []byte, _ bool) {
		mu.Lock()
		received = append(received, topic)
		mu.Unlock()
		select {
		case <-ready:
		default:
			close(ready)
		}
	}); err != nil {
		t.Fatalf("subscribe: %v", err)
	}

	// Allow SUBACK to land; Mosquitto usually acks within a few ms.
	time.Sleep(200 * time.Millisecond)

	if err := pub.Publish(ctx, "gh/test/hello", []byte("world"), mqtt.QoS1, false); err != nil {
		t.Fatalf("publish: %v", err)
	}

	select {
	case <-ready:
	case <-time.After(5 * time.Second):
		t.Fatalf("subscriber never received the message; got %v", received)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(received) == 0 || received[0] != "gh/test/hello" {
		t.Fatalf("received=%v", received)
	}
}
