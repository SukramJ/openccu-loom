// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package ws

import (
	"strings"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/central/events"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
)

// newSecuritySubscriberFixture wires a fresh Hub and a started
// SecuritySubscriber over a fresh bus. Like the alarm subscriber it
// fans one shared daemon-level bus onto the hub, so there is no
// per-central registry to build.
func newSecuritySubscriberFixture(t *testing.T) (*Hub, *events.Bus) {
	t.Helper()
	h := NewHub()
	bus := events.NewBus()
	sub := NewSecuritySubscriber(bus, h)
	sub.Start()
	t.Cleanup(sub.Stop)
	return h, bus
}

// securityTopicFilter matches every hub event on one of the three
// security topics.
func securityTopicFilter(topic string) bool {
	return strings.HasPrefix(topic, "security.")
}

// securitySourceRef is a fully populated source reference — every
// broadcast that carries sources must carry all of it through.
func securitySourceRef() hmevent.SecuritySourceRef {
	return hmevent.SecuritySourceRef{
		Ref:            "home|home:HmIP-RF|ABC123:1|SMOKE_DETECTOR_ALARM_STATUS",
		Central:        "home",
		InterfaceID:    "home:HmIP-RF",
		ChannelAddress: "ABC123:1",
		DeviceAddress:  "ABC123",
		Parameter:      "SMOKE_DETECTOR_ALARM_STATUS",
		SensorID:       "sensor-1",
		Name:           "Rauchmelder Flur",
		SensorType:     hmenum.AlarmSensorTypeHazard,
		Class:          hmenum.SecurityClassSmoke,
		AtMS:           1_754_000_000_000,
	}
}

// securityFanoutCases lists one event per security event type the
// domain defines, paired with the broadcast it must produce and the
// topic it must ride.
//
// Membership is decided by pkg/hmevent's EventTypeSecurity* constants,
// not by the subscriber's Start method: an event type without an entry
// here is an event type the WebSocket plane silently drops, which is
// the state this whole file exists to prevent. The domain published all
// five to MQTT, the webhook and the metrics collector while no
// WebSocket consumer received any of them.
var securityFanoutCases = []struct {
	eventType hmevent.EventType
	publish   func(bus *events.Bus)
	wantType  string
	wantTopic string
}{
	{
		eventType: hmevent.EventTypeSecurityStateChanged,
		publish: func(bus *events.Bus) {
			events.Publish(bus, hmevent.SecurityStateChangedEvent{
				Base: hmevent.NewBase(),
				To:   hmenum.SecuritySeverityAlarm,
			})
		},
		wantType:  broadcastSecurityStateChanged,
		wantTopic: securityStateTopic,
	},
	{
		eventType: hmevent.EventTypeSecurityClassChanged,
		publish: func(bus *events.Bus) {
			events.Publish(bus, hmevent.SecurityClassChangedEvent{
				Base:  hmevent.NewBase(),
				Class: hmenum.SecurityClassSmoke,
			})
		},
		wantType:  broadcastSecurityClassChanged,
		wantTopic: securityStateTopic,
	},
	{
		eventType: hmevent.EventTypeSecurityZoneChanged,
		publish: func(bus *events.Bus) {
			events.Publish(bus, hmevent.SecurityZoneChangedEvent{
				Base:   hmevent.NewBase(),
				ZoneID: "z1",
			})
		},
		wantType:  broadcastSecurityZoneChanged,
		wantTopic: securityStateTopic,
	},
	{
		eventType: hmevent.EventTypeSecurityFaultChanged,
		publish: func(bus *events.Bus) {
			events.Publish(bus, hmevent.SecurityFaultChangedEvent{
				Base:    hmevent.NewBase(),
				FaultID: "f1",
			})
		},
		wantType:  broadcastSecurityFaultChanged,
		wantTopic: securityFaultsTopic,
	},
	{
		eventType: hmevent.EventTypeSecurityNotification,
		publish: func(bus *events.Bus) {
			events.Publish(bus, hmevent.SecurityNotificationEvent{
				Base:       hmevent.NewBase(),
				Class:      hmenum.SecurityClassSmoke,
				Retainable: true,
			})
		},
		wantType:  broadcastSecurityNotification,
		wantTopic: securityNotificationTopic,
	},
}

// TestSecuritySubscriberFansOutEveryEventType drives one event of every
// security type through the real subscriber and asserts each reaches
// the hub under the declared broadcast name and topic.
func TestSecuritySubscriberFansOutEveryEventType(t *testing.T) {
	t.Parallel()

	// Every EventTypeSecurity* constant the domain declares. Kept as an
	// explicit list because Go cannot enumerate constants — a new event
	// type added to pkg/hmevent without a case below fails here.
	declared := []hmevent.EventType{
		hmevent.EventTypeSecurityStateChanged,
		hmevent.EventTypeSecurityClassChanged,
		hmevent.EventTypeSecurityZoneChanged,
		hmevent.EventTypeSecurityFaultChanged,
		hmevent.EventTypeSecurityNotification,
	}
	covered := make(map[hmevent.EventType]bool, len(securityFanoutCases))
	for _, c := range securityFanoutCases {
		covered[c.eventType] = true
	}
	for _, tp := range declared {
		if !covered[tp] {
			t.Errorf("event type %q has no fan-out case — the WebSocket plane would drop it silently", tp)
		}
	}

	for _, tc := range securityFanoutCases {
		t.Run(string(tc.eventType), func(t *testing.T) {
			t.Parallel()
			h, bus := newSecuritySubscriberFixture(t)

			tc.publish(bus)

			ev := pollHub(t, h, securityTopicFilter)
			if ev.Type != tc.wantType {
				t.Fatalf("type = %q, want %q", ev.Type, tc.wantType)
			}
			if ev.Topic != tc.wantTopic {
				t.Fatalf("topic = %q, want %q", ev.Topic, tc.wantTopic)
			}
			// The broadcast name is the domain's own event tag: one
			// vocabulary on both sides, unlike the alarm family.
			if ev.Type != string(tc.eventType) {
				t.Fatalf("broadcast %q diverges from event tag %q", ev.Type, tc.eventType)
			}
		})
	}
}

// TestSecuritySubscriberStateChanged verifies the folded severity
// transition carries through, including the previous value a consumer
// needs to render a direction.
func TestSecuritySubscriberStateChanged(t *testing.T) {
	t.Parallel()
	h, bus := newSecuritySubscriberFixture(t)

	events.Publish(bus, hmevent.SecurityStateChangedEvent{
		Base:          hmevent.NewBase(),
		From:          hmenum.SecuritySeverityOK,
		To:            hmenum.SecuritySeverityAlarm,
		ActiveClasses: []hmenum.SecurityClass{hmenum.SecurityClassSmoke, hmenum.SecurityClassWater},
		OpenFaults:    3,
	})

	ev := pollHub(t, h, securityTopicFilter)
	p, ok := ev.Payload.(SecurityStateChangedPayload)
	if !ok {
		t.Fatalf("payload type %T, want SecurityStateChangedPayload", ev.Payload)
	}
	if p.Severity != "alarm" || p.PreviousSeverity != "ok" {
		t.Fatalf("severity fields = %+v", p)
	}
	if len(p.ActiveClasses) != 2 || p.ActiveClasses[0] != "smoke" || p.ActiveClasses[1] != "water" {
		t.Fatalf("active classes = %v", p.ActiveClasses)
	}
	if p.OpenFaults != 3 {
		t.Fatalf("open faults = %d, want 3", p.OpenFaults)
	}
}

// TestSecuritySubscriberClassChanged verifies the class payload carries
// the full source identity, so a consumer can name the detector that
// fired without a second read.
func TestSecuritySubscriberClassChanged(t *testing.T) {
	t.Parallel()
	h, bus := newSecuritySubscriberFixture(t)

	events.Publish(bus, hmevent.SecurityClassChangedEvent{
		Base:     hmevent.NewBase(),
		Class:    hmenum.SecurityClassSmoke,
		Active:   true,
		Sources:  []hmevent.SecuritySourceRef{securitySourceRef()},
		Centrals: []string{"home"},
		SinceMS:  1_754_000_000_000,
	})

	ev := pollHub(t, h, securityTopicFilter)
	p, ok := ev.Payload.(SecurityClassChangedPayload)
	if !ok {
		t.Fatalf("payload type %T, want SecurityClassChangedPayload", ev.Payload)
	}
	if p.Class != "smoke" || !p.Active {
		t.Fatalf("class fields = %+v", p)
	}
	if len(p.Sources) != 1 {
		t.Fatalf("sources = %v, want one entry", p.Sources)
	}
	src := p.Sources[0]
	if src.Ref != "home|home:HmIP-RF|ABC123:1|SMOKE_DETECTOR_ALARM_STATUS" {
		t.Fatalf("source ref = %q", src.Ref)
	}
	if src.DeviceAddress != "ABC123" || src.Name != "Rauchmelder Flur" || src.Class != "smoke" {
		t.Fatalf("source identity = %+v", src)
	}
	if p.Since == nil || !p.Since.Equal(time.UnixMilli(1_754_000_000_000).UTC()) {
		t.Fatalf("since = %v", p.Since)
	}
	if len(p.Centrals) != 1 || p.Centrals[0] != "home" {
		t.Fatalf("centrals = %v", p.Centrals)
	}
}

// TestSecuritySubscriberClassChangedOmitsUnsetSince pins the missing
// occurrence to an omitted field rather than the 1970 epoch — the same
// rule the REST message DTOs follow, and the reason a consumer can
// render "since" without a sentinel check.
func TestSecuritySubscriberClassChangedOmitsUnsetSince(t *testing.T) {
	t.Parallel()
	h, bus := newSecuritySubscriberFixture(t)

	events.Publish(bus, hmevent.SecurityClassChangedEvent{
		Base:   hmevent.NewBase(),
		Class:  hmenum.SecurityClassWater,
		Active: false,
	})

	ev := pollHub(t, h, securityTopicFilter)
	p, ok := ev.Payload.(SecurityClassChangedPayload)
	if !ok {
		t.Fatalf("payload type %T, want SecurityClassChangedPayload", ev.Payload)
	}
	if p.Since != nil {
		t.Fatalf("since = %v, want nil for an unset occurrence", p.Since)
	}
}

// TestSecuritySubscriberZoneChanged verifies the per-zone view,
// including the by-class grouping that spares a consumer a
// zone-by-class matrix of entities.
func TestSecuritySubscriberZoneChanged(t *testing.T) {
	t.Parallel()
	h, bus := newSecuritySubscriberFixture(t)

	events.Publish(bus, hmevent.SecurityZoneChangedEvent{
		Base:     hmevent.NewBase(),
		ZoneID:   "z1",
		ZoneSlug: "erdgeschoss",
		ZoneName: "Erdgeschoss",
		State:    hmenum.AlarmZoneStateTriggered,
		Mode:     hmenum.AlarmModeFull,
		Sources:  []hmevent.SecuritySourceRef{securitySourceRef()},
		ByClass: map[hmenum.SecurityClass][]string{
			hmenum.SecurityClassSmoke: {"Rauchmelder Flur"},
		},
		IncidentID: 42,
	})

	ev := pollHub(t, h, securityTopicFilter)
	p, ok := ev.Payload.(SecurityZoneChangedPayload)
	if !ok {
		t.Fatalf("payload type %T, want SecurityZoneChangedPayload", ev.Payload)
	}
	if p.ZoneID != "z1" || p.ZoneSlug != "erdgeschoss" || p.ZoneName != "Erdgeschoss" {
		t.Fatalf("zone identity = %+v", p)
	}
	if p.State != "triggered" || p.Mode != "full" || p.IncidentID != 42 {
		t.Fatalf("zone state = %+v", p)
	}
	names := p.ByClass["smoke"]
	if len(names) != 1 || names[0] != "Rauchmelder Flur" {
		t.Fatalf("by_class = %v", p.ByClass)
	}
}

// TestSecuritySubscriberFaultChanged verifies a fault carries its
// direction, its acknowledgement marker and the standing count.
func TestSecuritySubscriberFaultChanged(t *testing.T) {
	t.Parallel()
	h, bus := newSecuritySubscriberFixture(t)

	events.Publish(bus, hmevent.SecurityFaultChangedEvent{
		Base:         hmevent.NewBase(),
		FaultID:      "fault-1",
		Class:        hmenum.SecurityClassBattery,
		Reason:       hmenum.SecurityFaultReasonLowBattery,
		Severity:     hmenum.SecuritySeverityWarning,
		Source:       securitySourceRef(),
		Open:         true,
		Acknowledged: false,
		SinceMS:      1_754_000_000_000,
		OpenCount:    2,
	})

	ev := pollHub(t, h, securityTopicFilter)
	if ev.Topic != securityFaultsTopic {
		t.Fatalf("topic = %q, want %q", ev.Topic, securityFaultsTopic)
	}
	p, ok := ev.Payload.(SecurityFaultChangedPayload)
	if !ok {
		t.Fatalf("payload type %T, want SecurityFaultChangedPayload", ev.Payload)
	}
	if p.FaultID != "fault-1" || p.Class != "battery" || p.Reason != "low_battery" {
		t.Fatalf("fault identity = %+v", p)
	}
	if p.Severity != "warning" || !p.Open || p.Acknowledged {
		t.Fatalf("fault state = %+v", p)
	}
	if p.OpenCount != 2 {
		t.Fatalf("open count = %d, want 2", p.OpenCount)
	}
	if p.Source.Ref == "" || p.Source.Name != "Rauchmelder Flur" {
		t.Fatalf("fault source = %+v", p.Source)
	}
}

// TestSecuritySubscriberNotification verifies a retainable report
// carries its prose plus the i18n key and args a consumer needs to
// re-render in its own locale.
func TestSecuritySubscriberNotification(t *testing.T) {
	t.Parallel()
	h, bus := newSecuritySubscriberFixture(t)

	events.Publish(bus, hmevent.SecurityNotificationEvent{
		Base:       hmevent.NewBase(),
		Class:      hmenum.SecurityClassSmoke,
		Severity:   hmenum.SecuritySeverityAlarm,
		Verb:       hmenum.SecurityVerbTriggered,
		Subject:    "Rauchalarm",
		Message:    "Rauchmelder Flur meldet Rauch.",
		I18nKey:    "security.smoke.triggered",
		Args:       map[string]string{"name": "Rauchmelder Flur"},
		Sources:    []hmevent.SecuritySourceRef{securitySourceRef()},
		ZoneID:     "z1",
		ZoneSlug:   "erdgeschoss",
		ZoneName:   "Erdgeschoss",
		Mode:       hmenum.AlarmModeFull,
		IncidentID: 42,
		Link:       "https://loom.example/security",
		AtMS:       1_754_000_000_000,
		Fault:      false,
		Retainable: true,
	})

	ev := pollHub(t, h, securityTopicFilter)
	if ev.Topic != securityNotificationTopic {
		t.Fatalf("topic = %q, want %q", ev.Topic, securityNotificationTopic)
	}
	p, ok := ev.Payload.(SecurityNotificationPayload)
	if !ok {
		t.Fatalf("payload type %T, want SecurityNotificationPayload", ev.Payload)
	}
	if p.Subject != "Rauchalarm" || p.Message != "Rauchmelder Flur meldet Rauch." {
		t.Fatalf("prose = %+v", p)
	}
	if p.I18nKey != "security.smoke.triggered" || p.Args["name"] != "Rauchmelder Flur" {
		t.Fatalf("i18n fields = %+v", p)
	}
	if p.Class != "smoke" || p.Severity != "alarm" || p.Verb != "triggered" {
		t.Fatalf("classification = %+v", p)
	}
	if p.ZoneSlug != "erdgeschoss" || p.IncidentID != 42 || p.Fault {
		t.Fatalf("zone fields = %+v", p)
	}
	if !p.At.Equal(time.UnixMilli(1_754_000_000_000).UTC()) {
		t.Fatalf("at = %v", p.At)
	}
}

// TestSecuritySubscriberDropsNonRetainableNotification pins the covert
// report to silence on this plane.
//
// The WebSocket feeds the SPA and every dashboard a browser has open,
// which is exactly the exposure a silent panic or a duress code exists
// to avoid: hmenum.DuressVisibility names the WebSocket under the
// `full` level alone, and the domain has already folded that policy
// into Retainable. Broadcasting regardless would put "duress code
// entered" on the hallway tablet while the attacker reads it.
func TestSecuritySubscriberDropsNonRetainableNotification(t *testing.T) {
	t.Parallel()
	h, bus := newSecuritySubscriberFixture(t)

	events.Publish(bus, hmevent.SecurityNotificationEvent{
		Base:       hmevent.NewBase(),
		Class:      hmenum.SecurityClassPanic,
		Severity:   hmenum.SecuritySeverityCritical,
		Verb:       hmenum.SecurityVerbTriggered,
		Subject:    "Panik",
		Retainable: false,
	})
	// A retainable report published afterwards proves the plane is
	// alive: without it a silently broken subscriber would pass this
	// test for the wrong reason.
	events.Publish(bus, hmevent.SecurityNotificationEvent{
		Base:       hmevent.NewBase(),
		Class:      hmenum.SecurityClassSmoke,
		Severity:   hmenum.SecuritySeverityAlarm,
		Verb:       hmenum.SecurityVerbTriggered,
		Subject:    "Rauchalarm",
		Retainable: true,
	})

	ev := pollHub(t, h, securityTopicFilter)
	p, ok := ev.Payload.(SecurityNotificationPayload)
	if !ok {
		t.Fatalf("payload type %T, want SecurityNotificationPayload", ev.Payload)
	}
	if p.Class == "panic" {
		t.Fatal("covert report reached the WebSocket; it must stay off local screen surfaces")
	}
	if p.Subject != "Rauchalarm" {
		t.Fatalf("first broadcast = %+v, want the retainable smoke report", p)
	}
	if res := h.Replay(0, securityTopicFilter); len(res.Events) != 1 {
		t.Fatalf("hub carries %d security events, want exactly the retainable one", len(res.Events))
	}
}

// TestSecuritySubscriberNilBusIsInert pins the disabled-domain path: a
// daemon without the Security & Safety service must not panic on
// start-up, it must simply publish nothing.
func TestSecuritySubscriberNilBusIsInert(t *testing.T) {
	t.Parallel()
	sub := NewSecuritySubscriber(nil, NewHub())
	sub.Start()
	sub.Stop()
}
