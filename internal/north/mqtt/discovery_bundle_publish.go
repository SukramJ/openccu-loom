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
// The order is not a preference. Measured against a live instance (ADR 0070,
// amendment of 2026-09-10), publishing a bundle while a per-entity config for
// the same unique id is still retained is refused:
//
//	WARNING [mqtt.entity] Received a conflicting MQTT discovery message …
//	previously discovered on topic homeassistant/sensor/…/config; the
//	conflicting discovery message was received on topic
//	homeassistant/device/…/config
//
// A log line on the consumer's side is the entire signal. The bundle stays
// retained on the broker, the entity keeps its old config, and nothing
// anywhere reports that the migration did not happen. So the superseded
// per-entity topics are retracted first and the bundle goes out second.
//
// The two belong together. Between the retraction and the bundle the entity
// does not exist — not merely unavailable, absent — so a failure in between
// leaves the operator without it until the next start republishes. That is
// why a retraction error aborts before the bundle rather than pressing on:
// having lost the old config and then failed to write the new one is the one
// outcome worse than not having started.
func (b *Bridge) publishDeviceBundle(ctx context.Context, centralName, nodeID string, store *discoveryBundleStore) error {
	if !b.cfg.HADiscoveryEnabled || store == nil {
		return nil
	}
	bundle, ok := store.Bundle(nodeID)
	if !ok {
		return nil
	}
	payload, err := json.Marshal(bundle)
	if err != nil {
		return fmt.Errorf("marshal bundle %s: %w", nodeID, err)
	}
	topic := bundle.Topic("")

	// Nothing to do when the document has not moved. Checked before the
	// retraction, not after: a repeat publish must not tear down the
	// per-entity topics a second time, and on a steady-state boot this is
	// the common path.
	b.mu.Lock()
	previous, declared := b.declared[topic]
	b.mu.Unlock()
	if declared && bytesEqual(previous, payload) {
		return nil
	}

	if err := b.retractSuperseded(ctx, centralName, store.SupersededTopics(nodeID)); err != nil {
		return err
	}

	// Same reason as the per-entity path: the broker fans a message out to
	// its subscribers — the orphan sweep's snapshot subscription included —
	// before Publish returns, so the claim has to be taken first.
	b.mu.Lock()
	b.announced[topic] = true
	b.mu.Unlock()

	// The bundle is validated per component. A bundle carries up to several
	// dozen entities, and one of them emitting a key its platform does not
	// declare is exactly the invisible defect the counter exists for —
	// Home Assistant drops the key and keeps everything else, so the
	// document is published either way.
	b.countInvalidComponents(centralName, bundle)

	if err := b.client.Publish(ctx, topic, payload, b.cfg.QoS.Discovery, true); err != nil {
		b.incPublishErrors(centralName)
		return fmt.Errorf("publish bundle %s: %w", nodeID, err)
	}
	b.mu.Lock()
	b.declared[topic] = payload
	b.mu.Unlock()
	b.incDiscoverySent(centralName)
	return nil
}

// retractSuperseded clears the per-entity configs a bundle replaces.
//
// A topic this process has not published is retracted anyway: on the boot
// that performs the migration, `declared` is empty and the retained configs
// on the broker are precisely the ones from the *previous* build. Skipping
// them because this process does not know them would leave every one of them
// in place — and every bundle refused.
func (b *Bridge) retractSuperseded(ctx context.Context, centralName string, topics []string) error {
	for _, topic := range topics {
		if err := b.client.Publish(ctx, topic, nil, b.cfg.QoS.Discovery, true); err != nil {
			b.incPublishErrors(centralName)
			return fmt.Errorf("retract superseded %s: %w", topic, err)
		}
		// An empty payload is a retraction, not a declaration. Leaving the
		// topic in `declared` would keep a retracted entity in the set the
		// orphan sweeps treat as live, and the Home-Assistant-birth replay
		// would re-publish the empty payload to a topic the broker no
		// longer retains.
		b.mu.Lock()
		delete(b.declared, topic)
		delete(b.announced, topic)
		b.mu.Unlock()
	}
	return nil
}

// countInvalidComponents raises mqtt_discovery_invalid once per component
// that carries a key its platform does not declare.
//
// Per component rather than per document, because that is the granularity at
// which Home Assistant makes the decision: it strips the offending key from
// that one entity and keeps the rest of the bundle.
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
