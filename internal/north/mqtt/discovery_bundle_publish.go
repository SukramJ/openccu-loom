// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package mqtt

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	hadiscovery "github.com/SukramJ/go-hamqtt/discovery"
)

// publishDeviceBundle puts one node's whole discovery document on the broker,
// in the only order Home Assistant accepts.
//
// The order is not a preference, and it is not this daemon's to keep any
// more. Measured against a live instance (ADR 0070, amendment of
// 2026-09-10), publishing a bundle while a per-entity config for the same
// unique id is still retained is refused:
//
//	WARNING [mqtt.entity] Received a conflicting MQTT discovery message …
//	previously discovered on topic homeassistant/sensor/…/config; the
//	conflicting discovery message was received on topic
//	homeassistant/device/…/config
//
// A log line on the consumer's side is the entire signal. That measurement
// is now written beside the code that acts on it, in
// [hapublisher.Runtime.PublishBundle]: it retracts the superseded per-entity
// topics first, aborts before the bundle if a retraction fails, and skips
// the pair entirely when the document has not moved. The one difference from
// what this daemon did by hand is that a superseded topic is retracted once
// per process rather than on every change of the document — after the first
// retraction the broker holds nothing there, and a boot that rewrites a
// device forty times used to send forty rounds of them.
//
// What is left here is what the runtime deliberately did not take: the store
// lookup, the per-central counters and the per-component validity count.
func (b *Bridge) publishDeviceBundle(ctx context.Context, centralName, nodeID string, store *discoveryBundleStore) error {
	if !b.cfg.HADiscoveryEnabled || store == nil {
		return nil
	}
	bundle, ok := store.Bundle(nodeID)
	if !ok {
		return nil
	}
	published, err := b.pub.PublishBundle(ctx, bundle)
	if err != nil {
		b.incPublishErrors(centralName)
		return fmt.Errorf("publish bundle %s: %w", nodeID, err)
	}
	if !published {
		return nil
	}
	b.countInvalidComponents(centralName, bundle)
	b.incDiscoverySent(centralName)
	return nil
}

// countInvalidComponents raises mqtt_discovery_invalid once per component
// that carries a key its platform does not declare.
//
// Per component rather than per document, because that is the granularity at
// which Home Assistant makes the decision: it strips the offending key from
// that one entity and keeps the rest of the bundle. So the document is
// published either way and this only counts.
//
// It stays on this side of the ADR 0070 boundary because it is catalog
// knowledge and a metrics policy rather than publish mechanics: which keys a
// platform declares comes from go-ha-catalog, and whether a violation is
// worth a counter is this daemon's call. It runs on a document the runtime
// reports as actually published, so a boot that re-renders an unchanged
// fleet raises the counter once per distinct document rather than once per
// call — which is what the dedup gate used to buy it here.
func (b *Bridge) countInvalidComponents(centralName string, bundle *hadiscovery.Bundle) {
	for _, key := range bundle.Keys() {
		comp := bundle.Components[key]
		body, err := json.Marshal(comp)
		if err != nil {
			continue
		}
		if err := ValidateDiscoveryBody(string(comp.Platform), body); errors.Is(err, hadiscovery.ErrInvalidBundle) {
			b.incDiscoveryInvalid(centralName)
		}
	}
}
