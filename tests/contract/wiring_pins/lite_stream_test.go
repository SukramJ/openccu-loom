// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package wiring_pins

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/central/adapter"
	"github.com/SukramJ/openccu-loom/internal/central/events"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
	"github.com/SukramJ/openccu-loom/tests/harness/litefake"
)

const liteSwitchChannel = "VCU0000321:1" // the fake's HM-LC-Sw1-Pl on BidCos-RF

// waitFor polls cond until it holds or the deadline passes.
func waitFor(t *testing.T, within time.Duration, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(within)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// TestLiteStreamEventReachesDataPoint pins the event path of a lite
// central: a value an interface process reports reaches the data point and
// is published on the central's bus, exactly as an XML-RPC callback is.
func TestLiteStreamEventReachesDataPoint(t *testing.T) {
	fake := startFake(t, litefake.Options{})
	unit := startLiteCentral(t, fake, litefake.DefaultToken)
	waitLiteReady(t, unit)

	published := make(chan hmevent.DataPointValueChangedEvent, 16)
	unsub := events.Subscribe(unit.EventBus, func(e hmevent.DataPointValueChangedEvent) {
		if e.Key.ChannelAddress == liteSwitchChannel && e.Key.Parameter == string(hmenum.ParameterState) {
			published <- e
		}
	})
	t.Cleanup(unsub)

	dev, ok := unit.ModelRegistry.Get("VCU0000321")
	if !ok {
		t.Fatal("the fake's switch is not in the model")
	}
	before, _ := dev.Channel(liteSwitchChannel).Parameter(hmenum.ParameterState).RawValue()
	next := before != true
	if err := fake.V().InterfaceRPC("BidCos-RF").SimulateDeviceEvent(liteSwitchChannel, "STATE", next); err != nil {
		t.Fatalf("SimulateDeviceEvent: %v", err)
	}
	select {
	case <-published:
	case <-time.After(10 * time.Second):
		t.Fatal("the device event never reached the central's bus")
	}
	if got, observed := dev.Channel(liteSwitchChannel).Parameter(hmenum.ParameterState).RawValue(); !observed || got != next {
		t.Errorf("STATE = %v (observed %v), want %v", got, observed, next)
	}
}

// TestLiteStreamStampsLivenessOnHeartbeat pins that a quiet interface stays
// alive on the stream's heartbeat alone: without it a lite interface with no
// device events would go stale after the callback freshness window and
// recovery would loop.
func TestLiteStreamStampsLivenessOnHeartbeat(t *testing.T) {
	fake := startFake(t, litefake.Options{HeartbeatInterval: 100 * time.Millisecond})
	unit := startLiteCentral(t, fake, litefake.DefaultToken)
	waitLiteReady(t, unit)
	entry, ok := unit.Clients.Get(adapter.WireInterfaceID("box", hmenum.InterfaceHmIPRF))
	if !ok || entry.Client == nil {
		t.Fatal("no HmIP-RF client registered")
	}
	first := entry.Client.LastCallbackAt()
	waitFor(t, 3*time.Second, "a heartbeat to stamp the quiet interface", func() bool {
		return entry.Client.LastCallbackAt().After(first)
	})
}

// TestLiteInterfaceDownForcesUnavailable pins that an interface the box
// reports down makes its devices unavailable — the same connection loss a
// CCU outage publishes — and that they come back once it is up again.
func TestLiteInterfaceDownForcesUnavailable(t *testing.T) {
	fake := startFake(t, litefake.Options{})
	unit := startLiteCentral(t, fake, litefake.DefaultToken)
	closer := adapter.WireDeviceAvailability(unit)
	t.Cleanup(closer)
	waitLiteReady(t, unit)
	dev, ok := unit.ModelRegistry.Get("VCU0000321")
	if !ok {
		t.Fatal("the fake's BidCos-RF switch is not in the model")
	}
	if err := fake.SetInterfaceDown("BidCos-RF", true); err != nil {
		t.Fatalf("SetInterfaceDown: %v", err)
	}
	waitFor(t, 15*time.Second, "the BidCos-RF devices to turn unavailable", func() bool {
		return !dev.AvailabilityInfo().IsReachable
	})
	if err := fake.SetInterfaceDown("BidCos-RF", false); err != nil {
		t.Fatalf("SetInterfaceDown: %v", err)
	}
	waitFor(t, 60*time.Second, "the BidCos-RF devices to recover", func() bool {
		return dev.AvailabilityInfo().IsReachable
	})
}

// TestLiteResyncTriggersReseed pins that a window in which the stream may
// have missed events is reseeded: after a dropped stream whose resume
// position the box no longer holds, the central reads its values again
// through the current generation's pipeline.
func TestLiteResyncTriggersReseed(t *testing.T) {
	fake := startFake(t, litefake.Options{})
	unit := startLiteCentral(t, fake, litefake.DefaultToken)
	waitLiteReady(t, unit)
	stateReads := func() int {
		n := 0
		for _, c := range fake.Calls() {
			if c.Path == "/api/rpc/v1/state" {
				n++
			}
		}
		return n
	}
	// The seed of the bring-up happens before the ready latch; let the
	// stream settle, then take the baseline.
	time.Sleep(300 * time.Millisecond)
	base := stateReads()
	fake.ForceGap()
	fake.DropStreams()
	waitFor(t, 20*time.Second, "a reseed after the resync", func() bool { return stateReads() > base })
}

// TestLiteHotplugFetchesDescriptionsForUnknownAddresses pins hot-plug on a
// lite central: the stream announces only the new device's address, and
// the central reads the descriptions through the proxy and materialises
// the device.
func TestLiteHotplugFetchesDescriptionsForUnknownAddresses(t *testing.T) {
	fake := startFake(t, litefake.Options{})
	unit := startLiteCentral(t, fake, litefake.DefaultToken)
	waitLiteReady(t, unit)
	before := len(unit.ModelRegistry.List())
	if err := fake.V().InterfaceRPC("HmIP-RF").AddDevices(context.Background(), []string{"HmIP-PS"}); err != nil {
		t.Fatalf("AddDevices: %v", err)
	}
	waitFor(t, 20*time.Second, "the hot-plugged device in the model", func() bool {
		for _, d := range unit.ModelRegistry.List() {
			if strings.EqualFold(d.Model, "HmIP-PS") {
				return true
			}
		}
		return false
	})
	if after := len(unit.ModelRegistry.List()); after <= before {
		t.Errorf("model grew from %d to %d devices", before, after)
	}
}
