// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package main

import (
	"testing"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/central/events"
	"github.com/SukramJ/openccu-loom/internal/config"
	"github.com/SukramJ/openccu-loom/internal/north/rest/ws"
	"github.com/SukramJ/openccu-loom/internal/security"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
)

// newSecurityServiceForWiring builds the Security & Safety service the
// way the daemon builds it — through wireSecurityService, off a real
// migrated database — so this test cannot pass on a service the
// composition root would never produce.
func newSecurityServiceForWiring(t *testing.T, visibility hmenum.DuressVisibility) (*central.Registry, *security.Service) {
	t.Helper()
	db := openMigratedTestDB(t, "security_ws.db")

	cfg := config.Default()
	cfg.Alarm.DuressVisibility = string(visibility)

	reg := central.NewRegistry()
	svc := wireSecurityService(cfg, reg, db, nil, nil, discardTestLogger())
	if svc == nil {
		t.Fatal("wireSecurityService returned nil — the WebSocket plane would have no bus to ride")
	}
	return reg, svc
}

// TestWireSystemStatusSubscribersBroadcastsSecurityEvents pins the
// Security & Safety plane onto the WebSocket through the real
// composition root.
//
// It asserts the effect — a domain event reaches a WebSocket client —
// rather than that some Start method was called, because the defect it
// guards against is exactly a subscriber that exists, compiles and is
// unit-tested while nothing in a running daemon attaches it. That was
// the state of the whole domain before this wiring: five events reached
// MQTT, the webhook and the metrics collector, and every REST/WebSocket
// consumer had to poll GET /security to learn about a smoke alarm.
func TestWireSystemStatusSubscribersBroadcastsSecurityEvents(t *testing.T) {
	t.Parallel()

	reg, svc := newSecurityServiceForWiring(t, hmenum.DuressVisibilityFull)
	wsHub := ws.NewHub()

	_, _, teardown := wireSystemStatusSubscribers(reg, wsHub, nil, nil, nil, nil, svc, "", "", discardTestLogger())
	t.Cleanup(teardown)

	events.Publish(svc.Bus(), hmevent.SecurityClassChangedEvent{
		Base:    hmevent.NewBase(),
		Class:   hmenum.SecurityClassSmoke,
		Active:  true,
		SinceMS: 1_754_000_000_000,
	})

	ev := waitForWSTopic(t, wsHub, "security.state")
	if ev.Type != "security.class_changed" {
		t.Fatalf("broadcast type = %q, want security.class_changed", ev.Type)
	}
	p, ok := ev.Payload.(ws.SecurityClassChangedPayload)
	if !ok {
		t.Fatalf("payload type %T, want ws.SecurityClassChangedPayload", ev.Payload)
	}
	if p.Class != "smoke" || !p.Active {
		t.Errorf("payload = %+v, want an active smoke class", p)
	}
}

// TestWireSystemStatusSubscribersWithoutSecurityService pins the
// disabled-domain path: a daemon whose persistence tier is missing gets
// a nil security service, and the wiring must stay silent rather than
// panic on start-up.
func TestWireSystemStatusSubscribersWithoutSecurityService(t *testing.T) {
	t.Parallel()

	wsHub := ws.NewHub()
	_, _, teardown := wireSystemStatusSubscribers(
		central.NewRegistry(), wsHub, nil, nil, nil, nil, nil, "", "", discardTestLogger(),
	)
	teardown()

	if got := len(wsEventsOnTopic(wsHub, "security.state")); got != 0 {
		t.Fatalf("hub carries %d security broadcasts without a security service", got)
	}
}
