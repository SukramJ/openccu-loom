// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

//go:build e2e

package harness

import (
	"fmt"
	"testing"

	mqtt "github.com/mochi-mqtt/server/v2"
	"github.com/mochi-mqtt/server/v2/hooks/auth"
	"github.com/mochi-mqtt/server/v2/listeners"
	"github.com/mochi-mqtt/server/v2/packets"
)

// MQTTBroker is the harness's pure-Go MQTT broker. The concrete
// implementation wraps mochi-mqtt/server v2 — MIT, pure-Go, MQTT v5.
//
// Tests use the broker through three surfaces:
//
//   - URL() / Stop() — basic lifecycle, used by the daemon-side
//     config and the harness teardown.
//   - Subscribe(filter, handler) — observe outbound traffic from the
//     daemon (HA Discovery, state echoes). Handler runs synchronously
//     for every matching frame.
//   - Publish(topic, payload, retain, qos) — inject inbound traffic
//     for round-trip tests (raw-plane writes, command frames).
//
// The Subscribe and Publish surfaces are wired via mochi-mqtt's
// built-in InlineClient, so tests do not need a separate MQTT
// client library. That keeps the harness pure-Go end-to-end.
type MQTTBroker interface {
	URL() string
	Stop() error

	Subscribe(filter string, handler MQTTHandler) error
	Publish(topic string, payload []byte, retain bool, qos byte) error
}

// MQTTHandler is invoked for every frame matching a Subscribe
// filter. The handler runs on the broker's dispatch goroutine —
// keep it short, fan out to a channel for any real work.
type MQTTHandler func(topic string, payload []byte, retain bool)

// startMQTTBroker spins up an embedded broker on an OS-assigned port
// and registers a t.Cleanup that stops it. Anonymous CONNECTs are
// allowed (the harness is loopback-only).
func startMQTTBroker(t *testing.T) MQTTBroker {
	t.Helper()

	rp := pickFreePort(t)
	// The mochi-mqtt listener does its own bind, so we release the
	// reserved socket immediately — mochi takes over on srv.Serve().
	rp.Release(t)
	port := rp.Port()
	srv := mqtt.New(&mqtt.Options{
		// InlineClient enables in-process Subscribe/Publish so tests
		// can observe and inject without a separate MQTT client lib.
		InlineClient: true,
	})
	if err := srv.AddHook(new(auth.AllowHook), nil); err != nil {
		t.Fatalf("mqtt: add auth hook: %v", err)
	}
	tcp := listeners.NewTCP(listeners.Config{
		ID:      "harness",
		Address: loopbackAddr(port),
	})
	if err := srv.AddListener(tcp); err != nil {
		t.Fatalf("mqtt: add listener: %v", err)
	}

	ready := make(chan error, 1)
	go func() {
		// Serve blocks until the broker is closed; surface startup
		// errors via the ready channel.
		if err := srv.Serve(); err != nil {
			ready <- err
			return
		}
		ready <- nil
	}()

	b := &mochiBroker{srv: srv, port: port}
	t.Cleanup(func() { _ = b.Stop() })
	return b
}

type mochiBroker struct {
	srv  *mqtt.Server
	port int
	// nextSubID monotonically increases so concurrent Subscribe
	// calls do not collide on mochi's subscription-id key.
	nextSubID int
}

func (b *mochiBroker) URL() string {
	return fmt.Sprintf("tcp://127.0.0.1:%d", b.port)
}

func (b *mochiBroker) Stop() error {
	if b == nil || b.srv == nil {
		return nil
	}
	return b.srv.Close()
}

// Subscribe routes every matching message to handler. The
// subscription stays active for the broker's lifetime — there is no
// Unsubscribe surface today because tests never need it (the harness
// tears the broker down at t.Cleanup time).
func (b *mochiBroker) Subscribe(filter string, handler MQTTHandler) error {
	if b == nil || b.srv == nil {
		return nil
	}
	b.nextSubID++
	id := b.nextSubID
	return b.srv.Subscribe(filter, id, func(_ *mqtt.Client, sub packets.Subscription, pk packets.Packet) {
		// pk.FixedHeader.Retain is set when the broker is replaying
		// a retained message at subscribe time; live publishes carry
		// it only when the publisher requested it.
		_ = sub
		handler(pk.TopicName, pk.Payload, pk.FixedHeader.Retain)
	})
}

// Publish injects a frame as if a normal client had published it.
func (b *mochiBroker) Publish(topic string, payload []byte, retain bool, qos byte) error {
	if b == nil || b.srv == nil {
		return nil
	}
	return b.srv.Publish(topic, payload, retain, qos)
}
