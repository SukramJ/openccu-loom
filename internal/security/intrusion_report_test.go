// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package security

import (
	"testing"

	"github.com/SukramJ/openccu-loom/internal/central/events"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// collectNotifications captures every rendered report the domain emits.
func collectNotifications(t *testing.T, svc *Service) *[]hmevent.SecurityNotificationEvent {
	t.Helper()
	var got []hmevent.SecurityNotificationEvent
	unsub := events.Subscribe(svc.Bus(), func(e hmevent.SecurityNotificationEvent) {
		got = append(got, e)
	})
	t.Cleanup(unsub)
	return &got
}

// fireSource drives one classified source active through the real
// data-point path.
func fireSource(t *testing.T, svc *Service, class hmenum.SecurityClass, suffix int) {
	t.Helper()
	svc.mu.Lock()
	key, ref := addAggSource(svc.agg, "home", suffix, class, true)
	_ = key
	svc.mu.Unlock()

	svc.onDataPoint("home", hmevent.DataPointValueChangedEvent{
		Base: hmevent.NewBase(),
		Key: hmtypes.DataPointKey{
			InterfaceID:    ref.InterfaceID,
			ChannelAddress: ref.ChannelAddress,
			Parameter:      ref.Parameter,
		},
		NewValue: hmtypes.BoolValue(true),
	})
}

// TestAnActiveIntrusionSourceProducesNoAlarmReport pins the fix for a
// report that claimed a burglary that had not happened.
//
// The intrusion class is fed by every enrolled door, window and motion
// sensor, which report all day on a disarmed system. The source path
// nevertheless rendered a `triggered` report for each of them — and
// since that path carries neither zone nor mode, the message came out
// with empty placeholders:
//
//	In Zone  wurde um 15:43 Uhr ein Einbruchalarm ausgelöst (Modus ):
//	Bewegungsmelder WZ, Bewegungsmelder HAR, Bewegungsmelder FL.
//
// Whether an active motion sensor means a break-in is the alarm engine's
// verdict, and it reports it itself with the zone and mode filled in.
func TestAnActiveIntrusionSourceProducesNoAlarmReport(t *testing.T) {
	t.Parallel()
	svc, _, _ := newTestService(t)
	reports := collectNotifications(t, svc)

	fireSource(t, svc, hmenum.SecurityClassIntrusion, 1)

	if len(*reports) != 0 {
		t.Fatalf("an active intrusion source produced %d report(s): %+v", len(*reports), *reports)
	}
}

// TestAnActiveIntrusionSourceStillFlipsItsClass pins the other half: the
// detection itself must stay visible. The class entity reports that a
// monitored door, window or motion sensor is active and is named for
// exactly that — removing it would take away the "is anything open?"
// answer the arming flow needs.
func TestAnActiveIntrusionSourceStillFlipsItsClass(t *testing.T) {
	t.Parallel()
	svc, _, _ := newTestService(t)
	var classes []hmevent.SecurityClassChangedEvent
	unsub := events.Subscribe(svc.Bus(), func(e hmevent.SecurityClassChangedEvent) {
		classes = append(classes, e)
	})
	t.Cleanup(unsub)

	fireSource(t, svc, hmenum.SecurityClassIntrusion, 2)

	if len(classes) == 0 {
		t.Fatal("no class change published; the detection became invisible")
	}
	last := classes[len(classes)-1]
	if last.Class != hmenum.SecurityClassIntrusion || !last.Active {
		t.Fatalf("class event = %+v, want an active intrusion class", last)
	}
}

// TestOtherHazardClassesStillReport guards the scope of the exclusion.
// Smoke, water, gas and CO mean the same thing whatever the alarm system
// is doing, and a hold-up trigger must alert precisely when nobody armed
// anything — so all of them keep reporting from the source path.
func TestOtherHazardClassesStillReport(t *testing.T) {
	t.Parallel()

	for i, class := range []hmenum.SecurityClass{
		hmenum.SecurityClassSmoke,
		hmenum.SecurityClassWater,
		hmenum.SecurityClassGas,
		hmenum.SecurityClassCO,
		hmenum.SecurityClassPanic,
	} {
		t.Run(string(class), func(t *testing.T) {
			t.Parallel()
			svc, _, _ := newTestService(t)
			reports := collectNotifications(t, svc)

			fireSource(t, svc, class, 10+i)

			if len(*reports) == 0 {
				t.Fatalf("class %s produced no report from the source path", class)
			}
			if got := (*reports)[0].Class; got != class {
				t.Fatalf("report class = %s, want %s", got, class)
			}
		})
	}
}

// TestTheEngineStillReportsAnIntrusion pins the replacement: excluding
// the source path must not leave a real break-in unreported. The engine's
// own path carries the zone and the mode the message names.
func TestTheEngineStillReportsAnIntrusion(t *testing.T) {
	t.Parallel()
	svc, _, _ := newTestService(t)
	reports := collectNotifications(t, svc)

	svc.onAlarmTriggered(hmevent.AlarmTriggeredEvent{
		Base:       hmevent.NewBase(),
		ZoneID:     "z1",
		ZoneName:   "Erdgeschoss",
		Mode:       hmenum.AlarmModeFull,
		IncidentID: 42,
		Sources: []hmevent.SecuritySourceRef{
			hmevent.NewSecuritySourceRef("home", "HmIP-RF", "ABC123:1", "MOTION"),
		},
	})

	if len(*reports) != 1 {
		t.Fatalf("engine trigger produced %d report(s), want exactly one", len(*reports))
	}
	rep := (*reports)[0]
	if rep.Class != hmenum.SecurityClassIntrusion || rep.Verb != hmenum.SecurityVerbTriggered {
		t.Fatalf("report = %s/%s, want intrusion/triggered", rep.Class, rep.Verb)
	}
	// The placeholders the source path left empty.
	if rep.ZoneName != "Erdgeschoss" || rep.Mode != hmenum.AlarmModeFull {
		t.Fatalf("zone/mode = %q/%q, want them filled", rep.ZoneName, rep.Mode)
	}
}
