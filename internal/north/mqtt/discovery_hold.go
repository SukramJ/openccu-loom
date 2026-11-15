// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package mqtt

import (
	"context"
	"sort"
	"strings"
	"sync"

	"github.com/SukramJ/openccu-loom/internal/model/naming"
)

// A deferred discovery retraction.
//
// A restart of a central and the cache-reset re-pull tear the central's model
// down and load it again. Retracting each config as its device leaves the
// model deletes the entity in Home Assistant, and rediscovery brings back a
// new registry entry: a user-renamed entity id falls back to the seeded
// default and a helper built on the entity (switch_as_x) is deleted with it.
//
// So the teardown holds instead. A held config stays retained; any publish of
// the same key — the reloaded model declaring it again, unchanged or not, or a
// producer retracting it — settles the hold, and what is still held once the
// model is whole again is a config the new model no longer declares. The
// caller decides per group (a device, a hub entity class) whether that rest is
// retracted or kept, because only the caller knows whether the model that
// failed to declare it is complete.
//
// The keys are the ones the broker sees: the config topic in the per-entity
// form, the scoped node id and object id in the bundle form, where a held
// rest is retracted as a tombstone in the node's document.

// discoveryHolds is the bridge's table of held discovery keys. The zero value
// is ready for use.
type discoveryHolds struct {
	mu    sync.Mutex
	byKey map[string]heldDiscovery
}

// heldDiscovery is one held key: the central and group it belongs to, and
// either its config topic (per-entity form) or its node and object id
// (bundle form).
type heldDiscovery struct {
	central string
	group   string
	topic   string
	node    string
	object  string
}

func topicHoldKey(topic string) string { return "t\x00" + topic }

func bundleHoldKey(node, object string) string { return "b\x00" + node + "\x00" + object }

// hold records h under key unless the key is already held: a second teardown
// before the model is whole keeps the first baseline.
func (h *discoveryHolds) hold(key string, entry heldDiscovery) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.byKey == nil {
		h.byKey = make(map[string]heldDiscovery)
	}
	if _, ok := h.byKey[key]; ok {
		return false
	}
	h.byKey[key] = entry
	return true
}

// cancel settles key: something published it, so nothing is deferred for it
// any more.
func (h *discoveryHolds) cancel(key string) {
	h.mu.Lock()
	delete(h.byKey, key)
	h.mu.Unlock()
}

// take removes and returns every held key of one group.
func (h *discoveryHolds) take(central, group string) []heldDiscovery {
	h.mu.Lock()
	defer h.mu.Unlock()
	var out []heldDiscovery
	for k, e := range h.byKey {
		if e.central == central && e.group == group {
			out = append(out, e)
			delete(h.byKey, k)
		}
	}
	return out
}

// groups lists the groups of one central that still hold a key.
func (h *discoveryHolds) groups(central string) []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	seen := map[string]struct{}{}
	for _, e := range h.byKey {
		if e.central == central {
			seen[e.group] = struct{}{}
		}
	}
	out := make([]string, 0, len(seen))
	for g := range seen {
		out = append(out, g)
	}
	sort.Strings(out)
	return out
}

// DeviceHoldGroup is the hold group of one device's discovery configs.
func DeviceHoldGroup(deviceAddress string) string {
	return "device/" + strings.ToLower(deviceAddress)
}

// HoldDiscoveryForCentralDevice defers the retraction
// [Bridge.RetractDiscoveryForCentralDevice] would make now: every config the
// device has declared under the central's node-id namespace is held in the
// group [DeviceHoldGroup]. It reports how many keys it newly held.
func (b *Bridge) HoldDiscoveryForCentralDevice(centralName, deviceAddress string) int {
	if b == nil || deviceAddress == "" || !b.cfg.HADiscoveryEnabled {
		return 0
	}
	group := DeviceHoldGroup(deviceAddress)
	n := 0
	if b.bundles != nil {
		addr := strings.ToLower(deviceAddress)
		prefixes := discoveryNodePrefixes(b.topics.DiscoveryNodeScope(), b.retractUnscopedDiscovery(), centralName)
		for _, node := range b.bundles.Nodes() {
			if !nodeIsDevice(strings.ToLower(node), prefixes, addr) {
				continue
			}
			for _, object := range b.bundles.LiveObjects(node) {
				if b.holds.hold(bundleHoldKey(node, object), heldDiscovery{central: centralName, group: group, node: node, object: object}) {
					n++
				}
			}
		}
		return n
	}
	for _, topic := range b.centralDeviceDiscoveryTopics(centralName, deviceAddress) {
		if b.holds.hold(topicHoldKey(topic), heldDiscovery{central: centralName, group: group, topic: topic}) {
			n++
		}
	}
	return n
}

// nodeIsDevice reports whether a bundle node id is the device's node under
// one of the central's prefixes, or under any central when there are none.
func nodeIsDevice(node string, prefixes []string, addr string) bool {
	if len(prefixes) == 0 {
		return strings.HasSuffix(node, "_"+addr)
	}
	for _, p := range prefixes {
		if node == p+addr {
			return true
		}
	}
	return false
}

// HoldHubDiscovery holds the configs of hub-plane items under one group of
// the central. Items that are not OK declare nothing and are skipped.
func (b *Bridge) HoldHubDiscovery(centralName, group string, items []DiscoveryItem) int {
	if b == nil || !b.cfg.HADiscoveryEnabled {
		return 0
	}
	n := 0
	for _, item := range items {
		if !item.OK {
			continue
		}
		var key string
		entry := heldDiscovery{central: centralName, group: group}
		if b.bundles != nil {
			entry.node = naming.ScopedDiscoveryNodeID(b.topics.Base, item.NodeID)
			entry.object = item.ObjectID
			key = bundleHoldKey(entry.node, entry.object)
		} else {
			entry.topic = b.topics.DiscoveryConfig(item.Component, item.NodeID, item.ObjectID)
			key = topicHoldKey(entry.topic)
		}
		if b.holds.hold(key, entry) {
			n++
		}
	}
	return n
}

// HeldDiscoveryGroups lists the central's groups that still hold a config.
func (b *Bridge) HeldDiscoveryGroups(centralName string) []string {
	if b == nil {
		return nil
	}
	return b.holds.groups(centralName)
}

// DropHeldDiscovery ends the hold of one group without retracting anything:
// the caller has established that the configs it held are still right.
func (b *Bridge) DropHeldDiscovery(centralName, group string) int {
	if b == nil {
		return 0
	}
	return len(b.holds.take(centralName, group))
}

// RetractHeldDiscovery retracts what is still held of one group and ends its
// hold. In the per-entity form each config topic is cleared; in the bundle
// form each component becomes a tombstone and every touched node's document
// is published once.
func (b *Bridge) RetractHeldDiscovery(ctx context.Context, centralName, group string) int {
	if b == nil {
		return 0
	}
	held := b.holds.take(centralName, group)
	nodes := map[string]struct{}{}
	for _, e := range held {
		if e.topic != "" {
			if err := b.pub.Retract(ctx, e.topic); err != nil {
				b.incPublishErrors(centralName)
			}
			continue
		}
		if b.bundles == nil {
			continue
		}
		b.bundles.Remove(e.node, e.object)
		nodes[e.node] = struct{}{}
	}
	for node := range nodes {
		b.mu.Lock()
		batching := b.bundleBatch
		b.mu.Unlock()
		if batching {
			b.markBundleDirty(node, centralName)
			continue
		}
		_ = b.publishDeviceBundle(ctx, centralName, node, b.bundles)
	}
	return len(held)
}
