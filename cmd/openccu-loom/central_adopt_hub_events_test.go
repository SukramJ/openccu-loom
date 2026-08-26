// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package main

import (
	"context"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/config"
	hubmodel "github.com/SukramJ/openccu-loom/internal/model/hub"
	"github.com/SukramJ/openccu-loom/internal/north/rest/ws"
)

// waitForWSTopic waits up to 2 s for a broadcast on topic and returns it.
func waitForWSTopic(t *testing.T, h *ws.Hub, topic string) ws.Event {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if events := wsEventsOnTopic(h, topic); len(events) > 0 {
			return events[0]
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("no WebSocket broadcast on %q within the deadline", topic)
	return ws.Event{}
}

// wsEventsOnTopic returns every buffered broadcast on topic. Hub-model change
// hooks and Hub.Publish both run on the caller's goroutine, so a change made
// before this call is already visible.
func wsEventsOnTopic(h *ws.Hub, topic string) []ws.Event {
	return h.Replay(0, func(got string) bool { return got == topic }).Events
}

// TestWireSystemStatusSubscribersReachesACentralRegisteredLater verifies that
// the north-bound subscribers the composition root stands up attach to a
// central that joins the registry afterwards. They used to walk the registry
// once at wiring time, so a central that appeared later was never subscribed
// and none of its hub singletons reached a client.
func TestWireSystemStatusSubscribersReachesACentralRegisteredLater(t *testing.T) {
	t.Parallel()

	reg := central.NewRegistry()
	wsHub := ws.NewHub()

	_, teardown := wireSystemStatusSubscribers(reg, wsHub, nil, nil, nil, nil, nil, "", "", discardTestLogger())
	t.Cleanup(teardown)

	unit, err := central.New(central.Config{Name: "late-central"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	if err := reg.Register(unit); err != nil {
		t.Fatalf("reg.Register: %v", err)
	}

	unit.HubModel.ServiceMessages.Replace([]hubmodel.ServiceMessage{{ID: "SM1", Name: "Low battery"}})

	ev := waitForWSTopic(t, wsHub, ws.ServiceMessagesTopic("late-central"))
	p, ok := ev.Payload.(ws.HubCountChangedPayload)
	if !ok {
		t.Fatalf("payload type %T, want ws.HubCountChangedPayload", ev.Payload)
	}
	if p.Central != "late-central" || p.Count != 1 {
		t.Errorf("payload = %+v, want central=late-central count=1", p)
	}
}

// TestAdoptCentralWiresHubEventsBroadcasts is the end-to-end proof through
// the production adopt path: a central adopted at runtime (the same call the
// REST centrals admin API drives) must push its hub singletons — service /
// alarm message counts, inbox, connectivity — to WebSocket clients. Before the
// subscriber joined the registry observer it stayed silent forever, which the
// last half of this test reproduces so the assertion cannot go vacuous.
func TestAdoptCentralWiresHubEventsBroadcasts(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	reg := central.NewRegistry()
	orch := buildLiveTestOrchestrator(ctx, t, reg, &config.Config{})

	wsHub := ws.NewHub()
	subscriber := ws.NewHubEventsSubscriber(reg, wsHub)
	subscriber.Start() // the registry is empty at this point
	t.Cleanup(subscriber.Stop)

	if err := orch.adoptCentral(ctx, unreachableTestCentralConfig("hooked")); err != nil {
		t.Fatalf("adoptCentral(hooked): %v", err)
	}
	hooked, ok := reg.Get("hooked")
	if !ok {
		t.Fatal("adopted central 'hooked' not present in the registry")
	}

	hooked.HubModel.ServiceMessages.Replace([]hubmodel.ServiceMessage{
		{ID: "SM1", Name: "Low battery"},
		{ID: "SM2", Name: "Sabotage"},
	})

	ev := waitForWSTopic(t, wsHub, ws.ServiceMessagesTopic("hooked"))
	p, ok := ev.Payload.(ws.HubCountChangedPayload)
	if !ok {
		t.Fatalf("payload type %T, want ws.HubCountChangedPayload", ev.Payload)
	}
	if p.Central != "hooked" {
		t.Errorf("central = %q, want %q", p.Central, "hooked")
	}
	if p.Count != 2 {
		t.Errorf("count = %d, want 2", p.Count)
	}

	if err := orch.removeCentral(ctx, "hooked"); err != nil {
		t.Fatalf("removeCentral(hooked): %v", err)
	}

	// Removal must detach the subscriptions. The hub model outlives the
	// registry entry, so a refresh still in flight can call its change
	// hooks after the central is gone; if the unwire is not invoked those
	// callbacks keep publishing on the removed central's topic - and after
	// a re-adopt under the same name they would land on the live one.
	before := len(wsEventsOnTopic(wsHub, ws.ServiceMessagesTopic("hooked")))
	hooked.HubModel.ServiceMessages.Replace([]hubmodel.ServiceMessage{{ID: "SM3", Name: "After removal"}})
	if after := len(wsEventsOnTopic(wsHub, ws.ServiceMessagesTopic("hooked"))); after != before {
		t.Errorf("broadcasts after removeCentral = %d, want %d (subscriptions leaked past removal)", after, before)
	}

	// Negative control: with the subscriber stopped, a central adopted
	// afterwards is unheard — the state every runtime adopt used to be in.
	subscriber.Stop()
	if err := orch.adoptCentral(ctx, unreachableTestCentralConfig("unhooked")); err != nil {
		t.Fatalf("adoptCentral(unhooked): %v", err)
	}
	unhooked, ok := reg.Get("unhooked")
	if !ok {
		t.Fatal("adopted central 'unhooked' not present in the registry")
	}
	unhooked.HubModel.ServiceMessages.Replace([]hubmodel.ServiceMessage{{ID: "SM1", Name: "Low battery"}})
	if got := wsEventsOnTopic(wsHub, ws.ServiceMessagesTopic("unhooked")); len(got) != 0 {
		t.Fatalf("broadcasts after the subscriber stopped = %d, want 0", len(got))
	}

	if err := orch.removeCentral(ctx, "unhooked"); err != nil {
		t.Fatalf("removeCentral(unhooked): %v", err)
	}
}
