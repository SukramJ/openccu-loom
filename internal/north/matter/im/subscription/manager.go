// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package subscription

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/SukramJ/openccu-loom/internal/north/matter/im"
)

// Errors.
var (
	// ErrFabricQuotaExceeded is returned when [Manager.Subscribe]
	// would exceed [Config.MaxSubscriptionsPerFabric] for the
	// requested fabric.
	ErrFabricQuotaExceeded = errors.New("subscription: per-fabric quota exceeded")
	// ErrCadenceOutOfRange is returned for MinInterval > MaxInterval
	// or values outside Matter §10.6.9 caps.
	ErrCadenceOutOfRange = errors.New("subscription: cadence out of range")
	// ErrCadenceInvertedAfterClamp is returned when the post-clamp
	// cadence is inverted: MinIntervalFloor (floored up to the
	// configured minimum) exceeds MaxIntervalCeiling (capped down to
	// the configured ceiling). Example: cfg.MaxIntervalCeilingSeconds=5,
	// request min=10, max=30 → after clamp min=10 > max=5. A silent
	// inversion causes drainDirtyIfElapsed to never fire because the
	// floor can never elapse relative to a ceiling that is below it.
	ErrCadenceInvertedAfterClamp = errors.New("subscription: cadence inverted after clamp")
	// ErrNotFound is returned when a Get / Close hits no subscription.
	ErrNotFound = errors.New("subscription: not found")
)

// Reporter receives ReportData payloads the engine produces. The
// caller wires this into the bridge's IM dispatcher to encode +
// transmit the report on the right secure-channel session.
type Reporter func(ctx context.Context, sub *Subscription, paths []im.ConcreteAttributePath)

// EventReporter is the symmetrical hook for event drains. The engine
// invokes it when a subscription has pending events ready to flush
// (Critical priority bypasses the MinIntervalFloor gate per Matter
// §10.6.6). Optional — a nil hook means events fan into the queue
// but never leave the bridge (useful for unit tests of the cadence
// math alone).
type EventReporter func(ctx context.Context, sub *Subscription, events []im.EventReport)

// Config tunes the manager.
type Config struct {
	// MaxSubscriptionsPerFabric caps the number of active
	// subscriptions per fabric. Matter floor is 3 (§8.5.4); v1.1
	// defaults to 16.
	MaxSubscriptionsPerFabric int
	// MinIntervalFloorSeconds is the spec-mandated lowest cadence
	// the bridge will accept. Matter §10.6.9 says ≥ 0; we set 1 s
	// to keep busy-looping commissioners off the wire.
	MinIntervalFloorSeconds uint16
	// MaxIntervalCeilingSeconds is the upper bound the bridge will
	// accept. The bridge negotiates *down* to this value if the
	// commissioner asked for more. Default 3600 s (1 h) — Apple Home
	// and Google Home both stay well below.
	MaxIntervalCeilingSeconds uint16
	// TickInterval drives the engine's report sweeper. Smaller =
	// finer-grained MinIntervalFloor enforcement; larger = lower
	// goroutine overhead. Default 250 ms.
	TickInterval time.Duration
}

// Manager owns the active subscription set and runs the report
// engine. NewManager + Start + Stop is the lifecycle.
type Manager struct {
	cfg           Config
	logger        *slog.Logger
	reporter      Reporter
	eventReporter EventReporter

	mu        sync.RWMutex
	byID      map[uint32]*Subscription
	perFabric map[uint8]int
	nextID    uint32

	stopOnce sync.Once
	stopCh   chan struct{}
	wg       sync.WaitGroup
}

// SetEventReporter wires the optional event-drain hook. Pass nil to
// detach. Safe to call before Start; calling after Start swaps in the
// new hook for subsequent ticks.
func (m *Manager) SetEventReporter(r EventReporter) {
	m.mu.Lock()
	m.eventReporter = r
	m.mu.Unlock()
}

// NewManager constructs a manager. logger and reporter may be nil;
// nil reporter means subscriptions are tracked but no reports flow
// out (useful for tests of the cadence math alone).
func NewManager(cfg Config, reporter Reporter, logger *slog.Logger) *Manager {
	if cfg.MaxSubscriptionsPerFabric == 0 {
		cfg.MaxSubscriptionsPerFabric = 16
	}
	if cfg.MinIntervalFloorSeconds == 0 {
		cfg.MinIntervalFloorSeconds = 1
	}
	if cfg.MaxIntervalCeilingSeconds == 0 {
		cfg.MaxIntervalCeilingSeconds = 3600
	}
	if cfg.TickInterval == 0 {
		cfg.TickInterval = 250 * time.Millisecond
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Manager{
		cfg:       cfg,
		logger:    logger,
		reporter:  reporter,
		byID:      make(map[uint32]*Subscription),
		perFabric: make(map[uint8]int),
		// SubscriptionId is a uint32 carried on every ReportData chunk
		// and on the SubscribeResponse. matter.js Sample uses a random
		// uint32 (observed: 1561508961 / 5f7d1e3c hex) — Apple's
		// ReadClient (`src/app/ReadClient.cpp:643`) reads the id from
		// the first priming report, stores it, and matches every
		// follow-up. A uniform low value `1` across every Subscribe
		// looks like a placeholder / uninitialised state; randomising
		// the seed avoids that pattern.
		nextID: randomSubscriptionIDStart(),
		stopCh: make(chan struct{}),
	}
}

// randomSubscriptionIDStart returns a uniformly-random uint32 seed for
// the subscription-id allocator. Zero is reserved by the IM layer for
// "absent"; we re-roll until non-zero.
func randomSubscriptionIDStart() uint32 {
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		// crypto/rand failure at process start is unrecoverable —
		// surface as panic so the operator notices, rather than
		// silently regressing to the predictable starting id that
		// reproduces the Apple cache-drop symptom.
		panic("matter/subscription: crypto/rand.Read failed: " + err.Error())
	}
	id := binary.BigEndian.Uint32(b[:])
	if id == 0 {
		id = 1
	}
	return id
}

// Start launches the engine tick goroutine. Idempotent.
func (m *Manager) Start(ctx context.Context) {
	m.wg.Add(1)
	go m.run(ctx)
}

// Stop terminates the engine and waits for it to drain. Idempotent.
func (m *Manager) Stop() {
	m.stopOnce.Do(func() {
		close(m.stopCh)
	})
	m.wg.Wait()
}

// Subscribe accepts a SubscribeRequest and returns the live record.
// The caller wires the [Subscription.ID] into the SubscribeResponse.
func (m *Manager) Subscribe(req SubscribeArgs) (*Subscription, error) {
	if err := m.validateCadence(req.MinIntervalFloor, req.MaxIntervalCeiling); err != nil {
		return nil, err
	}

	// When the caller flags that this request is a replacement (same session
	// re-subscribing), evict any prior subscription on the same session before
	// the quota check so that the new one fits even when the old entry still
	// holds a slot.
	if req.ReplaceSessionDuplicate && req.SessionID != 0 {
		m.CloseSession(req.SessionID)
	}

	m.mu.Lock()
	if m.perFabric[req.FabricIndex] >= m.cfg.MaxSubscriptionsPerFabric {
		m.mu.Unlock()
		return nil, fmt.Errorf("%w: fabric=%d quota=%d", ErrFabricQuotaExceeded, req.FabricIndex, m.cfg.MaxSubscriptionsPerFabric)
	}
	id := m.allocateIDLocked()
	// MaxInterval publisher selection semantics (chip alignment):
	// Matter §10.6.3.2 requires the publisher's MaxInterval in SubscribeResponse
	// to be ≤ the subscriber's requested MaxInterval. chip (non-ICD path,
	// `src/app/ReadHandler.cpp:769-809`) accepts the subscriber's MaxInterval
	// directly and bounds it only by `kSubscriptionMaxIntervalPublisherLimit`
	// (3600 s). matter.js (`packages/node/src/node/server/ServerSubscription.ts:269-282`)
	// further clamps to min(subscriptionMaxInterval, maxIntervalCeiling) and
	// also lifts by minIntervalFloor. openccu-loom clamps maxCeil down to
	// cfg.MaxIntervalCeilingSeconds (default 3600 s, matching chip) and
	// minFloor up to cfg.MinIntervalFloorSeconds. The post-clamp inversion
	// check (ErrCadenceInvertedAfterClamp) rejects requests where the floored
	// minimum exceeds the capped ceiling — equivalent to matter.js's lower-bound
	// guarantee. The negotiated MaxIntervalCeiling stored on the Subscription is
	// what the bridge advertises in SubscribeResponse, satisfying §10.6.3.2.
	maxCeil := req.MaxIntervalCeiling
	if maxCeil > m.cfg.MaxIntervalCeilingSeconds {
		maxCeil = m.cfg.MaxIntervalCeilingSeconds
	}
	minFloor := req.MinIntervalFloor
	if minFloor < m.cfg.MinIntervalFloorSeconds {
		minFloor = m.cfg.MinIntervalFloorSeconds
	}
	// Re-validate AFTER clamp: the configured ceiling may be below the
	// floored minimum, producing a silent inversion that permanently
	// blocks drainDirtyIfElapsed. Reject early so callers can surface
	// a SubscribeResponse.Status = CONSTRAINT_ERROR rather than silently
	// delivering a subscription that never reports dirty changes.
	// matter.js ref: packages/node/src/node/server/ServerSubscription.ts
	// — cadence validation runs on negotiated values.
	if minFloor > maxCeil {
		m.mu.Unlock()
		return nil, fmt.Errorf("%w: min=%d > max=%d (after clamp)", ErrCadenceInvertedAfterClamp, minFloor, maxCeil)
	}
	sub := &Subscription{
		ID:                 id,
		FabricIndex:        req.FabricIndex,
		PeerNodeID:         req.PeerNodeID,
		SessionID:          req.SessionID,
		MinIntervalFloor:   minFloor,
		MaxIntervalCeiling: maxCeil,
		KeepSubscriptions:  req.KeepSubscriptions,
		AttributePaths:     append([]im.ConcreteAttributePath(nil), req.AttributePaths...),
		EventPaths:         append([]im.ConcreteEventPath(nil), req.EventPaths...),
		// Stamp lastReport=now at admission so the very first engine tick
		// after Subscribe() does not see [Subscription.lastReport.IsZero]
		// and fire an immediate keep-alive. The bridge follows up with
		// TouchLastReport after the initial-report-stream + SubscribeResponse
		// has shipped, but the 250 ms engine tick can fire BETWEEN
		// Subscribe() and that TouchLastReport call — the bridge takes
		// ~100 ms to chunk + send + per-chunk-wait + dispatch the
		// SubscribeResponse. The empty 52-byte keep-alive ReportData the
		// engine emits in that window arrives at the controller before
		// it has piggyback-acked the initial report, and chip-tool's
		// MRP layer drops it with `Dropping message without piggyback
		// ack when we are waiting for an ack`, eventually timing out
		// the entire subscription (chip-tool-test-brief T7/T8). Stamping
		// here closes the race; TouchLastReport remains a no-op refresh.
		lastReport: time.Now(),
	}
	m.byID[id] = sub
	m.perFabric[req.FabricIndex]++
	m.mu.Unlock()

	m.logger.Debug(
		"matter subscription added",
		slog.Uint64("id", uint64(id)),
		slog.Int("fabric", int(req.FabricIndex)),
		slog.Int("min", int(minFloor)),
		slog.Int("max", int(maxCeil)),
		slog.Int("paths", len(req.AttributePaths)),
	)
	return sub, nil
}

// Get returns the subscription for id.
func (m *Manager) Get(id uint32) (*Subscription, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	sub, ok := m.byID[id]
	if !ok {
		return nil, ErrNotFound
	}
	return sub, nil
}

// Close terminates a single subscription.
func (m *Manager) Close(id uint32) error {
	m.mu.Lock()
	sub, ok := m.byID[id]
	if !ok {
		m.mu.Unlock()
		return ErrNotFound
	}
	delete(m.byID, id)
	m.perFabric[sub.FabricIndex]--
	if m.perFabric[sub.FabricIndex] <= 0 {
		delete(m.perFabric, sub.FabricIndex)
	}
	m.mu.Unlock()
	sub.Close()
	return nil
}

// CloseSession terminates every subscription tied to sessionID. Used
// when an operational session disappears.
func (m *Manager) CloseSession(sessionID uint16) {
	m.mu.Lock()
	victims := make([]*Subscription, 0)
	for id, sub := range m.byID {
		if sub.SessionID == sessionID {
			victims = append(victims, sub)
			delete(m.byID, id)
			m.perFabric[sub.FabricIndex]--
			if m.perFabric[sub.FabricIndex] <= 0 {
				delete(m.perFabric, sub.FabricIndex)
			}
		}
	}
	m.mu.Unlock()
	for _, sub := range victims {
		sub.Close()
	}
}

// ClosePeer terminates every subscription owned by the (fabric, peer)
// tuple. Returns the number of subscriptions cleared, so callers can
// log a meaningful diagnostic.
//
// Mirrors matter.js packages/node/src/node/server/InteractionServer.ts:
// 549-566 — when a SubscribeRequest carries `KeepSubscriptions=false`,
// every existing subscription from the same peer (matched on
// PeerAddress = (fabric, peerNodeId)) is cancelled before the new
// subscription is admitted.
func (m *Manager) ClosePeer(fabricIndex uint8, peerNodeID uint64) int {
	m.mu.Lock()
	victims := make([]*Subscription, 0)
	for id, sub := range m.byID {
		if sub.FabricIndex == fabricIndex && sub.PeerNodeID == peerNodeID {
			victims = append(victims, sub)
			delete(m.byID, id)
			m.perFabric[fabricIndex]--
			if m.perFabric[fabricIndex] <= 0 {
				delete(m.perFabric, fabricIndex)
			}
		}
	}
	m.mu.Unlock()
	for _, sub := range victims {
		sub.Close()
	}
	return len(victims)
}

// CloseFabric terminates every subscription on fabricIndex. Used
// when OperationalCredentials.RemoveFabric fires.
func (m *Manager) CloseFabric(fabricIndex uint8) {
	m.mu.Lock()
	victims := make([]*Subscription, 0)
	for id, sub := range m.byID {
		if sub.FabricIndex == fabricIndex {
			victims = append(victims, sub)
			delete(m.byID, id)
		}
	}
	delete(m.perFabric, fabricIndex)
	m.mu.Unlock()
	for _, sub := range victims {
		sub.Close()
	}
}

// CloseFabricExcept terminates every subscription on fabricIndex except those
// bound to exceptSessionID. UpdateNOC tears down the rotated fabric's other
// CASE sessions but must preserve the invoking session (and the subscriptions
// it carries) so its NOCResponse reaches the wire and it can re-CASE. Mirrors
// chip FabricTable::AbortAllOtherCommunicationOnFabric pinning the invoking
// session. exceptSessionID==0 terminates every subscription on the fabric.
func (m *Manager) CloseFabricExcept(fabricIndex uint8, exceptSessionID uint16) {
	m.mu.Lock()
	victims := make([]*Subscription, 0)
	for id, sub := range m.byID {
		if sub.FabricIndex == fabricIndex && sub.SessionID != exceptSessionID {
			victims = append(victims, sub)
			delete(m.byID, id)
			m.perFabric[fabricIndex]--
		}
	}
	if m.perFabric[fabricIndex] <= 0 {
		delete(m.perFabric, fabricIndex)
	}
	m.mu.Unlock()
	for _, sub := range victims {
		sub.Close()
	}
}

// CloseEndpoint terminates every subscription that contains at least
// one AttributePath or EventPath targeting endpointID. Called after a
// topology Reassemble removes an endpoint so that the engine stops
// ticking subscriptions that can never match a live cluster again.
//
// Mirrors matter.js packages/protocol/src/interaction/SubscriptionHandler.ts
// (ServerSubscription) lifecycle: when a BridgedNode endpoint is
// removed from the Aggregator the outer bridge calls
// `endpoint.lifecycle.remove()` which causes any subscription targeting
// that endpoint to be closed by the InteractionServer.
func (m *Manager) CloseEndpoint(endpointID uint16) int {
	m.mu.Lock()
	victims := make([]*Subscription, 0)
	for id, sub := range m.byID {
		if subReferencesEndpoint(sub, endpointID) {
			victims = append(victims, sub)
			delete(m.byID, id)
			m.perFabric[sub.FabricIndex]--
			if m.perFabric[sub.FabricIndex] <= 0 {
				delete(m.perFabric, sub.FabricIndex)
			}
		}
	}
	m.mu.Unlock()
	for _, sub := range victims {
		sub.Close()
	}
	return len(victims)
}

// subReferencesEndpoint reports whether sub has any AttributePath or
// EventPath whose Endpoint == endpointID.
func subReferencesEndpoint(sub *Subscription, endpointID uint16) bool {
	for _, p := range sub.AttributePaths {
		if p.HasEndpoint && p.Endpoint == endpointID {
			return true
		}
	}
	for _, p := range sub.EventPaths {
		if p.HasEndpoint && p.Endpoint == endpointID {
			return true
		}
	}
	return false
}

// Active returns the count of live subscriptions.
func (m *Manager) Active() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.byID)
}

// SubjectHasActiveSubscription reports whether the given (fabricIndex,
// peerNodeID) pair holds at least one active subscription. A "subject"
// in the Matter sense is the (fabric, subject/peer) identity — the same
// concept used by the AccessControl cluster's SubjectID field.
//
// This is a diagnostic helper that mirrors chip's
// src/app/ReadHandler.cpp SubjectHasActiveSubscription semantics, used
// by the IM engine to decide whether to emit a Busy status when a
// subscription request arrives from a peer that is already subscribed
// at quota.
func (m *Manager) SubjectHasActiveSubscription(fabricIndex uint8, peerNodeID uint64) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, sub := range m.byID {
		if sub.FabricIndex == fabricIndex && sub.PeerNodeID == peerNodeID {
			return true
		}
	}
	return false
}

// FabricHasAtLeastOneActiveSubscription reports whether fabricIndex
// holds any active subscription. Mirrors chip's
// src/app/ReadHandler.cpp FabricHasAtLeastOneActiveSubscription, used
// by the IM engine to determine whether a fabric is still "in use"
// before accepting a KeepSubscriptions=false request that would evict
// all subscriptions for that fabric.
func (m *Manager) FabricHasAtLeastOneActiveSubscription(fabricIndex uint8) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.perFabric[fabricIndex] > 0
}

// OnAttributeChanged notifies every subscription whose path-set
// covers `path` that a new value is available. The engine emits the
// report on the next tick once MinInterval has elapsed.
func (m *Manager) OnAttributeChanged(path im.ConcreteAttributePath) {
	m.mu.RLock()
	matches := make([]*Subscription, 0)
	for _, sub := range m.byID {
		for _, p := range sub.AttributePaths {
			if pathMatches(p, path) {
				matches = append(matches, sub)
				break
			}
		}
	}
	m.mu.RUnlock()
	for _, sub := range matches {
		sub.markDirty(path)
	}
}

// SubscribeArgs is the input to [Manager.Subscribe].
type SubscribeArgs struct {
	FabricIndex        uint8
	PeerNodeID         uint64
	SessionID          uint16
	MinIntervalFloor   uint16
	MaxIntervalCeiling uint16
	KeepSubscriptions  bool
	AttributePaths     []im.ConcreteAttributePath
	EventPaths         []im.ConcreteEventPath
	// ReplaceSessionDuplicate instructs Subscribe to close any
	// existing subscriptions tied to the same SessionID before
	// admitting the new one. Set this when a CASE session sends a
	// second SubscribeRequest while a subscription from its previous
	// attempt on the same session is still live — keeping the old
	// entry would accumulate stale subscriptions and waste per-fabric
	// quota. When false (the default) existing subscriptions on the
	// session are left untouched, which is the correct behaviour for
	// a peer that genuinely intends to hold multiple simultaneous
	// subscriptions with different path sets.
	ReplaceSessionDuplicate bool
}

// EventFiring is the payload of an [Manager.OnEventFired] call. The
// caller (typically a cluster server when its emit-API fires)
// supplies the cluster-native event Data; the manager fans it out to
// every subscription whose EventPaths cover the (endpoint, cluster,
// event) triple.
type EventFiring struct {
	// Path identifies the concrete event. HasEndpoint / HasCluster /
	// HasEvent must all be true on this side — this is a firing, not
	// a subscription.
	Path im.ConcreteEventPath
	// Number is the monotonic event number per cluster instance.
	Number uint64
	// Priority drives the urgency gate (Critical bypasses MinInterval).
	Priority im.EventPriority
	// Timestamp is system-ticks-since-boot in milliseconds; the engine
	// stamps it when zero.
	Timestamp uint64
	// Data is the cluster-native event payload (struct, scalar, …).
	Data im.AttributeValue
}

// OnEventFired notifies every subscription whose EventPaths cover the
// event path. The manager queues the event on each matching
// subscription; the engine drains it on the next tick honoring
// MinIntervalFloor (with Critical-priority bypass).
func (m *Manager) OnEventFired(ev EventFiring) {
	if !ev.Path.HasEndpoint || !ev.Path.HasCluster || !ev.Path.HasEvent {
		return
	}
	m.mu.RLock()
	matches := make([]*Subscription, 0)
	for _, sub := range m.byID {
		for _, p := range sub.EventPaths {
			if p.Matches(ev.Path) {
				matches = append(matches, sub)
				break
			}
		}
	}
	m.mu.RUnlock()
	pe := pendingEvent(ev)
	for _, sub := range matches {
		sub.queueEvent(pe)
	}
}

func (m *Manager) validateCadence(minFloor, maxCeil uint16) error {
	if minFloor > maxCeil {
		return fmt.Errorf("%w: min=%d > max=%d", ErrCadenceOutOfRange, minFloor, maxCeil)
	}
	return nil
}

func (m *Manager) allocateIDLocked() uint32 {
	for {
		id := m.nextID
		m.nextID++
		if m.nextID == 0 {
			m.nextID = 1
		}
		if _, in := m.byID[id]; !in {
			return id
		}
	}
}

// pathMatches reports whether `concrete` falls within the path set
// described by `subscribed`. Wildcards on Endpoint / Cluster /
// Attribute / Node propagate; ListIndex is ignored (the engine emits
// whole-attribute reports, not list-element reports).
func pathMatches(subscribed, concrete im.ConcreteAttributePath) bool {
	if subscribed.HasNode && subscribed.Node != concrete.Node {
		return false
	}
	if subscribed.HasEndpoint && subscribed.Endpoint != concrete.Endpoint {
		return false
	}
	if subscribed.HasCluster && subscribed.Cluster != concrete.Cluster {
		return false
	}
	if subscribed.HasAttribute && subscribed.Attribute != concrete.Attribute {
		return false
	}
	return true
}

// snapshot returns a defensive copy of the live subscription list
// for the engine tick.
//
// Concurrency note: this function acquires RLock, copies the map into a
// slice, and releases the lock before the caller iterates. A concurrent Subscribe or Close
// that races the copy is fine — the caller receives a snapshot of the
// map at that instant; new subscriptions added after the snapshot are
// picked up on the next tick, and removed subscriptions are guarded by
// [Subscription.IsClosed] inside the engine loop. Per-subscription
// mutations (markDirty, drainDirtyIfElapsed, drainEventsIfElapsed) are
// each protected by [Subscription.mu], so no data race exists. This
// pattern mirrors matter.js's per-subscription timer model where each
// ServerSubscription owns its own timer and there is no shared-map
// iteration (packages/node/src/node/server/ServerSubscription.ts).
func (m *Manager) snapshot() []*Subscription {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]*Subscription, 0, len(m.byID))
	for _, sub := range m.byID {
		out = append(out, sub)
	}
	return out
}
