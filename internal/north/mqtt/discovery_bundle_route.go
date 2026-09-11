// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package mqtt

import "context"

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
// The bundle is published on every call. That is deliberately the simple
// version: a device with n entities publishes its document n times during
// the boot snapshot instead of once, which is the same number of messages
// the per-entity form sends and roughly eight times the bytes for a typical
// sixteen-entity device. Not worse than today in message count, worse in
// volume, and the fix — batching the snapshot and flushing once per device —
// is a change to when this is called rather than to what it does.
func (b *Bridge) routeToBundle(ctx context.Context, centralName, component, nodeID, objectID string, payload []byte) error {
	if len(payload) == 0 {
		b.bundles.Remove(nodeID, objectID)
	} else if err := b.bundles.Put(nodeID, objectID, component, payload); err != nil {
		// A body this daemon just marshalled and cannot read back is a bug
		// here, not a broker problem. Reporting it is right; dropping the
		// entity silently is how one goes missing for a release.
		return err
	}
	return b.publishDeviceBundle(ctx, centralName, nodeID, b.bundles)
}
