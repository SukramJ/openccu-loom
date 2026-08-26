// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package webhook

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/central/events"
	"github.com/SukramJ/openccu-loom/internal/config"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
)

// This file covers the Security & Safety half of the webhook bridge:
// Outbound.SetSecurityBus wires the domain bus so rendered reports and
// fault transitions reach the operator's endpoint. A standalone bus is
// enough — subscribeSecurity only needs *events.Bus, not a live central.

// securityOutboundFixture builds an Outbound with an empty registry (no
// per-central subscriptions) wired to a standalone security bus via
// SetSecurityBus, started against a fake transport.
func securityOutboundFixture(t *testing.T, ft *fakeTransport) (*Outbound, *events.Bus) {
	t.Helper()
	reg := central.NewRegistry()
	bus := events.NewBus()
	cfg := config.NorthWebhook{Enabled: true, URL: "http://hook.test"}
	o := NewOutbound(
		reg, cfg, nil,
		WithHTTPClient(&http.Client{Transport: ft}),
		WithBackoff(instantBackoff()),
		WithClock(fixedClock),
	)
	o.SetSecurityBus(bus)
	if err := o.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = o.Stop(context.Background()) })
	return o, bus
}

// faultSource is the contributing data point shared by the fault cases.
func faultSource() hmevent.SecuritySourceRef {
	return hmevent.SecuritySourceRef{
		Ref:            "ccu1|HmIP-RF|0001ABC:1|SMOKE_DETECTOR_ALARM_STATUS",
		Central:        "ccu1",
		InterfaceID:    "HmIP-RF",
		ChannelAddress: "0001ABC:1",
		DeviceAddress:  "0001ABC",
		Parameter:      "SMOKE_DETECTOR_ALARM_STATUS",
	}
}

// TestOutboundFaultAcknowledgeIsDistinguishableFromRaise pins the three
// fault transitions apart on the wire.
//
// An acknowledgement leaves the condition standing, so the domain
// publishes it with Open still true. A payload that derives its verb
// from Open alone therefore delivers an acknowledgement that is
// byte-identical to the original raise, and a messenger integration
// re-fires "smoke detector fault" because somebody pressed acknowledge.
// The identity has to travel too: without fault_id a consumer cannot
// tell a repeat of one standing fault from a second, independent one.
func TestOutboundFaultAcknowledgeIsDistinguishableFromRaise(t *testing.T) {
	t.Parallel()
	ft := &fakeTransport{}
	_, bus := securityOutboundFixture(t, ft)

	const faultID = "fault-smoke-1"
	src := faultSource()

	events.Publish(bus, hmevent.SecurityFaultChangedEvent{
		Base: hmevent.NewBaseAt(fixedNow), FaultID: faultID,
		Class: hmenum.SecurityClassSmoke, Reason: hmenum.SecurityFaultReasonUnreachable,
		Severity: hmenum.SecuritySeverityCritical, Source: src,
		Open: true, SinceMS: fixedNow.UnixMilli(), OpenCount: 1,
	})
	waitForCount(t, ft, 1, 2*time.Second)

	events.Publish(bus, hmevent.SecurityFaultChangedEvent{
		Base: hmevent.NewBaseAt(fixedNow), FaultID: faultID,
		Class: hmenum.SecurityClassSmoke, Reason: hmenum.SecurityFaultReasonUnreachable,
		Severity: hmenum.SecuritySeverityCritical, Source: src,
		Open: true, Acknowledged: true, SinceMS: fixedNow.UnixMilli(), OpenCount: 1,
	})
	waitForCount(t, ft, 2, 2*time.Second)

	_, raised := alarmEnvelope(t, ft.get(0))
	_, acked := alarmEnvelope(t, ft.get(1))

	if raised["fault_id"] != faultID || acked["fault_id"] != faultID {
		t.Errorf("fault_id = %v / %v, want %q on both", raised["fault_id"], acked["fault_id"], faultID)
	}
	if raised["cause"] != "raised" {
		t.Errorf("raise cause = %v, want %q", raised["cause"], "raised")
	}
	if acked["cause"] != "acknowledged" {
		t.Errorf("acknowledge cause = %v, want %q (an acknowledgement is not a fresh raise)", acked["cause"], "acknowledged")
	}
	if _, present := raised["acknowledged"]; present {
		t.Errorf("raise carries acknowledged = %v, want the flag absent", raised["acknowledged"])
	}
	if acked["acknowledged"] != true {
		t.Errorf("acknowledge acknowledged = %v, want true", acked["acknowledged"])
	}
	if raised["open_count"] != float64(1) {
		t.Errorf("raise open_count = %v, want 1", raised["open_count"])
	}
	if _, present := raised["entry_id"]; present {
		t.Errorf("raise carries entry_id = %v, but entry_id is a journal entry id everywhere else", raised["entry_id"])
	}
}

// TestOutboundFaultClearedCarriesZeroOpenCount pins the clear
// transition: the standing tally drops to zero and that zero has to
// reach the wire, because a consumer that keeps a fault badge lit needs
// "none standing" to be said, not merely implied by an absent field.
func TestOutboundFaultClearedCarriesZeroOpenCount(t *testing.T) {
	t.Parallel()
	ft := &fakeTransport{}
	_, bus := securityOutboundFixture(t, ft)

	events.Publish(bus, hmevent.SecurityFaultChangedEvent{
		Base: hmevent.NewBaseAt(fixedNow), Class: hmenum.SecurityClassSmoke,
		Reason: hmenum.SecurityFaultReasonUnreachable, Source: faultSource(),
		Open: false, OpenCount: 0,
	})
	waitForCount(t, ft, 1, 2*time.Second)

	env, cleared := alarmEnvelope(t, ft.get(0))
	if env.Event != string(hmevent.EventTypeSecurityFaultChanged) {
		t.Errorf("event = %q, want %q", env.Event, hmevent.EventTypeSecurityFaultChanged)
	}
	if cleared["cause"] != "cleared" {
		t.Errorf("cause = %v, want %q", cleared["cause"], "cleared")
	}
	if cleared["open_count"] != float64(0) {
		t.Errorf("open_count = %v, want an explicit 0", cleared["open_count"])
	}
	if cleared["note"] != string(hmenum.SecurityFaultReasonUnreachable) {
		t.Errorf("note = %v, want %q", cleared["note"], hmenum.SecurityFaultReasonUnreachable)
	}
}
