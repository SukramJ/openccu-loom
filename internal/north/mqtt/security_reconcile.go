// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package mqtt

import (
	"context"
	"encoding/json"
	"strconv"
	"time"

	"github.com/SukramJ/openccu-loom/internal/model/security"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
)

// maxAttributeSources bounds how many sources an attribute payload
// carries before it reports a count instead.
//
// A consumer's recorder discards state attributes past a size limit, so
// an unbounded list on a fleet-wide fault would silently lose the whole
// attribute set — worse than an explicitly truncated one. The list
// therefore truncates and says so.
//
// It says only that. The truncated payloads carry no route to the
// remainder: `link` exists on the rendered report, not on the attribute
// builders, so a consumer learns that a list was cut without learning
// where the rest is. Stated here rather than left implied, because the
// bound reads like a complete design otherwise.
const maxAttributeSources = 30

// reconcile republishes the retained half of the plane from a coherent
// snapshot.
//
// Everything retained is derived from one snapshot rather than from the
// event that triggered the reconcile, so the aggregate state and the
// class states can never disagree — a consumer reading "critical" with
// an empty smoke class would report a fire nobody can locate.
func (p *SecurityMQTTPublisher) reconcile() {
	if p == nil || p.src == nil {
		return
	}
	snap := p.src.Snapshot()
	base := p.base()
	if base == "" {
		return
	}
	p.enqueue(securityMsg{topic: securityAvailabilityTopic(base), payload: []byte("online"), retained: true})

	p.publishDiscoveryOnce(snap)

	p.enqueueJSON(securityStateTopic(base, "state"), string(snap.Severity), systemAttributes(snap))
	p.enqueueJSON(securityStateTopic(base, "alarm"), onOff(hazardActive(snap)), hazardAttributes(snap))
	p.enqueueJSON(securityStateTopic(base, "problem"), onOff(len(snap.Faults) > 0), faultAttributes(snap))
	p.enqueue(securityMsg{
		topic:    securityStateTopic(base, "health"),
		payload:  []byte(onOff(!snap.EngineHealthy)),
		retained: true,
	})

	for class := range snap.Classes {
		st := snap.Classes[class]
		p.enqueueJSON(securityClassTopic(base, class), onOff(st.Active), classAttributes(st))
	}
	for slug := range snap.Zones {
		z := snap.Zones[slug]
		p.enqueueJSON(securityZoneTopic(base, slug), strconv.Itoa(len(z.Sources)), zoneAttributes(z))
	}
	p.retractGone(snap)
}

// enqueueJSON publishes a state whose payload doubles as the attribute
// source: the state itself under `state`, the facets alongside. The
// discovery config points `value_template` at `state` and
// `json_attributes_topic` at the same topic, which is the pattern hub
// discovery already uses.
//
// Attributes are always an object, never a bare list — a consumer
// discards a non-object attribute payload outright.
func (p *SecurityMQTTPublisher) enqueueJSON(topic, state string, attrs map[string]any) {
	if attrs == nil {
		attrs = map[string]any{}
	}
	attrs["state"] = state
	buf, err := json.Marshal(attrs)
	if err != nil {
		p.logger.Error("security mqtt payload not serializable", "topic", topic, "error", err)
		return
	}
	p.enqueue(securityMsg{topic: topic, payload: buf, retained: true})
}

// publishDiscoveryOnce declares the entities the installation actually
// has, and only those.
//
// A class the index knows no source for is not declared at all: an
// installation without gas detectors should not carry a permanently-off
// gas alarm in its entity list.
func (p *SecurityMQTTPublisher) publishDiscoveryOnce(snap security.Snapshot) {
	b := p.wiring.Bridge()
	if b == nil {
		return
	}
	if !b.cfg.HADiscoveryEnabled {
		// The raw plane still publishes retained class and zone states,
		// so the known-sets have to be maintained regardless — they are
		// what lets retractGone evacuate a topic later. Gating them
		// behind discovery left un-evacuable retained topics piling up
		// in the documented raw-only deployment.
		p.mu.Lock()
		for class := range snap.Classes {
			p.knownClasses[class] = true
		}
		for slug := range snap.Zones {
			p.knownZones[slug] = true
		}
		p.mu.Unlock()
		return
	}
	base := b.topics.Base
	device := p.tr8("discovery.security_system", "Security & Safety")
	ctx := context.Background()

	system := securitySystemEntities(p.tr8)
	for i := range system {
		p.publishDiscovery(ctx, base, device, system[i])
	}
	for class := range snap.Classes {
		p.mu.Lock()
		known := p.knownClasses[class]
		p.knownClasses[class] = true
		p.mu.Unlock()
		if known {
			continue
		}
		p.publishDiscovery(ctx, base, device, securityClassEntity(base, class, p.tr8))
	}
	for slug := range snap.Zones {
		p.mu.Lock()
		known := p.knownZones[slug]
		p.knownZones[slug] = true
		p.mu.Unlock()
		if known {
			continue
		}
		p.publishDiscovery(ctx, base, device, securityZoneEntity(base, slug, snap.Zones[slug].Name, p.tr8))
	}
	// The plane has now declared, so its retained configs are eligible
	// for the orphan sweep. Before this point the sweep cannot tell an
	// orphan from an entity that has not been published yet.
	b.MarkPlaneDeclared(securityDiscoveryNodeID)
}

func (p *SecurityMQTTPublisher) publishDiscovery(ctx context.Context, base, device string, e securityEntity) {
	item := BuildSecurityDiscovery(base, device, p.configURL, e)
	if !item.OK {
		return
	}
	if b := p.wiring.Bridge(); b != nil {
		if err := b.PublishAlarmDiscovery(ctx, item); err != nil {
			p.logger.Error("security discovery publish failed", "object", item.ObjectID, "error", err)
		}
	}
}

// retractGone clears the retained discovery and state of a class that
// lost its last source or a zone that was deleted. Without it a deleted
// zone keeps a stale entity alive in every consumer indefinitely.
func (p *SecurityMQTTPublisher) retractGone(snap security.Snapshot) {
	b := p.wiring.Bridge()
	if b == nil {
		return
	}
	ctx := context.Background()
	base := b.topics.Base

	p.mu.Lock()
	var goneClasses []hmenum.SecurityClass
	for class := range p.knownClasses {
		if _, ok := snap.Classes[class]; !ok {
			goneClasses = append(goneClasses, class)
			delete(p.knownClasses, class)
		}
	}
	var goneZones []string
	for slug := range p.knownZones {
		if _, ok := snap.Zones[slug]; !ok {
			goneZones = append(goneZones, slug)
			delete(p.knownZones, slug)
		}
	}
	p.mu.Unlock()

	for _, class := range goneClasses {
		p.retract(ctx, b, string(HAComponentBinarySensor), "class_"+string(class),
			securityClassTopic(base, class))
	}
	for _, slug := range goneZones {
		p.retract(ctx, b, string(HAComponentSensor), "zone_"+slug, securityZoneTopic(base, slug))
	}
}

func (p *SecurityMQTTPublisher) retract(ctx context.Context, b *Bridge, component, objectID, stateTopic string) {
	if err := b.RetractAlarmDiscovery(ctx, component, securityDiscoveryNodeID, objectID); err != nil {
		p.logger.Error("security discovery retract failed", "object", objectID, "error", err)
	}
	// An empty retained payload evicts the state the consumer would
	// otherwise keep showing for an entity that no longer exists.
	p.enqueue(securityMsg{topic: stateTopic, payload: nil, retained: true})
}

// tr8 resolves a catalogue key with a fallback, so a missing entry
// renders readable English rather than the raw key.
func (p *SecurityMQTTPublisher) tr8(key, fallback string) string {
	if p.tr == nil {
		return fallback
	}
	if v := p.tr.T(p.locale, key); v != "" && v != key {
		return v
	}
	return fallback
}

// --- attribute builders ---

func systemAttributes(snap security.Snapshot) map[string]any {
	classes := map[string]any{}
	for c, st := range snap.Classes {
		classes[string(c)] = map[string]any{"active": st.Active, "count": len(st.Sources), "known": st.Known}
	}
	zones := map[string]any{}
	for slug := range snap.Zones {
		z := snap.Zones[slug]
		zones[slug] = map[string]any{
			"state": string(z.State), "mode": string(z.Mode),
			"triggered": z.State == hmenum.AlarmZoneStateTriggered,
		}
	}
	return map[string]any{
		"classes":        classes,
		"zones":          zones,
		"open_faults":    len(snap.Faults),
		"engine_healthy": snap.EngineHealthy,
	}
}

func hazardActive(snap security.Snapshot) bool {
	for c, st := range snap.Classes {
		if c.Hazard() && st.Active {
			return true
		}
	}
	return false
}

func hazardAttributes(snap security.Snapshot) map[string]any {
	var refs []hmevent.SecuritySourceRef
	byClass := map[string]any{}
	for c, st := range snap.Classes {
		if !c.Hazard() || !st.Active {
			continue
		}
		refs = append(refs, st.Sources...)
		byClass[string(c)] = sourceNamesOf(st.Sources)
	}
	attrs := sourcesAttribute(refs)
	attrs["by_class"] = byClass
	return attrs
}

func faultAttributes(snap security.Snapshot) map[string]any {
	total := len(snap.Faults)
	shown := snap.Faults
	truncated := false
	if total > maxAttributeSources {
		shown = shown[:maxAttributeSources]
		truncated = true
	}
	list := make([]map[string]any, 0, len(shown))
	for i := range shown {
		f := &shown[i]
		list = append(list, map[string]any{
			"id": f.ID, "class": string(f.Class), "reason": string(f.Reason),
			"severity": string(f.Severity), "since_ms": f.SinceMS,
			"acknowledged": f.AcknowledgedAtMS != 0,
			"source":       securitySourcePayload(f.Source),
		})
	}
	return map[string]any{"faults": list, "count": total, "truncated": truncated, "total": total}
}

func classAttributes(st security.ClassState) map[string]any {
	attrs := sourcesAttribute(st.Sources)
	attrs["known"] = st.Known
	attrs["centrals"] = st.Centrals
	attrs["since_ms"] = st.SinceMS
	// The grade the domain derived for this detection, so an automation
	// can branch on "is this worth waking someone" without re-deriving
	// the arm state the daemon already resolved.
	attrs["severity"] = string(st.Severity)
	return attrs
}

func zoneAttributes(z security.ZoneState) map[string]any {
	attrs := sourcesAttribute(z.Sources)
	byClass := map[string]any{}
	for c, names := range z.ByClass {
		byClass[string(c)] = names
	}
	attrs["by_class"] = byClass
	attrs["zone_id"] = z.ID
	attrs["zone_name"] = z.Name
	attrs["zone_state"] = string(z.State)
	attrs["mode"] = string(z.Mode)
	attrs["incident_id"] = z.IncidentID
	return attrs
}

// sourcesAttribute renders a bounded source list plus the names, and
// says explicitly when it truncated.
func sourcesAttribute(refs []hmevent.SecuritySourceRef) map[string]any {
	total := len(refs)
	shown := refs
	truncated := false
	if total > maxAttributeSources {
		shown = shown[:maxAttributeSources]
		truncated = true
	}
	list := make([]map[string]any, 0, len(shown))
	for i := range shown {
		list = append(list, securitySourcePayload(shown[i]))
	}
	return map[string]any{
		"sources":      list,
		"source_names": sourceNamesOf(shown),
		"count":        total,
		"truncated":    truncated,
		"total":        total,
	}
}

func sourceNamesOf(refs []hmevent.SecuritySourceRef) []string {
	out := make([]string, 0, len(refs))
	for i := range refs {
		switch {
		case refs[i].Name != "":
			out = append(out, refs[i].Name)
		case refs[i].ChannelAddress != "":
			out = append(out, refs[i].ChannelAddress)
		}
	}
	return out
}

func securitySourcePayload(r hmevent.SecuritySourceRef) map[string]any {
	return map[string]any{
		"ref": r.Ref, "central": r.Central, "interface_id": r.InterfaceID,
		"channel_address": r.ChannelAddress, "device_address": r.DeviceAddress,
		"parameter": r.Parameter, "sensor_id": r.SensorID, "name": r.Name,
		"sensor_type": string(r.SensorType), "class": string(r.Class), "at_ms": r.AtMS,
	}
}

func securityNotificationPayload(e hmevent.SecurityNotificationEvent) map[string]any {
	attrs := sourcesAttribute(e.Sources)
	attrs["event_type"] = string(e.Verb)
	attrs["class"] = string(e.Class)
	attrs["severity"] = string(e.Severity)
	attrs["subject"] = e.Subject
	attrs["message"] = e.Message
	attrs["i18n_key"] = e.I18nKey
	attrs["args"] = e.Args
	attrs["zone_id"] = e.ZoneID
	attrs["zone_slug"] = e.ZoneSlug
	attrs["zone_name"] = e.ZoneName
	attrs["mode"] = string(e.Mode)
	attrs["incident_id"] = e.IncidentID
	attrs["link"] = e.Link
	// RFC3339, not epoch milliseconds: the entity declares
	// device_class timestamp, which rejects a bare number.
	if e.AtMS != 0 {
		attrs["at"] = time.UnixMilli(e.AtMS).UTC().Format(time.RFC3339)
	}
	return attrs
}

func onOff(b bool) string {
	if b {
		return "ON"
	}
	return "OFF"
}
