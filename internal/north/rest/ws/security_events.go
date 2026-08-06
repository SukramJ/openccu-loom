// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package ws

import (
	"time"

	"github.com/SukramJ/openccu-loom/internal/central/events"
	"github.com/SukramJ/openccu-loom/pkg/hmapi"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
)

// Security & Safety WebSocket topics. The domain is daemon-level, so
// none of them carries a central segment; the payload names the
// contributing centrals where that matters.
//
// Three topics rather than one flat family: a consumer that wants the
// prose reports (a messenger integration) subscribes to
// `security.notifications` alone and is spared the state churn, while a
// dashboard takes `security.state` and never sees a fault it does not
// render. A client that wants everything subscribes to `security.*`.
const (
	securityStateTopic        = "security.state"
	securityFaultsTopic       = "security.faults"
	securityNotificationTopic = "security.notifications"
)

// Broadcast Type labels for the security family. Unlike the alarm
// domain — where the internal tags read `alarm_panel.` and the wire
// reads `alarm.` — the Security & Safety domain uses one vocabulary on
// both sides, so these mirror the hmevent.EventTypeSecurity* values
// exactly (pkg/hmevent/security.go).
const (
	broadcastSecurityStateChanged = "security.state_changed"
	broadcastSecurityClassChanged = "security.class_changed"
	broadcastSecurityZoneChanged  = "security.zone_changed"
	broadcastSecurityFaultChanged = "security.fault_changed"
	broadcastSecurityNotification = "security.notification"
)

// SecurityStateChangedPayload is the broadcast payload for a change of
// the folded domain severity (security.state_changed). It carries the
// fold only; a consumer that needs the detail reads GET /security.
type SecurityStateChangedPayload struct {
	Severity string `json:"severity"`
	// PreviousSeverity is the severity the fold left. Empty on the
	// first report after start-up, where there is no previous value.
	PreviousSeverity string `json:"previous_severity,omitempty"`
	// ActiveClasses names the classes contributing to Severity.
	ActiveClasses []string `json:"active_classes,omitempty"`
	// OpenFaults is the standing fault count behind the fold.
	OpenFaults int `json:"open_faults"`
}

// SecurityClassChangedPayload is the broadcast payload for one hazard
// or fault class going active or inactive, or for its source set
// changing while it stays active (security.class_changed) — a second
// smoke detector joining an existing fire is a change worth announcing.
type SecurityClassChangedPayload struct {
	Class   string              `json:"class"`
	Active  bool                `json:"active"`
	Sources []hmapi.AlarmSource `json:"sources,omitempty"`
	// Centrals names the centrals contributing active sources.
	Centrals []string `json:"centrals,omitempty"`
	// Since is when the class entered its current state. Omitted while
	// the class is inactive and has no recorded transition.
	Since *time.Time `json:"since,omitempty"`
}

// SecurityZoneChangedPayload is the broadcast payload for a change of
// one alarm zone's security view (security.zone_changed).
type SecurityZoneChangedPayload struct {
	ZoneID string `json:"zone_id"`
	// ZoneSlug is frozen at zone creation, so a consumer's entity ids
	// survive a rename; ZoneName is the display name and does not.
	ZoneSlug string              `json:"zone_slug"`
	ZoneName string              `json:"zone_name,omitempty"`
	State    string              `json:"state"`
	Mode     string              `json:"mode,omitempty"`
	Sources  []hmapi.AlarmSource `json:"sources,omitempty"`
	// ByClass groups the active source names per class, so a consumer
	// gets the per-zone-and-per-class axis in one object instead of a
	// zone-by-class matrix of entities.
	ByClass    map[string][]string `json:"by_class,omitempty"`
	IncidentID int64               `json:"incident_id,omitempty"`
}

// SecurityFaultChangedPayload is the broadcast payload for a fault
// opening, clearing or being acknowledged (security.fault_changed).
type SecurityFaultChangedPayload struct {
	FaultID  string            `json:"fault_id"`
	Class    string            `json:"class"`
	Reason   string            `json:"reason"`
	Severity string            `json:"severity"`
	Source   hmapi.AlarmSource `json:"source"`
	// Open reports the direction: true when the fault was raised,
	// false when it cleared.
	Open bool `json:"open"`
	// Acknowledged marks an acknowledgement rather than a state
	// change — the condition is unchanged, the operator has merely
	// stopped needing to be told.
	Acknowledged bool `json:"acknowledged"`
	// Since is when the fault opened. Omitted when the ledger records
	// no such occurrence.
	Since *time.Time `json:"since,omitempty"`
	// OpenCount is the standing fault count after this change, so a
	// count entity needs no second read.
	OpenCount int `json:"open_count"`
}

// SecurityNotificationPayload is the broadcast payload of one rendered
// report (security.notification) — the only payload in the domain that
// carries prose, so a consumer can show a sentence without inventing
// alarm wording, and I18nKey plus Args let it re-render in its own
// locale instead.
type SecurityNotificationPayload struct {
	Class      string              `json:"class"`
	Severity   string              `json:"severity"`
	Verb       string              `json:"verb"`
	Subject    string              `json:"subject"`
	Message    string              `json:"message"`
	I18nKey    string              `json:"i18n_key"`
	Args       map[string]string   `json:"args,omitempty"`
	Sources    []hmapi.AlarmSource `json:"sources,omitempty"`
	ZoneID     string              `json:"zone_id,omitempty"`
	ZoneSlug   string              `json:"zone_slug,omitempty"`
	ZoneName   string              `json:"zone_name,omitempty"`
	Mode       string              `json:"mode,omitempty"`
	IncidentID int64               `json:"incident_id,omitempty"`
	Link       string              `json:"link,omitempty"`
	At         time.Time           `json:"at"`
	// Fault marks a fault report rather than a hazard report, so a
	// consumer can route the two without inspecting the class.
	Fault bool `json:"fault"`
}

// SecuritySubscriber bridges the Security & Safety event bus onto the
// WebSocket [*Hub].
//
// Like [AlarmPanelSubscriber] it binds to one daemon-level bus — the
// domain aggregates across every central — so there is no per-central
// fan-out. It exists because the domain's five events reached MQTT, the
// webhook and the metrics collector but no WebSocket consumer, which
// left every REST/WS client polling GET /security for a smoke alarm.
type SecuritySubscriber struct {
	bus    *events.Bus
	hub    *Hub
	unsubs []func()
}

// NewSecuritySubscriber returns a subscriber bound to the security bus
// and the WebSocket hub.
func NewSecuritySubscriber(bus *events.Bus, hub *Hub) *SecuritySubscriber {
	return &SecuritySubscriber{bus: bus, hub: hub}
}

// Start attaches one subscription per security event type to the bus.
func (s *SecuritySubscriber) Start() {
	if s.bus == nil || s.hub == nil {
		return
	}
	s.unsubs = append(
		s.unsubs,
		events.Subscribe(s.bus, s.onStateChanged),
		events.Subscribe(s.bus, s.onClassChanged),
		events.Subscribe(s.bus, s.onZoneChanged),
		events.Subscribe(s.bus, s.onFaultChanged),
		events.Subscribe(s.bus, s.onNotification),
	)
}

// Stop drops all subscriptions.
func (s *SecuritySubscriber) Stop() {
	for _, u := range s.unsubs {
		u()
	}
	s.unsubs = nil
}

func (s *SecuritySubscriber) onStateChanged(e hmevent.SecurityStateChangedEvent) {
	classes := make([]string, 0, len(e.ActiveClasses))
	for _, c := range e.ActiveClasses {
		classes = append(classes, string(c))
	}
	s.hub.Publish(Event{
		Topic: securityStateTopic,
		Type:  broadcastSecurityStateChanged,
		When:  e.Timestamp(),
		Payload: SecurityStateChangedPayload{
			Severity:         string(e.To),
			PreviousSeverity: string(e.From),
			ActiveClasses:    classes,
			OpenFaults:       e.OpenFaults,
		},
	})
}

func (s *SecuritySubscriber) onClassChanged(e hmevent.SecurityClassChangedEvent) {
	s.hub.Publish(Event{
		Topic: securityStateTopic,
		Type:  broadcastSecurityClassChanged,
		When:  e.Timestamp(),
		Payload: SecurityClassChangedPayload{
			Class:    string(e.Class),
			Active:   e.Active,
			Sources:  securitySources(e.Sources),
			Centrals: e.Centrals,
			Since:    msToTimePtr(e.SinceMS),
		},
	})
}

func (s *SecuritySubscriber) onZoneChanged(e hmevent.SecurityZoneChangedEvent) {
	var byClass map[string][]string
	if len(e.ByClass) > 0 {
		byClass = make(map[string][]string, len(e.ByClass))
		for c, names := range e.ByClass {
			byClass[string(c)] = names
		}
	}
	s.hub.Publish(Event{
		Topic: securityStateTopic,
		Type:  broadcastSecurityZoneChanged,
		When:  e.Timestamp(),
		Payload: SecurityZoneChangedPayload{
			ZoneID:     e.ZoneID,
			ZoneSlug:   e.ZoneSlug,
			ZoneName:   e.ZoneName,
			State:      string(e.State),
			Mode:       string(e.Mode),
			Sources:    securitySources(e.Sources),
			ByClass:    byClass,
			IncidentID: e.IncidentID,
		},
	})
}

func (s *SecuritySubscriber) onFaultChanged(e hmevent.SecurityFaultChangedEvent) {
	s.hub.Publish(Event{
		Topic: securityFaultsTopic,
		Type:  broadcastSecurityFaultChanged,
		When:  e.Timestamp(),
		Payload: SecurityFaultChangedPayload{
			FaultID:      e.FaultID,
			Class:        string(e.Class),
			Reason:       string(e.Reason),
			Severity:     string(e.Severity),
			Source:       securitySource(e.Source),
			Open:         e.Open,
			Acknowledged: e.Acknowledged,
			Since:        msToTimePtr(e.SinceMS),
			OpenCount:    e.OpenCount,
		},
	})
}

// onNotification republishes a rendered report — unless the domain
// marked it non-retainable.
//
// The WebSocket is a local screen surface: it feeds the SPA and every
// dashboard a browser has open. A covert trigger (duress code, silent
// panic) is delivered but never retained unless the operator chose
// `full`, precisely because a hallway tablet showing "duress code
// entered" while the attacker stands next to you defeats the feature
// (hmenum.DuressVisibility, which names the WebSocket under `full`
// alone). Retainability is decided once, by the domain; this plane
// honours the flag rather than re-deriving the policy.
func (s *SecuritySubscriber) onNotification(e hmevent.SecurityNotificationEvent) {
	if !e.Retainable {
		return
	}
	s.hub.Publish(Event{
		Topic: securityNotificationTopic,
		Type:  broadcastSecurityNotification,
		When:  e.Timestamp(),
		Payload: SecurityNotificationPayload{
			Class:      string(e.Class),
			Severity:   string(e.Severity),
			Verb:       string(e.Verb),
			Subject:    e.Subject,
			Message:    e.Message,
			I18nKey:    e.I18nKey,
			Args:       e.Args,
			Sources:    securitySources(e.Sources),
			ZoneID:     e.ZoneID,
			ZoneSlug:   e.ZoneSlug,
			ZoneName:   e.ZoneName,
			Mode:       string(e.Mode),
			IncidentID: e.IncidentID,
			Link:       e.Link,
			At:         time.UnixMilli(e.AtMS).UTC(),
			Fault:      e.Fault,
		},
	})
}

// securitySources reuses the alarm source shape, so one parser serves
// the REST snapshot, the incident ledger and these broadcasts alike.
func securitySources(refs []hmevent.SecuritySourceRef) []hmapi.AlarmSource {
	if len(refs) == 0 {
		return nil
	}
	out := make([]hmapi.AlarmSource, 0, len(refs))
	for i := range refs {
		out = append(out, securitySource(refs[i]))
	}
	return out
}

func securitySource(r hmevent.SecuritySourceRef) hmapi.AlarmSource {
	return hmapi.AlarmSource{
		Ref:            r.Ref,
		Central:        r.Central,
		InterfaceID:    r.InterfaceID,
		ChannelAddress: r.ChannelAddress,
		DeviceAddress:  r.DeviceAddress,
		Parameter:      r.Parameter,
		SensorID:       r.SensorID,
		Name:           r.Name,
		SensorType:     string(r.SensorType),
		Class:          string(r.Class),
		At:             time.UnixMilli(r.AtMS).UTC(),
	}
}

// msToTimePtr renders a Unix-millisecond stamp as a pointer so a
// missing occurrence is omitted from the payload rather than becoming
// the 1970 epoch — the same rule the REST message DTOs follow.
func msToTimePtr(ms int64) *time.Time {
	if ms == 0 {
		return nil
	}
	t := time.UnixMilli(ms).UTC()
	return &t
}
