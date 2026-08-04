// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package hmapi

import "time"

// SecuritySnapshot is the whole Security & Safety domain at one
// instant: what is active, what is broken, and what was last reported.
type SecuritySnapshot struct {
	// Severity folds the domain onto one value.
	Severity string `json:"severity"`
	// Classes holds one entry per class the installation actually has
	// sources for. A class with no sources is absent rather than
	// present-and-false — an installation without gas detectors should
	// not advertise a permanently-off gas alarm.
	Classes []SecurityClassState `json:"classes"`
	// Zones is empty when the alarm engine is disabled; the rest of the
	// domain still reports.
	Zones []SecurityZoneState `json:"zones,omitempty"`
	// Faults lists the standing problems, oldest first.
	Faults []SecurityFault `json:"faults,omitempty"`
	// EngineHealthy is the alarm engine's verdict about itself,
	// distinct from a transport outage.
	EngineHealthy bool `json:"engine_healthy"`
	// LastAlarm / LastFault survive a consumer restart, which an event
	// cannot.
	LastAlarm *SecurityNotification `json:"last_alarm,omitempty"`
	LastFault *SecurityNotification `json:"last_fault,omitempty"`
}

// SecurityClassState aggregates one hazard or fault class across every
// central.
type SecurityClassState struct {
	Class string `json:"class"`
	// Active reports whether at least one source is currently active.
	Active bool `json:"active"`
	// Sources lists the active sources, oldest first.
	Sources []AlarmSource `json:"sources,omitempty"`
	// Known counts the sources of this class the index knows — the
	// denominator behind Active.
	Known int `json:"known"`
	// Centrals names the centrals contributing active sources.
	Centrals []string  `json:"centrals,omitempty"`
	Since    time.Time `json:"since,omitzero"`
}

// SecurityZoneState is the security view of one alarm zone.
type SecurityZoneState struct {
	ID    string `json:"id"`
	Slug  string `json:"slug"`
	Name  string `json:"name"`
	State string `json:"state"`
	Mode  string `json:"mode"`
	// Sources lists everything currently active in the zone.
	Sources []AlarmSource `json:"sources,omitempty"`
	// ByClass groups active source names per class — the "per zone and
	// per type" axis without a zone-by-class matrix of entities.
	ByClass    map[string][]string `json:"by_class,omitempty"`
	IncidentID int64               `json:"incident_id,omitempty"`
	Since      time.Time           `json:"since,omitzero"`
}

// SecurityFault is one standing problem.
type SecurityFault struct {
	ID       string      `json:"id"`
	Class    string      `json:"class"`
	Reason   string      `json:"reason"`
	Severity string      `json:"severity"`
	Source   AlarmSource `json:"source"`
	Since    time.Time   `json:"since"`
	// AcknowledgedAt / AcknowledgedBy record an operator having seen
	// it. Acknowledgement never clears the fault.
	AcknowledgedAt time.Time `json:"acknowledged_at,omitzero"`
	AcknowledgedBy string    `json:"acknowledged_by,omitempty"`
}

// SecurityNotification is one rendered report.
//
// It carries the sentence and the machine facets side by side: the text
// makes a three-line automation possible, the facets make a different
// wording possible without parsing prose.
type SecurityNotification struct {
	Class    string `json:"class"`
	Severity string `json:"severity"`
	Verb     string `json:"verb"`
	// Subject is one line suitable as a notification title.
	Subject string `json:"subject"`
	// Message is a full sentence naming cause, place and time.
	Message string `json:"message"`
	// I18nKey and Args let a consumer render in its own locale.
	I18nKey    string            `json:"i18n_key"`
	Args       map[string]string `json:"args,omitempty"`
	Sources    []AlarmSource     `json:"sources,omitempty"`
	ZoneID     string            `json:"zone_id,omitempty"`
	ZoneSlug   string            `json:"zone_slug,omitempty"`
	ZoneName   string            `json:"zone_name,omitempty"`
	Mode       string            `json:"mode,omitempty"`
	IncidentID int64             `json:"incident_id,omitempty"`
	// Link deep-links into the config UI.
	Link string    `json:"link,omitempty"`
	At   time.Time `json:"at"`
}

// SecuritySourceView is one classified data point as the inventory
// shows it: the classifier's verdict, whether an operator overrode it,
// and whether an alarm zone holds it.
type SecuritySourceView struct {
	Ref            string `json:"ref"`
	Central        string `json:"central"`
	InterfaceID    string `json:"interface_id"`
	ChannelAddress string `json:"channel_address"`
	DeviceAddress  string `json:"device_address"`
	Parameter      string `json:"parameter"`
	Name           string `json:"name,omitempty"`
	Class          string `json:"class"`
	Reason         string `json:"reason,omitempty"`
	Active         bool   `json:"active"`
	// Relevant reports whether the source contributes to an aggregate.
	// A classifiable source on a device with no alarm role is indexed
	// but not aggregated; that gate keeps the fault plane from standing
	// permanently on across a whole fleet.
	Relevant bool `json:"relevant"`
	// ZoneID names the alarm zone holding it, empty when not enrolled.
	ZoneID string `json:"zone_id,omitempty"`
	// Overridden reports that an operator decision, not the classifier,
	// produced this verdict.
	Overridden bool      `json:"overridden,omitempty"`
	Since      time.Time `json:"since,omitzero"`
}

// SecuritySourceOverride is the operator decision about one data point.
//
// An empty class with included=true and no note removes the override,
// returning the data point to the classifier's verdict — the undo a
// wrong override needs.
type SecuritySourceOverride struct {
	Class string `json:"class,omitempty"`
	// Included=false removes the source from every aggregate.
	//
	// It is a pointer so an omitted field is distinguishable from an
	// explicit false. Omitting it means "leave inclusion as it is",
	// which is what `{"class":"technical"}` — the natural way to just
	// reclassify — has to mean. As a plain bool that request decoded to
	// false and silently excluded the source instead, returning 204
	// while doing the opposite of what was asked.
	Included *bool  `json:"included,omitempty"`
	Note     string `json:"note,omitempty"`
}
