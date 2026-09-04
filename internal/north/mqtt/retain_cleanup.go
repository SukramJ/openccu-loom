// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package mqtt

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/SukramJ/openccu-loom/internal/model/naming"
	"github.com/SukramJ/openccu-loom/internal/routingkey"
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

	// retiredMetrics holds the exact retained topics of the retired
	// metric spelling (see [retiredMetricTopics]). A set of literal
	// topics rather than a matcher, because whether one of them is an
	// orphan or a live topic depends on the configured central names,
	// which are resolved once when the pass is constructed.
	retiredMetrics map[string]bool

	mu       sync.Mutex
	worklist []string // retained topic names earmarked for eviction
}

// NewRetainCleanup constructs a cleanup pass for the given bridge.
func NewRetainCleanup(b *Bridge) *RetainCleanup {
	c := &RetainCleanup{bridge: b}
	if b == nil {
		return c
	}
	retired := retiredMetricTopics(b.topics, b.cleanupCentralNames())
	if len(retired) > 0 {
		c.retiredMetrics = make(map[string]bool, len(retired))
		for _, topic := range retired {
			c.retiredMetrics[topic] = true
		}
	}
	return c
}

// retiredMetricTopics returns the central-wide metric topics that no
// publisher writes any more, for the given set of configured centrals.
//
// The three metric sensors used to put a lower-cased central into their
// topic while every other topic on the plane escapes it through
// [naming.TopicSafe]. The two spellings are the identical string for a
// plain lower-case ASCII CCU name and differ for every other one, so the
// old topics are orphans only in the deployments the change actually
// moved. Comparing per central is what makes the sweep safe: for a CCU
// named `ccu1` the "old" topic IS the live one, and clearing it would
// blank a value the daemon publishes.
//
// The same collision exists across centrals — one CCU's retired spelling
// can be another CCU's live topic — which is why every candidate is
// checked against the live topics of ALL configured centrals, not only
// against its own.
func retiredMetricTopics(topics *TopicBuilder, centralNames []string) []string {
	if topics == nil || len(centralNames) == 0 {
		return nil
	}
	live := make(map[string]bool, 3*len(centralNames))
	for _, name := range centralNames {
		if name == "" {
			continue
		}
		for _, topic := range topics.systemMetricTopics(name) {
			live[topic] = true
		}
	}
	var out []string
	seen := make(map[string]bool, len(live))
	for _, name := range centralNames {
		if name == "" {
			continue
		}
		for _, topic := range topics.systemMetricTopics(name) {
			retired := retiredMetricSpelling(topics.Base, name, topic)
			if retired == "" || live[retired] || seen[retired] {
				continue
			}
			seen[retired] = true
			out = append(out, retired)
		}
	}
	return out
}

// RetiredMetricTopics returns the metric topics [Bridge.RunRetainCleanupOnce]
// would clear if the broker still held them: the retired spelling of every
// configured central, minus every spelling that is some central's live topic.
//
// Exported so the composition root can pin that the bridge it builds knows
// the whole set of configured centrals. With only the default central
// reaching it the sweep stays silent for every other CCU, which is
// indistinguishable from a correctly guarded sweep — both clear nothing.
func (b *Bridge) RetiredMetricTopics() []string {
	if b == nil {
		return nil
	}
	return retiredMetricTopics(b.topics, b.cleanupCentralNames())
}

// retiredMetricSpelling rewrites one current metric topic into the shape
// the earlier build wrote: same base, same metric leaf, central segment
// lower-cased instead of escaped. The leaf is taken from the live topic
// so this cannot name a metric the builder does not publish, and stays
// correct when a fourth metric joins the group.
func retiredMetricSpelling(base, centralName, liveTopic string) string {
	slash := strings.LastIndex(liveTopic, "/")
	if slash < 0 {
		return ""
	}
	return base + "/" + strings.ToLower(centralName) + "/system/" + liveTopic[slash+1:]
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
	// The reserved hub subtree (`<central>/hub/...`) is not a device
	// channel: `hub/programs/<numeric-id>/state` and a numeric-named
	// `hub/sysvars/<n>/state` would otherwise pass the numeric-channel
	// check and get their live retained state wiped on every boot.
	if parts[1] == "hub" {
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
	// Reserved hub subtree — never a device data point (see
	// [LegacyAggregateStateMatcher]).
	if parts[1] == "hub" {
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

// ProgramTriggerMirrorMatcher reports whether topic matches the program
// trigger-command shape:
//
//	<topic_base>/<central>/hub/programs/<id>/trigger
//
// A retained message parked there can only be junk: the topic is a
// command topic — the daemon is its sole intended consumer and drops
// retained deliveries — and earlier builds mirrored the program's
// active flag onto it retained ("true"/"false"), which is how the
// state plane leaked into the command plane in the first place (see
// [Bridge.PublishProgram]). Evicting the parked payload keeps foreign
// tools and mis-flagging brokers from ever replaying it as a command.
func ProgramTriggerMirrorMatcher(topicBase, topic string) bool {
	if topicBase == "" {
		return false
	}
	if !strings.HasPrefix(topic, topicBase+"/") {
		return false
	}
	tail := strings.TrimPrefix(topic, topicBase+"/")
	parts := strings.Split(tail, "/")
	// `<central>/hub/programs/<id>/trigger` — 5 segments.
	if len(parts) != 5 || parts[1] != "hub" || parts[2] != "programs" || parts[4] != "trigger" {
		return false
	}
	return parts[0] != "" && parts[3] != ""
}

// collect inspects topic+payload pairs delivered by a retained-topic
// snapshot subscription and accumulates eviction candidates. The
// payload is unused here — we only care about the topic shape — but
// kept on the signature so the call site matches the
// [MessageHandler] contract.
//
// Matches three legacy shapes retired by the Option-B topology
// migration:
//   - bucket-less DataPointState (`<addr>/<ch>/<PARAM>`)
//   - verbose SlotState (`<addr>/channels/<ch>/<bucket>/<PARAM>/state`)
//   - aggregated channel state where the StatePayload schema changed
//     between builds (custom DP roll-ups; retain when JSON shape no
//     longer parses)
//
// plus the retired program-trigger state mirror (see
// [ProgramTriggerMirrorMatcher]) and the retired lower-cased spelling of
// the central-wide metric topics (see [retiredMetricTopics]).
func (c *RetainCleanup) collect(topic string, _ []byte, _ bool) {
	if c.bridge == nil {
		return
	}
	base := c.bridge.cfg.Base
	if LegacyDataPointStateMatcher(base, topic) ||
		LegacySlotStateMatcher(base, topic) ||
		LegacyAggregateStateMatcher(base, topic) ||
		ProgramTriggerMirrorMatcher(base, topic) ||
		c.retiredMetrics[topic] {
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
// is narrower; production wiring uses the [Client] alias of the shared
// go-mqtt client.
func (b *Bridge) RunRetainCleanupOnce(ctx context.Context, snapshotWindow time.Duration) (int, error) {
	if !b.cfg.RawEnabled {
		return 0, nil
	}
	if snapshotWindow <= 0 {
		snapshotWindow = 2 * time.Second
	}
	subClient, ok := b.cleanupSubscriber()
	if !ok {
		return 0, errCleanupClientLacksSubscribe
	}
	cleanup := NewRetainCleanup(b)
	// Wait for the broker to deliver retained messages.
	if err := b.snapshotRetained(ctx, subClient, b.cfg.Base+"/#", b.cfg.QoS.State, snapshotWindow, cleanup.collect); err != nil {
		return 0, err
	}
	worklist := cleanup.Worklist()
	for _, topic := range worklist {
		_ = b.client.Publish(ctx, topic, nil, b.cfg.QoS.State, true)
	}
	return len(worklist), nil
}

// snapshotRetained installs filter on the shared subscribe client for
// the length of window, feeding every delivered message to collect, and
// takes the subscription down again on every exit path — a cancelled
// context and a broker that refuses the UNSUBSCRIBE included.
//
// Both halves matter, and both used to be missing. The sweeps are
// one-shot boot passes over broad wildcards (`<base>/#`,
// `homeassistant/#`) whose handler closes over a growing worklist, so a
// subscription left installed keeps that worklist alive and keeps
// running on the daemon's own publishes for the rest of the process —
// and go-mqtt replays its subscriptions on reconnect, so it survives the
// very broker restart that stranded it. The collect gate bounds the
// damage in the case the teardown itself fails: the handler stays
// registered, but it stops accumulating once its window has closed.
func (b *Bridge) snapshotRetained(
	ctx context.Context,
	subClient Subscriber,
	filter string,
	qos QoS,
	window time.Duration,
	collect func(topic string, payload []byte, retained bool),
) error {
	// One snapshot window at a time per bridge. Every sweep rides the same
	// subscribe client, which keys its subscriptions by filter: two
	// concurrent windows on the same filter — the per-central discovery
	// sweeps and the unscoped pass all use `homeassistant/#` — leave the
	// second handler installed over the first, and the first teardown
	// unsubscribes the filter for both. Both sweeps then report nothing
	// and the orphaned configs they exist to evict survive.
	//
	// The sweeps are one-shot boot passes of a couple of seconds each, so
	// serialising them costs boot time, not correctness. Waiting is
	// bounded by the holder's own window and its context.
	if !b.acquireSweepSlot(ctx) {
		return fmt.Errorf("%w: %w", ErrSweepSlotBusy, ctx.Err())
	}
	defer b.releaseSweepSlot()

	// Spend what is left of the caller's budget rather than the full
	// window it asked for. Waiting for the slot eats into that budget,
	// and a caller whose budget is the window plus a small margin — the
	// boot scrubs are — would otherwise open a window it cannot finish
	// and return DeadlineExceeded having evicted nothing. A short window
	// only means fewer retained messages are seen, and the sweeps evict
	// strictly what they saw, so trading window length for a completed
	// pass is safe in a way that returning empty-handed is not.
	if deadline, ok := ctx.Deadline(); ok {
		// Leave room for the unsubscribe and the eviction publishes that
		// follow the window; a window that runs to the last microsecond
		// cancels its own teardown. The reserve is a quarter of what is
		// left for a small budget and a flat second once the budget is
		// large enough to spare one.
		remaining := time.Until(deadline)
		margin := min(snapshotWindowMargin, remaining/4)
		usable := remaining - margin
		if usable <= 0 {
			return fmt.Errorf("%w: no budget left for a snapshot window", ErrSweepSlotBusy)
		}
		window = min(window, usable)
	}

	var closed atomic.Bool
	gated := func(topic string, payload []byte, retained bool) {
		if closed.Load() {
			return
		}
		collect(topic, payload, retained)
	}
	if _, err := subClient.Subscribe(ctx, filter, qos, LegacyHandler(gated)); err != nil {
		return err
	}
	defer func() {
		closed.Store(true)
		// Detached from ctx on purpose: a shutdown mid-window is exactly
		// the case where the subscription would otherwise be stranded.
		if err := subClient.Unsubscribe(context.WithoutCancel(ctx), filter); err != nil {
			slog.Default().Warn("mqtt.retain_cleanup.unsubscribe",
				slog.String("filter", filter), slog.String("err", err.Error()))
		}
	}()
	timer := time.NewTimer(window)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// cleanupSubscriber returns the subscribe-capable client the cleanup
// passes ride on: the explicitly wired [Bridge.WithSubscriber] client
// (production — the publish path is a publish-only circuit-breaker
// decorator), or the publish client itself when it happens to satisfy
// [Client] (tests and the no-broker NoopClient wiring).
func (b *Bridge) cleanupSubscriber() (Subscriber, bool) {
	if b.subscriber != nil {
		return b.subscriber, true
	}
	if c, ok := b.client.(Client); ok {
		return c, true
	}
	return nil, false
}

// ErrSweepSlotBusy reports that a retained-snapshot pass never opened its
// window because another sweep held the bridge's snapshot slot for longer
// than the caller's budget allowed.
//
// It is a distinct sentinel because it means "not attempted", not
// "attempted and found nothing": a caller that runs its sweeps once per
// process must retry on this rather than latch its once-guard, or the
// scrub is skipped for the rest of the daemon's life.
var ErrSweepSlotBusy = errors.New("mqtt: retained-snapshot slot busy")

// snapshotWindowMargin is the slice of a caller's remaining budget the
// snapshot window leaves untouched, so the unsubscribe and the eviction
// publishes that follow it still have a live context.
const snapshotWindowMargin = time.Second

// ErrCleanupNeedsSubscriber surfaces when the bridge has neither a
// [Bridge.WithSubscriber]-wired client nor a publish client that
// satisfies the Client interface — the cleanup passes need subscribe
// capability to snapshot the broker's retained store. Exported so the
// composition root can pin that its wiring gets past this check.
var ErrCleanupNeedsSubscriber = retainCleanupError("bridge client must satisfy the Client interface (publish+subscribe) for retain cleanup")

// errCleanupClientLacksSubscribe is the internal alias the cleanup
// passes return.
var errCleanupClientLacksSubscribe = ErrCleanupNeedsSubscriber

type retainCleanupError string

func (e retainCleanupError) Error() string { return string(e) }

// hubPlaneNodeKinds are the node-id leaves the per-central hub plane
// publishes under: `<central-slug>_<kind>`. Device configs use the
// device address as their leaf, which is never one of these words.
//
// The hub plane needs naming because it is central-scoped and therefore
// passes the sweep's `<central>_` ownership filter, unlike the
// daemon-level planes in [daemonLevelNodeIDs] — and it is the plane that
// publishes LAST: sysvars, programs, install-mode and the system sensors
// only reach the broker once the CCU's serial has resolved, well after
// the device snapshot that triggers the sweep.
var hubPlaneNodeKinds = map[string]bool{
	"central":      true,
	"connectivity": true,
	"messages":     true,
	"programs":     true,
	"sysvars":      true,
	"system":       true,
}

// hubPlaneNodeID reports whether nodeID is a hub-plane node id — its leaf
// (everything after the central-slug separator [discoveryNodeIDBelongsTo]
// matched on) names one of [hubPlaneNodeKinds].
func hubPlaneNodeID(nodeID string) bool {
	i := strings.LastIndexByte(nodeID, '_')
	if i < 0 {
		return false
	}
	return hubPlaneNodeKinds[nodeID[i+1:]]
}

// hubPlaneKey is the [Bridge.planesDeclared] key of one central's hub
// plane. Namespaced so it can never collide with a daemon-level plane's
// node id.
func hubPlaneKey(centralName string) string {
	return "hub:" + naming.DiscoverySlug(centralName)
}

// MarkHubPlaneDeclared records that the hub plane has published everything
// it declares for centralName, making its retained configs eligible for the
// orphan sweep.
//
// The hub publisher calls it behind its own publish queue, so by the time
// the mark lands every config of that pass is on the broker and in
// `declared`. Until then the sweep leaves hub node ids alone: a hub config
// the previous boot retained is indistinguishable from an orphan, and
// retracting it deletes the Home Assistant entity (and its registry entry,
// with the operator's name / area / entity_id on it) seconds before this
// boot re-announces it.
func (b *Bridge) MarkHubPlaneDeclared(centralName string) {
	if b == nil || centralName == "" {
		return
	}
	b.MarkPlaneDeclared(hubPlaneKey(centralName))
}

// MarkPlaneDeclared records that a plane has finished its first discovery
// pass, making its retained configs eligible for the orphan sweep.
//
// Until a plane says this, the sweep cannot distinguish its orphans from
// its not-yet-published entities, and treating the two alike deletes
// working entities on every restart.
func (b *Bridge) MarkPlaneDeclared(nodeID string) {
	if b == nil {
		return
	}
	b.mu.Lock()
	if b.planesDeclared == nil {
		b.planesDeclared = map[string]bool{}
	}
	b.planesDeclared[strings.ToLower(nodeID)] = true
	b.mu.Unlock()
}

// planeDeclared reports whether a daemon-level plane has declared.
func (b *Bridge) planeDeclared(nodeID string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.planesDeclared[nodeID]
}

// daemonLevelNodeIDs are the discovery node ids of the planes that are
// not scoped to a central: the alarm engine and the Security & Safety
// domain.
//
// They need naming explicitly because the orphan sweep otherwise filters
// on the `<central>_` node prefix — which these deliberately do not
// carry (ADR 0052). Without this, a retracted zone panel or a class that
// lost its last source would keep a retained discovery config alive in
// every consumer forever, and no cleanup pass could ever reach it.
var daemonLevelNodeIDs = map[string]bool{
	alarmDiscoveryNodeID:    true,
	securityDiscoveryNodeID: true,
}

// discoveryNodePrefixes returns every `<central>_` node-id prefix the
// orphan sweep has to accept for one central.
//
// Both producers — [naming.PathData.DiscoveryNodeID] for per-device
// configs and [hubNodeID] for hub configs — now slug the central name
// through [naming.DiscoverySlug], so the canonical prefix is the first
// entry. The second is the spelling earlier builds put on the wire
// (`strings.ToLower(naming.TopicSafe(name))`), kept so the configs an
// older daemon retained under a name carrying a dot or an umlaut are
// still reachable and can be swept once. Deriving the prefix by hand
// (`strings.ToLower(name)`) matched neither, which made the whole pass
// a silent no-op for every central whose name is not already a slug.
func discoveryNodePrefixes(centralName string) []string {
	if centralName == "" {
		return nil
	}
	canonical := naming.DiscoverySlug(centralName) + "_"
	prefixes := []string{canonical}
	if legacy := strings.ToLower(naming.TopicSafe(centralName)) + "_"; legacy != canonical {
		prefixes = append(prefixes, legacy)
	}
	return prefixes
}

// discoveryNodeIDBelongsTo reports whether nodeID is a retained HA-Discovery
// config owned by the central whose node-id prefixes are `prefixes` (its
// canonical and legacy slug spellings, each ending in "_"; see
// [discoveryNodePrefixes]).
//
// Every producer spells a node id as `<central-slug>_<object>`, where the
// object — a device address ([naming.PathData.DiscoveryNodeID]) or a hub kind
// ([hubNodeID]) — never carries an underscore. The central slug therefore runs
// up to the LAST underscore, which is a real boundary: matching the exact
// segment there keeps a central named `ccu` (slug `ccu`) from claiming a
// sibling `ccu wohnung`'s (slug `ccu_wohnung`) `ccu_wohnung_*` node ids. A bare
// HasPrefix("ccu_") test did claim them, and because the sibling's entities are
// not yet in `declared` during the first CCU's boot sweep, it evicted every one
// of them until the daemon next restarted. The raw plane escapes this because
// its central segment is delimited by "/", never "_".
func discoveryNodeIDBelongsTo(nodeID string, prefixes []string) bool {
	i := strings.LastIndexByte(nodeID, '_')
	if i <= 0 || i == len(nodeID)-1 {
		return false
	}
	segment := nodeID[:i+1] // "<central-slug>_", the exact segment to match
	for _, p := range prefixes {
		if segment == p {
			return true
		}
	}
	return false
}

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
//
// centralName scopes the pass to one CCU's node-id namespace, exactly
// like [Bridge.RunRawOrphanCleanupOnce]: both node-id producers
// ([naming.PathData.DiscoveryNodeID] and [hubNodeID]) slug the central
// they belong to, so a pass that derived the prefix from the default
// central alone could never reach a second CCU's orphans — their
// retained configs kept re-creating permanently unavailable phantom
// entities that no automatic pass could remove. An empty centralName
// falls back to the bridge default, which is the single-CCU case.
// Run it per central, after that central's snapshot: the other
// centrals' entities are not in `declared` yet and must not be judged.
func (b *Bridge) RunDiscoveryOrphanCleanupOnce(ctx context.Context, centralName string, snapshotWindow time.Duration) (int, error) {
	if !b.cfg.HADiscoveryEnabled {
		return 0, nil
	}
	if snapshotWindow <= 0 {
		snapshotWindow = 2 * time.Second
	}
	subClient, ok := b.cleanupSubscriber()
	if !ok {
		return 0, errCleanupClientLacksSubscribe
	}
	rawCentral := b.resolvedCentral(centralName)
	if rawCentral == "" {
		// Without a central name we cannot scope the orphan filter to
		// our own node_id namespace; refuse rather than risk wiping
		// another integration's discovery configs.
		return 0, nil
	}
	prefix := naming.DiscoveryTopicPrefix
	nodePrefixes := discoveryNodePrefixes(rawCentral)

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
		nodeID := strings.ToLower(parts[1])
		switch {
		case discoveryNodeIDBelongsTo(nodeID, nodePrefixes):
			// The central's own namespace — but the hub plane inside it
			// publishes on its own schedule, long after the device
			// snapshot that triggers this sweep. Judging it before it has
			// declared retracts every sysvar, program, install-mode and
			// system entity of the previous boot; the pass runs once per
			// boot, so whatever it deletes stays deleted until the daemon
			// restarts. See [Bridge.MarkHubPlaneDeclared].
			if hubPlaneNodeID(nodeID) && !b.planeDeclared(hubPlaneKey(rawCentral)) {
				return
			}
		case daemonLevelNodeIDs[nodeID]:
			// A daemon-level plane is only swept once it has declared.
			//
			// The sweep runs during southbound bring-up, hundreds of
			// lines before these planes are even constructed, so at that
			// moment nothing of theirs is in `declared` and every one of
			// their retained configs looks like an orphan. Sweeping then
			// deleted the security entities on every single restart, and
			// with the domain not yet started nothing re-declared them —
			// they vanished from the consumer along with every
			// automation and dashboard card built on them.
			if !b.planeDeclared(nodeID) {
				return
			}
		default:
			// Not our daemon's namespace — skip.
			return
		}
		mu.Lock()
		seen++
		mu.Unlock()
		// Compare against what this process claims: any retained topic
		// the current build did not (re)publish during boot is an
		// orphan. `announced` is consulted alongside `declared` because
		// a config still inside its Publish call is already on the
		// broker — and already delivered to this very subscription —
		// while `declared` records it only afterwards. The check is
		// lock-protected by the bridge.
		b.mu.Lock()
		_, declared := b.declared[topic]
		claimed := declared || b.announced[topic]
		b.mu.Unlock()
		if claimed {
			return
		}
		mu.Lock()
		orphans = append(orphans, topic)
		mu.Unlock()
	}

	if err := b.snapshotRetained(ctx, subClient, prefix+"#", b.cfg.QoS.Discovery, snapshotWindow, handler); err != nil {
		return 0, err
	}
	// Copy under the same lock the deliveries append under: the window is
	// closed by an atomic flag, so a delivery that passed the gate a
	// moment earlier can still be inside the append while this goroutine
	// reads the slice.
	// Both are read under the same lock the deliveries write under: the
	// window is closed by an atomic flag, so a delivery that passed the
	// gate a moment earlier can still be inside the handler while this
	// goroutine reads.
	mu.Lock()
	topics := append([]string(nil), orphans...)
	inspected := seen
	mu.Unlock()
	evicted := b.evictDiscoveryOrphans(ctx, topics)
	// The pair is what makes a silent sweep diagnosable: zero inspected
	// means the snapshot window saw none of our retained configs, which
	// is a different fault from a window that saw them all and found
	// nothing orphaned.
	slog.Default().Debug("mqtt.discovery_orphan_cleanup.snapshot",
		slog.String("central", rawCentral),
		slog.Int("inspected", inspected),
		slog.Int("evicted", evicted))
	return evicted, nil
}

// evictDiscoveryOrphans retracts each candidate topic and reports how many
// it actually cleared.
//
// Every topic is re-checked against the bridge's claims immediately before
// its retraction rather than on the verdict the snapshot window produced:
// clearing thousands of topics takes seconds, and a publisher that declared
// one of them in the meantime has made it live again — retracting it would
// delete an entity that exists.
func (b *Bridge) evictDiscoveryOrphans(ctx context.Context, topics []string) int {
	evicted := 0
	for _, topic := range topics {
		b.mu.Lock()
		_, declared := b.declared[topic]
		claimed := declared || b.announced[topic]
		b.mu.Unlock()
		if claimed {
			continue
		}
		_ = b.client.Publish(ctx, topic, nil, b.cfg.QoS.Discovery, true)
		evicted++
		// Also drop the orphan from `declared` so a subsequent publish
		// with the same topic is not silently dedup-suppressed against
		// the now-empty payload.
		b.mu.Lock()
		delete(b.declared, topic)
		b.mu.Unlock()
	}
	return evicted
}

// rawCentralPrefix returns the `<base>/<central>/` prefix the raw plane
// publishes every per-data-point topic under. One definition for both halves
// of the orphan sweep — the subscribe filter and the candidate matcher — so
// they cannot look in different places.
//
// The base is trimmed and the central name escaped exactly as
// [naming.PathData.MQTTState] does when it writes those topics. Both had
// drifted: a `topic_base` written with a trailing slash and a CCU whose name
// carries a space each produced a sweep that collected nothing at all, so
// retained topics from a previous build kept feeding stale values forever.
func rawCentralPrefix(topicBase, centralName string) string {
	return strings.Trim(topicBase, "/") + "/" + naming.TopicSafe(centralName) + "/"
}

// RawOrphanCandidateMatcher reports whether topic is a per-DP bucket topic
// (state or its /config companion) of the given central under topicBase:
//
//	<topic_base>/<central>/<iface>/<address>/<channelNo>/<bucket>/<PARAM>
//	<topic_base>/<central>/<iface>/<address>/<channelNo>/<bucket>/<PARAM>/config
//
// with `<bucket>` one of values / master / calculated / custom and a numeric
// channel id. The reserved hub subtree (`<central>/hub/...`) never matches.
// Exposed for unit testing — production consumes it via
// [Bridge.RunRawOrphanCleanupOnce].
//
// centralName is the configured name; the comparison escapes it through
// [naming.TopicSafe] because that is what every publisher writes into
// the topic. Matching the raw name instead made the sweep miss every
// topic of a central whose name contains a space.
func RawOrphanCandidateMatcher(topicBase, centralName, topic string) bool {
	if topicBase == "" || centralName == "" {
		return false
	}
	prefix := rawCentralPrefix(topicBase, centralName)
	if !strings.HasPrefix(topic, prefix) {
		return false
	}
	parts := strings.Split(strings.TrimPrefix(topic, prefix), "/")
	// `<iface>/<address>/<channel>/<bucket>/<PARAM>` — 5 segments for the
	// state topic, 6 with the trailing `config` companion.
	if len(parts) == 6 {
		if parts[5] != "config" {
			return false
		}
		parts = parts[:5]
	}
	if len(parts) != 5 {
		return false
	}
	if parts[0] == "hub" {
		return false
	}
	switch parts[3] {
	case "values", "master", "calculated", "custom":
	default:
		return false
	}
	channel := parts[2]
	if channel == "" {
		return false
	}
	for _, r := range channel {
		if r < '0' || r > '9' {
			return false
		}
	}
	return parts[4] != ""
}

// RunRawOrphanCleanupOnce subscribes to the central's raw-plane subtree for a
// short snapshot window, accumulates every retained per-DP bucket topic
// (state + /config), then evicts the ones the current model did NOT
// (re)publish during the central's snapshot — i.e. topics absent from the
// bridge's rawTopics / configCache bookkeeping. Anything in that set is a
// leftover from a previous build or boot: a MASTER paramset published before
// the visibility passes gated it, a suppressed VALUES parameter, a retired
// calculated DP.
//
// Must run AFTER the central's snapshot pass populated the bookkeeping —
// the daemon drives it from the post-snapshot hook, never inline at boot.
// Best-effort: returns the number of orphans evicted plus any subscribe
// error.
func (b *Bridge) RunRawOrphanCleanupOnce(ctx context.Context, centralName string, snapshotWindow time.Duration) (int, error) {
	if !b.cfg.RawEnabled {
		return 0, nil
	}
	if snapshotWindow <= 0 {
		snapshotWindow = 2 * time.Second
	}
	centralName = b.resolvedCentral(centralName)
	if centralName == "" {
		return 0, nil
	}
	subClient, ok := b.cleanupSubscriber()
	if !ok {
		return 0, errCleanupClientLacksSubscribe
	}

	var (
		mu      sync.Mutex
		orphans []string
	)
	handler := func(topic string, payload []byte, _ bool) {
		// A zero-length payload is an eviction in flight, not a retained value.
		if len(payload) == 0 {
			return
		}
		if !RawOrphanCandidateMatcher(b.cfg.Base, centralName, topic) {
			return
		}
		b.mu.Lock()
		_, isState := b.rawTopics[topic]
		_, isConfig := b.configCache[topic]
		b.mu.Unlock()
		if isState || isConfig {
			return
		}
		mu.Lock()
		orphans = append(orphans, topic)
		mu.Unlock()
	}

	filter := rawCentralPrefix(b.cfg.Base, centralName) + "#"
	if err := b.snapshotRetained(ctx, subClient, filter, b.cfg.QoS.State, snapshotWindow, handler); err != nil {
		return 0, err
	}
	// Copy under the same lock the deliveries append under — see the
	// identical note in [Bridge.RunDiscoveryOrphanCleanupOnce].
	mu.Lock()
	topics := append([]string(nil), orphans...)
	mu.Unlock()
	for _, topic := range topics {
		_ = b.client.Publish(ctx, topic, nil, b.cfg.QoS.State, true)
	}
	return len(topics), nil
}

// unscopedUniqueIDMarker is what an entity id looks like when the CCU
// serial slot was empty at publish time: the loom namespace, the missing
// discriminator, and the separator that follows it.
const unscopedUniqueIDMarker = loomNamespacePrefix + "_"

// loomNamespacePrefix is the namespace every entity id this daemon
// publishes carries. Spelled here rather than imported so the sweep
// matches the string that is actually on the broker.
const loomNamespacePrefix = "loom_"

// cuxdIDSegment opens a folded CUxD address inside a routing key. A CUxD
// serial is "CUX" plus digits, so the digit that has to follow is what
// separates the address from a CCU-serial discriminator that merely
// happens to begin with the same three letters.
const cuxdIDSegment = "cux"

// carriesLegacyUnscopedCUxDID reports whether an entity id was built
// before CUxD addresses joined the central-scoped address classes.
//
// CUxD hands out the same synthetic addresses on every CCU it runs on, so
// its ids gained the CCU-serial discriminator. The discovery TOPIC did not
// change — it is built from the address, not from the id — so the retained
// config is overwritten in place and the ordinary orphan sweep, which
// compares topics, sees nothing. Home Assistant keys its registry on the
// id: it keeps the entity it already has under the old key and, on its
// next restart, adds a second one under the new key while the first stays
// registered and permanently unavailable.
//
// The two shapes below are the only ones the discovery builders ever
// produced for a CUxD data point — every call site passes an empty family
// prefix except the calculated branch — so the match is exact rather than
// heuristic. The scoped spelling puts the serial discriminator in the
// segment they occupy, so a current payload never matches.
func carriesLegacyUnscopedCUxDID(uniqueID string) bool {
	for _, marker := range [...]string{
		loomNamespacePrefix + cuxdIDSegment,
		loomNamespacePrefix + routingkey.CalculatedFamilyPrefix + "_" + cuxdIDSegment,
	} {
		rest, ok := strings.CutPrefix(uniqueID, marker)
		if !ok || rest == "" {
			continue
		}
		if rest[0] >= '0' && rest[0] <= '9' {
			return true
		}
	}
	return false
}

// RunUnscopedDiscoveryCleanupOnce subscribes to `homeassistant/#` for a
// short window and clears every retained discovery config this daemon
// published with an entity id it can no longer address: one whose
// CCU-serial slot was empty, leaving `loom__…`, and one built for a CUxD
// address before that class was namespaced by the central
// ([carriesLegacyUnscopedCUxDID]).
//
// It exists because the ordinary orphan sweep cannot see this class. That
// one compares topics against what the current build declared, and here
// the topic is unchanged: the same entity is republished on the same
// topic with a corrected id. What changes is inside the payload. So the
// broker ends up carrying the right config while the consumer still holds
// the wrong identity — Home Assistant keys its registry on unique_id, so
// the corrected payload creates a second entity beside the stale one
// instead of replacing it.
//
// Clearing the topic first is what makes the consumer forget: an empty
// retained payload removes the entity, and the snapshot that follows
// announces it again under the id that carries the serial. That ordering
// is the reason this runs before [EventBridge.PublishInitialSnapshot]
// rather than beside the other sweeps, which run after it.
//
// Scope is the daemon's own payloads, identified by the origin block the
// discovery builder writes. A second loom daemon on the same broker
// would have its unscoped ids cleared too — they are equally ambiguous,
// and it republishes them correctly on its own next snapshot.
//
// Best-effort: returns the number of configs cleared plus any subscribe
// error.
func (b *Bridge) RunUnscopedDiscoveryCleanupOnce(ctx context.Context, snapshotWindow time.Duration) (int, error) {
	if !b.cfg.HADiscoveryEnabled {
		return 0, nil
	}
	if snapshotWindow <= 0 {
		snapshotWindow = 2 * time.Second
	}
	subClient, ok := b.cleanupSubscriber()
	if !ok {
		return 0, errCleanupClientLacksSubscribe
	}

	var (
		mu    sync.Mutex
		stale []string
	)
	handler := func(topic string, payload []byte, _ bool) {
		if !strings.HasPrefix(topic, naming.DiscoveryTopicPrefix) || !strings.HasSuffix(topic, "/config") {
			return
		}
		if len(payload) == 0 {
			return
		}
		if !payloadCarriesUnscopedUniqueID(payload) {
			return
		}
		mu.Lock()
		stale = append(stale, topic)
		mu.Unlock()
	}

	if err := b.snapshotRetained(ctx, subClient, naming.DiscoveryTopicPrefix+"#", b.cfg.QoS.Discovery, snapshotWindow, handler); err != nil {
		return 0, err
	}

	mu.Lock()
	topics := append([]string(nil), stale...)
	mu.Unlock()
	for _, topic := range topics {
		_ = b.client.Publish(ctx, topic, nil, b.cfg.QoS.Discovery, true)
		// Drop it from `declared` as well: the snapshot that follows has
		// to republish this topic, and the diff gate would otherwise
		// suppress it against the payload we just cleared.
		b.mu.Lock()
		delete(b.declared, topic)
		b.mu.Unlock()
	}
	return len(topics), nil
}

// payloadCarriesUnscopedUniqueID reports whether a retained discovery
// config was published by this daemon with an entity id the current build
// no longer emits for that topic: an empty serial slot, or a CUxD address
// from before that class was namespaced by the central.
//
// Both halves matter. The origin block keeps the sweep off other
// integrations' payloads, which may legitimately use any id shape; the
// markers identify the ones this daemon can no longer address, because a
// second CCU would produce the identical string.
func payloadCarriesUnscopedUniqueID(payload []byte) bool {
	var body struct {
		UniqueID string `json:"unique_id"`
		Origin   struct {
			Name string `json:"name"`
		} `json:"origin"`
	}
	if err := json.Unmarshal(payload, &body); err != nil {
		return false
	}
	if body.Origin.Name != originName {
		return false
	}
	if strings.HasPrefix(body.UniqueID, unscopedUniqueIDMarker) {
		return true
	}
	return carriesLegacyUnscopedCUxDID(body.UniqueID)
}
