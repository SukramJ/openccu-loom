// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package webhook

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/central/events"
	"github.com/SukramJ/openccu-loom/internal/config"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// TestOutboundWithholdsUnreleasedDevices pins the outbound half of the
// release gate, driven through the real event path.
//
// This plane carries raw values rather than a device model, so a device
// the onboarding wizard has not released looks exactly like any other on
// the bus — nothing but this gate keeps it from a downstream system that
// the operator has not finished naming it for.
func TestOutboundWithholdsUnreleasedDevices(t *testing.T) {
	t.Parallel()
	u := makeCentral(t, "ccuHold")
	reg := makeRegistry(t, u)

	iface := hmtypes.ParseWireInterfaceID("HmIP-RF")
	// ABC is held by the wizard; DEF never entered it, which is what
	// every device on an existing installation looks like.
	u.Devices.StoreDelayedDeviceDescriptions(context.Background(), iface, heldDescs())
	_ = u.Devices.TakeDelayedDeviceDescriptions(context.Background(), iface, "ABC")

	ft := &fakeTransport{}
	o := NewOutbound(
		reg,
		config.NorthWebhook{Enabled: true, URL: "http://hook.test"},
		nil,
		WithHTTPClient(&http.Client{Transport: ft}),
		WithBackoff(instantBackoff()),
		WithClock(fixedClock),
	)
	if err := o.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = o.Stop(context.Background()) })

	// The released device gets through.
	events.Publish(u.EventBus, datapointEvent("HmIP-RF", "DEF:1", "STATE",
		hmtypes.BoolValue(true), hmtypes.BoolValue(false)))
	waitForCount(t, ft, 1, 2*time.Second)

	// The withheld one must not, even though its event rides the same bus
	// on the same interface with the same shape.
	events.Publish(u.EventBus, datapointEvent("HmIP-RF", "ABC:1", "STATE",
		hmtypes.BoolValue(true), hmtypes.BoolValue(false)))
	time.Sleep(250 * time.Millisecond)
	if n := ft.count(); n != 1 {
		t.Fatalf("delivered %d POST(s), want 1 — the withheld device reached the downstream system", n)
	}

	// Negative control: after the release the same event must arrive.
	// Without this half the test would pass on a gate that drops
	// everything.
	if !u.Devices.ReleaseDevice(context.Background(), iface, "ABC") {
		t.Fatal("ReleaseDevice reported nothing to release")
	}
	events.Publish(u.EventBus, datapointEvent("HmIP-RF", "ABC:1", "STATE",
		hmtypes.BoolValue(false), hmtypes.BoolValue(true)))
	waitForCount(t, ft, 2, 2*time.Second)
	if n := ft.count(); n != 2 {
		t.Errorf("delivered %d POST(s) after the release, want 2", n)
	}
}

// heldDescs is one device the wizard holds plus one it never saw.
func heldDescs() []hmproto.DeviceDescription {
	return []hmproto.DeviceDescription{
		{Address: "ABC", Type: "HmIP-STH"},
		{Address: "ABC:1", Type: "SWITCH_VIRTUAL_RECEIVER", Parent: "ABC"},
	}
}
