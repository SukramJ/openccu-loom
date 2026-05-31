// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package adapter

import (
	"testing"

	"github.com/SukramJ/openccu-loom/internal/central/events"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
)

// TestWireDeviceAvailabilityForcesUnavailableOnDisconnect pins the
// audit L-A4-01 fix: when a ClientStateChangedEvent reports a non-
// connected state, every device on the affected interface gets its
// forced-availability flipped to ForcedDeviceAvailabilityForceFalse.
func TestWireDeviceAvailabilityForcesUnavailableOnDisconnect(t *testing.T) {
	c := newCentralForHealthTest(t)

	dev := device.New(device.Config{InterfaceID: "HmIP-RF", Address: "DEV001", Model: "HmIP-PSM"})
	c.ModelRegistry.Put(dev)

	closer := WireDeviceAvailability(c)
	defer closer()

	events.Publish(c.EventBus, hmevent.ClientStateChangedEvent{
		Base:        hmevent.NewBase(),
		InterfaceID: "HmIP-RF",
		To:          hmenum.ClientStateDisconnected,
	})

	got := dev.Availability().Forced()
	if got != hmenum.ForcedDeviceAvailabilityForceFalse {
		t.Fatalf("disconnect → ForceFalse, got Forced=%q", got)
	}
}

// TestWireDeviceAvailabilityClearsOnReconnect verifies the inverse:
// when the client transitions back to CONNECTED the override is
// cleared (NotSet) so the device's own availability tracker resumes
// driving the visible state.
func TestWireDeviceAvailabilityClearsOnReconnect(t *testing.T) {
	c := newCentralForHealthTest(t)

	dev := device.New(device.Config{InterfaceID: "HmIP-RF", Address: "DEV002", Model: "HmIP-PSM"})
	c.ModelRegistry.Put(dev)
	dev.SetForcedAvailability(hmenum.ForcedDeviceAvailabilityForceFalse) // pre-condition: previously disconnected

	closer := WireDeviceAvailability(c)
	defer closer()

	events.Publish(c.EventBus, hmevent.ClientStateChangedEvent{
		Base:        hmevent.NewBase(),
		InterfaceID: "HmIP-RF",
		To:          hmenum.ClientStateConnected,
	})

	got := dev.Availability().Forced()
	if got != hmenum.ForcedDeviceAvailabilityNotSet {
		t.Fatalf("reconnect → NotSet, got Forced=%q", got)
	}
}

// TestWireDeviceAvailabilityScopedToInterface ensures the override
// applies only to devices on the matching interface and leaves
// devices on other interfaces untouched.
func TestWireDeviceAvailabilityScopedToInterface(t *testing.T) {
	c := newCentralForHealthTest(t)

	devHmIP := device.New(device.Config{InterfaceID: "HmIP-RF", Address: "DEV003", Model: "HmIP-PSM"})
	devBidCos := device.New(device.Config{InterfaceID: "BidCos-RF", Address: "DEV004", Model: "HM-LC-Sw1"})
	c.ModelRegistry.Put(devHmIP)
	c.ModelRegistry.Put(devBidCos)

	closer := WireDeviceAvailability(c)
	defer closer()

	events.Publish(c.EventBus, hmevent.ClientStateChangedEvent{
		Base:        hmevent.NewBase(),
		InterfaceID: "HmIP-RF",
		To:          hmenum.ClientStateFailed,
	})

	if got := devHmIP.Availability().Forced(); got != hmenum.ForcedDeviceAvailabilityForceFalse {
		t.Errorf("HmIP-RF device must be ForceFalse, got %q", got)
	}
	if got := devBidCos.Availability().Forced(); got != hmenum.ForcedDeviceAvailabilityNotSet {
		t.Errorf("BidCos-RF device must remain NotSet, got %q", got)
	}
}

// TestWireDeviceAvailabilityCloserUnsubscribes verifies that the
// returned closer drops the bus subscription so a disconnect after
// teardown no longer flips device state.
func TestWireDeviceAvailabilityCloserUnsubscribes(t *testing.T) {
	c := newCentralForHealthTest(t)
	dev := device.New(device.Config{InterfaceID: "HmIP-RF", Address: "DEV005", Model: "HmIP-PSM"})
	c.ModelRegistry.Put(dev)

	closer := WireDeviceAvailability(c)
	closer()

	events.Publish(c.EventBus, hmevent.ClientStateChangedEvent{
		Base:        hmevent.NewBase(),
		InterfaceID: "HmIP-RF",
		To:          hmenum.ClientStateFailed,
	})

	if got := dev.Availability().Forced(); got != hmenum.ForcedDeviceAvailabilityNotSet {
		t.Fatalf("subscription must be inactive after closer; got Forced=%q", got)
	}
}
