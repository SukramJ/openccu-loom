// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package subscription_test

import (
	"context"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/north/matter/im"
	"github.com/SukramJ/openccu-loom/internal/north/matter/im/subscription"
)

// ---- helpers ----

// mkEventPath builds a ConcreteEventPath.  Pass 0 for HasEndpoint=false,
// HasCluster=false or HasEvent=false by omitting the dimension from the
// Has* flags — use the named args pattern below for clarity.
func mkEventPath(endpoint uint16, cluster, event uint32) im.ConcreteEventPath {
	return im.ConcreteEventPath{
		Endpoint:    endpoint,
		Cluster:     cluster,
		Event:       event,
		HasEndpoint: true,
		HasCluster:  true,
		HasEvent:    true,
	}
}

// mkEventFiring builds an EventFiring with the given priority.
func mkEventFiring(endpoint uint16, cluster, event uint32, priority im.EventPriority) subscription.EventFiring {
	return subscription.EventFiring{
		Path:     mkEventPath(endpoint, cluster, event),
		Number:   1,
		Priority: priority,
		Data:     im.AttributeValue{Value: uint8(1)},
	}
}

// eventReporterCall captures one EventReporter invocation.
type eventReporterCall struct {
	sub    *subscription.Subscription
	events []im.EventReport
}

// chanEventReporter returns an EventReporter that sends to ch.
func chanEventReporter(ch chan eventReporterCall) subscription.EventReporter {
	return func(_ context.Context, sub *subscription.Subscription, events []im.EventReport) {
		ch <- eventReporterCall{sub: sub, events: events}
	}
}

// defaultEventArgs builds SubscribeArgs with a single event path covering
// (endpoint=1, cluster=0x003B, event=0x01) and a 1s MinIntervalFloor.
func defaultEventArgs() subscription.SubscribeArgs {
	return subscription.SubscribeArgs{
		FabricIndex:        1,
		PeerNodeID:         0xBEEF,
		SessionID:          10,
		MinIntervalFloor:   1,
		MaxIntervalCeiling: 60,
		AttributePaths:     []im.ConcreteAttributePath{}, // no attribute paths
		EventPaths: []im.ConcreteEventPath{
			mkEventPath(1, 0x003B, 0x01),
		},
	}
}

// ---- Test: matching event path → reporter receives 1 EventReport ----

func TestOnEventFired_MatchingPath_ReporterReceivesOne(t *testing.T) {
	t.Parallel()
	ch := make(chan eventReporterCall, 4)
	m := subscription.NewManager(subscription.Config{}, nil, nil)
	m.SetEventReporter(chanEventReporter(ch))

	sub, err := m.Subscribe(defaultEventArgs())
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}

	// Fire matching event.
	m.OnEventFired(mkEventFiring(1, 0x003B, 0x01, im.EventPriorityCritical))

	ctx := context.Background()
	// Critical priority → bypasses MinIntervalFloor; Tick with t0+0 is enough.
	m.Tick(ctx, time.Now())

	select {
	case call := <-ch:
		if call.sub.ID != sub.ID {
			t.Errorf("wrong sub: %d vs %d", call.sub.ID, sub.ID)
		}
		if len(call.events) != 1 {
			t.Errorf("expected 1 event, got %d", len(call.events))
		}
	default:
		t.Fatal("EventReporter not called after matching OnEventFired + Tick")
	}
}

// ---- Test: non-matching event path → reporter receives 0 ----

func TestOnEventFired_NonMatchingPath_ReporterSilent(t *testing.T) {
	t.Parallel()
	ch := make(chan eventReporterCall, 4)
	m := subscription.NewManager(subscription.Config{}, nil, nil)
	m.SetEventReporter(chanEventReporter(ch))

	// Subscribe for (endpoint=1, cluster=0x003B, event=0x01).
	_, err := m.Subscribe(defaultEventArgs())
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}

	// Fire a different cluster — no match.
	m.OnEventFired(mkEventFiring(1, 0x0006, 0x01, im.EventPriorityCritical))

	ctx := context.Background()
	m.Tick(ctx, time.Now())

	if len(ch) != 0 {
		t.Fatalf("EventReporter called %d time(s), want 0 for non-matching path", len(ch))
	}
}

// ---- Test: two subscriptions with overlapping wildcards → both receive ----

func TestOnEventFired_TwoSubscriptions_BothReceive(t *testing.T) {
	t.Parallel()
	ch := make(chan eventReporterCall, 8)
	m := subscription.NewManager(subscription.Config{}, nil, nil)
	m.SetEventReporter(chanEventReporter(ch))

	// Subscription A: wildcard endpoint, concrete cluster+event.
	argsA := subscription.SubscribeArgs{
		FabricIndex:        1,
		PeerNodeID:         0xA,
		SessionID:          11,
		MinIntervalFloor:   1,
		MaxIntervalCeiling: 60,
		AttributePaths:     []im.ConcreteAttributePath{},
		EventPaths: []im.ConcreteEventPath{
			{Cluster: 0x003B, HasCluster: true, Event: 0x01, HasEvent: true},
		},
	}
	// Subscription B: concrete endpoint+cluster, wildcard event.
	argsB := subscription.SubscribeArgs{
		FabricIndex:        1,
		PeerNodeID:         0xB,
		SessionID:          12,
		MinIntervalFloor:   1,
		MaxIntervalCeiling: 60,
		AttributePaths:     []im.ConcreteAttributePath{},
		EventPaths: []im.ConcreteEventPath{
			{Endpoint: 1, HasEndpoint: true, Cluster: 0x003B, HasCluster: true},
		},
	}

	if _, err := m.Subscribe(argsA); err != nil {
		t.Fatalf("Subscribe A: %v", err)
	}
	if _, err := m.Subscribe(argsB); err != nil {
		t.Fatalf("Subscribe B: %v", err)
	}

	// Fire concrete event (endpoint=1, cluster=0x003B, event=0x01). Both must match.
	m.OnEventFired(mkEventFiring(1, 0x003B, 0x01, im.EventPriorityCritical))

	ctx := context.Background()
	m.Tick(ctx, time.Now())

	// Drain up to 2 calls.
	var calls []eventReporterCall
	for range 2 {
		select {
		case c := <-ch:
			calls = append(calls, c)
		default:
		}
	}
	if len(calls) != 2 {
		t.Errorf("expected 2 EventReporter calls (one per sub), got %d", len(calls))
	}
}

// ---- Test: Critical priority bypasses MinIntervalFloor ----

func TestOnEventFired_CriticalPriority_BypassesMinIntervalFloor(t *testing.T) {
	t.Parallel()
	ch := make(chan eventReporterCall, 4)
	// Use a large MinIntervalFloor (10 s) that would normally block.
	cfg := subscription.Config{MinIntervalFloorSeconds: 10}
	m := subscription.NewManager(cfg, nil, nil)
	m.SetEventReporter(chanEventReporter(ch))

	args := subscription.SubscribeArgs{
		FabricIndex:        1,
		PeerNodeID:         0xC,
		SessionID:          20,
		MinIntervalFloor:   10, // large floor
		MaxIntervalCeiling: 60,
		AttributePaths:     []im.ConcreteAttributePath{},
		EventPaths:         []im.ConcreteEventPath{mkEventPath(1, 0x003B, 0x01)},
	}
	if _, err := m.Subscribe(args); err != nil {
		t.Fatalf("Subscribe: %v", err)
	}

	ctx := context.Background()
	t0 := time.Now()
	// Establish lastReport via a Tick (keepalive because MaxInterval elapsed).
	m.Tick(ctx, t0)
	for len(ch) > 0 {
		<-ch
	}

	// Fire Critical event just 1s after last report (well within the 10s floor).
	m.OnEventFired(mkEventFiring(1, 0x003B, 0x01, im.EventPriorityCritical))
	m.Tick(ctx, t0.Add(1*time.Second))

	if len(ch) == 0 {
		t.Fatal("EventReporter not called for Critical event within MinIntervalFloor window")
	}
}

// ---- Test: non-Critical events wait for MinIntervalFloor ----

func TestOnEventFired_InfoPriority_WaitsForMinIntervalFloor(t *testing.T) {
	t.Parallel()
	ch := make(chan eventReporterCall, 4)
	cfg := subscription.Config{MinIntervalFloorSeconds: 5}
	m := subscription.NewManager(cfg, nil, nil)
	m.SetEventReporter(chanEventReporter(ch))

	args := subscription.SubscribeArgs{
		FabricIndex:        1,
		PeerNodeID:         0xD,
		SessionID:          30,
		MinIntervalFloor:   5,
		MaxIntervalCeiling: 60,
		AttributePaths:     []im.ConcreteAttributePath{},
		EventPaths:         []im.ConcreteEventPath{mkEventPath(1, 0x003B, 0x01)},
	}
	if _, err := m.Subscribe(args); err != nil {
		t.Fatalf("Subscribe: %v", err)
	}

	ctx := context.Background()
	t0 := time.Now()
	// Establish lastReport.
	m.Tick(ctx, t0)
	for len(ch) > 0 {
		<-ch
	}

	// Fire Info-priority event.
	m.OnEventFired(mkEventFiring(1, 0x003B, 0x01, im.EventPriorityInfo))

	// Tick only 2s later — MinIntervalFloor (5s) not yet elapsed.
	m.Tick(ctx, t0.Add(2*time.Second))
	if len(ch) != 0 {
		t.Fatal("EventReporter fired before MinIntervalFloor elapsed for non-Critical event")
	}

	// Tick after 6s — floor elapsed now.
	m.Tick(ctx, t0.Add(6*time.Second))
	if len(ch) == 0 {
		t.Fatal("EventReporter not called after MinIntervalFloor elapsed for Info event")
	}
}

// ---- Test: OnEventFired with non-concrete path (HasEndpoint=false) is a no-op ----

func TestOnEventFired_NonConcretePath_IsNoOp(t *testing.T) {
	t.Parallel()
	ch := make(chan eventReporterCall, 4)
	m := subscription.NewManager(subscription.Config{}, nil, nil)
	m.SetEventReporter(chanEventReporter(ch))

	_, err := m.Subscribe(defaultEventArgs())
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}

	// Path without HasEndpoint=true — must be rejected by OnEventFired.
	nonConcrete := subscription.EventFiring{
		Path: im.ConcreteEventPath{
			// HasEndpoint deliberately false
			Cluster: 0x003B, HasCluster: true,
			Event: 0x01, HasEvent: true,
		},
		Priority: im.EventPriorityCritical,
	}
	m.OnEventFired(nonConcrete)

	ctx := context.Background()
	m.Tick(ctx, time.Now())

	if len(ch) != 0 {
		t.Fatalf("EventReporter called %d time(s) for non-concrete path, want 0", len(ch))
	}
}
