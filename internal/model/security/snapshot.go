// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// Package security holds the projection types of the Security & Safety
// domain: the shapes REST, WebSocket and MQTT render.
//
// They are plain structs rather than model data points, following
// internal/model/alarmpanel. The domain is daemon-level and singular —
// it has no central, no channel and no paramset — so the data-point
// machinery would have to be bent to fit it, and every north-bound
// consumer of this domain is a hand-written typed method anyway.
package security

import (
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
)

// Snapshot is the coherent state of the whole domain at one instant.
//
// It is produced under a single lock so the severity can never disagree
// with the class states it was folded from — a consumer that reads
// "critical" and an empty smoke class would report a fire nobody can
// locate.
type Snapshot struct {
	// Severity is the folded overall state.
	Severity hmenum.SecuritySeverity
	// Classes holds one entry per class that has at least one known
	// source. A class with no sources is absent rather than present and
	// false: an installation without gas detectors should not advertise
	// a permanently-off gas alarm.
	Classes map[hmenum.SecurityClass]ClassState
	// Zones holds one entry per alarm zone, keyed by slug. Empty when
	// the alarm engine is disabled — the rest of the domain still works.
	Zones map[string]ZoneState
	// Faults lists the standing faults, oldest first.
	Faults []Fault
	// EngineHealthy mirrors the alarm engine's own health verdict.
	// False means the engine reported a problem with itself, which is
	// distinct from a broker outage and must be distinguishable.
	EngineHealthy bool
	// LastAlarm and LastFault are the most recent notifications of each
	// kind; they survive a restart of the consumer, which an event
	// cannot.
	LastAlarm *Notification
	LastFault *Notification
}

// ClassState is the aggregate of one hazard or fault class across every
// central.
type ClassState struct {
	Class hmenum.SecurityClass
	// Active reports whether at least one source of this class is
	// currently active.
	Active bool
	// Sources lists the currently active sources, oldest first.
	Sources []hmevent.SecuritySourceRef
	// Known counts the sources of this class the index knows about,
	// active or not — the denominator behind Active.
	Known int
	// Centrals names the centrals contributing active sources.
	Centrals []string
	// SinceMS is when the class last became active; 0 while inactive.
	SinceMS int64
}

// ZoneState is the security view of one alarm zone.
type ZoneState struct {
	ID   string
	Slug string
	Name string
	// State and Mode mirror the alarm engine.
	State hmenum.AlarmZoneState
	Mode  hmenum.AlarmMode
	// Sources lists everything currently active in the zone.
	Sources []hmevent.SecuritySourceRef
	// ByClass groups the active source names per class — the "per zone
	// and per type" axis in one entity rather than a zone-by-class
	// matrix of entities.
	ByClass map[hmenum.SecurityClass][]string
	// IncidentID references the running incident; 0 when none.
	IncidentID int64
	// Blockers explains why the zone cannot be armed, per mode.
	Blockers map[hmenum.AlarmMode][]hmevent.AlarmBlockerDetail
	// SinceMS is when the zone last became non-quiet.
	SinceMS int64
}

// Fault is one standing problem of a security-relevant data point.
type Fault struct {
	ID       string
	Class    hmenum.SecurityClass
	Reason   hmenum.SecurityFaultReason
	Severity hmenum.SecuritySeverity
	Source   hmevent.SecuritySourceRef
	SinceMS  int64
	// AcknowledgedAtMS and AcknowledgedBy record an operator having
	// seen the fault. Acknowledgement never clears it: the condition
	// stands, the operator has merely stopped needing to be told.
	AcknowledgedAtMS int64
	AcknowledgedBy   string
}

// Notification is one rendered report about the domain — the shape a
// messenger integration consumes.
//
// It carries the rendered text and the machine facets side by side, on
// purpose. The text makes the three-line automation possible; the
// facets make a different wording possible without re-deriving anything
// from prose. Offering only one of the two forces every consumer into
// the wrong half.
type Notification struct {
	Class    hmenum.SecurityClass
	Severity hmenum.SecuritySeverity
	Verb     hmenum.SecurityVerb
	// Subject is one line, at most 120 characters, no trailing
	// punctuation — it maps onto a notification title.
	Subject string
	// Message is a full sentence naming cause, place and time.
	Message string
	// I18nKey and Args let a consumer re-render in another locale, and
	// let the SPA render in the request locale rather than the
	// daemon's.
	I18nKey string
	Args    map[string]string
	// Sources lists what caused it.
	Sources []hmevent.SecuritySourceRef
	// ZoneID / ZoneSlug / ZoneName identify the zone, empty for a
	// system-level report.
	ZoneID   string
	ZoneSlug string
	ZoneName string
	Mode     hmenum.AlarmMode
	// IncidentID references the alarm incident, 0 when none.
	IncidentID int64
	// Link is the deep link into the config UI.
	Link string
	// AtMS is when the report was produced.
	AtMS int64
	// Retainable reports whether the notification may be written to
	// retained state. A duress report under the notify-only visibility
	// level is delivered but never retained: it must reach a phone
	// without lingering where an attacker could read it back.
	Retainable bool
}

// SourceNames returns the display names of the notification's sources,
// falling back to the channel address for an unnamed one.
func (n Notification) SourceNames() []string {
	if len(n.Sources) == 0 {
		return nil
	}
	out := make([]string, 0, len(n.Sources))
	for i := range n.Sources {
		switch {
		case n.Sources[i].Name != "":
			out = append(out, n.Sources[i].Name)
		case n.Sources[i].ChannelAddress != "":
			out = append(out, n.Sources[i].ChannelAddress)
		}
	}
	return out
}
