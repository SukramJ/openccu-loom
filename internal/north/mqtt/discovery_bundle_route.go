// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package mqtt

import (
	"context"
	"errors"
	"sort"
	"strings"
	"sync"
	"time"

	hapublisher "github.com/SukramJ/go-hamqtt/publisher"

	"github.com/SukramJ/openccu-loom/internal/model/naming"
)

// newBundleStoreIf returns a store when device-bundle mode is on, and nil
// otherwise. The nil is what every bundle path switches on, so the mode is
// decided in one place rather than re-read from the config at each one.
func newBundleStoreIf(on bool) *discoveryBundleStore {
	if !on {
		return nil
	}
	return newDiscoveryBundleStore()
}

// routeToBundle is what [Bridge.publishDiscovery] becomes in device-bundle
// mode: the same call from the same eight producers, landing in a document
// instead of on a topic of its own.
//
// Putting the diversion at that one function is what keeps this change
// small. The producers build a typed component and marshal it at 54
// construction sites across 11 files; they all funnel through
// publishDiscovery, so the fan-in converts here or nowhere.
//
// An empty payload is a retraction, and in a document a retraction is a
// tombstone rather than an absence — Home Assistant removes a component only
// when its entry is present and carries a platform alone. The store keeps
// the platform for exactly this.
//
// The bundle is published on every call, unless a batch is open — see
// [Bridge.BeginBundleBatch], which is what keeps the boot snapshot from
// writing a sixteen-entity device's document sixteen times.
func (b *Bridge) routeToBundle(ctx context.Context, centralName, component, nodeID, objectID string, payload []byte) error {
	if len(payload) == 0 {
		b.bundles.Remove(nodeID, objectID)
	} else if err := b.bundles.Put(nodeID, objectID, component, payload); err != nil {
		// A body this daemon just marshalled and cannot read back is a bug
		// here, not a broker problem. Reporting it is right; dropping the
		// entity silently is how one goes missing for a release.
		return err
	}
	b.mu.Lock()
	batching := b.bundleBatch
	b.mu.Unlock()
	if batching {
		b.markBundleDirty(nodeID, centralName)
		return nil
	}
	return b.publishDeviceBundle(ctx, centralName, nodeID, b.bundles)
}

// BeginBundleBatch suspends the per-entity bundle publish until
// [Bridge.FlushBundles].
//
// The boot snapshot walks every datapoint of every device, and each one
// arrives as its own publishDiscovery call. Publishing the node's document
// on each would write a sixteen-entity device sixteen times — the same
// message count the per-entity form sends, but each message carrying the
// whole growing document, so roughly eight times the bytes for that device.
//
// Batching turns that back into one write per device. It is a change to when
// the publish happens and to nothing else: the document a flush writes is
// the one the last call would have written.
//
// A no-op outside device-bundle mode, so the boot path can call it
// unconditionally rather than branching on a mode it does not own.
func (b *Bridge) BeginBundleBatch() {
	if b.bundles == nil {
		return
	}
	b.mu.Lock()
	b.bundleBatch = true
	if b.bundleDirty == nil {
		b.bundleDirty = map[string]string{}
	}
	b.mu.Unlock()
}

// FlushBundles publishes every node that changed during the batch and ends
// it.
//
// It flushes all of them, not just one central's. The hub, alarm and
// security planes publish under no central at all, so a per-central flush
// would leave their documents waiting for a central that never comes. The
// cost of flushing everything is that a node can be written once per central
// instead of exactly once — bounded by the number of CCUs, which is one or
// two, against the hundreds of writes batching removes.
//
// Best-effort per node: one device whose publish fails must not abort the
// rest, or a single broker hiccup costs the whole fleet its discovery. Every
// failure is joined so the caller can log the whole picture, and the node
// stays dirty for the next flush.
func (b *Bridge) FlushBundles(ctx context.Context) error {
	if b.bundles == nil {
		return nil
	}
	b.mu.Lock()
	b.bundleBatch = false
	dirty := b.bundleDirty
	b.bundleDirty = map[string]string{}
	b.mu.Unlock()

	nodes := make([]string, 0, len(dirty))
	for node := range dirty {
		nodes = append(nodes, node)
	}
	// Sorted so a boot's publish order is reproducible, which is what makes
	// a capture of one boot comparable with a capture of the next.
	sort.Strings(nodes)

	var errs []error
	for _, node := range nodes {
		if err := ctx.Err(); err != nil {
			// A cancelled context is a shutdown, not a per-node failure.
			errs = append(errs, err)
			b.markBundleDirty(node, dirty[node])
			break
		}
		if err := b.publishDeviceBundle(ctx, dirty[node], node, b.bundles); err != nil {
			errs = append(errs, err)
			b.markBundleDirty(node, dirty[node])
		}
	}
	return errors.Join(errs...)
}

// markBundleDirty records a node for the next flush. A node re-marked after
// a failed flush keeps its central label, so its counters stay attributed
// even when the publish that failed is retried in a later batch.
func (b *Bridge) markBundleDirty(nodeID, centralName string) {
	b.mu.Lock()
	if b.bundleDirty == nil {
		b.bundleDirty = map[string]string{}
	}
	b.bundleDirty[nodeID] = centralName
	b.mu.Unlock()
}

// RunBundleRollbackOnce clears the retained device documents this daemon
// left behind, for a boot that is publishing per-entity configs again.
//
// It exists because Home Assistant's refusal is symmetric. Measured on a
// live instance (ADR 0070, amendment of 2026-09-11): a per-entity config
// published while a device document for the same entity is still retained
// is refused with the same warning as the other direction, the topics named
// the other way round, and the same total silence everywhere else. Turning
// device-bundle mode back off is therefore not "stop publishing bundles" —
// without this, every per-entity config of that first boot is refused and
// the entities stay missing until some later boot happens to clear the
// documents.
//
// The shared runtime builds that symmetry into
// [hapublisher.Runtime.PublishComponent], which retracts the device
// document before the per-entity config of the same node. It does not cover
// this daemon, and the reason is the payload: PublishComponent renders the
// body from a [hadiscovery.Component] through EntityJSON, while every one
// of this daemon's 54 producers hands the publish path bytes it has already
// marshalled — bytes pinned to the byte under
// `testdata/discovery_golden*.json`, which HA keys entities off. Routing
// them through PublishComponent would re-render them. So this pass stays,
// and it is the cheaper shape anyway: one narrow subscribe for the whole
// fleet instead of one retraction message per entity.
//
// It runs before the snapshot rather than with the orphan sweep, which runs
// after it. That is the whole point: the sweep would clear the documents
// too — it has recognised the bundle topic shape since the first slice of
// step 13 — but only after the configs it invalidates have already been
// refused.
//
// It also needs none of the sweep's machinery. The sweep decides what is an
// orphan by comparing against what this process declared; here there is
// nothing to compare. In per-entity mode this daemon publishes no documents
// at all, so every document under a node id it owns is superseded by
// definition.
//
// The subscription is narrow — `<prefix>/device/+/config` — so a boot with
// nothing to roll back costs one short subscribe and not one message. That
// is the common case and it has to stay cheap, because this runs on every
// boot: a daemon cannot tell whether the previous run used bundles without
// asking the broker.
func (b *Bridge) RunBundleRollbackOnce(ctx context.Context, centralName string, window time.Duration) (int, error) {
	if !b.cfg.HADiscoveryEnabled || b.bundles != nil {
		return 0, nil
	}
	if window <= 0 {
		window = 2 * time.Second
	}
	subClient, ok := b.cleanupSubscriber()
	if !ok {
		return 0, errCleanupClientLacksSubscribe
	}
	rawCentral := b.resolvedCentral(centralName)
	if rawCentral == "" {
		// Without a central we cannot scope the filter to our own node-id
		// namespace, and clearing another integration's device documents —
		// a parallel Zigbee2MQTT publishes them too — is far worse than
		// leaving our own in place.
		return 0, nil
	}
	nodePrefixes := discoveryNodePrefixes(rawCentral)

	var (
		mu      sync.Mutex
		stale   []string
		filter  = naming.DiscoveryTopicPrefix + hapublisher.BundleSegment + "/+/config"
		handler = func(topic string, payload []byte, _ bool) {
			// An empty retained payload is a topic the broker is already
			// clearing. Retracting it again would be a message for nothing.
			if len(payload) == 0 {
				return
			}
			parsed, ok := hapublisher.ParseConfigTopic(naming.DiscoveryTopicPrefix, topic)
			if !ok || !parsed.Bundle {
				return
			}
			if !discoveryNodeIDBelongsTo(strings.ToLower(parsed.NodeID), nodePrefixes) {
				return
			}
			mu.Lock()
			stale = append(stale, topic)
			mu.Unlock()
		}
	)

	if err := b.snapshotRetained(ctx, subClient, filter, b.cfg.QoS.Discovery, window, handler); err != nil {
		return 0, err
	}
	mu.Lock()
	topics := append([]string(nil), stale...)
	mu.Unlock()

	cleared := 0
	for _, topic := range topics {
		// Through the runtime, so the document also leaves the dedup set
		// and the in-flight claim set: a topic cleared here must not be
		// dedup-suppressed if this daemon ever publishes a document there
		// again, and must not be protected from a later orphan sweep.
		if err := b.pub.Retract(ctx, topic); err != nil {
			b.incPublishErrors(centralName)
			continue
		}
		cleared++
	}
	return cleared, nil
}
