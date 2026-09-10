// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package mqtt

import (
	"sort"
	"sync"

	hacatalog "github.com/SukramJ/go-ha-catalog"
	hadiscovery "github.com/SukramJ/go-hamqtt/discovery"

	"github.com/SukramJ/openccu-loom/internal/model/naming"
)

// discoveryBundleStore accumulates the components of one node so they can be
// published as a single retained document.
//
// It exists because the two forms of Home Assistant discovery need opposite
// shapes of the same information. The per-entity form is a stream: eight
// planes in this package — device datapoints, combined projections, channel
// aggregates, schedules, week profiles, the hub, the alarm panel, security —
// each emit one entity at a time as events arrive, and each publish is
// complete in itself. A device bundle is a document: every component of a
// node in one message, so one entity changing means republishing all of them.
//
// Nothing here publishes. The store is the seam between the streaming
// producers and a document consumer, and keeping it a plain value with no
// broker access is what lets the bundle shape be tested without a broker.
type discoveryBundleStore struct {
	mu    sync.Mutex
	nodes map[string]*bundleNode
}

// bundleNode is one node's accumulated document.
type bundleNode struct {
	device *hadiscovery.DeviceInfo
	origin *hadiscovery.Origin
	// components is keyed by object id, which is what the per-entity form
	// puts in the topic and what the bundle form puts in the `components`
	// map. Keeping the same key in both is what makes a component's identity
	// survive the migration.
	components map[string]hadiscovery.Component
	// platforms remembers each component's platform even after it is
	// removed. Home Assistant deletes a component from a bundle only when
	// its entry is present and carries a platform and nothing else — an
	// empty object leaves the entity in place — so a removal has to know
	// what the thing used to be.
	platforms map[string]string
	// removed are the object ids to publish as platform-only tombstones.
	removed map[string]struct{}
}

func newDiscoveryBundleStore() *discoveryBundleStore {
	return &discoveryBundleStore{nodes: map[string]*bundleNode{}}
}

// Put records one component under its node.
//
// The device and origin blocks are lifted out of the component: a bundle
// carries them once at the top, and a component that repeated them there
// would be describing a device inside a device. The per-entity form needs
// them on every component, which is why the producers set them and why this
// is the place that takes them off again.
func (s *discoveryBundleStore) Put(nodeID, objectID, platform string, comp hadiscovery.Component) {
	if nodeID == "" || objectID == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	node, ok := s.nodes[nodeID]
	if !ok {
		node = &bundleNode{
			components: map[string]hadiscovery.Component{},
			platforms:  map[string]string{},
			removed:    map[string]struct{}{},
		}
		s.nodes[nodeID] = node
	}
	// First writer wins for the frame. Every component of one node carries
	// the same device block, so a later one can only repeat it — but the
	// hub and alarm planes attach a *narrower* block to some of their
	// entities, and letting those overwrite would shrink the device Home
	// Assistant already knows.
	if node.device == nil && comp.Device != nil {
		node.device = comp.Device
	}
	if node.origin == nil && comp.Origin != nil {
		node.origin = comp.Origin
	}
	comp.Device = nil
	comp.Origin = nil

	node.components[objectID] = comp
	if platform != "" {
		node.platforms[objectID] = platform
	}
	delete(node.removed, objectID)
}

// Remove marks a component deleted. It stays in the document as a
// platform-only entry, because that is the only thing Home Assistant reads as
// a deletion — dropping the key leaves the entity in place forever.
func (s *discoveryBundleStore) Remove(nodeID, objectID string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	node, ok := s.nodes[nodeID]
	if !ok {
		return
	}
	if _, known := node.platforms[objectID]; !known {
		// Never seen, so there is nothing for Home Assistant to delete and
		// no platform to name. A tombstone without one is an empty object,
		// which Home Assistant ignores.
		return
	}
	delete(node.components, objectID)
	node.removed[objectID] = struct{}{}
}

// Bundle renders the node's document, or reports false when the node has
// nothing to say.
//
// A node with only tombstones still renders: that is how the last entity of a
// device is retracted without abandoning the retained topic.
func (s *discoveryBundleStore) Bundle(nodeID string) (*hadiscovery.Bundle, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	node, ok := s.nodes[nodeID]
	if !ok || (len(node.components) == 0 && len(node.removed) == 0) {
		return nil, false
	}
	// Home Assistant refuses a bundle with no device block, and a bundle
	// whose device block has no identifiers registers a device under
	// nothing. Neither is worth publishing.
	if node.device == nil || len(node.device.Identifiers) == 0 {
		return nil, false
	}

	bundle := &hadiscovery.Bundle{
		NodeID:     nodeID,
		Device:     *node.device,
		Components: make(map[string]hadiscovery.Component, len(node.components)+len(node.removed)),
	}
	if node.origin != nil {
		bundle.Origin = *node.origin
	}
	// Indexed rather than ranged by value: a Component is 552 bytes and a
	// busy node carries dozens.
	for k := range node.components {
		bundle.Components[k] = node.components[k]
	}
	for k := range node.removed {
		bundle.Components[k] = hadiscovery.Component{Platform: hacatalog.Platform(node.platforms[k])}
	}
	return bundle, true
}

// SupersededTopics returns the per-entity config topics this node's bundle
// replaces, in stable order.
//
// The migration needs them, and it needs them exactly: measured against a
// live Home Assistant (ADR 0070, amendment of 2026-09-10), publishing a
// bundle while a per-entity config for the same unique id is still retained
// is refused with nothing but a log line. The per-entity topics have to be
// retracted first, and these are they — derived from what this daemon itself
// would have published rather than read back from the broker, so the
// migration needs no snapshot.
//
// Tombstoned components are included: their retained per-entity config is
// exactly what has to go.
func (s *discoveryBundleStore) SupersededTopics(nodeID string) []string {
	s.mu.Lock()
	defer s.mu.Unlock()

	node, ok := s.nodes[nodeID]
	if !ok {
		return nil
	}
	out := make([]string, 0, len(node.platforms))
	for objectID, platform := range node.platforms {
		if platform == "" {
			continue
		}
		out = append(out, naming.DiscoveryConfigTopic(platform, nodeID, objectID))
	}
	sort.Strings(out)
	return out
}

// Nodes lists the node ids the store holds, in stable order.
func (s *discoveryBundleStore) Nodes() []string {
	s.mu.Lock()
	defer s.mu.Unlock()

	out := make([]string, 0, len(s.nodes))
	for k := range s.nodes {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
