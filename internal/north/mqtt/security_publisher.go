// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package mqtt

import (
	"context"
	"encoding/json"
	"log/slog"
	"sync"

	"github.com/SukramJ/openccu-loom/internal/central/events"
	"github.com/SukramJ/openccu-loom/internal/i18n"
	"github.com/SukramJ/openccu-loom/internal/model/security"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
)

// SecuritySnapshotSource is the read side of the domain the publisher
// needs. *security.Service satisfies it.
//
// It deliberately does not expose the duress-visibility policy. The
// domain applies that policy once, where the report is created, and
// hands the plane a Retainable flag. Under the hidden level no report
// reaches this plane at all, so there is nothing here to gate — and a
// second copy of the rule is a second chance to get it wrong.
type SecuritySnapshotSource interface {
	Snapshot() security.Snapshot
}

// securityMsg is one queued publish.
type securityMsg struct {
	topic    string
	payload  []byte
	retained bool
}

// SecurityMQTTPublisher mirrors the Security & Safety domain onto the
// MQTT plane.
//
// The retained/non-retained split is the load-bearing design decision,
// not a performance choice:
//
//   - Aggregates (state, alarm, problem, health, class/*, zone/*) are
//     retained, so a consumer that connects later sees the truth
//     immediately instead of waiting for the next change.
//   - The two event topics are NOT retained. A consumer ignores retained
//     payloads on an event topic entirely, and a retained alarm event
//     would re-fire every automation on every reconnect.
//   - last_alarm / last_fault are retained precisely because the event
//     entities are not: after a consumer restart they are the only
//     record of what happened.
//
// Handlers run on the domain's bus goroutine, so they only enqueue; a
// single worker performs every broker publish off that path.
type SecurityMQTTPublisher struct {
	src    SecuritySnapshotSource
	wiring *Wiring
	logger *slog.Logger
	tr     *i18n.Catalogs
	locale string
	// configURL is the operator-facing deep link on the device card.
	configURL string

	mu      sync.Mutex
	started bool
	// knownClasses / knownZones track what carries retained discovery
	// right now, so a class that loses its last source or a zone that is
	// deleted gets retracted rather than lingering.
	knownClasses map[hmenum.SecurityClass]bool
	knownZones   map[string]bool

	unsubs []func()
	msgCh  chan securityMsg
	stopCh chan struct{}
	doneCh chan struct{}
}

// NewSecurityMQTTPublisher binds a publisher to the domain and the MQTT
// wiring. A nil source or wiring makes Start a no-op.
func NewSecurityMQTTPublisher(src SecuritySnapshotSource, wiring *Wiring, locale, configURL string, logger *slog.Logger) *SecurityMQTTPublisher {
	if logger == nil {
		logger = slog.Default()
	}
	p := &SecurityMQTTPublisher{
		src:          src,
		wiring:       wiring,
		logger:       logger,
		locale:       locale,
		configURL:    configURL,
		knownClasses: map[hmenum.SecurityClass]bool{},
		knownZones:   map[string]bool{},
		msgCh:        make(chan securityMsg, 128),
		stopCh:       make(chan struct{}),
		doneCh:       make(chan struct{}),
	}
	if cat, err := i18n.NewCatalogs(); err == nil {
		p.tr = cat
	}
	return p
}

// Start subscribes the domain bus and begins publishing.
func (p *SecurityMQTTPublisher) Start(bus *events.Bus) {
	if p == nil || p.src == nil || p.wiring == nil || bus == nil {
		return
	}
	p.mu.Lock()
	if p.started {
		p.mu.Unlock()
		return
	}
	p.started = true
	p.unsubs = []func(){
		events.Subscribe(bus, p.onStateChanged),
		events.Subscribe(bus, p.onClassChanged),
		events.Subscribe(bus, p.onZoneChanged),
		events.Subscribe(bus, p.onFaultChanged),
		events.Subscribe(bus, p.onNotification),
	}
	p.mu.Unlock()

	go p.run()
	p.reconcile()
}

// Stop drops the subscriptions and the worker.
func (p *SecurityMQTTPublisher) Stop() {
	if p == nil {
		return
	}
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
}

// run drains the publish queue.
func (p *SecurityMQTTPublisher) run() {
	defer close(p.doneCh)
	ctx := context.Background()
	for {
		select {
		case <-p.stopCh:
			return
		case m := <-p.msgCh:
			p.publish(ctx, m)
		}
	}
}

func (p *SecurityMQTTPublisher) publish(ctx context.Context, m securityMsg) {
	b := p.wiring.Bridge()
	if b == nil {
		return
	}
	qos := b.cfg.QoS.State
	if !m.retained {
		// An event is a moment, not a state: at-most-once delivery is
		// the right trade, and a re-delivered alarm event would re-fire
		// every automation subscribed to it.
		qos = 0
	}
	if err := b.client.Publish(ctx, m.topic, m.payload, qos, m.retained); err != nil {
		p.logger.Error("security mqtt publish failed", "topic", m.topic, "error", err)
	}
}

// enqueue queues a publish, dropping the oldest rather than blocking
// the domain's bus goroutine if the broker stalls.
func (p *SecurityMQTTPublisher) enqueue(m securityMsg) {
	select {
	case p.msgCh <- m:
	default:
		p.logger.Warn("security mqtt queue full; dropping message", "topic", m.topic)
	}
}

// --- bus handlers (run on the domain goroutine — enqueue only) ---

func (p *SecurityMQTTPublisher) onStateChanged(hmevent.SecurityStateChangedEvent) { p.reconcile() }

func (p *SecurityMQTTPublisher) onClassChanged(hmevent.SecurityClassChangedEvent) { p.reconcile() }

func (p *SecurityMQTTPublisher) onZoneChanged(hmevent.SecurityZoneChangedEvent) { p.reconcile() }

func (p *SecurityMQTTPublisher) onFaultChanged(e hmevent.SecurityFaultChangedEvent) {
	p.reconcile()
	body, err := json.Marshal(map[string]any{
		"event_type": verbForFault(e),
		"fault_id":   e.FaultID,
		"class":      string(e.Class),
		"reason":     string(e.Reason),
		"severity":   string(e.Severity),
		"source":     securitySourcePayload(e.Source),
		"since_ms":   e.SinceMS,
		"open_count": e.OpenCount,
	})
	if err != nil {
		return
	}
	p.enqueue(securityMsg{topic: securityStateTopic(p.base(), "fault"), payload: body})
}

// onNotification publishes the rendered report.
//
// The duress policy is enforced here rather than at the source: the
// domain renders the report either way, and each plane decides what it
// is allowed to carry. A report the policy marks non-retainable is
// still delivered on the event topic — it must reach a phone — but is
// kept out of last_alarm, which would leave it readable on a screen an
// attacker could reach long afterwards.
func (p *SecurityMQTTPublisher) onNotification(e hmevent.SecurityNotificationEvent) {
	body, err := json.Marshal(securityNotificationPayload(e))
	if err != nil {
		return
	}
	topic := "event"
	if e.Fault {
		topic = "fault"
	}
	p.enqueue(securityMsg{topic: securityStateTopic(p.base(), topic), payload: body})

	// Retainability is decided once, by the domain, according to the
	// duress-visibility policy. The plane honours the flag rather than
	// re-deriving the policy — a rule implemented twice is a rule that
	// will eventually disagree with itself.
	if !e.Retainable {
		return
	}
	key := "last_alarm"
	if e.Fault {
		key = "last_fault"
	}
	p.enqueue(securityMsg{topic: securityStateTopic(p.base(), key), payload: body, retained: true})
}

// verbForFault maps a fault transition onto an announced event type.
func verbForFault(e hmevent.SecurityFaultChangedEvent) string {
	switch {
	case e.Acknowledged:
		return string(hmenum.SecurityVerbRaised)
	case e.Open:
		return string(hmenum.SecurityVerbRaised)
	default:
		return string(hmenum.SecurityVerbCleared)
	}
}

func (p *SecurityMQTTPublisher) base() string {
	if b := p.wiring.Bridge(); b != nil {
		return b.cfg.Base
	}
	return ""
}

// OnBrokerConnect re-seeds the retained half of the plane after a
// broker (re)connect.
//
// Only the retained aggregates are rewritten. The event topics are
// deliberately NOT replayed: an alarm that fired an hour ago must not
// re-fire every automation because the broker restarted. That
// distinction is the reason the two halves are separated at all.
func (p *SecurityMQTTPublisher) OnBrokerConnect() {
	if p == nil {
		return
	}
	p.mu.Lock()
	// Forget what is declared so discovery is rewritten too: the broker
	// dropped the retained configs along with the states.
	p.knownClasses = map[hmenum.SecurityClass]bool{}
	p.knownZones = map[string]bool{}
	p.mu.Unlock()
	p.reconcile()
}
