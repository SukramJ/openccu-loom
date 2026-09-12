// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

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

// securityMsgKind selects which [Bridge] publisher a queued message goes
// through.
//
// The kind is carried rather than inferred from the payload, because
// "retained with an empty body" is a retraction and "retained with a
// body" is a state, and an availability marker is neither — it has its
// own delivery guarantee. Inferring would have made the three
// indistinguishable at the point the guarantee is chosen.
type securityMsgKind uint8

const (
	// securityMsgState is a retained aggregate: the plane's state,
	// alarm, problem, health, per-class and per-zone topics.
	securityMsgState securityMsgKind = iota
	// securityMsgEvent is a non-retained pulse on one of the two event
	// topics.
	securityMsgEvent
	// securityMsgRetract clears a retained topic whose entity no longer
	// exists.
	securityMsgRetract
	// securityMsgAvailability is the plane's single availability marker.
	securityMsgAvailability
)

// securityMsg is one queued publish.
type securityMsg struct {
	kind    securityMsgKind
	topic   string
	payload []byte
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
	// reported records that the domain has published at least one event,
	// which is the only signal that separates "this installation has no
	// classes or zones" from "the domain has not built its index yet".
	reported bool
	// knownClasses / knownZones track what carries retained discovery
	// right now, so a class that loses its last source or a zone that is
	// deleted gets retracted rather than lingering.
	knownClasses map[hmenum.SecurityClass]bool
	knownZones   map[string]bool

	unsubs []func()
	msgCh  chan securityMsg
	// reconcileCh coalesces reconcile requests. A handler raises the flag
	// and returns; the worker performs the snapshot read and every broker
	// publish the reconcile implies, so a broker that withholds its
	// acknowledgement can never stall the domain's bus goroutine.
	reconcileCh chan struct{}
	stopCh      chan struct{}
	doneCh      chan struct{}
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
		reconcileCh:  make(chan struct{}, 1),
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
	p.signalReconcile()
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
	p.reported = false
	unsubs := p.unsubs
	p.unsubs = nil
	p.mu.Unlock()
	for _, u := range unsubs {
		u()
	}
	// An orderly shutdown suppresses the broker's last-will, so without
	// this both declared availability sources stay `online` and a
	// consumer shows the card with frozen values as available — exactly
	// the case the second source exists to distinguish.
	if b := p.wiring.Bridge(); b != nil {
		_ = b.PublishSecurityAvailability(context.Background(),
			securityAvailabilityTopic(b.topics.Base), false)
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
		case <-p.reconcileCh:
			p.reconcile()
		case m := <-p.msgCh:
			p.publish(ctx, m)
		}
	}
}

// publish hands one queued message to the [Bridge] publisher for its
// kind.
//
// It used to reach past the bridge into the raw client. That made the
// whole Security & Safety plane invisible to the bridge: its topics
// never entered the retained-topic index, so no sweep and no retraction
// could reach them, and its publishes were counted by neither
// `messages_sent` nor `publish_errors` — the one plane whose entities
// an operator cannot see failing was also the one plane whose publishes
// no metric could see at all.
func (p *SecurityMQTTPublisher) publish(ctx context.Context, m securityMsg) {
	b := p.wiring.Bridge()
	if b == nil {
		return
	}
	var err error
	switch m.kind {
	case securityMsgEvent:
		err = b.PublishSecurityEvent(ctx, m.topic, m.payload)
	case securityMsgRetract:
		err = b.RetractSecurityState(ctx, m.topic)
	case securityMsgAvailability:
		err = b.PublishSecurityAvailability(ctx, m.topic, string(m.payload) == "online")
	case securityMsgState:
		err = b.PublishSecurityState(ctx, m.topic, m.payload)
	}
	if err != nil {
		p.logger.Error("security mqtt publish failed", "topic", m.topic, "error", err)
	}
}

// --- Bridge publishers for the Security & Safety plane ----------------
//
// None of them is gated on the raw plane. The funnel carries the very
// topics the security discovery payloads name as their `state_topic`,
// so silencing it while discovery still declares them would leave every
// Security & Safety entity present in Home Assistant and permanently
// unknown.
//
// The plane is daemon-level, not per-central — a hazard class spans
// centrals rather than the other way around — so every counter
// increment carries an empty central label rather than attributing a
// daemon-wide plane to one CCU.

// PublishSecurityState publishes one retained Security & Safety
// aggregate and records the topic in the bridge's retained-topic index.
func (b *Bridge) PublishSecurityState(ctx context.Context, topic string, body []byte) error {
	return b.publishRuntimeState(ctx, "", topic, body)
}

// PublishSecurityEvent publishes one non-retained Security & Safety
// pulse.
//
// QoS 0, and not recorded in the retained-topic index: an event is a
// moment, not a state. At-most-once is the right trade — a re-delivered
// alarm event would re-fire every automation subscribed to it — and
// there is no retained message on the topic for a sweep to find.
func (b *Bridge) PublishSecurityEvent(ctx context.Context, topic string, body []byte) error {
	if err := b.client.Publish(ctx, topic, body, QoS0, false); err != nil {
		b.incPublishErrors("")
		return err
	}
	b.incMessagesSent("")
	return nil
}

// PublishSecurityAvailability publishes the plane's retained
// availability marker.
//
// QoS 1, not the state QoS. Availability is the one topic whose loss
// cannot be repaired by the next publish: the plane writes it on a flip,
// so a marker dropped at QoS 0 — which is what the state profile
// resolves to in practice — leaves the entity in the state it last
// carried until something flips it again. For the `offline` marker
// written on shutdown, "something flips it again" is the next start, and
// for a crash that suppresses the broker's last-will it is never: every
// Security & Safety entity stays available, showing frozen values,
// which is precisely the case this second availability source exists to
// distinguish. [Bridge.PublishAvailability] and
// [Bridge.PublishAlarmAvailability] already pin QoS 1 for the same
// reason; the three availability topics of one daemon must not have
// three different delivery guarantees.
func (b *Bridge) PublishSecurityAvailability(ctx context.Context, topic string, online bool) error {
	sent, err := b.avail.Publish(ctx, topic, online)
	if err != nil {
		b.incPublishErrors("")
		return err
	}
	// In both indexes, for the reason spelled out on
	// [Bridge.PublishAlarmAvailability].
	b.rememberRawTopic(topic)
	if sent {
		b.incMessagesSent("")
	}
	return nil
}

// RetractSecurityState clears a retained Security & Safety topic whose
// entity no longer exists, and drops it from the retained-topic index
// so nothing retracts an already-empty topic a second time.
func (b *Bridge) RetractSecurityState(ctx context.Context, topic string) error {
	return b.evictRuntimeState(ctx, "", topic)
}

// enqueue queues a publish without blocking the domain's bus goroutine.
//
// On overflow it drops the *newest* message — the one that cannot be
// queued — which is the alarm plane's policy, and the two planes of one
// daemon must not disagree about what a full queue means.
//
// Dropping the oldest was the other way round and read as the safer
// choice, because reconcile enqueues the per-class and per-zone states
// last and losing those leaves the aggregate state disagreeing with the
// classes it was folded from. It is not the safer choice, because not
// every queued message is recoverable: a state is corrected by the next
// reconcile, while a retraction has no next attempt — the class or zone
// it evacuates is already out of the known-sets, so nothing will ever
// enqueue it again and the retained topic stays on the broker for good,
// feeding an entity for something that no longer exists. Reconcile
// therefore enqueues its retractions first and the queue discards from
// the end, so the messages at risk are the ones a later pass repairs.
//
// A drop is never silent: it is logged with the topic and counted in
// `publish_errors`, because a message that never reaches the broker is
// a failed publish however it failed.
func (p *SecurityMQTTPublisher) enqueue(m securityMsg) {
	select {
	case p.msgCh <- m:
	default:
		p.logger.Warn("security mqtt queue full; dropped the newest message",
			"dropped_topic", m.topic, "kind", m.kind)
		if b := p.wiring.Bridge(); b != nil {
			b.incPublishErrors("")
		}
	}
}

// signalReconcile asks the worker for a reconcile without blocking the
// caller. Requests coalesce: a reconcile always republishes from a fresh
// snapshot, so a pass that has not started yet subsumes every request
// raised before it.
func (p *SecurityMQTTPublisher) signalReconcile() {
	select {
	case p.reconcileCh <- struct{}{}:
	default:
	}
}

// markDomainReported records that the domain has spoken on its bus.
//
// The publisher starts before the domain does and reconciles once on its
// own, against whatever the source reports at that moment — which is a
// placeholder, not the installation. The domain announces its state as
// the last step of its own start, after it has built the classification
// index, so the first event of any kind is the boundary between the two.
// [SecurityMQTTPublisher.declareEntities] needs that boundary to tell an
// installation that has no classes or zones from one whose index has not
// been built yet.
func (p *SecurityMQTTPublisher) markDomainReported() {
	p.mu.Lock()
	p.reported = true
	p.mu.Unlock()
}

// domainReported reports whether the domain has published at least one
// event since this publisher started.
func (p *SecurityMQTTPublisher) domainReported() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.reported
}

// --- bus handlers (run on the domain goroutine — enqueue only) ---

func (p *SecurityMQTTPublisher) onStateChanged(hmevent.SecurityStateChangedEvent) {
	p.markDomainReported()
	p.signalReconcile()
}

func (p *SecurityMQTTPublisher) onClassChanged(hmevent.SecurityClassChangedEvent) {
	p.markDomainReported()
	p.signalReconcile()
}

func (p *SecurityMQTTPublisher) onZoneChanged(hmevent.SecurityZoneChangedEvent) {
	p.markDomainReported()
	p.signalReconcile()
}

// onFaultChanged republishes the retained half.
//
// It deliberately writes no event. The fault event topic has exactly one
// producer — onNotification — because a consumer's event entity parses
// one payload shape per topic, and this handler wrote a different one:
// every raise and clear arrived twice, once as a ledger transition
// (fault_id, open_count, no text) and once as a rendered report
// (subject, message, sources, link, no id), so an automation reading
// either field got it on half the messages.
//
// Nothing is lost by staying quiet. The ledger facts this used to carry
// live in the retained `problem` attributes, which reconcile republishes
// here with the full standing list, its ids, its count and its
// acknowledgement flags.
//
// It also removes an acknowledgement announcing itself as `raised`,
// which is what a consumer's automation would act on a second time.
func (p *SecurityMQTTPublisher) onFaultChanged(hmevent.SecurityFaultChangedEvent) {
	p.markDomainReported()
	p.signalReconcile()
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
	p.markDomainReported()
	body, err := json.Marshal(securityNotificationPayload(e))
	if err != nil {
		return
	}
	topic := "event"
	if e.Fault {
		topic = "fault"
	}
	p.enqueue(securityMsg{kind: securityMsgEvent, topic: securityStateTopic(p.base(), topic), payload: body})

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
	p.enqueue(securityMsg{topic: securityStateTopic(p.base(), key), payload: body})
}

// base is the topic prefix of every security topic. It reads the topic
// builder's normalised base, like the alarm plane beside it, so the
// plane cannot end up one slash away from the topics the rest of the
// bridge publishes on.
func (p *SecurityMQTTPublisher) base() string {
	if b := p.wiring.Bridge(); b != nil {
		return b.topics.Base
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
//
// The known-sets are deliberately left alone. They track which class and
// zone topics carry retained state so retractGone can evacuate one that
// disappears; clearing them here would drop exactly the entries for the
// classes and zones that went away while the broker was unreachable,
// which are the ones that still need evacuating. Retained discovery
// configs a restarted broker dropped come back through
// [Bridge.RepublishDiscovery], which replays every config the bridge
// published.
// The dedup gates of the shared state and availability publishers are reset
// first, for the same reason the known-sets are not: a broker that came back
// without its retained store holds none of the bytes those gates remember, so
// without the reset the reconcile below would be suppressed as unchanged and
// rewrite nothing.
func (p *SecurityMQTTPublisher) OnBrokerConnect() {
	if p == nil {
		return
	}
	if b := p.wiring.Bridge(); b != nil {
		b.ResetRuntimeGates()
	}
	p.signalReconcile()
}
