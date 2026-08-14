// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package ws

import (
	"testing"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/central/events"
	"github.com/SukramJ/openccu-loom/internal/model/hub"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
)

// registerHubEventsCentral builds an extra central and registers it in reg,
// mirroring the runtime-adopt order (register first, attach the north-bound
// subscriptions afterwards).
func registerHubEventsCentral(t *testing.T, reg *central.Registry, name string) *central.Unit {
	t.Helper()
	cu, err := central.New(central.Config{Name: name})
	if err != nil {
		t.Fatalf("central.New(%q): %v", name, err)
	}
	if err := reg.Register(cu); err != nil {
		t.Fatalf("reg.Register(%q): %v", name, err)
	}
	return cu
}

// hubEventsOnTopic returns every buffered hub event published on topic.
// Both the hub-model change hooks and [Hub.Publish] run on the caller's
// goroutine, so a change made before this call is already visible — no
// polling needed for a negative assertion.
func hubEventsOnTopic(h *Hub, topic string) []Event {
	return h.Replay(0, func(got string) bool { return got == topic }).Events
}

// TestHubEventsSubscriberAttachesACentralRegisteredAfterStart is the
// regression proof for a central adopted after boot: Start used to walk the
// registry as it stood at boot time, so a later arrival got no subscriptions
// at all and its hub singletons never reached a WebSocket client. Joining the
// registry must now produce the same broadcast a boot-time central produces.
func TestHubEventsSubscriberAttachesACentralRegisteredAfterStart(t *testing.T) {
	t.Parallel()

	h := NewHub()
	reg, _ := hubEventsRegistry(t)

	sub := NewHubEventsSubscriber(reg, h)
	sub.Start()
	t.Cleanup(sub.Stop)

	adopted := registerHubEventsCentral(t, reg, "adopted")

	adopted.HubModel.ServiceMessages.Replace([]hub.ServiceMessage{
		{ID: "SM1", Name: "Low battery"},
		{ID: "SM2", Name: "Sabotage"},
	})

	ev := pollHub(t, h, func(topic string) bool {
		return topic == ServiceMessagesTopic("adopted")
	})
	if ev.Type != string(hmevent.EventTypeServiceMessage) {
		t.Fatalf("type = %q, want %q", ev.Type, string(hmevent.EventTypeServiceMessage))
	}
	p, ok := ev.Payload.(HubCountChangedPayload)
	if !ok {
		t.Fatalf("payload type %T, want HubCountChangedPayload", ev.Payload)
	}
	if p.Central != "adopted" {
		t.Errorf("central = %q, want %q", p.Central, "adopted")
	}
	if p.Count != 2 {
		t.Errorf("count = %d, want 2", p.Count)
	}
}

// TestHubEventsSubscriberStartCentralAttachesEventBusSubscriptions verifies
// the bus-driven half of the per-central wiring: connectivity changes ride
// the event bus (the tracker is attached lazily), so an adopted central's
// reachability must reach the WebSocket too, not just its hub-model
// singletons.
func TestHubEventsSubscriberStartCentralAttachesEventBusSubscriptions(t *testing.T) {
	t.Parallel()

	h := NewHub()
	reg, _ := hubEventsRegistry(t)

	sub := NewHubEventsSubscriber(reg, h)
	sub.Start()
	t.Cleanup(sub.Stop)

	adopted := registerHubEventsCentral(t, reg, "adopted")

	events.Publish(adopted.EventBus, hmevent.ConnectivityChangedEvent{
		CentralName: "adopted",
		InterfaceID: "HmIP-RF",
		Reachable:   true,
		LatencyMs:   9.5,
	})

	ev := pollHub(t, h, func(topic string) bool {
		return topic == ConnectivityTopic("adopted", "HmIP-RF")
	})
	p, ok := ev.Payload.(HubConnectivityChangedPayload)
	if !ok {
		t.Fatalf("payload type %T, want HubConnectivityChangedPayload", ev.Payload)
	}
	if p.Central != "adopted" || p.InterfaceID != "HmIP-RF" || !p.Reachable {
		t.Errorf("payload = %+v, want central=adopted interface=HmIP-RF reachable=true", p)
	}
}

// TestHubEventsSubscriberUnregisterDetaches verifies that leaving the registry
// really detaches: the live-remove path relies on it, and a subscription that
// outlives its central would keep broadcasting for a central no client can
// resolve any more.
func TestHubEventsSubscriberUnregisterDetaches(t *testing.T) {
	t.Parallel()

	h := NewHub()
	reg, _ := hubEventsRegistry(t)

	sub := NewHubEventsSubscriber(reg, h)
	sub.Start()
	t.Cleanup(sub.Stop)

	adopted := registerHubEventsCentral(t, reg, "adopted")

	adopted.HubModel.Messages.Replace([]hub.AlarmMessage{{ID: "1", Name: "Alarm A"}})
	if got := hubEventsOnTopic(h, AlarmMessagesTopic("adopted")); len(got) != 1 {
		t.Fatalf("broadcasts while wired = %d, want 1", len(got))
	}

	if !reg.Unregister("adopted") {
		t.Fatal("Unregister reported the central was not present")
	}

	adopted.HubModel.Messages.Replace([]hub.AlarmMessage{
		{ID: "1", Name: "Alarm A"},
		{ID: "2", Name: "Alarm B"},
	})
	events.Publish(adopted.EventBus, hmevent.ConnectivityChangedEvent{
		CentralName: "adopted",
		InterfaceID: "HmIP-RF",
		Reachable:   false,
	})

	if got := hubEventsOnTopic(h, AlarmMessagesTopic("adopted")); len(got) != 1 {
		t.Errorf("hub-model broadcasts after Unregister = %d, want 1 (no further broadcast)", len(got))
	}
	if got := hubEventsOnTopic(h, ConnectivityTopic("adopted", "HmIP-RF")); len(got) != 0 {
		t.Errorf("event-bus broadcasts after Unregister = %d, want 0", len(got))
	}
}

// TestHubEventsSubscriberStartCentralNilUnitIsNoop pins nil-safety: the
// registry runs the observer for whatever it is handed, so a nil unit must
// yield a nil unwire instead of panicking.
func TestHubEventsSubscriberStartCentralNilUnitIsNoop(t *testing.T) {
	t.Parallel()

	h := NewHub()
	reg, _ := hubEventsRegistry(t)
	sub := NewHubEventsSubscriber(reg, h)

	if unwire := sub.StartCentral(nil); unwire != nil {
		t.Error("StartCentral(nil) returned a non-nil unwire, want nil")
	}

	// A subscriber without a registry or hub has nothing to attach to.
	bare := NewHubEventsSubscriber(nil, nil)
	if unwire := bare.StartCentral(registerHubEventsCentral(t, reg, "bare")); unwire != nil {
		t.Error("StartCentral on a subscriber without registry/hub returned a non-nil unwire, want nil")
	}
}

// TestHubEventsSubscriberStartCentralWithoutEventBusWiresHubModel pins the
// deliberate ordering inside StartCentral: the hub-model hooks are attached
// BEFORE the event-bus guard, so a central whose bus is unavailable still
// pushes its singletons. Wiring the bus first and bailing out early would
// silently drop them.
func TestHubEventsSubscriberStartCentralWithoutEventBusWiresHubModel(t *testing.T) {
	t.Parallel()

	h := NewHub()
	reg, _ := hubEventsRegistry(t)

	sub := NewHubEventsSubscriber(reg, h)
	sub.Start()
	t.Cleanup(sub.Stop)

	busless, err := central.New(central.Config{Name: "busless"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	busless.EventBus = nil

	unwire := sub.StartCentral(busless)
	if unwire == nil {
		t.Fatal("StartCentral returned a nil unwire for a central without an event bus; " +
			"the hub-model subscriptions must still be attached")
	}
	t.Cleanup(unwire)

	busless.HubModel.Inbox.Replace([]hub.InboxDevice{
		{Address: "ADDR1", Name: "New device"},
	})

	ev := pollHub(t, h, func(topic string) bool {
		return topic == InboxTopic("busless")
	})
	p, ok := ev.Payload.(HubCountChangedPayload)
	if !ok {
		t.Fatalf("payload type %T, want HubCountChangedPayload", ev.Payload)
	}
	if p.Central != "busless" || p.Count != 1 {
		t.Errorf("payload = %+v, want central=busless count=1", p)
	}
}

// TestHubEventsSubscriberStopAfterUnregisterIsSafe covers the teardown
// interleaving the adopt path creates: a central may already have left the
// registry when the process-wide Stop runs. Stop must neither panic nor
// detach a foreign consumer of the same hub model.
func TestHubEventsSubscriberStopAfterUnregisterIsSafe(t *testing.T) {
	t.Parallel()

	h := NewHub()
	reg, boot := hubEventsRegistry(t)

	sub := NewHubEventsSubscriber(reg, h)
	sub.Start()

	adopted := registerHubEventsCentral(t, reg, "adopted")

	// A consumer registered after the subscriber's own hooks: an unwire that
	// dropped the wrong slot would silently take this one down with it.
	foreign := 0
	stopForeign := adopted.HubModel.Inbox.OnUpdate(func([]hub.InboxDevice) { foreign++ })
	t.Cleanup(stopForeign)

	reg.Unregister("adopted")
	sub.Stop()
	sub.Stop() // idempotent: remove and stop may run in either order

	adopted.HubModel.Inbox.Replace([]hub.InboxDevice{{Address: "ADDR1", Name: "New device"}})
	boot.HubModel.Inbox.Replace([]hub.InboxDevice{{Address: "ADDR2", Name: "Another device"}})

	if foreign != 1 {
		t.Errorf("foreign hub-model consumer fired %d times, want 1 (Stop must not detach it)", foreign)
	}
	if got := hubEventsOnTopic(h, InboxTopic("adopted")); len(got) != 0 {
		t.Errorf("broadcasts for the unregistered central = %d, want 0", len(got))
	}
	if got := hubEventsOnTopic(h, InboxTopic("home")); len(got) != 0 {
		t.Errorf("broadcasts for the boot central after Stop = %d, want 0", len(got))
	}
}
