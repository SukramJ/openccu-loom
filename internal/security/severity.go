// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package security

import (
	"github.com/SukramJ/openccu-loom/internal/model/security"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
)

// classSeverity derives what one class contributes to the folded
// overall severity, from the class, its active sources and the current
// zone states. It is the single derivation: snapshot() writes it into
// ClassState.Severity and severity() folds the same values, so the
// per-class badge a Config UI paints and the `security/state` a Home
// Assistant automation reads can never disagree.
//
// Every class but intrusion contributes its static severity, because
// every other detection means the same thing whoever is home: smoke,
// gas, CO, water and panic escalate unconditionally, tamper is a
// warning, technical and battery are information.
//
// Intrusion is the one class whose meaning depends on the arm state.
// The class entity reports a detection, never a verdict — an open
// window on a disarmed system sets it just as a burglar does — so
// treating it as a permanent alarm folded the whole domain onto "alarm"
// every time somebody tilted a window. The verdict belongs to the alarm
// engine, which is the only component that knows whether anyone asked
// to be protected.
//
// The caller holds the lock.
func (a *aggregate) classSeverity(st security.ClassState) hmenum.SecuritySeverity {
	if !st.Active {
		return hmenum.SecuritySeverityOK
	}
	if st.Class != hmenum.SecurityClassIntrusion {
		return hmenum.SeverityForClass(st.Class)
	}
	return a.intrusionSeverity(st.Sources)
}

// intrusionSeverity resolves the arm state behind the active intrusion
// sources and grades the class accordingly. The caller holds the lock.
//
// Three outcomes, and the difference between the lower two is the
// point:
//
//   - Alarm — at least one active source sits in a zone that is armed.
//     "Armed" is every zone state but disarmed: arming (exit delay
//     running), pending (entry delay running) and triggered are all
//     states in which the operator has asked to be protected, so a
//     detection during one of them escalates exactly as it does under
//     armed. Mirrors hmenum.AlarmZoneState.
//
//   - Info — the detection is an observation nobody asked to act on:
//     either every source's zone is disarmed, or the installation has
//     no zones at all. No zones means the alarm engine is disabled;
//     then no arm state exists anywhere, intrusion can never escalate,
//     and saying so is a complete answer rather than a gap.
//
//   - Warning — the arm state behind at least one active source cannot
//     be resolved: the source is enrolled in no zone, names a zone the
//     domain does not hold, or the zone has not reported a state yet
//     (identity seeding fills id/slug/name, the arm state arrives with
//     the first panel projection). An unresolved state is deliberately
//     not counted as armed, but it is not reported as "all clear"
//     either — on a security surface an invented "disarmed" is worse
//     than an admitted gap, the same rule the zone display follows.
func (a *aggregate) intrusionSeverity(sources []hmevent.SecuritySourceRef) hmenum.SecuritySeverity {
	if len(a.zones) == 0 {
		return hmenum.SecuritySeverityInfo
	}
	unresolved := false
	for i := range sources {
		switch state, ok := a.armStateOf(sources[i]); {
		case !ok:
			unresolved = true
		case state != hmenum.AlarmZoneStateDisarmed:
			return hmenum.SeverityForClass(hmenum.SecurityClassIntrusion)
		}
	}
	if unresolved {
		return hmenum.SecuritySeverityWarning
	}
	return hmenum.SecuritySeverityInfo
}

// armStateOf resolves the arm state of the zone holding one source.
// ok=false means the arm state is unknown — an unenrolled source, an
// unknown zone, or a zone whose state has not arrived yet — which the
// caller must surface rather than read as disarmed. The caller holds
// the lock.
func (a *aggregate) armStateOf(ref hmevent.SecuritySourceRef) (hmenum.AlarmZoneState, bool) {
	src, ok := a.sources[ref.Ref]
	if !ok || src.zoneID == "" {
		return "", false
	}
	zone, ok := a.zones[src.zoneID]
	if !ok || !zone.State.Valid() {
		return "", false
	}
	return zone.State, true
}
