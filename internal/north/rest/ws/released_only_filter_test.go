// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package ws

import (
	"net/http/httptest"
	"testing"
	"time"
)

// TestReleasedOnlyFilterIsPerConnectionAndOptIn pins the shape of the
// filter, which is the part that is easy to get wrong.
//
// This plane serves two kinds of consumer at once: the Config UI, which
// must see a device that is still being onboarded — that is the whole
// point of the state — and an ecosystem client, which must not adopt it.
// So the filter is per connection and off by default. Making it the
// default would blind the first; not offering it makes the second
// reimplement it, including the race where a creation push arrives
// before its snapshot read completes.
func TestReleasedOnlyFilterIsPerConnectionAndOptIn(t *testing.T) {
	t.Parallel()
	h := NewHub()
	h.SetReleaseChecker(func(addr string) bool { return addr != "HELD0001" })

	plain := &client{hub: h}
	filtering := &client{hub: h}
	filtering.setReleasedOnly(true)

	held := DataPointValueChangedPayload{DeviceAddress: "HELD0001"}
	free := DataPointValueChangedPayload{DeviceAddress: "FREE0001"}

	// Default off: a client that never asked keeps seeing everything.
	if plain.withheldFromThisClient(held) {
		t.Error("a client that did not opt in lost a frame — the Config UI would go blind")
	}
	if filtering.withheldFromThisClient(free) {
		t.Error("a released device was withheld from a filtering client")
	}
	if !filtering.withheldFromThisClient(held) {
		t.Error("a withheld device reached a filtering client")
	}
}

// TestReleasedOnlyFilterLetsTheReleaseFrameThrough pins the one frame
// that must never be dropped.
//
// device.released is what lifts the filter. The state flips before the
// event is published, so by the time it reaches the write path the
// address reads released and the frame passes — but only as long as the
// filter asks about the CURRENT state rather than the state the frame
// describes. Dropping it would strand a filtering client forever: the
// device is invisible, and the one announcement that would make it
// visible is invisible too.
func TestReleasedOnlyFilterLetsTheReleaseFrameThrough(t *testing.T) {
	t.Parallel()
	h := NewHub()
	released := map[string]bool{"NOW0001": true}
	h.SetReleaseChecker(func(addr string) bool { return released[addr] })

	c := &client{hub: h}
	c.setReleasedOnly(true)

	if c.withheldFromThisClient(DeviceReleasedPayload{DeviceAddress: "NOW0001"}) {
		t.Error("the release frame was withheld — the client can never learn the device became adoptable")
	}
	// Negative control: while still held, its other frames are dropped.
	if !c.withheldFromThisClient(DeviceCreatedPayload{DeviceAddress: "STILL001"}) {
		t.Error("a still-held device's creation frame reached a filtering client")
	}
}

// TestMissingReleaseCheckerWithholdsNothing pins the fallback direction.
//
// A hub with no checker wired must treat everything as released. The
// opposite — withholding on a missing dependency — blanks a subscriber's
// entire stream, and it does it silently: the client sees a connection
// that works and carries nothing.
func TestMissingReleaseCheckerWithholdsNothing(t *testing.T) {
	t.Parallel()
	h := NewHub()
	c := &client{hub: h}
	c.setReleasedOnly(true)

	if c.withheldFromThisClient(DeviceCreatedPayload{DeviceAddress: "ANY00001"}) {
		t.Error("an unwired hub withheld a frame — a filtering subscriber would receive nothing at all")
	}
}

// TestNonDeviceFramesAreNeverWithheld pins that the filter stays narrow.
// A frame that names no device — hub, system, alarm — has no device to
// be withheld on behalf of, and dropping it would silently break every
// non-device feature for a filtering client.
func TestNonDeviceFramesAreNeverWithheld(t *testing.T) {
	t.Parallel()
	h := NewHub()
	h.SetReleaseChecker(func(string) bool { return false })
	c := &client{hub: h}
	c.setReleasedOnly(true)

	if c.withheldFromThisClient(map[string]any{"anything": 1}) {
		t.Error("a frame that names no device was withheld")
	}
}

// TestReleasedOnlySubscriptionDropsFramesOnTheWire drives the real write
// path: a live connection subscribes with `released_only:true` and the
// server must not send it frames about a withheld device.
//
// The predicate tests above prove the decision is correct; they do NOT
// prove writePump asks for it. Removing the call left them green, which
// is exactly the bracketing failure this repository keeps paying for — so
// the guard that matters is this one.
func TestReleasedOnlySubscriptionDropsFramesOnTheWire(t *testing.T) {
	t.Parallel()
	hub, _, _, _, _, _ := newTestHub(t)
	hub.SetReleaseChecker(func(addr string) bool { return addr != "HELD0002" })
	server := httptest.NewServer(Handler(hub, nil, nil))
	t.Cleanup(server.Close)

	c := dialWS(t, server)
	waitForClientCount(t, hub, 1)
	c.send(map[string]any{
		"op":            "subscribe",
		"topics":        []string{"*"},
		"released_only": true,
	})
	// Drain the subscribe ACK before asserting on broadcasts.
	time.Sleep(100 * time.Millisecond)

	// The withheld device first, the released one second. If the filter is
	// absent the first frame arrives and the assertion below reads it
	// instead of the second — which is precisely what a missing filter
	// does to a client: it hands it the device it asked not to see.
	hub.Publish(Event{
		Topic:   DeviceLifecycleTopic("HELD0002"),
		Type:    "device.created",
		Payload: DeviceCreatedPayload{DeviceAddress: "HELD0002", Central: "c1"},
	})
	hub.Publish(Event{
		Topic:   DeviceLifecycleTopic("FREE0002"),
		Type:    "device.created",
		Payload: DeviceCreatedPayload{DeviceAddress: "FREE0002", Central: "c1"},
	})

	var got struct {
		Payload DeviceCreatedPayload `json:"payload"`
	}
	c.recv(&got)
	if got.Payload.DeviceAddress != "FREE0002" {
		t.Fatalf("first frame was for %q, want FREE0002 — the withheld device reached a filtering subscriber",
			got.Payload.DeviceAddress)
	}
}

// TestPlainSubscriptionStillSeesEverything is the negative control for
// the test above: without the opt-in the same two publishes both arrive.
// Without it the guard would pass on a server that dropped every frame.
func TestPlainSubscriptionStillSeesEverything(t *testing.T) {
	t.Parallel()
	hub, _, _, _, _, _ := newTestHub(t)
	hub.SetReleaseChecker(func(addr string) bool { return addr != "HELD0003" })
	server := httptest.NewServer(Handler(hub, nil, nil))
	t.Cleanup(server.Close)

	c := dialWS(t, server)
	waitForClientCount(t, hub, 1)
	c.send(map[string]any{"op": "subscribe", "topics": []string{"*"}})
	time.Sleep(100 * time.Millisecond)

	hub.Publish(Event{
		Topic:   DeviceLifecycleTopic("HELD0003"),
		Type:    "device.created",
		Payload: DeviceCreatedPayload{DeviceAddress: "HELD0003", Central: "c1"},
	})

	var got struct {
		Payload DeviceCreatedPayload `json:"payload"`
	}
	c.recv(&got)
	if got.Payload.DeviceAddress != "HELD0003" {
		t.Fatalf("a client that did not opt in got %q instead of the withheld device — the Config UI would go blind",
			got.Payload.DeviceAddress)
	}
}
