// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package security

import (
	"fmt"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/security"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
)

// activateZonedSource registers a relevant, active source of the given
// class enrolled in the named zone. An empty zoneID reproduces a source
// no alarm zone holds.
func activateZonedSource(a *aggregate, channelSuffix int, class hmenum.SecurityClass, zoneID string) {
	ref := hmevent.NewSecuritySourceRef("ccu1", "HmIP-RF", fmt.Sprintf("ADDR%d:1", channelSuffix), "STATE")
	ref.Class = class
	a.sources[ref.Ref] = &indexedSource{ref: ref, class: class, relevant: true, zoneID: zoneID}
	a.active[ref.Ref] = int64(channelSuffix)
}

// putZone registers one alarm zone in the given arm state. An empty
// state reproduces a zone whose identity was seeded from the store but
// whose arm state has not arrived from the engine yet.
func putZone(a *aggregate, id string, state hmenum.AlarmZoneState) {
	a.zones[id] = security.ZoneState{ID: id, Slug: id, Name: id, State: state}
}

// TestIntrusionSeverityFollowsTheArmStateOfItsZone is the load-bearing
// case: the intrusion class is active whenever an enrolled door, window
// or motion sensor reports — deliberately, and independently of the arm
// state. Grading that detection as an alarm regardless folded the whole
// domain onto "alarm" every time somebody tilted a window on a disarmed
// system, which is a detection and not a break-in. The verdict belongs
// to the arm state; only an armed zone escalates.
func TestIntrusionSeverityFollowsTheArmStateOfItsZone(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		zone hmenum.AlarmZoneState
		want hmenum.SecuritySeverity
	}{
		// Disarmed is the reported defect: an observation, never an alarm.
		{name: "disarmed", zone: hmenum.AlarmZoneStateDisarmed, want: hmenum.SecuritySeverityInfo},
		// Every state but disarmed means the operator asked to be
		// protected, so a detection during it escalates.
		{name: "armed", zone: hmenum.AlarmZoneStateArmed, want: hmenum.SecuritySeverityAlarm},
		{name: "arming", zone: hmenum.AlarmZoneStateArming, want: hmenum.SecuritySeverityAlarm},
		{name: "pending", zone: hmenum.AlarmZoneStatePending, want: hmenum.SecuritySeverityAlarm},
		{name: "triggered", zone: hmenum.AlarmZoneStateTriggered, want: hmenum.SecuritySeverityAlarm},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			a := newAggregate()
			putZone(a, "z1", tc.zone)
			activateZonedSource(a, 1, hmenum.SecurityClassIntrusion, "z1")

			st := a.classState(hmenum.SecurityClassIntrusion)
			if !st.Active {
				t.Fatal("intrusion must stay active regardless of the arm state")
			}
			if st.Severity != tc.want {
				t.Errorf("class severity = %q, want %q", st.Severity, tc.want)
			}
			if got := a.severity(); got != tc.want {
				t.Errorf("folded severity = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestDisarmedIntrusionKeepsTheDomainBelowAlarm states the operator's
// report as an assertion: a motion detector reporting on a disarmed
// system must not fold the domain onto "alarm".
func TestDisarmedIntrusionKeepsTheDomainBelowAlarm(t *testing.T) {
	t.Parallel()
	a := newAggregate()
	putZone(a, "z1", hmenum.AlarmZoneStateDisarmed)
	putZone(a, "z2", hmenum.AlarmZoneStateDisarmed)
	activateZonedSource(a, 1, hmenum.SecurityClassIntrusion, "z1")
	activateZonedSource(a, 2, hmenum.SecurityClassIntrusion, "z2")

	if got := a.severity(); got.Rank() >= hmenum.SecuritySeverityAlarm.Rank() {
		t.Errorf("folded severity = %q, want below %q", got, hmenum.SecuritySeverityAlarm)
	}
}

// TestOneArmedZoneEscalatesTheWholeClass verifies the fold is an OR over
// the active sources: a second source sitting in a disarmed zone must
// not talk the armed one down.
func TestOneArmedZoneEscalatesTheWholeClass(t *testing.T) {
	t.Parallel()
	a := newAggregate()
	putZone(a, "z1", hmenum.AlarmZoneStateDisarmed)
	putZone(a, "z2", hmenum.AlarmZoneStateArmed)
	activateZonedSource(a, 1, hmenum.SecurityClassIntrusion, "z1")
	activateZonedSource(a, 2, hmenum.SecurityClassIntrusion, "z2")

	if got := a.severity(); got != hmenum.SecuritySeverityAlarm {
		t.Errorf("folded severity = %q, want %q", got, hmenum.SecuritySeverityAlarm)
	}
}

// TestIntrusionWithoutZonesStaysObservation pins the disabled-engine
// case: with no zones there is no arm state anywhere, so intrusion can
// never escalate. That is a complete answer, not a gap — it must not
// take the warning the unresolvable case gets.
func TestIntrusionWithoutZonesStaysObservation(t *testing.T) {
	t.Parallel()
	a := newAggregate()
	activateZonedSource(a, 1, hmenum.SecurityClassIntrusion, "")

	st := a.classState(hmenum.SecurityClassIntrusion)
	if !st.Active {
		t.Fatal("intrusion must stay active without an alarm engine")
	}
	if st.Severity != hmenum.SecuritySeverityInfo {
		t.Errorf("class severity = %q, want %q", st.Severity, hmenum.SecuritySeverityInfo)
	}
	if got := a.severity(); got != hmenum.SecuritySeverityInfo {
		t.Errorf("folded severity = %q, want %q", got, hmenum.SecuritySeverityInfo)
	}
}

// TestUnresolvableArmStateAdmitsTheGap pins the refusal to guess. When
// the arm state behind an active source cannot be resolved, the domain
// says so with a warning instead of reporting the reassuring "info" a
// disarmed zone earns — on a security surface an invented "all clear"
// is worse than an admitted gap.
func TestUnresolvableArmStateAdmitsTheGap(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		zoneID string
		// seed installs the zone the source names, if any.
		seed func(*aggregate)
	}{
		{
			name:   "source in no zone",
			zoneID: "",
			seed:   func(a *aggregate) { putZone(a, "other", hmenum.AlarmZoneStateDisarmed) },
		},
		{
			name:   "zone the domain does not hold",
			zoneID: "ghost",
			seed:   func(a *aggregate) { putZone(a, "other", hmenum.AlarmZoneStateDisarmed) },
		},
		{
			// Identity seeding fills id, slug and name; the arm state
			// arrives with the first panel projection. Between the two a
			// zone stands with an empty state.
			name:   "zone has not reported a state yet",
			zoneID: "z1",
			seed:   func(a *aggregate) { putZone(a, "z1", "") },
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			a := newAggregate()
			tc.seed(a)
			activateZonedSource(a, 1, hmenum.SecurityClassIntrusion, tc.zoneID)

			st := a.classState(hmenum.SecurityClassIntrusion)
			if st.Severity != hmenum.SecuritySeverityWarning {
				t.Errorf("class severity = %q, want %q", st.Severity, hmenum.SecuritySeverityWarning)
			}
		})
	}
}

// TestHazardClassesIgnoreTheArmState verifies the arm-awareness is
// scoped to intrusion alone. Smoke, gas, CO, water and panic mean the
// same thing whoever is home, so they escalate on a disarmed system
// exactly as they do on an armed one; tamper stays a warning and the
// diagnostic classes stay information.
func TestHazardClassesIgnoreTheArmState(t *testing.T) {
	t.Parallel()

	cases := []struct {
		class hmenum.SecurityClass
		want  hmenum.SecuritySeverity
	}{
		{class: hmenum.SecurityClassSmoke, want: hmenum.SecuritySeverityCritical},
		{class: hmenum.SecurityClassGas, want: hmenum.SecuritySeverityCritical},
		{class: hmenum.SecurityClassCO, want: hmenum.SecuritySeverityCritical},
		{class: hmenum.SecurityClassWater, want: hmenum.SecuritySeverityAlarm},
		{class: hmenum.SecurityClassPanic, want: hmenum.SecuritySeverityAlarm},
		{class: hmenum.SecurityClassTamper, want: hmenum.SecuritySeverityWarning},
		{class: hmenum.SecurityClassTechnical, want: hmenum.SecuritySeverityInfo},
		{class: hmenum.SecurityClassBattery, want: hmenum.SecuritySeverityInfo},
	}
	for _, tc := range cases {
		t.Run(string(tc.class), func(t *testing.T) {
			t.Parallel()
			// A fully disarmed installation: the arm state is present and
			// says "nobody asked to be protected". It must change nothing
			// for these classes.
			a := newAggregate()
			putZone(a, "z1", hmenum.AlarmZoneStateDisarmed)
			activateZonedSource(a, 1, tc.class, "z1")

			st := a.classState(tc.class)
			if st.Severity != tc.want {
				t.Errorf("class severity = %q, want %q", st.Severity, tc.want)
			}
			if got := a.severity(); got != tc.want {
				t.Errorf("folded severity = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestInactiveClassContributesNothing verifies a known but quiet class
// grades ok, so the fold cannot be raised by a class nobody triggered.
func TestInactiveClassContributesNothing(t *testing.T) {
	t.Parallel()
	a := newAggregate()
	putZone(a, "z1", hmenum.AlarmZoneStateArmed)
	addAggSource(a, "ccu1", 1, hmenum.SecurityClassSmoke, true)

	st := a.classState(hmenum.SecurityClassSmoke)
	if st.Active {
		t.Fatal("class must be inactive")
	}
	if st.Severity != hmenum.SecuritySeverityOK {
		t.Errorf("class severity = %q, want %q", st.Severity, hmenum.SecuritySeverityOK)
	}
	if got := a.severity(); got != hmenum.SecuritySeverityOK {
		t.Errorf("folded severity = %q, want %q", got, hmenum.SecuritySeverityOK)
	}
}
