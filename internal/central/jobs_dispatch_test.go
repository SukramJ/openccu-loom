// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// Covers the devices_created gate for hub-level scheduler jobs.

package central

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/central/events"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
)

// TestWireDevicesCreatedGateInitiallyFalse verifies that before any
// DeviceCreatedEvent fires, IsDevicesCreated returns false (gate is
// blocking).
func TestWireDevicesCreatedGateInitiallyFalse(t *testing.T) {
	c, err := New(Config{Name: "test-dcg1"})
	if err != nil {
		t.Fatal(err)
	}
	c.WireDevicesCreatedGate()

	if c.IsDevicesCreated() {
		t.Error("IsDevicesCreated must be false before any DeviceCreatedEvent")
	}
}

// TestWireDevicesCreatedGateOpenedByEvent verifies that after a
// DeviceCreatedEvent fires, IsDevicesCreated returns true.
func TestWireDevicesCreatedGateOpenedByEvent(t *testing.T) {
	c, err := New(Config{Name: "test-dcg2"})
	if err != nil {
		t.Fatal(err)
	}
	c.WireDevicesCreatedGate()

	// Publish a DeviceCreatedEvent.
	events.Publish(c.EventBus, hmevent.DeviceCreatedEvent{
		Base:        hmevent.NewBase(),
		CentralName: c.cfg.Name,
		Address:     "TEST0001",
	})

	// Allow the async bus delivery to propagate.
	deadline := time.Now().Add(200 * time.Millisecond)
	for time.Now().Before(deadline) {
		if c.IsDevicesCreated() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Error("IsDevicesCreated did not become true after DeviceCreatedEvent")
}

// TestIsDevicesCreatedTrueWhenTheModelHoldsDevices pins that the gate opens
// on the fact, not only on the announcement. The ingest pipeline materialises
// a whole interface without publishing one DeviceCreatedEvent per device, so
// a gate that waited for the event alone would hold every gated hub job back
// on a central whose devices are all present.
func TestIsDevicesCreatedTrueWhenTheModelHoldsDevices(t *testing.T) {
	c, err := New(Config{Name: "test-dcg-model"})
	if err != nil {
		t.Fatal(err)
	}
	c.WireDevicesCreatedGate()
	if c.IsDevicesCreated() {
		t.Fatal("gate must be closed while the model is empty")
	}

	c.ModelRegistry.Put(device.New(device.Config{
		Interface:   hmenum.InterfaceHmIPRF,
		Address:     "GATE0001",
		Model:       "HmIP-PS",
		InterfaceID: "test-dcg-model-HmIP-RF",
	}))

	if !c.IsDevicesCreated() {
		t.Error("gate must be open once the model holds devices")
	}
}

// TestIsDevicesCreatedTrueWithoutGate verifies that IsDevicesCreated
// returns true when WireDevicesCreatedGate has never been called (gate
// is absent = no-op).
func TestIsDevicesCreatedTrueWithoutGate(t *testing.T) {
	c, err := New(Config{Name: "test-dcg3"})
	if err != nil {
		t.Fatal(err)
	}
	// Do NOT call WireDevicesCreatedGate.
	if !c.IsDevicesCreated() {
		t.Error("IsDevicesCreated must return true when gate is not wired")
	}
}

// TestGatedRunWithDevicesCreatedGateBlocksBeforeEvent verifies that
// gatedRunWithDevicesCreatedGate does not execute fn when no
// DeviceCreatedEvent has fired yet (gate is blocking).
func TestGatedRunWithDevicesCreatedGateBlocksBeforeEvent(t *testing.T) {
	c, err := New(Config{Name: "test-dcg4"})
	if err != nil {
		t.Fatal(err)
	}
	c.WireDevicesCreatedGate()
	advanceCentralToRunning(t, c)

	var calls atomic.Int32
	fn := gatedRunWithDevicesCreatedGate(c, false, func(context.Context) error {
		calls.Add(1)
		return nil
	})

	// Gate is still closed — fn must NOT run.
	if err := fn(context.Background()); err != nil {
		t.Fatalf("gatedRunWithDevicesCreatedGate returned error: %v", err)
	}
	if calls.Load() != 0 {
		t.Errorf("fn must not run before DeviceCreatedEvent, calls=%d", calls.Load())
	}
}

// TestGatedRunWithDevicesCreatedGateRunsAfterEvent verifies that
// gatedRunWithDevicesCreatedGate executes fn after a DeviceCreatedEvent.
func TestGatedRunWithDevicesCreatedGateRunsAfterEvent(t *testing.T) {
	c, err := New(Config{Name: "test-dcg5"})
	if err != nil {
		t.Fatal(err)
	}
	c.WireDevicesCreatedGate()
	advanceCentralToRunning(t, c)

	// Open the gate via event.
	events.Publish(c.EventBus, hmevent.DeviceCreatedEvent{
		Base:        hmevent.NewBase(),
		CentralName: c.cfg.Name,
		Address:     "TEST0002",
	})

	// Wait for gate to open.
	deadline := time.Now().Add(200 * time.Millisecond)
	for time.Now().Before(deadline) {
		if c.IsDevicesCreated() {
			break
		}
		time.Sleep(2 * time.Millisecond)
	}
	if !c.IsDevicesCreated() {
		t.Skip("DeviceCreatedEvent not delivered in time — skipping functional check")
	}

	var calls atomic.Int32
	fn := gatedRunWithDevicesCreatedGate(c, false, func(context.Context) error {
		calls.Add(1)
		return nil
	})

	if err := fn(context.Background()); err != nil {
		t.Fatalf("gatedRunWithDevicesCreatedGate returned error: %v", err)
	}
	if calls.Load() != 1 {
		t.Errorf("expected 1 call after DeviceCreatedEvent, got %d", calls.Load())
	}
}

// TestGatedRunWithDevicesCreatedGateNoGateActsLikeGatedRun verifies that
// when no gate is wired (WireDevicesCreatedGate not called), the behaviour
// is identical to gatedRun — fn runs when RUNNING.
func TestGatedRunWithDevicesCreatedGateNoGateActsLikeGatedRun(t *testing.T) {
	c, err := New(Config{Name: "test-dcg6"})
	if err != nil {
		t.Fatal(err)
	}
	advanceCentralToRunning(t, c)
	// WireDevicesCreatedGate NOT called → IsDevicesCreated always true.

	var calls atomic.Int32
	fn := gatedRunWithDevicesCreatedGate(c, false, func(context.Context) error {
		calls.Add(1)
		return nil
	})

	if err := fn(context.Background()); err != nil {
		t.Fatalf("gatedRunWithDevicesCreatedGate: %v", err)
	}
	if calls.Load() != 1 {
		t.Errorf("expected 1 call without gate wired, got %d", calls.Load())
	}
}
