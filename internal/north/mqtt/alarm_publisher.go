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
	"github.com/SukramJ/openccu-loom/internal/alarm/engine"
	"github.com/SukramJ/openccu-loom/internal/central/events"
	"github.com/SukramJ/openccu-loom/internal/i18n"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
)

// Non-retained alarm event-topic types (docs/alarm-concept.md §13.3).
const (
	alarmEventTypeTrigger     = "TRIGGER"
	alarmEventTypeSilenced    = "SILENCED"
	alarmEventTypeFailedToArm = "FAILED_TO_ARM"
	alarmEventTypeDisarmed    = "DISARMED"
	alarmEventTypeArmed       = "ARMED"
)

// alarmMasterNameKey resolves the master panel's display name; the
// fallback is used when the catalogue lacks the key.
const (
	alarmMasterNameKey      = "discovery.alarm_system"
	alarmMasterNameFallback = "Alarm system"
)

// alarmEventPayload is the JSON body published on the non-retained
// `<base>/alarm/<area>/event` topic (docs/alarm-concept.md §13.3).
type alarmEventPayload struct {
	Type        string   `json:"type"`
	AreaID      string   `json:"area_id"`
	AreaName    string   `json:"area_name,omitempty"`
	ChangedBy   string   `json:"changed_by,omitempty"`
	Mode        string   `json:"mode,omitempty"`
	OpenSensors []string `json:"open_sensors,omitempty"`
	DelayS      int      `json:"delay_s,omitempty"`
}

// alarmEventMsg is one queued non-retained event-topic publish.
type alarmEventMsg struct {
	area string
	body []byte
}

// AlarmMQTTPublisher mirrors the daemon-level alarm engine onto the MQTT
// alarm plane: a retained HA alarm_control_panel discovery config, a
// retained plain-token state, and a retained availability flag per area
// (plus an aggregate master panel once two or more areas exist), and a
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
	knownAreas  map[string]bool   // areaID → retained discovery+state currently published
	masterKnown bool              // aggregate master panel currently published
	names       map[string]string // areaID → last-seen display name (for event JSON)

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
		knownAreas:  map[string]bool{},
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
// The readiness subscription is the area-set-change trigger: the engine
// fires a readiness event for every area on start and on reload, which is
// the only signal a freshly-loaded disarmed area emits — the worker turns
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
// area. The composition root wires this as the master-arm failure hook so
// a best-effort master arm reports each area it could not arm, with the
// blocking sensors. Safe to call from any goroutine.
func (p *AlarmMQTTPublisher) PublishFailedToArm(areaID, areaName string, mode hmenum.AlarmMode, blockers []string) {
	name := areaName
	if name == "" {
		name = p.lookupName(areaID)
	}
	p.enqueueEvent(areaID, alarmEventPayload{
		Type:        alarmEventTypeFailedToArm,
		AreaID:      areaID,
		AreaName:    name,
		Mode:        string(mode),
		OpenSensors: blockers,
	})
}

// --- bus handlers (run on the engine goroutine — lock-free only) ---

func (p *AlarmMQTTPublisher) onStateChanged(e hmevent.AlarmStateChangedEvent) {
	p.rememberName(e.AreaID, e.AreaName)
	switch e.To {
	case hmenum.AlarmAreaStateArmed:
		p.enqueueEvent(e.AreaID, alarmEventPayload{
			Type: alarmEventTypeArmed, AreaID: e.AreaID, AreaName: e.AreaName,
			ChangedBy: e.ChangedBy, Mode: string(e.Mode),
		})
	case hmenum.AlarmAreaStateDisarmed:
		p.enqueueEvent(e.AreaID, alarmEventPayload{
			Type: alarmEventTypeDisarmed, AreaID: e.AreaID, AreaName: e.AreaName,
			ChangedBy: e.ChangedBy,
		})
	default:
		// arming/pending/triggered surface via the state topic; no event entry.
	}
	p.signalReconcile()
}

func (p *AlarmMQTTPublisher) onTriggered(e hmevent.AlarmTriggeredEvent) {
	p.rememberName(e.AreaID, e.AreaName)
	pay := alarmEventPayload{
		Type: alarmEventTypeTrigger, AreaID: e.AreaID, AreaName: e.AreaName, Mode: string(e.Mode),
	}
	if e.SensorName != "" {
		pay.OpenSensors = []string{e.SensorName}
	}
	p.enqueueEvent(e.AreaID, pay)
	p.signalReconcile()
}

func (p *AlarmMQTTPublisher) onJournalAppended(e hmevent.AlarmJournalAppendedEvent) {
	if e.Class == hmenum.AlarmJournalClassSilence && e.Event == "silenced" {
		p.enqueueEvent(e.AreaID, alarmEventPayload{
			Type: alarmEventTypeSilenced, AreaID: e.AreaID, AreaName: p.lookupName(e.AreaID),
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
// area's deletion and the master panel's 2-to-1 retraction emit no
// state transition, only a panel event — without this subscription the
// retained discovery/state/availability of a deleted area would ghost
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
// current area and the master panel, and retracts anything that vanished.
// It runs on the single worker goroutine, so reading engine snapshots and
// mutating knownAreas/masterKnown needs no lock. p.mu guards only the
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
	snaps := eng.Areas()

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
	for id := range p.knownAreas {
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
		armReq, disarmReq := p.areaCodePolicy(ctx, s.ID)
		item := BuildAlarmPanelDiscovery(base, s.ID, s.Name, modes, false, armReq, disarmReq)
		if err := b.PublishAlarmDiscovery(ctx, item); err != nil {
			p.logger.Warn("mqtt.alarm.discovery", slog.String("area", s.ID), slog.String("err", err.Error()))
		}
		if err := b.PublishAlarmAvailability(ctx, alarmAvailabilityTopic(base, s.ID), healthy); err != nil {
			p.logger.Warn("mqtt.alarm.availability", slog.String("area", s.ID), slog.String("err", err.Error()))
		}
		token := alarmpanel.StateToken(s.State, s.Mode)
		tokens = append(tokens, token)
		if err := b.PublishAlarmState(ctx, alarmStateTopic(base, s.ID), token); err != nil {
			p.logger.Warn("mqtt.alarm.state", slog.String("area", s.ID), slog.String("err", err.Error()))
		}
		p.knownAreas[s.ID] = true
	}

	for id := range p.knownAreas {
		if current[id] {
			continue
		}
		p.retractPanel(ctx, b, base, id)
		delete(p.knownAreas, id)
	}

	if len(snaps) >= 2 {
		modes := modesFromSet(union)
		// The master panel arms/disarms every area at once; a single code
		// gate cannot express the union of per-area policies, so it stays
		// code-free (code entry happens on the individual area panels).
		item := BuildAlarmPanelDiscovery(base, alarmMasterArea, p.masterName(), modes, true, false, false)
		if err := b.PublishAlarmDiscovery(ctx, item); err != nil {
			p.logger.Warn("mqtt.alarm.discovery", slog.String("area", alarmMasterArea), slog.String("err", err.Error()))
		}
		if err := b.PublishAlarmAvailability(ctx, alarmAvailabilityTopic(base, alarmMasterArea), healthy); err != nil {
			p.logger.Warn("mqtt.alarm.availability", slog.String("area", alarmMasterArea), slog.String("err", err.Error()))
		}
		if err := b.PublishAlarmState(ctx, alarmStateTopic(base, alarmMasterArea), alarmpanel.MasterStateToken(tokens)); err != nil {
			p.logger.Warn("mqtt.alarm.state", slog.String("area", alarmMasterArea), slog.String("err", err.Error()))
		}
		p.masterKnown = true
	} else if p.masterKnown {
		p.retractPanel(ctx, b, base, alarmMasterArea)
		p.masterKnown = false
	}
}

// retractPanel clears the retained discovery, state, and availability of a
// panel that no longer exists (empty payloads delete the retained
// messages). Runs on the worker goroutine.
func (p *AlarmMQTTPublisher) retractPanel(ctx context.Context, b *Bridge, base, area string) {
	if err := b.RetractAlarmDiscovery(ctx, string(HAComponentAlarmControlPanel), alarmDiscoveryNodeID, area); err != nil {
		p.logger.Warn("mqtt.alarm.retract_discovery", slog.String("area", area), slog.String("err", err.Error()))
	}
	_ = b.RetractAlarmTopic(ctx, alarmStateTopic(base, area))
	_ = b.RetractAlarmTopic(ctx, alarmAvailabilityTopic(base, area))
}

func (p *AlarmMQTTPublisher) publishEventMsg(msg alarmEventMsg) {
	b := p.wiring.Bridge()
	if b == nil {
		return
	}
	topic := alarmEventTopic(b.topics.Base, msg.area)
	if err := b.PublishAlarmEvent(context.Background(), topic, msg.body); err != nil {
		p.logger.Warn("mqtt.alarm.event.publish", slog.String("area", msg.area), slog.String("err", err.Error()))
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
	// <-p.doneCh), so knownAreas/masterKnown are quiescent — no lock needed.
	for id := range p.knownAreas {
		_ = b.PublishAlarmAvailability(ctx, alarmAvailabilityTopic(base, id), false)
	}
	if p.masterKnown {
		_ = b.PublishAlarmAvailability(ctx, alarmAvailabilityTopic(base, alarmMasterArea), false)
	}
}

// --- helpers ---

func (p *AlarmMQTTPublisher) signalReconcile() {
	select {
	case p.reconcileCh <- struct{}{}:
	default:
	}
}

func (p *AlarmMQTTPublisher) enqueueEvent(area string, pay alarmEventPayload) {
	body, err := json.Marshal(pay)
	if err != nil {
		p.logger.Warn("mqtt.alarm.event.marshal", slog.String("area", area), slog.String("err", err.Error()))
		return
	}
	select {
	case p.eventCh <- alarmEventMsg{area: area, body: body}:
	default:
		p.logger.Warn("mqtt.alarm.event.drop", slog.String("area", area))
	}
}

func (p *AlarmMQTTPublisher) rememberName(areaID, name string) {
	if name == "" {
		return
	}
	p.mu.Lock()
	p.names[areaID] = name
	p.mu.Unlock()
}

func (p *AlarmMQTTPublisher) lookupName(areaID string) string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.names[areaID]
}

func (p *AlarmMQTTPublisher) masterName() string {
	if p.tr != nil {
		if name := p.tr.T(p.locale, alarmMasterNameKey); name != "" && name != alarmMasterNameKey {
			return name
		}
	}
	return alarmMasterNameFallback
}

// areaCodePolicy resolves the per-area arm/disarm code requirement for
// the discovery flags, mirroring the requirement the engine will
// actually enforce (docs/alarm-concept.md §11/§13.3): the policy half
// comes from the area config (RequireDisarm defaults to required when
// nil), and the "codes exist" half from the codes facade — an area
// without an applicable enabled pin code advertises no requirement
// (the engine passes an empty code through), while an area with one
// advertises it even off the nil default, so HA prompts for the code
// the engine is going to demand. Advertising either half alone would
// trap disarm: requirement-without-codes leaves HA prompting for a
// code that cannot exist, codes-without-requirement makes HA send a
// bare DISARM the engine refuses. A missing area, parse error, or
// absent store degrades to no requirement.
func (p *AlarmMQTTPublisher) areaCodePolicy(ctx context.Context, areaID string) (armReq, disarmReq bool) {
	stores := p.svc.Stores()
	if stores == nil || stores.Areas == nil {
		return false, false
	}
	row, ok, err := stores.Areas.Get(ctx, areaID)
	if err != nil || !ok {
		return false, false
	}
	cfg, err := engine.ParseAreaConfig(row.ConfigJSON)
	if err != nil {
		return false, false
	}
	armReq = cfg.CodePolicy.RequireArm
	disarmReq = cfg.CodePolicy.RequireDisarm == nil || *cfg.CodePolicy.RequireDisarm
	if armReq || disarmReq {
		facade := p.svc.Codes()
		hasPIN := facade != nil && facade.HasPINCodes(ctx, areaID)
		armReq = armReq && hasPIN
		disarmReq = disarmReq && hasPIN
	}
	return armReq, disarmReq
}

// modesFromReadiness extracts the configured protection modes of an area
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
// area (or the master panel).
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
