// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package mqtt

import (
	"context"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/central/events"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
)

// gatedDiscoveryPlane stalls every discovery-config publish once armed,
// standing in for a broker that keeps the link up but stops acking — the
// case where a QoS1 publish blocks until the client's ack timeout.
type gatedDiscoveryPlane struct {
	mu    sync.Mutex
	armed bool
	gate  chan struct{}
}

func (g *gatedDiscoveryPlane) arm() {
	g.mu.Lock()
	g.armed = true
	g.mu.Unlock()
}

func (g *gatedDiscoveryPlane) Publish(_ context.Context, topic string, _ []byte, _ QoS, _ bool, _ ...PublishOption) error {
	g.mu.Lock()
	blocked := g.armed && isDiscoveryConfigTopic(topic)
	g.mu.Unlock()
	if blocked {
		<-g.gate
	}
	return nil
}

func (g *gatedDiscoveryPlane) Subscribe(context.Context, string, QoS, MessageHandler, ...SubscribeOption) (SubscribeResult, error) {
	return SubscribeResult{}, nil
}

func (g *gatedDiscoveryPlane) Unsubscribe(context.Context, string) error { return nil }

// TestSecurityPublisherKeepsDiscoveryOffTheBusGoroutine pins the contract
// the publisher states for itself: bus handlers enqueue, a worker performs
// every broker publish.
//
// The security domain publishes its events synchronously from within the
// per-central bus handler, so a discovery publish performed inline stalls
// that central's whole event dispatch — every other subscriber of that bus
// with it — for as long as the broker withholds its acknowledgement.
func TestSecurityPublisherKeepsDiscoveryOffTheBusGoroutine(t *testing.T) {
	t.Parallel()

	plane := &gatedDiscoveryPlane{gate: make(chan struct{})}
	bridge := NewBridge(BridgeConfig{
		Base: "openccu-loom", CentralName: "ccu-01",
		RawEnabled: true, HADiscoveryEnabled: true,
	}, plane)
	if err := bridge.AnnounceOnline(context.Background()); err != nil {
		t.Fatalf("bridge announce: %v", err)
	}

	src := &mutableSecuritySnapshot{snap: roundTripSnapshot()}
	p := NewSecurityMQTTPublisher(src, NewWiring(bridge, slog.Default()), "en", "", slog.Default())
	bus := events.NewBus()
	p.Start(bus)
	defer p.Stop()
	// The release must run before Stop joins the worker: a worker parked
	// on a stalled publish would never reach its done channel.
	defer close(plane.gate)

	// Let the first pass through unblocked so the plane is declared.
	events.Publish(bus, hmevent.SecurityStateChangedEvent{Base: hmevent.NewBaseAt(time.Now())})
	time.Sleep(150 * time.Millisecond)

	// A renamed zone changes the zone's discovery payload, so the next
	// reconcile has something the bridge must actually send.
	src.rename("erdgeschoss", "Parterre")
	plane.arm()

	done := make(chan time.Duration, 1)
	go func() {
		start := time.Now()
		events.Publish(bus, hmevent.SecurityZoneChangedEvent{Base: hmevent.NewBaseAt(time.Now())})
		done <- time.Since(start)
	}()

	select {
	case d := <-done:
		if d > time.Second {
			t.Errorf("bus dispatch took %s; the handler waited on the broker", d)
		}
	case <-time.After(2 * time.Second):
		t.Error("the security bus handler is still blocked on a stalled discovery publish")
	}
}
