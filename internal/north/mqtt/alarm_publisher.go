// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package mqtt

import (
	"context"
	"encoding/json"
	"log/slog"
	"sync"

	"github.com/SukramJ/openccu-loom/internal/model/alarmpanel"

	"github.com/SukramJ/openccu-loom/internal/alarm"
	"github.com/SukramJ/openccu-loom/internal/central/events"
	"github.com/SukramJ/openccu-loom/internal/i18n"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
)

// Non-retained alarm event-topic types (notes/concepts/alarm-concept.md §13.3).
const (
	alarmEventTypeTrigger      = "TRIGGER"
	alarmEventTypeSilenced     = "SILENCED"
	alarmEventTypeFailedToArm  = "FAILED_TO_ARM"
	alarmEventTypeDisarmed     = "DISARMED"
	alarmEventTypeArmed        = "ARMED"
	alarmEventTypeNotification = "NOTIFICATION"
)

// alarmMasterNameKey resolves the master panel's display name; the
// fallback is used when the catalogue lacks the key.
const (
	alarmMasterNameKey      = "discovery.alarm_system"
	alarmMasterNameFallback = "Alarm system"
)

// alarmEventPayload is the JSON body published on the non-retained
// `<base>/alarm/<zone>/event` topic (notes/concepts/alarm-concept.md §13.3).
type alarmEventPayload struct {
	Type        string   `json:"type"`
	ZoneID      string   `json:"zone_id"`
	ZoneName    string   `json:"zone_name,omitempty"`
	ChangedBy   string   `json:"changed_by,omitempty"`
	Mode        string   `json:"mode,omitempty"`
	OpenSensors []string `json:"open_sensors,omitempty"`
	DelayS      int      `json:"delay_s,omitempty"`
	// Output carries the enrolled notification output's name (or ID
	// when unnamed) on NOTIFICATION events.
	Output string `json:"output,omitempty"`
	// Sources carries the full identity of every contributing data
	// point on TRIGGER events: address, parameter, central and the
	// hazard class. OpenSensors stays the human-readable short form.
	Sources []alarmSourcePayload `json:"sources,omitempty"`
}

// alarmSourcePayload is one contributing data point on the alarm event
// topic. The field names match the Security & Safety source shape so a
// consumer parses one form across both planes.
type alarmSourcePayload struct {
	Ref            string `json:"ref"`
	Central        string `json:"central,omitempty"`
	InterfaceID    string `json:"interface_id,omitempty"`
	ChannelAddress string `json:"channel_address,omitempty"`
	DeviceAddress  string `json:"device_address,omitempty"`
	Parameter      string `json:"parameter,omitempty"`
	SensorID       string `json:"sensor_id,omitempty"`
	Name           string `json:"name,omitempty"`
	SensorType     string `json:"sensor_type,omitempty"`
	Class          string `json:"class,omitempty"`
	AtMS           int64  `json:"at_ms,omitempty"`
}

// alarmSourcePayloads projects the domain refs onto the wire shape.
func alarmSourcePayloads(refs []hmevent.SecuritySourceRef) []alarmSourcePayload {
	if len(refs) == 0 {
		return nil
	}
	out := make([]alarmSourcePayload, 0, len(refs))
	for i := range refs {
		r := &refs[i]
		out = append(out, alarmSourcePayload{
			Ref: r.Ref, Central: r.Central, InterfaceID: r.InterfaceID,
			ChannelAddress: r.ChannelAddress, DeviceAddress: r.DeviceAddress,
			Parameter: r.Parameter, SensorID: r.SensorID, Name: r.Name,
			SensorType: string(r.SensorType), Class: string(r.Class), AtMS: r.AtMS,
		})
	}
	return out
}

// alarmSourceNames returns the display names of the refs, falling back
// to the channel address when a source has no name.
func alarmSourceNames(refs []hmevent.SecuritySourceRef) []string {
	if len(refs) == 0 {
		return nil
	}
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

// alarmEventMsg is one queued non-retained event-topic publish.
type alarmEventMsg struct {
	zone string
	body []byte
}

// AlarmMQTTPublisher mirrors the daemon-level alarm engine onto the MQTT
// alarm plane: a retained HA alarm_control_panel discovery config, a
// retained plain-token state, and a retained availability flag per zone
// (plus an aggregate master panel once two or more zones exist), and a
// non-retained JSON event stream. It follows the [SystemStatusPublisher]
// shape — Start subscribes the alarm bus, Stop drops the subscriptions.
//
// Alarm events are published on the alarm event bus while the engine
// holds its lock, so the subscription handlers must never call back into
// the engine (that would re-enter the engine mutex and deadlock). The
// handlers therefore do only lock-free work — remember a name, enqueue an
// event body, raise a reconcile flag — and a single background goroutine
// performs every engine read (snapshot fan-out) and broker publish off
// that hot path.
type AlarmMQTTPublisher struct {
	svc    *alarm.Service
	wiring *Wiring
	logger *slog.Logger
	tr     *i18n.Catalogs
	locale string

	mu          sync.Mutex
	started     bool
	healthy     bool
	knownZones  map[string]bool   // zoneID → retained discovery+state currently published
	masterKnown bool              // aggregate master panel currently published
	names       map[string]string // zoneID → last-seen display name (for event JSON)

	unsubs      []func()
	reconcileCh chan struct{}
	eventCh     chan alarmEventMsg
	stopCh      chan struct{}
	doneCh      chan struct{}
}

// NewAlarmMQTTPublisher binds a publisher to the alarm service and the
// MQTT wiring. A nil service or wiring makes Start a no-op.
func NewAlarmMQTTPublisher(svc *alarm.Service, wiring *Wiring, logger *slog.Logger) *AlarmMQTTPublisher {
	if logger == nil {
		logger = slog.Default()
	}
	p := &AlarmMQTTPublisher{
		svc:         svc,
		wiring:      wiring,
		logger:      logger,
		healthy:     true,
		knownZones:  map[string]bool{},
		names:       map[string]string{},
		reconcileCh: make(chan struct{}, 1),
		eventCh:     make(chan alarmEventMsg, 64),
		stopCh:      make(chan struct{}),
		doneCh:      make(chan struct{}),
	}
	if cat, err := i18n.NewCatalogs(); err == nil {
		p.tr = cat
	}
	if wiring != nil {
		if b := wiring.Bridge(); b != nil {
			p.locale = b.cfg.Locale
		}
	}
	return p
}

// Start subscribes to the alarm bus and launches the reconcile worker.
// The readiness subscription is the zone-set-change trigger: the engine
// fires a readiness event for every zone on start and on reload, which is
// the only signal a freshly-loaded disarmed zone emits — the worker turns
// it into the initial retained discovery + state publish. Safe to call
// once; subsequent calls are no-ops.
func (p *AlarmMQTTPublisher) Start() {
	if p.svc == nil || p.wiring == nil {
		return
	}
	bus := p.svc.Bus()
	if bus == nil {
		return
	}
	p.mu.Lock()
	if p.started {
		p.mu.Unlock()
		return
	}
	p.started = true
	p.mu.Unlock()

	p.unsubs = append(
		p.unsubs,
		events.Subscribe(bus, p.onStateChanged),
		events.Subscribe(bus, p.onTriggered),
		events.Subscribe(bus, p.onNotification),
		events.Subscribe(bus, p.onJournalAppended),
		events.Subscribe(bus, p.onReadinessChanged),
		events.Subscribe(bus, p.onHealthChanged),
		events.Subscribe(bus, p.onPanelChanged),
		events.Subscribe(bus, p.onCodesChanged),
	)
	go p.run()
	p.signalReconcile()
}

// Stop drops the subscriptions, stops the worker, and marks every
// published panel offline so HA renders the alarm surface unavailable
// once the daemon exits.
func (p *AlarmMQTTPublisher) Stop() {
	p.mu.Lock()
	if !p.started {
		p.mu.Unlock()
		return
	}
	p.started = false
	unsubs := p.unsubs
	p.unsubs = nil
	p.mu.Unlock()

	for _, u := range unsubs {
		u()
	}
	close(p.stopCh)
	<-p.doneCh
	p.publishOfflineAll()
}

// PublishFailedToArm emits a non-retained FAILED_TO_ARM event for one
// zone. The composition root wires this as the master-arm failure hook so
// a best-effort master arm reports each zone it could not arm, with the
// blocking sensors. Safe to call from any goroutine.
func (p *AlarmMQTTPublisher) PublishFailedToArm(zoneID, zoneName string, mode hmenum.AlarmMode, blockers []hmevent.AlarmBlockerDetail) {
	name := zoneName
	if name == "" {
		name = p.lookupName(zoneID)
	}
	refs := make([]hmevent.SecuritySourceRef, 0, len(blockers))
	for i := range blockers {
		ref := blockers[i].Source
		if ref.Name == "" {
			ref.Name = blockers[i].Name
		}
		refs = append(refs, ref)
	}
	p.enqueueEvent(zoneID, alarmEventPayload{
		Type:     alarmEventTypeFailedToArm,
		ZoneID:   zoneID,
		ZoneName: name,
		Mode:     string(mode),
		// Display names, matching the TRIGGER event. Before this the
		// two events disagreed: TRIGGER carried names, FAILED_TO_ARM
		// carried opaque sensor row IDs.
		OpenSensors: alarmSourceNames(refs),
		Sources:     alarmSourcePayloads(refs),
	})
}

// --- bus handlers (run on the engine goroutine — lock-free only) ---

func (p *AlarmMQTTPublisher) onStateChanged(e hmevent.AlarmStateChangedEvent) {
	p.rememberName(e.ZoneID, e.ZoneName)
	switch e.To {
	case hmenum.AlarmZoneStateArmed:
		p.enqueueEvent(e.ZoneID, alarmEventPayload{
			Type: alarmEventTypeArmed, ZoneID: e.ZoneID, ZoneName: e.ZoneName,
			ChangedBy: e.ChangedBy, Mode: string(e.Mode),
		})
	case hmenum.AlarmZoneStateDisarmed:
		p.enqueueEvent(e.ZoneID, alarmEventPayload{
			Type: alarmEventTypeDisarmed, ZoneID: e.ZoneID, ZoneName: e.ZoneName,
			ChangedBy: e.ChangedBy,
		})
	default:
		// arming/pending/triggered surface via the state topic; no event entry.
	}
	p.signalReconcile()
}

func (p *AlarmMQTTPublisher) onTriggered(e hmevent.AlarmTriggeredEvent) {
	p.rememberName(e.ZoneID, e.ZoneName)
	pay := alarmEventPayload{
		Type: alarmEventTypeTrigger, ZoneID: e.ZoneID, ZoneName: e.ZoneName, Mode: string(e.Mode),
		Sources: alarmSourcePayloads(e.Sources),
	}
	// Prefer the accumulated source list: a second detector firing
	// during the same incident belongs in open_sensors too. The single
	// headline name remains the fallback for causes without a source.
	if names := alarmSourceNames(e.Sources); len(names) > 0 {
		pay.OpenSensors = names
	} else if e.SensorName != "" {
		pay.OpenSensors = []string{e.SensorName}
	}
	p.enqueueEvent(e.ZoneID, pay)
	p.signalReconcile()
}

// onNotification publishes one enrolled notification output's fire
// signal on the zone's event topic; outputs that opted out of the
// MQTT plane are skipped.
func (p *AlarmMQTTPublisher) onNotification(e hmevent.AlarmNotificationEvent) {
	if !e.MQTT {
		return
	}
	p.rememberName(e.ZoneID, e.ZoneName)
	name := e.OutputName
	if name == "" {
		name = e.OutputID
	}
	p.enqueueEvent(e.ZoneID, alarmEventPayload{
		Type: alarmEventTypeNotification, ZoneID: e.ZoneID, ZoneName: e.ZoneName,
		Mode: string(e.Mode), Output: name,
	})
}

func (p *AlarmMQTTPublisher) onJournalAppended(e hmevent.AlarmJournalAppendedEvent) {
	if e.Class == hmenum.AlarmJournalClassSilence && e.Event == "silenced" {
		p.enqueueEvent(e.ZoneID, alarmEventPayload{
			Type: alarmEventTypeSilenced, ZoneID: e.ZoneID, ZoneName: p.lookupName(e.ZoneID),
			ChangedBy: e.Actor,
		})
	}
	p.signalReconcile()
}

func (p *AlarmMQTTPublisher) onReadinessChanged(_ hmevent.AlarmReadinessChangedEvent) {
	p.signalReconcile()
}

// onPanelChanged reconciles on every entity-projection change. This is
// the lifecycle trigger the state events cannot provide: a disarmed
// zone's deletion and the master panel's 2-to-1 retraction emit no
// state transition, only a panel event — without this subscription the
// retained discovery/state/availability of a deleted zone would ghost
// in the broker forever.
func (p *AlarmMQTTPublisher) onPanelChanged(_ hmevent.AlarmPanelChangedEvent) {
	p.signalReconcile()
}

// onCodesChanged re-derives the retained discovery configs after a code
// mutation: the effective code_arm_required / code_disarm_required flags
// depend on whether an applicable enabled pin code exists, so creating,
// deleting, or toggling a code can flip them.
func (p *AlarmMQTTPublisher) onCodesChanged(hmevent.AlarmCodesChangedEvent) {
	p.signalReconcile()
}

// OnBrokerConnect re-seeds the retained alarm plane after a broker
// (re)connect: a broker restart wipes the retained store, and the
// initial connect may land after Start's one-shot reconcile — either
// way every panel would render unavailable in HA until the next alarm
// event, which a quiescent disarmed system might never produce.
func (p *AlarmMQTTPublisher) OnBrokerConnect() {
	p.signalReconcile()
}

func (p *AlarmMQTTPublisher) onHealthChanged(e hmevent.AlarmHealthChangedEvent) {
	p.mu.Lock()
	p.healthy = e.Healthy
	p.mu.Unlock()
	p.signalReconcile()
}

// --- worker goroutine ---

func (p *AlarmMQTTPublisher) run() {
	defer close(p.doneCh)
	for {
		select {
		case <-p.stopCh:
			return
		case <-p.reconcileCh:
			p.reconcile()
		case msg := <-p.eventCh:
			p.publishEventMsg(msg)
		}
	}
}

// reconcile publishes retained discovery + availability + state for every
// current zone and the master panel, and retracts anything that vanished.
// It runs on the single worker goroutine, so reading engine snapshots and
// mutating knownZones/masterKnown needs no lock. p.mu guards only the
// cross-goroutine fields (healthy, names) and is never held across broker
// I/O — the engine-goroutine handlers take p.mu, so holding it during a
// slow publish would stall the engine.
func (p *AlarmMQTTPublisher) reconcile() {
	b := p.wiring.Bridge()
	if b == nil {
		return
	}
	eng := p.svc.Engine()
	if eng == nil {
		return
	}
	ctx := context.Background()
	base := b.topics.Base
	snaps := eng.Zones()

	current := make(map[string]bool, len(snaps))
	for i := range snaps {
		current[snaps[i].ID] = true
	}

	// Refresh the shared name index under a brief lock, then release it
	// before any broker publish.
	p.mu.Lock()
	healthy := p.healthy
	for i := range snaps {
		p.names[snaps[i].ID] = snaps[i].Name
	}
	for id := range p.knownZones {
		if !current[id] {
			delete(p.names, id)
		}
	}
	p.mu.Unlock()

	tokens := make([]string, 0, len(snaps))
	union := map[hmenum.AlarmMode]bool{}
	for i := range snaps {
		s := snaps[i]
		modes := modesFromReadiness(s.Readiness)
		for _, m := range modes {
			union[m] = true
		}
		armReq, disarmReq := p.zoneCodePolicy(ctx, s.ID)
		item := BuildAlarmPanelDiscovery(base, s.ID, s.Name, modes, false, armReq, disarmReq)
		if err := b.PublishAlarmDiscovery(ctx, item); err != nil {
			p.logger.Warn("mqtt.alarm.discovery", slog.String("zone", s.ID), slog.String("err", err.Error()))
		}
		if err := b.PublishAlarmAvailability(ctx, alarmAvailabilityTopic(base, s.ID), healthy); err != nil {
			p.logger.Warn("mqtt.alarm.availability", slog.String("zone", s.ID), slog.String("err", err.Error()))
		}
		token := alarmpanel.StateToken(s.State, s.Mode)
		tokens = append(tokens, token)
		if err := b.PublishAlarmState(ctx, alarmStateTopic(base, s.ID), token); err != nil {
			p.logger.Warn("mqtt.alarm.state", slog.String("zone", s.ID), slog.String("err", err.Error()))
		}
		p.knownZones[s.ID] = true
	}

	for id := range p.knownZones {
		if current[id] {
			continue
		}
		p.retractPanel(ctx, b, base, id)
		delete(p.knownZones, id)
	}

	if len(snaps) >= 2 {
		modes := modesFromSet(union)
		// The master panel arms/disarms every zone at once; a single code
		// gate cannot express the union of per-zone policies, so it stays
		// code-free (code entry happens on the individual zone panels).
		item := BuildAlarmPanelDiscovery(base, alarmMasterZone, p.masterName(), modes, true, false, false)
		if err := b.PublishAlarmDiscovery(ctx, item); err != nil {
			p.logger.Warn("mqtt.alarm.discovery", slog.String("zone", alarmMasterZone), slog.String("err", err.Error()))
		}
		if err := b.PublishAlarmAvailability(ctx, alarmAvailabilityTopic(base, alarmMasterZone), healthy); err != nil {
			p.logger.Warn("mqtt.alarm.availability", slog.String("zone", alarmMasterZone), slog.String("err", err.Error()))
		}
		if err := b.PublishAlarmState(ctx, alarmStateTopic(base, alarmMasterZone), alarmpanel.MasterStateToken(tokens)); err != nil {
			p.logger.Warn("mqtt.alarm.state", slog.String("zone", alarmMasterZone), slog.String("err", err.Error()))
		}
		p.masterKnown = true
	} else if p.masterKnown {
		p.retractPanel(ctx, b, base, alarmMasterZone)
		p.masterKnown = false
	}
}

// retractPanel clears the retained discovery, state, and availability of a
// panel that no longer exists (empty payloads delete the retained
// messages). Runs on the worker goroutine.
func (p *AlarmMQTTPublisher) retractPanel(ctx context.Context, b *Bridge, base, zone string) {
	if err := b.RetractAlarmDiscovery(ctx, string(HAComponentAlarmControlPanel), alarmDiscoveryNodeID, zone); err != nil {
		p.logger.Warn("mqtt.alarm.retract_discovery", slog.String("zone", zone), slog.String("err", err.Error()))
	}
	_ = b.RetractAlarmTopic(ctx, alarmStateTopic(base, zone))
	_ = b.RetractAlarmTopic(ctx, alarmAvailabilityTopic(base, zone))
}

func (p *AlarmMQTTPublisher) publishEventMsg(msg alarmEventMsg) {
	b := p.wiring.Bridge()
	if b == nil {
		return
	}
	topic := alarmEventTopic(b.topics.Base, msg.zone)
	if err := b.PublishAlarmEvent(context.Background(), topic, msg.body); err != nil {
		p.logger.Warn("mqtt.alarm.event.publish", slog.String("zone", msg.zone), slog.String("err", err.Error()))
	}
}

func (p *AlarmMQTTPublisher) publishOfflineAll() {
	b := p.wiring.Bridge()
	if b == nil {
		return
	}
	ctx := context.Background()
	base := b.topics.Base
	// Called from Stop after the worker goroutine has joined (see Stop's
	// <-p.doneCh), so knownZones/masterKnown are quiescent — no lock needed.
	for id := range p.knownZones {
		_ = b.PublishAlarmAvailability(ctx, alarmAvailabilityTopic(base, id), false)
	}
	if p.masterKnown {
		_ = b.PublishAlarmAvailability(ctx, alarmAvailabilityTopic(base, alarmMasterZone), false)
	}
}

// --- helpers ---

func (p *AlarmMQTTPublisher) signalReconcile() {
	select {
	case p.reconcileCh <- struct{}{}:
	default:
	}
}

func (p *AlarmMQTTPublisher) enqueueEvent(zone string, pay alarmEventPayload) {
	body, err := json.Marshal(pay)
	if err != nil {
		p.logger.Warn("mqtt.alarm.event.marshal", slog.String("zone", zone), slog.String("err", err.Error()))
		return
	}
	select {
	case p.eventCh <- alarmEventMsg{zone: zone, body: body}:
	default:
		p.logger.Warn("mqtt.alarm.event.drop", slog.String("zone", zone))
	}
}

func (p *AlarmMQTTPublisher) rememberName(zoneID, name string) {
	if name == "" {
		return
	}
	p.mu.Lock()
	p.names[zoneID] = name
	p.mu.Unlock()
}

func (p *AlarmMQTTPublisher) lookupName(zoneID string) string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.names[zoneID]
}

func (p *AlarmMQTTPublisher) masterName() string {
	if p.tr != nil {
		if name := p.tr.T(p.locale, alarmMasterNameKey); name != "" && name != alarmMasterNameKey {
			return name
		}
	}
	return alarmMasterNameFallback
}

// zoneCodePolicy resolves the per-zone arm/disarm code requirement for
// the discovery flags. The derivation (zone-config policy half AND the
// "an applicable enabled pin code exists" half, notes/concepts/alarm-concept.md
// §11/§13.3) is the service's — the REST/WS panel entities carry the
// same flags, so the two surfaces can never diverge.
func (p *AlarmMQTTPublisher) zoneCodePolicy(ctx context.Context, zoneID string) (armReq, disarmReq bool) {
	return p.svc.EffectiveCodePolicy(ctx, zoneID)
}

// modesFromReadiness extracts the configured protection modes of an zone
// from its per-mode readiness map (the engine computes readiness for
// exactly the configured modes). Order is irrelevant — the discovery
// builder re-orders features canonically.
func modesFromReadiness(readiness map[hmenum.AlarmMode]hmevent.AlarmModeReadiness) []hmenum.AlarmMode {
	if len(readiness) == 0 {
		return nil
	}
	modes := make([]hmenum.AlarmMode, 0, len(readiness))
	for m := range readiness {
		modes = append(modes, m)
	}
	return modes
}

// modesFromSet collapses a mode set into a slice (master feature union).
func modesFromSet(set map[hmenum.AlarmMode]bool) []hmenum.AlarmMode {
	modes := make([]hmenum.AlarmMode, 0, len(set))
	for m := range set {
		modes = append(modes, m)
	}
	return modes
}

// --- Bridge alarm-plane publish primitives ---

// PublishAlarmDiscovery publishes a retained alarm_control_panel discovery
// config through the shared dedup path. An OK=false item is a no-op, and
// discovery-disabled bridges skip silently — mirrors [Bridge.PublishHubDiscovery].
func (b *Bridge) PublishAlarmDiscovery(ctx context.Context, item DiscoveryItem) error {
	if !item.OK || !b.cfg.HADiscoveryEnabled {
		return nil
	}
	return b.publishDiscovery(ctx, item.Component, item.NodeID, item.ObjectID, item.Payload)
}

// RetractAlarmDiscovery clears a retained alarm discovery config (empty
// retained payload) and forgets the topic so RepublishDiscovery does not
// resurrect it.
func (b *Bridge) RetractAlarmDiscovery(ctx context.Context, component, nodeID, objectID string) error {
	if !b.cfg.HADiscoveryEnabled {
		return nil
	}
	topic := b.topics.DiscoveryConfig(component, nodeID, objectID)
	b.mu.Lock()
	delete(b.declared, topic)
	b.mu.Unlock()
	return b.client.Publish(ctx, topic, nil, b.cfg.QoS.Discovery, true)
}

// PublishAlarmState publishes the retained plain HA state token for an
// zone (or the master panel).
func (b *Bridge) PublishAlarmState(ctx context.Context, topic, token string) error {
	return b.client.Publish(ctx, topic, []byte(token), b.cfg.QoS.State, true)
}

// PublishAlarmAvailability publishes the retained per-panel availability
// flag (online/offline).
func (b *Bridge) PublishAlarmAvailability(ctx context.Context, topic string, online bool) error {
	body := []byte("offline")
	if online {
		body = []byte("online")
	}
	return b.client.Publish(ctx, topic, body, QoS1, true)
}

// RetractAlarmTopic clears a retained alarm state or availability topic.
func (b *Bridge) RetractAlarmTopic(ctx context.Context, topic string) error {
	return b.client.Publish(ctx, topic, nil, b.cfg.QoS.State, true)
}

// PublishAlarmEvent publishes a non-retained JSON alarm event.
func (b *Bridge) PublishAlarmEvent(ctx context.Context, topic string, body []byte) error {
	return b.client.Publish(ctx, topic, body, QoS0, false)
}
