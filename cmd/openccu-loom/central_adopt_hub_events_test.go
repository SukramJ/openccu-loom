// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

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

// TestWireSystemStatusSubscribersCentralHookAttachesCentral verifies the
// composition root hands back a usable per-central hook: every north-bound
// subscriber it stands up walks the registry once at wiring time, so without
// this hook a central that appears later is never subscribed and none of its
// hub singletons reach a client.
func TestWireSystemStatusSubscribersCentralHookAttachesCentral(t *testing.T) {
	t.Parallel()

	reg := central.NewRegistry()
	wsHub := ws.NewHub()

	_, centralHook, teardown := wireSystemStatusSubscribers(reg, wsHub, nil, nil, nil, nil, nil, "", "", discardTestLogger())
	t.Cleanup(teardown)

	if centralHook == nil {
		t.Fatal("wireSystemStatusSubscribers returned a nil per-central hook")
	}

	unit, err := central.New(central.Config{Name: "late-central"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	if err := reg.Register(unit); err != nil {
		t.Fatalf("reg.Register: %v", err)
	}

	unwire := centralHook(unit)
	if unwire == nil {
		t.Fatal("per-central hook returned a nil unwire for a central with a hub model")
	}
	t.Cleanup(unwire)

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
// alarm message counts, inbox, connectivity — to WebSocket clients. Without
// the hook installed on the orchestrator it stays silent forever, which the
// first half of this test pins so the assertion cannot go vacuous.
func TestAdoptCentralWiresHubEventsBroadcasts(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	reg := central.NewRegistry()
	orch := buildLiveTestOrchestrator(ctx, t, reg, &config.Config{})

	wsHub := ws.NewHub()
	subscriber := ws.NewHubEventsSubscriber(reg, wsHub)
	subscriber.Start() // boot-time walk: the registry is empty at this point
	t.Cleanup(subscriber.Stop)

	// No hook installed yet — this is what a runtime-adopted central used to
	// get: registered, live, and unheard.
	if err := orch.adoptCentral(ctx, unreachableTestCentralConfig("unhooked")); err != nil {
		t.Fatalf("adoptCentral(unhooked): %v", err)
	}
	unhooked, ok := reg.Get("unhooked")
	if !ok {
		t.Fatal("adopted central 'unhooked' not present in the registry")
	}
	unhooked.HubModel.ServiceMessages.Replace([]hubmodel.ServiceMessage{{ID: "SM1", Name: "Low battery"}})
	if got := wsEventsOnTopic(wsHub, ws.ServiceMessagesTopic("unhooked")); len(got) != 0 {
		t.Fatalf("broadcasts without the hub-events hook = %d, want 0", len(got))
	}

	orch.addCentralHook(func(u *central.Unit) func() { return subscriber.StartCentral(u) })

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

	// The unhooked central must still be silent: the hook attaches exactly
	// the central it is called with, not every registry member.
	if got := wsEventsOnTopic(wsHub, ws.ServiceMessagesTopic("unhooked")); len(got) != 0 {
		t.Errorf("broadcasts for the central adopted before the hook = %d, want 0", len(got))
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

	if err := orch.removeCentral(ctx, "unhooked"); err != nil {
		t.Fatalf("removeCentral(unhooked): %v", err)
	}
}

// TestAddCentralHookNilSafe pins the nil-tolerant registrar contract the
// composition root relies on: a nil orchestrator (southbound never came up)
// and a nil hook must both be no-ops rather than panics.
func TestAddCentralHookNilSafe(t *testing.T) {
	t.Parallel()

	var nilOrch *centralOrchestrator
	nilOrch.addCentralHook(func(*central.Unit) func() { return nil })

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	orch := buildLiveTestOrchestrator(ctx, t, central.NewRegistry(), &config.Config{})
	orch.addCentralHook(nil)

	if err := orch.adoptCentral(ctx, unreachableTestCentralConfig("no-hook")); err != nil {
		t.Fatalf("adoptCentral with a nil per-central hook: %v", err)
	}
	if err := orch.removeCentral(ctx, "no-hook"); err != nil {
		t.Fatalf("removeCentral: %v", err)
	}
}
