// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package security

import (
	"sort"
	"time"

	"github.com/SukramJ/openccu-loom/internal/model/security"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
)

// aggregate holds the domain state. Every read produces a coherent
// copy under one lock: a consumer that saw "critical" together with an
// empty smoke class would report a fire nobody can locate.
type aggregate struct {
	// sources is the classification index: routing key → what the data
	// point means. Rebuilt when a central attaches or the operator
	// changes an override.
	// Values are pointers: classState walks the whole index on every
	// wire event, and copying a struct per entry across a fleet-sized
	// map is real work on a path that runs constantly.
	sources map[string]*indexedSource
	// active tracks which indexed sources are currently active, with
	// the time they became so.
	active map[string]int64
	// zones is the security view per zone, keyed by zone id.
	zones map[string]security.ZoneState
	// classSince records when each class last became active.
	classSince map[hmenum.SecurityClass]int64
	// faults is the standing fault set, keyed by fault id.
	faults map[string]*security.Fault
	// engineHealthy mirrors the alarm engine's own health verdict.
	//
	// Walk-test state is deliberately absent: the engine's walk-test
	// event reports progress (seen/total) with no start or stop edge,
	// so "a test is running" cannot be derived from the bus alone. A
	// test-mode flag that never turns true would be worse than none.
	engineHealthy bool
	// lastAlarm and lastFault survive across events; a consumer that
	// restarts has no way to replay an event, so the retained halves
	// are the only durable record.
	lastAlarm *security.Notification
	lastFault *security.Notification
}

// indexedSource is one classified data point.
type indexedSource struct {
	ref hmevent.SecuritySourceRef
	// class is the effective class after operator overrides.
	class hmenum.SecurityClass
	// reason narrows a fault class; empty for hazard classes.
	reason hmenum.SecurityFaultReason
	// activeValues narrows which enumerated values count as active.
	activeValues []string
	// valueList is the parameter's declared enumeration vocabulary.
	//
	// It has to travel with the source: a wire event carries only the
	// key and the value, and without the list the narrowing silently
	// degrades to "anything but index 0". For the smoke status that
	// reads INTRUSION_ALARM — the daemon's own siren command — as a
	// fire.
	valueList []string
	// silentPanic marks a panic source configured to trigger covertly.
	// The visibility policy has to see it here; PanicSilent otherwise
	// lives only inside the alarm engine's opaque config document.
	silentPanic bool
	// relevant marks the source as security-relevant. A source that is
	// merely classifiable is not automatically aggregated: tamper,
	// battery and technical are gated on the device carrying an alarm
	// role, so `problem` stays a real signal in a 400-device
	// installation instead of standing permanently on.
	relevant bool
	// zoneID names the alarm zone when the source is enrolled.
	zoneID string
}

func newAggregate() *aggregate {
	return &aggregate{
		sources:       map[string]*indexedSource{},
		active:        map[string]int64{},
		zones:         map[string]security.ZoneState{},
		classSince:    map[hmenum.SecurityClass]int64{},
		faults:        map[string]*security.Fault{},
		engineHealthy: true,
	}
}

// setActive records an activation change and reports whether it moved.
// The caller holds the lock.
func (a *aggregate) setActive(ref string, on bool, atMS int64) bool {
	_, was := a.active[ref]
	if on == was {
		return false
	}
	if on {
		a.active[ref] = atMS
	} else {
		delete(a.active, ref)
	}
	return true
}

// classState folds the active sources of one class. The caller holds
// the lock.
func (a *aggregate) classState(c hmenum.SecurityClass) security.ClassState {
	st := security.ClassState{Class: c}
	centrals := map[string]bool{}
	for key, src := range a.sources {
		if src.class != c || !src.relevant {
			continue
		}
		st.Known++
		at, on := a.active[key]
		if !on {
			continue
		}
		ref := src.ref
		ref.AtMS = at
		st.Sources = append(st.Sources, ref)
		if ref.Central != "" {
			centrals[ref.Central] = true
		}
	}
	st.Active = len(st.Sources) > 0
	if st.Active {
		sort.Slice(st.Sources, func(i, j int) bool {
			if st.Sources[i].AtMS != st.Sources[j].AtMS {
				return st.Sources[i].AtMS < st.Sources[j].AtMS
			}
			return st.Sources[i].Ref < st.Sources[j].Ref
		})
		st.SinceMS = a.classSince[c]
		st.Centrals = sortedKeys(centrals)
	}
	// The grade travels with the state it was derived from. Deriving it
	// here rather than at each consumer is what keeps the badge the
	// Config UI paints, the MQTT class attribute and the folded
	// `security/state` from disagreeing about the same detection.
	st.Severity = a.classSeverity(st)
	return st
}

// severity folds the whole domain onto one value. The caller holds the
// lock.
//
// An unhealthy engine contributes a warning of its own: the alarm
// system reporting a problem with itself is a degradation even when no
// sensor is active, and it must be distinguishable from a broker
// outage — which is why it is a severity contribution rather than an
// availability flag.
func (a *aggregate) severity() hmenum.SecuritySeverity {
	worst := hmenum.SecuritySeverityOK
	raise := func(s hmenum.SecuritySeverity) {
		if s.Rank() > worst.Rank() {
			worst = s
		}
	}
	// Each class contributes the severity it derived for itself, not the
	// static one its name implies. Folding SeverityForClass here instead
	// collapsed the whole domain onto "alarm" whenever a motion detector
	// reported on a disarmed system, which is a detection and not a
	// break-in.
	for _, c := range hmenum.SecurityClasses() {
		raise(a.classState(c).Severity)
	}
	for _, f := range a.faults {
		if src, ok := a.sources[f.Source.Ref]; ok && !src.relevant {
			// The source was excluded or lost its alarm role after the
			// fault opened; a stale ledger row must not keep pinning the
			// overall severity.
			continue
		}
		raise(f.Severity)
	}
	if !a.engineHealthy {
		raise(hmenum.SecuritySeverityWarning)
	}
	return worst
}

// snapshot renders the coherent state. The caller holds the lock.
func (a *aggregate) snapshot() security.Snapshot {
	snap := security.Snapshot{
		Severity:      a.severity(),
		Classes:       map[hmenum.SecurityClass]security.ClassState{},
		Zones:         map[string]security.ZoneState{},
		EngineHealthy: a.engineHealthy,
		LastAlarm:     cloneNotification(a.lastAlarm),
		LastFault:     cloneNotification(a.lastFault),
	}
	for _, c := range hmenum.SecurityClasses() {
		st := a.classState(c)
		// A class the index knows nothing about is omitted rather than
		// published as permanently inactive: an installation without
		// gas detectors should not advertise a gas alarm.
		if st.Known == 0 {
			continue
		}
		snap.Classes[c] = st
	}
	for id := range a.zones {
		z := a.zones[id]
		snap.Zones[z.Slug] = cloneZone(z)
	}
	for _, f := range a.faults {
		snap.Faults = append(snap.Faults, *f)
	}
	sort.Slice(snap.Faults, func(i, j int) bool {
		if snap.Faults[i].SinceMS != snap.Faults[j].SinceMS {
			return snap.Faults[i].SinceMS < snap.Faults[j].SinceMS
		}
		return snap.Faults[i].ID < snap.Faults[j].ID
	})
	return snap
}

// dropCentral removes every trace of one central. Without it a detached
// central leaves a ghost source pinning its class permanently active.
// The caller holds the lock.
func (a *aggregate) dropCentral(name string) {
	for key, src := range a.sources {
		if src.ref.Central != name {
			continue
		}
		delete(a.sources, key)
		delete(a.active, key)
	}
	for id, f := range a.faults {
		if f.Source.Central == name {
			delete(a.faults, id)
		}
	}
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func cloneNotification(n *security.Notification) *security.Notification {
	if n == nil {
		return nil
	}
	c := *n
	return &c
}

func cloneZone(z security.ZoneState) security.ZoneState {
	out := z
	if z.ByClass != nil {
		out.ByClass = make(map[hmenum.SecurityClass][]string, len(z.ByClass))
		for k, v := range z.ByClass {
			out.ByClass[k] = append([]string(nil), v...)
		}
	}
	out.Sources = append([]hmevent.SecuritySourceRef(nil), z.Sources...)
	return out
}

// nowMS is the millisecond stamp helper shared by the package.
func nowMS(t time.Time) int64 { return t.UnixMilli() }
