// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/central/events"
	"github.com/SukramJ/openccu-loom/internal/config"
	"github.com/SukramJ/openccu-loom/internal/north/webhook"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
)

// recordingTransport counts the webhook deliveries and remembers the central
// each one named, so the assertion is about what left the daemon rather than
// about which method was called.
type recordingTransport struct {
	mu       sync.Mutex
	centrals []string
}

func (rt *recordingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	body, _ := io.ReadAll(req.Body)
	var env struct {
		Central string `json:"central"`
	}
	_ = json.Unmarshal(body, &env)
	rt.mu.Lock()
	rt.centrals = append(rt.centrals, env.Central)
	rt.mu.Unlock()
	return &http.Response{StatusCode: http.StatusOK, Body: http.NoBody, Header: make(http.Header)}, nil
}

// deliveriesFor counts the deliveries that named central.
func (rt *recordingTransport) deliveriesFor(name string) int {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	n := 0
	for _, c := range rt.centrals {
		if c == name {
			n++
		}
	}
	return n
}

// awaitDeliveries waits until at least n deliveries named central, or fails.
func awaitDeliveries(t *testing.T, rt *recordingTransport, name string, n int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if rt.deliveriesFor(name) >= n {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("webhook deliveries for %q = %d, want at least %d", name, rt.deliveriesFor(name), n)
}

// publishIncidentOn fires one incident on the central's own bus, the way the
// health tracker does.
func publishIncidentOn(u *central.Unit) {
	events.Publish(u.EventBus, hmevent.IncidentRecordedEvent{
		Base:        hmevent.NewBase(),
		CentralName: u.Name(),
		Message:     "test probe",
	})
}

// TestAdoptCentralWiresTheOutboundWebhook is the end-to-end proof through the
// production adopt path: a central adopted at runtime — the same call the REST
// centrals admin API drives — must reach the operator's webhook endpoint.
//
// The bridge subscribes its centrals when the north-bound registry starts it
// and never consults the registry again, so a CCU adopted afterwards delivered
// nothing at all: no datapoint, no status change, no incident. The endpoint
// keeps receiving the boot-time CCUs' traffic throughout, so the gap reads as
// a quiet CCU rather than as a broken bridge.
//
// The first half pins that pre-hook silence so the assertion cannot go vacuous.
func TestAdoptCentralWiresTheOutboundWebhook(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	reg := central.NewRegistry()
	orch := buildLiveTestOrchestrator(ctx, t, reg, &config.Config{})

	rt := &recordingTransport{}
	outbound := webhook.NewOutbound(
		reg,
		config.NorthWebhook{Enabled: true, URL: "http://hook.test"},
		discardTestLogger(),
		webhook.WithHTTPClient(&http.Client{Transport: rt}),
		webhook.WithBackoff([]time.Duration{time.Millisecond}),
	)
	if err := outbound.Start(ctx); err != nil {
		t.Fatalf("webhook Start: %v", err)
	}
	t.Cleanup(func() { _ = outbound.Stop(context.Background()) })

	// No hook installed yet — this is what a runtime-adopted central used to
	// get: registered, live, and undelivered.
	if err := orch.adoptCentral(ctx, unreachableTestCentralConfig("unhooked")); err != nil {
		t.Fatalf("adoptCentral(unhooked): %v", err)
	}
	unhooked, ok := reg.Get("unhooked")
	if !ok {
		t.Fatal("adopted central 'unhooked' not present in the registry")
	}
	publishIncidentOn(unhooked)
	time.Sleep(100 * time.Millisecond)
	if got := rt.deliveriesFor("unhooked"); got != 0 {
		t.Fatalf("deliveries for the central adopted without the hook = %d, want 0", got)
	}

	orch.addCentralHook(func(u *central.Unit) func() { return outbound.AttachCentral(u) })

	if err := orch.adoptCentral(ctx, unreachableTestCentralConfig("hooked")); err != nil {
		t.Fatalf("adoptCentral(hooked): %v", err)
	}
	hooked, ok := reg.Get("hooked")
	if !ok {
		t.Fatal("adopted central 'hooked' not present in the registry")
	}
	publishIncidentOn(hooked)
	awaitDeliveries(t, rt, "hooked", 1)

	// Removal must detach again: a central torn down at runtime keeps its bus
	// alive long enough for an in-flight event to land, and after a re-adopt
	// under the same name it would deliver twice.
	if err := orch.removeCentral(ctx, "hooked"); err != nil {
		t.Fatalf("removeCentral(hooked): %v", err)
	}
	before := rt.deliveriesFor("hooked")
	publishIncidentOn(hooked)
	time.Sleep(100 * time.Millisecond)
	if after := rt.deliveriesFor("hooked"); after != before {
		t.Errorf("deliveries after removeCentral = %d, want %d (the subscription leaked past removal)", after, before)
	}

	if err := orch.removeCentral(ctx, "unhooked"); err != nil {
		t.Fatalf("removeCentral(unhooked): %v", err)
	}
}
