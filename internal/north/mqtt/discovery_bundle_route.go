// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package mqtt

import (
	"context"
	"errors"
	"sort"
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
