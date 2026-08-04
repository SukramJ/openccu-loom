// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package alarm

import (
	"log/slog"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/central/events"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
)

// sinkFanoutCases lists every event type that reaches Service.publish —
// the one sink the engine, the journal, the schedule runner and the
// panel registry all hand their events to.
//
// Membership is decided by the producers, not by the switch: an event
// belongs here as soon as anything calls publish with it. Adding a
// producer without adding a case here is exactly the defect this table
// exists to catch.
var sinkFanoutCases = []hmevent.Event{
	hmevent.AlarmStateChangedEvent{ZoneID: "z"},
	hmevent.AlarmTriggeredEvent{ZoneID: "z"},
	hmevent.AlarmReadinessChangedEvent{ZoneID: "z"},
	hmevent.AlarmJournalAppendedEvent{ZoneID: "z"},
	hmevent.AlarmCountdownEvent{ZoneID: "z"},
	hmevent.AlarmWalkTestEvent{ZoneID: "z"},
	hmevent.AlarmHealthChangedEvent{},
	hmevent.AlarmPanelChangedEvent{},
	hmevent.AlarmDuressEvent{ZoneID: "z", Verb: string(hmenum.AlarmModeDisarmed)},
	hmevent.AlarmReminderEvent{ZoneID: "z"},
}

// TestAlarmSinkFansOutEveryEventType drives every event type through the
// real Service.publish and asserts each one reaches the bus.
//
// It exists because two of them did not. AlarmDuressEvent and
// AlarmReminderEvent had no case in the type switch and fell into the
// default branch, which logged and returned. Every test around them
// stayed green: the engine tests assert the engine calls its sink, and
// the consumer tests build their own bus and publish onto it directly.
// Both halves worked; the seam between them dropped the event.
//
// A duress code entered under coercion therefore produced one hidden
// journal row and nothing else — no notification, no MQTT event, no
// webhook — regardless of alarm.duress_visibility, because the policy
// is applied downstream of this switch.
func TestAlarmSinkFansOutEveryEventType(t *testing.T) {
	t.Parallel()
	for _, ev := range sinkFanoutCases {
		t.Run(string(ev.Type()), func(t *testing.T) {
			t.Parallel()
			svc := &Service{bus: events.NewBus(), log: slog.New(slog.DiscardHandler)}

			got := make(chan hmevent.EventType, 1)
			// Subscribe by concrete type: a generic subscription would
			// hide a case that publishes the wrong type.
			unsub := subscribeByType(svc.bus, ev, func(tp hmevent.EventType) {
				select {
				case got <- tp:
				default:
				}
			})
			defer unsub()

			svc.publish(ev)

			select {
			case tp := <-got:
				if tp != ev.Type() {
					t.Fatalf("bus received %q, want %q", tp, ev.Type())
				}
			default:
				t.Fatalf("%s never reached the bus: Service.publish has no case for it, "+
					"so every consumer of this event is silently dead", ev.Type())
			}
		})
	}
}

// subscribeByType wires a typed subscription for the concrete type of
// sample. The generic Subscribe needs the static type, so the mapping is
// explicit; a new event type without an entry fails loudly rather than
// silently passing the test above.
func subscribeByType(bus *events.Bus, sample hmevent.Event, fn func(hmevent.EventType)) func() {
	switch sample.(type) {
	case hmevent.AlarmStateChangedEvent:
		return events.Subscribe(bus, func(e hmevent.AlarmStateChangedEvent) { fn(e.Type()) })
	case hmevent.AlarmTriggeredEvent:
		return events.Subscribe(bus, func(e hmevent.AlarmTriggeredEvent) { fn(e.Type()) })
	case hmevent.AlarmReadinessChangedEvent:
		return events.Subscribe(bus, func(e hmevent.AlarmReadinessChangedEvent) { fn(e.Type()) })
	case hmevent.AlarmJournalAppendedEvent:
		return events.Subscribe(bus, func(e hmevent.AlarmJournalAppendedEvent) { fn(e.Type()) })
	case hmevent.AlarmCountdownEvent:
		return events.Subscribe(bus, func(e hmevent.AlarmCountdownEvent) { fn(e.Type()) })
	case hmevent.AlarmWalkTestEvent:
		return events.Subscribe(bus, func(e hmevent.AlarmWalkTestEvent) { fn(e.Type()) })
	case hmevent.AlarmHealthChangedEvent:
		return events.Subscribe(bus, func(e hmevent.AlarmHealthChangedEvent) { fn(e.Type()) })
	case hmevent.AlarmPanelChangedEvent:
		return events.Subscribe(bus, func(e hmevent.AlarmPanelChangedEvent) { fn(e.Type()) })
	case hmevent.AlarmDuressEvent:
		return events.Subscribe(bus, func(e hmevent.AlarmDuressEvent) { fn(e.Type()) })
	case hmevent.AlarmReminderEvent:
		return events.Subscribe(bus, func(e hmevent.AlarmReminderEvent) { fn(e.Type()) })
	default:
		panic("subscribeByType: no subscription for " + string(sample.Type()) +
			"; add one so the fan-out table keeps covering it")
	}
}
