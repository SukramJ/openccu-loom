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

// delivered is one webhook POST, reduced to the two fields the assertions
// key on.
type delivered struct {
	central string
	event   string
}

// recordingTransport records the webhook deliveries with the central *and*
// the event each one carried.
//
// The event matters: adopting a central produces `system.status_changed`
// deliveries of its own (the fixtures are deliberately unreachable CCUs), so
// a count keyed on the central alone mixes them in with whatever the test
// published. That is what made this suite flaky — see
// [TestAdoptCentralWiresTheOutboundWebhook].
type recordingTransport struct {
	mu   sync.Mutex
	seen []delivered
}

// deliveryLatency is charged to every delivery so the assertions below run
// against a delivery path that is genuinely slower than the publishing that
// feeds it.
//
// Without it this suite could only fail by luck. Delivery is fast on a
// developer machine, so a mis-measured assertion — reading a count while
// more deliveries for the same central are still queued — sees a settled
// state and passes, then fails on a loaded CI runner where it does not.
// That is how this test failed: it counted every delivery naming the
// central, adopting one emits `system.status_changed` deliveries of its
// own, and the two arriving late read as an incident leaking past removal.
//
// Sized above the 100ms sleep the assertions used to rely on, so
// reintroducing that pattern fails here rather than in CI.
const deliveryLatency = 50 * time.Millisecond

func (rt *recordingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	time.Sleep(deliveryLatency)
	body, _ := io.ReadAll(req.Body)
	var env struct {
		Central string `json:"central"`
		Event   string `json:"event"`
	}
	_ = json.Unmarshal(body, &env)
	rt.mu.Lock()
	rt.seen = append(rt.seen, delivered{central: env.Central, event: env.Event})
	rt.mu.Unlock()
	return &http.Response{StatusCode: http.StatusOK, Body: http.NoBody, Header: make(http.Header)}, nil
}

// deliveriesFor counts every delivery that named central, whatever it
// carried. Used where the assertion really is "nothing at all left the
// daemon for this CCU".
func (rt *recordingTransport) deliveriesFor(name string) int {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	n := 0
	for _, d := range rt.seen {
		if d.central == name {
			n++
		}
	}
	return n
}

// incidentsFor counts the incident deliveries that named central — the
// event [publishIncidentOn] produces, and the only one a test controls the
// number of.
func (rt *recordingTransport) incidentsFor(name string) int {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	n := 0
	for _, d := range rt.seen {
		if d.central == name && d.event == webhookIncidentEvent {
			n++
		}
	}
	return n
}

// webhookIncidentEvent is the `event` field an IncidentRecordedEvent is
// delivered under (internal/north/webhook/outbound.go).
const webhookIncidentEvent = "incident.recorded"

// awaitIncidents waits until at least n incident deliveries named central,
// or fails.
func awaitIncidents(t *testing.T, rt *recordingTransport, name string, n int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if rt.incidentsFor(name) >= n {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("webhook incident deliveries for %q = %d, want at least %d", name, rt.incidentsFor(name), n)
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

	if err := orch.adoptCentral(ctx, unreachableTestCentralConfig("hooked")); err != nil {
		t.Fatalf("adoptCentral(hooked): %v", err)
	}
	hooked, ok := reg.Get("hooked")
	if !ok {
		t.Fatal("adopted central 'hooked' not present in the registry")
	}
	// A second central that stays registered for the removal check below.
	if err := orch.adoptCentral(ctx, unreachableTestCentralConfig("witness")); err != nil {
		t.Fatalf("adoptCentral(witness): %v", err)
	}
	witness, ok := reg.Get("witness")
	if !ok {
		t.Fatal("adopted central 'witness' not present in the registry")
	}
	publishIncidentOn(hooked)
	awaitIncidents(t, rt, "hooked", 1)

	// Removal must detach again: a central torn down at runtime keeps its bus
	// alive long enough for an in-flight event to land, and after a re-adopt
	// under the same name it would deliver twice.
	if err := orch.removeCentral(ctx, "hooked"); err != nil {
		t.Fatalf("removeCentral(hooked): %v", err)
	}
	before := rt.incidentsFor("hooked")
	publishIncidentOn(hooked)

	// The witness is the bound on "nothing arrived", and it is a real bound
	// rather than a duration: events.Publish dispatches in the caller's
	// frame, so both incidents are in the delivery queue by the time this
	// line returns, and the queue is drained by a single worker in order. A
	// leaked "hooked" delivery is therefore already recorded once the
	// witness's own delivery shows up. A sleep here proved nothing on a
	// loaded runner — which is exactly how this test failed in CI while
	// passing on every developer machine.
	publishIncidentOn(witness)
	awaitIncidents(t, rt, "witness", 1)
	if after := rt.incidentsFor("hooked"); after != before {
		t.Errorf("incident deliveries after removeCentral = %d, want %d (the subscription leaked past removal)", after, before)
	}

	// Negative control: with the bridge stopped, a central adopted afterwards
	// delivers nothing — the state every runtime adopt used to be in.
	_ = outbound.Stop(context.Background())
	if err := orch.adoptCentral(ctx, unreachableTestCentralConfig("unhooked")); err != nil {
		t.Fatalf("adoptCentral(unhooked): %v", err)
	}
	unhooked, ok := reg.Get("unhooked")
	if !ok {
		t.Fatal("adopted central 'unhooked' not present in the registry")
	}
	// No witness is possible here — the bridge is stopped, so nothing can
	// deliver at all. Stop() unsubscribed every handler, closed the delivery
	// queue and waited for the worker before returning, and Publish runs its
	// handlers synchronously, so by the time this call returns there is
	// nothing left in flight to wait for.
	publishIncidentOn(unhooked)
	if got := rt.deliveriesFor("unhooked"); got != 0 {
		t.Fatalf("deliveries for a central adopted after the bridge stopped = %d, want 0", got)
	}

	if err := orch.removeCentral(ctx, "unhooked"); err != nil {
		t.Fatalf("removeCentral(unhooked): %v", err)
	}
	if err := orch.removeCentral(ctx, "witness"); err != nil {
		t.Fatalf("removeCentral(witness): %v", err)
	}
}
