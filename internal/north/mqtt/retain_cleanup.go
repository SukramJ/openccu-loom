// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package mqtt

import (
	"context"
	"strings"
	"sync"
	"time"
)

// RetainCleanup implements a one-shot orphan-clear
// pass that runs once per daemon start and clears retained topics
// the new topology no longer publishes to. The legacy aggregate
// state topic (`<central>/<iface>/<addr>/<ch>/state`) is the most
// prominent example — ADR 0011 moved state to per-DP topics under
// `…/channels/<ch>/<bucket>/<param>/state`, but HA's retained-message
// store still carries the aggregate from the previous build.
//
// Mechanism: subscribe to `<topic_base>/#` with retained-only
// snapshot semantics, accumulate every retained topic that matches
// a legacy pattern into a worklist, then publish empty payloads
// (retain=true) at every worklist entry to evict it. The pass is
// idempotent — clearing an already-empty topic is a no-op.
//
// The pass is gated behind an explicit opt-in to avoid surprise
// data loss during the migration window. Callers enable it via
// [Bridge.RunRetainCleanupOnce] when they have audited the legacy
// patterns appropriate for their deployment.
type RetainCleanup struct {
	bridge *Bridge

	mu       sync.Mutex
	worklist []string // retained topic names earmarked for eviction
}

// NewRetainCleanup constructs a cleanup pass for the given bridge.
func NewRetainCleanup(b *Bridge) *RetainCleanup {
	return &RetainCleanup{bridge: b}
}

// LegacyAggregateStateMatcher reports whether topic matches the
// legacy aggregate-state shape (the topology phase 1b retired):
//
//	<topic_base>/<central>/<iface>/<address>/<channelNo>/state
//
// where channelNo is a small non-negative integer. The current
// topology still publishes the channel-aggregate at this exact
// shape for custom-DP rollups, so this matcher is **not** broadly
// destructive — it stays available for legacy schemas where the
// state JSON shape differs and operators want to force a clean
// republish.
//
// Exposed for unit testing — production callers consume it via
// [RetainCleanup.collect].
func LegacyAggregateStateMatcher(topicBase, topic string) bool {
	if topicBase == "" {
		return false
	}
	if !strings.HasPrefix(topic, topicBase+"/") {
		return false
	}
	tail := strings.TrimPrefix(topic, topicBase+"/")
	parts := strings.Split(tail, "/")
	// `<central>/<iface>/<address>/<channel>/state` — 5 segments.
	if len(parts) != 5 || parts[4] != "state" {
		return false
	}
	channel := parts[3]
	if channel == "" || channel == "channels" {
		return false
	}
	for _, r := range channel {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// LegacyDataPointStateMatcher reports whether topic matches the old
// bucket-less per-DP shape that conflated MASTER and VALUES paramsets
// on the broker:
//
//	<topic_base>/<central>/<iface>/<address>/<channelNo>/<PARAM>
//
// The new canonical shape always carries an explicit bucket:
//
//	<topic_base>/<central>/<iface>/<address>/<channelNo>/<bucket>/<PARAM>
//
// where `<bucket>` is `values` / `master` / `calculated`. So a
// retained topic with exactly 6 segments where the 5th is a numeric
// channel id and the 6th is the wire-parameter name is a legacy
// candidate.
//
// Conservative on the parameter side: only match topics where the
// last segment is upper-case (CCU wire parameters are uppercase by
// convention) so we never accidentally evict a sub-tree node like
// `…/<ch>/state` (matched separately) or
// `…/<ch>/svc/<method>/set` (different shape).
func LegacyDataPointStateMatcher(topicBase, topic string) bool {
	if topicBase == "" {
		return false
	}
	if !strings.HasPrefix(topic, topicBase+"/") {
		return false
	}
	tail := strings.TrimPrefix(topic, topicBase+"/")
	parts := strings.Split(tail, "/")
	// Must be exactly 5 segments after base — `<central>/<iface>/
	// <address>/<channel>/<PARAM>`. The new shape adds a bucket
	// segment and would have 6 parts.
	if len(parts) != 5 {
		return false
	}
	channel := parts[3]
	for _, r := range channel {
		if r < '0' || r > '9' {
			return false
		}
	}
	if channel == "" {
		return false
	}
	param := parts[4]
	if param == "" {
		return false
	}
	// Reserve known sub-tree nodes — the channel-aggregate "state",
	// "event" non-retained, "info"/"diagnostics" device snapshots.
	switch param {
	case "state", "event", "set", "config", "availability", "info", "diagnostics":
		return false
	}
	// Wire parameters are upper-case by convention; bucket labels
	// (`values`, `master`, `calculated`, `custom`, `channels`) are
	// lower-case. A param-segment that is fully lower-case is more
	// likely the new-shape `<bucket>` node than a wire parameter.
	for _, r := range param {
		if r >= 'a' && r <= 'z' {
			return false
		}
	}
	return true
}

// LegacySlotStateMatcher reports whether topic lives under the
// retired `channels/`-infix subtree. The previous build's per-DP
// topology used:
//
//	<topic_base>/<central>/<iface>/<address>/channels/<ch>/<bucket>/<PARAM>/state
//	<topic_base>/<central>/<iface>/<address>/channels/<ch>/<bucket>/<PARAM>/config
//	<topic_base>/<central>/<iface>/<address>/channels/<ch>/<bucket>/<PARAM>/set
//	<topic_base>/<central>/<iface>/<address>/channels/<ch>/custom/<kind>/state
//	<topic_base>/<central>/<iface>/<address>/channels/<ch>/custom/<kind>/config
//	<topic_base>/<central>/<iface>/<address>/channels/<ch>/custom/<kind>/set/<method>
//
// The new shape drops the `channels/` infix and the `/state` suffix
// — `<addr>/<ch>/<bucket>/<PARAM>` is canonical. Operators that ran
// a previous build still see retained content under the old path; the
// cleanup pass evicts every retained topic with `parts[3] ==
// "channels"` regardless of the trailing structure.
//
// Trailing-segment-tolerant: matches any topic whose 4th segment is
// the literal `"channels"` and whose 5th segment is a numeric channel
// id. Catches state, config, set, custom slots, and any future suffix
// the old shape might have used.
func LegacySlotStateMatcher(topicBase, topic string) bool {
	if topicBase == "" {
		return false
	}
	if !strings.HasPrefix(topic, topicBase+"/") {
		return false
	}
	tail := strings.TrimPrefix(topic, topicBase+"/")
	parts := strings.Split(tail, "/")
	// Need at least `<central>/<iface>/<address>/channels/<ch>/...`
	// — 5 segments minimum. The legacy state/config form has 8;
	// custom-slot forms vary.
	if len(parts) < 5 {
		return false
	}
	if parts[3] != "channels" {
		return false
	}
	channel := parts[4]
	if channel == "" {
		return false
	}
	for _, r := range channel {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// collect inspects topic+payload pairs delivered by a retained-topic
// snapshot subscription and accumulates eviction candidates. The
// payload is unused here — we only care about the topic shape — but
// kept on the signature so the call site matches the
// [MessageHandler] contract.
//
// Matches three legacy shapes, all retired by the Option-B topology
// migration:
//   - bucket-less DataPointState (`<addr>/<ch>/<PARAM>`)
//   - verbose SlotState (`<addr>/channels/<ch>/<bucket>/<PARAM>/state`)
//   - aggregated channel state where the StatePayload schema changed
//     between builds (custom DP roll-ups; retain when JSON shape no
//     longer parses)
func (c *RetainCleanup) collect(topic string, _ []byte, _ bool) {
	if c.bridge == nil {
		return
	}
	base := c.bridge.cfg.Base
	if LegacyDataPointStateMatcher(base, topic) ||
		LegacySlotStateMatcher(base, topic) ||
		LegacyAggregateStateMatcher(base, topic) {
		c.mu.Lock()
		c.worklist = append(c.worklist, topic)
		c.mu.Unlock()
	}
}

// Worklist returns a snapshot of the accumulated eviction
// candidates. Stable copy — safe to inspect from tests.
func (c *RetainCleanup) Worklist() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]string, len(c.worklist))
	copy(out, c.worklist)
	return out
}

// RunRetainCleanupOnce subscribes to <topic_base>/# for a short
// snapshot window, accumulates every legacy retained topic, then
// publishes empty payloads (retain=true) at each one to evict. The
// snapshot window defaults to 2 seconds — long enough for typical
// brokers (Mosquitto / EMQX / VerneMQ) to flush retained messages
// to a fresh subscriber, short enough to not delay daemon boot.
//
// Idempotent and safe to call multiple times: a second pass simply
// finds nothing to clear (the first one already published empties).
//
// Gated behind explicit invocation so a fresh build doesn't wipe
// retained state without the operator's consent. Returns the number
// of topics evicted.
//
// Requires the bridge's underlying client to satisfy the [Client]
// interface (publish + subscribe). The default [Publisher] interface
// is narrower; production wiring uses the [Client] implementation
// from `adapter_tcp.go`.
func (b *Bridge) RunRetainCleanupOnce(ctx context.Context, snapshotWindow time.Duration) (int, error) {
	if !b.cfg.RawEnabled {
		return 0, nil
	}
	if snapshotWindow <= 0 {
		snapshotWindow = 2 * time.Second
	}
	subClient, ok := b.client.(Client)
	if !ok {
		return 0, errCleanupClientLacksSubscribe
	}
	cleanup := NewRetainCleanup(b)
	filter := b.cfg.Base + "/#"
	if _, err := subClient.Subscribe(ctx, filter, b.cfg.QoS.State, LegacyHandler(cleanup.collect)); err != nil {
		return 0, err
	}
	// Wait for the broker to deliver retained messages.
	timer := time.NewTimer(snapshotWindow)
	defer timer.Stop()
	select {
	case <-timer.C:
	case <-ctx.Done():
		return 0, ctx.Err()
	}
	if err := subClient.Unsubscribe(ctx, filter); err != nil {
		return 0, err
	}
	worklist := cleanup.Worklist()
	for _, topic := range worklist {
		_ = b.client.Publish(ctx, topic, nil, b.cfg.QoS.State, true)
	}
	return len(worklist), nil
}

// errCleanupClientLacksSubscribe surfaces when the bridge was wired
// with a publish-only Publisher but the cleanup pass requires a
// subscribe-capable Client.
var errCleanupClientLacksSubscribe = retainCleanupError("bridge client must satisfy the Client interface (publish+subscribe) for retain cleanup")

type retainCleanupError string

func (e retainCleanupError) Error() string { return string(e) }

// RunDiscoveryOrphanCleanupOnce subscribes to `homeassistant/#` for a
// short snapshot window, accumulates every retained HA-Discovery
// config topic that targets a node_id this daemon owns, then evicts
// the topics that are NOT in the bridge's `declared` map. Designed to
// run AFTER [EventBridge.PublishInitialSnapshot] has populated
// `declared` with every entity the current build still drives —
// anything not in there is a leftover from a previous build (different
// MASTER-paramset gating, retired profiles, removed devices, …).
//
// Why this exists: HA-Discovery configs are retained at QoS1, so
// dropping an entity from the daemon's emit set leaves the broker
// holding the old payload indefinitely. HA reads it on the next
// integration restart and re-creates the phantom entity. Operators
// previously had to run `script/clean-mqtt-discovery.sh` by hand;
// this method does the equivalent automatically once per boot, scoped
// to the daemon's own node_id namespace so unrelated integrations
// (e.g. a parallel zigbee2mqtt deployment) stay untouched.
//
// Snapshot window defaults to 2 seconds — long enough for typical
// brokers (Mosquitto / EMQX / VerneMQ) to flush the retained QoS1
// queue to a fresh subscriber. Best-effort: returns the number of
// orphans evicted plus any subscribe error.
func (b *Bridge) RunDiscoveryOrphanCleanupOnce(ctx context.Context, snapshotWindow time.Duration) (int, error) {
	if !b.cfg.HADiscoveryEnabled {
		return 0, nil
	}
	if snapshotWindow <= 0 {
		snapshotWindow = 2 * time.Second
	}
	subClient, ok := b.client.(Client)
	if !ok {
		return 0, errCleanupClientLacksSubscribe
	}
	centralName := strings.ToLower(b.resolvedCentral(""))
	if centralName == "" {
		// Without a central name we cannot scope the orphan filter to
		// our own node_id namespace; refuse rather than risk wiping
		// another integration's discovery configs.
		return 0, nil
	}
	prefix := "homeassistant/"
	nodePrefix := centralName + "_"

	var (
		mu      sync.Mutex
		orphans []string
		seen    int
	)
	handler := func(topic string, _ []byte, _ bool) {
		if !strings.HasPrefix(topic, prefix) || !strings.HasSuffix(topic, "/config") {
			return
		}
		// Topic shape: homeassistant/<component>/<node_id>/<object_id>/config
		parts := strings.Split(strings.TrimPrefix(topic, prefix), "/")
		if len(parts) != 4 {
			return
		}
		nodeID := parts[1]
		if !strings.HasPrefix(strings.ToLower(nodeID), nodePrefix) {
			// Not our daemon's namespace — skip.
			return
		}
		mu.Lock()
		seen++
		mu.Unlock()
		// Compare against the live `declared` map: any retained topic
		// the current build did not (re)publish during boot is an
		// orphan. The check is lock-protected by the bridge.
		b.mu.Lock()
		_, declared := b.declared[topic]
		b.mu.Unlock()
		if declared {
			return
		}
		mu.Lock()
		orphans = append(orphans, topic)
		mu.Unlock()
	}

	filter := prefix + "#"
	if _, err := subClient.Subscribe(ctx, filter, b.cfg.QoS.Discovery, LegacyHandler(handler)); err != nil {
		return 0, err
	}
	timer := time.NewTimer(snapshotWindow)
	defer timer.Stop()
	select {
	case <-timer.C:
	case <-ctx.Done():
		return 0, ctx.Err()
	}
	if err := subClient.Unsubscribe(ctx, filter); err != nil {
		return 0, err
	}
	for _, topic := range orphans {
		_ = b.client.Publish(ctx, topic, nil, b.cfg.QoS.Discovery, true)
		// Also drop the orphan from `declared` so a subsequent
		// publish with the same topic is not silently dedup-suppressed
		// against the now-empty payload.
		b.mu.Lock()
		delete(b.declared, topic)
		b.mu.Unlock()
	}
	_ = seen
	return len(orphans), nil
}
