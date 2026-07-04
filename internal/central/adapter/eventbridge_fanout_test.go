// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package adapter

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/central/events"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/north/mqtt"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// slowPublisher is an mqtt.Publisher whose Publish blocks until released or the
// context is cancelled — a stand-in for a slow / half-open broker. It signals
// the first time Publish is entered so a test can wait until the fan-out worker
// is actually stuck in broker I/O.
type slowPublisher struct {
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func newSlowPublisher() *slowPublisher {
	return &slowPublisher{entered: make(chan struct{}), release: make(chan struct{})}
}

func (s *slowPublisher) Publish(ctx context.Context, _ string, _ []byte, _ mqtt.QoS, _ bool) error {
	s.once.Do(func() { close(s.entered) })
	select {
	case <-s.release:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func stateChangeOn(deviceAddr string) hmevent.DataPointValueChangedEvent {
	return hmevent.DataPointValueChangedEvent{
		Base: hmevent.NewBaseAt(time.Now()),
		Key: hmtypes.DataPointKey{
			ChannelAddress: deviceAddr + ":1",
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(hmenum.ParameterState),
		},
		NewValue: hmtypes.BoolValue(true),
	}
}

// registryWithTwoCentrals builds a registry holding two independent centrals,
// each with one device. Returns the two event buses so a test can drive them
// separately.
func registryWithTwoCentrals(t *testing.T) (reg *central.Registry, busA, busB *events.Bus, addrA, addrB string) {
	t.Helper()
	reg = central.NewRegistry()
	mk := func(name, addr string) *events.Bus {
		c, err := central.New(central.Config{Name: name})
		if err != nil {
			t.Fatalf("central %s: %v", name, err)
		}
		if err := reg.Register(c); err != nil {
			t.Fatalf("register %s: %v", name, err)
		}
		c.ModelRegistry.Put(device.New(device.Config{
			InterfaceID: "HmIP-RF", Interface: hmenum.InterfaceHmIPRF,
			Address: addr, Model: "HmIP-STH", Name: name + "-dev",
		}))
		return c.EventBus
	}
	addrA, addrB = "000A0001", "000B0001"
	return reg, mk("ccu-A", addrA), mk("ccu-B", addrB), addrA, addrB
}

// TestEventBridgeSlowBrokerDoesNotStallOtherCentralDispatch is the core
// decoupling test: with the fan-out worker stuck in a blocking broker publish
// for one central, another central's bus dispatch must still return promptly
// rather than freezing behind the shared broker.
func TestEventBridgeSlowBrokerDoesNotStallOtherCentralDispatch(t *testing.T) {
	t.Parallel()
	reg, busA, busB, addrA, addrB := registryWithTwoCentrals(t)

	slow := newSlowPublisher()
	bridge := mqtt.NewBridge(mqtt.BridgeConfig{Base: "openccu-loom", CentralName: "ccu-A", RawEnabled: true}, slow)
	eb := NewEventBridge(reg, nil, mqtt.NewWiring(bridge, nil))
	eb.Start(context.Background())
	defer eb.Stop() // cancels the fan-out context, unblocking the stuck worker

	// Central A: the worker dequeues this and blocks in slow.Publish.
	events.Publish(busA, stateChangeOn(addrA))
	select {
	case <-slow.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("fan-out worker never reached the broker publish")
	}

	// Central B: its bus dispatch must not block behind the stuck worker.
	done := make(chan struct{})
	go func() {
		events.Publish(busB, stateChangeOn(addrB))
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("central B bus dispatch stalled behind central A's slow broker")
	}
}

// TestEventBridgeSlowBrokerOverflowDropsAndCounts verifies the bounded fan-out
// queue applies drop-oldest backpressure — the bus dispatch never blocks — and
// counts the drops, when a slow broker cannot keep up with a value-change flood.
func TestEventBridgeSlowBrokerOverflowDropsAndCounts(t *testing.T) {
	t.Parallel()
	reg, d := registryWithDevice(t)

	slow := newSlowPublisher()
	bridge := mqtt.NewBridge(mqtt.BridgeConfig{Base: "openccu-loom", CentralName: "ccu-01", RawEnabled: true}, slow)
	eb := NewEventBridge(reg, nil, mqtt.NewWiring(bridge, nil))
	eb.Start(context.Background())
	defer eb.Stop()

	bus := reg.List()[0].EventBus

	// First event: the worker dequeues it and blocks in the broker.
	events.Publish(bus, stateChangeOn(d.Address))
	select {
	case <-slow.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("fan-out worker never reached the broker publish")
	}

	// Flood the queue past its capacity. Every publish must return without
	// blocking; the overflow is dropped (oldest first) and counted.
	for range mqttFanoutQueueDepth + 50 {
		events.Publish(bus, stateChangeOn(d.Address))
	}

	if dropped := eb.FanoutDropped(); dropped == 0 {
		t.Fatalf("expected fan-out overflow drops under a stuck broker, got %d", dropped)
	}
}

// TestEventBridgeStartIsIdempotent pins the finding-4 fix: a second Start must
// not double-subscribe. It asserts both the handler count on the bus and that a
// single value change produces a single MQTT fan-out (not two).
func TestEventBridgeStartIsIdempotent(t *testing.T) {
	t.Parallel()
	reg, d := registryWithDevice(t)

	pub := mqtt.NewNoopClient()
	bridge := mqtt.NewBridge(mqtt.BridgeConfig{Base: "openccu-loom", CentralName: "ccu-01", RawEnabled: true}, pub)
	eb := NewEventBridge(reg, nil, mqtt.NewWiring(bridge, nil))
	eb.Start(context.Background())
	eb.Start(context.Background()) // second Start must be a no-op re-attach
	defer eb.Stop()

	bus := reg.List()[0].EventBus
	if got := bus.HandlerCount(hmevent.DataPointValueChangedEvent{}.Type()); got != 1 {
		t.Fatalf("value-changed handlers after double Start: got %d, want 1", got)
	}

	events.Publish(bus, stateChangeOn(d.Address))
	eb.Flush()

	if got := nonAvailabilityPublishes(pub.Published()); len(got) != 1 {
		t.Fatalf("double Start double-published: got %d publishes, want 1: %+v", len(got), got)
	}
}
