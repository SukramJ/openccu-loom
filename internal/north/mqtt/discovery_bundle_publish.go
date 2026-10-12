// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package mqtt

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

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
//
// The runtime contains the document before it publishes it (see
// [discoveryRuntimeConfig]): a component Home Assistant would refuse is
// withheld, the rest is published. The store keeps the withheld component as
// rendered, so it stays declared here and is never turned into a
// platform-only tombstone — the store writes one only for an object a
// producer explicitly retracted. A tombstone would delete the entity and its
// registry entry; an omitted key leaves Home Assistant's entity as it was.
func (b *Bridge) publishDeviceBundle(ctx context.Context, centralName, nodeID string, store *discoveryBundleStore) error {
	if !b.cfg.HADiscoveryEnabled || store == nil {
		return nil
	}
	bundle, ok := store.Bundle(nodeID)
	if !ok {
		return nil
	}
	b.rememberBundleCentral(bundle.NodeID, centralName)
	published, err := b.pub.PublishBundle(ctx, bundle)
	if err != nil {
		b.incPublishErrors(centralName)
		if errors.Is(err, hadiscovery.ErrUnpublishable) {
			// Nothing reached the broker, so this is the only place the
			// components that made the document unpublishable are counted.
			b.countInvalidComponents(centralName, bundle)
		}
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
// Home Assistant would refuse or alter: every component [hadiscovery.Inspect]
// attributes a finding to, error or warning.
//
// Per component rather than per document, because that is the granularity at
// which Home Assistant decides: a component's own schema failure costs that
// entity (homeassistant/components/mqtt/entity.py:324-347), an unknown key is
// stripped from it (extra=REMOVE_EXTRA), and a missing platform or unique_id,
// which would refuse the whole document, is withheld by the runtime's
// containment instead. The bundle counted is the one rendered, before
// containment, so a withheld component is counted here and only here.
//
// It stays on this side of the ADR 0070 boundary because it is a metrics
// policy rather than publish mechanics. It runs on a document the runtime
// reports as actually published, so a boot that re-renders an unchanged
// fleet raises the counter once per distinct document rather than once per
// call — which is what the dedup gate used to buy it here.
func (b *Bridge) countInvalidComponents(centralName string, bundle *hadiscovery.Bundle) {
	findings := hadiscovery.Inspect(bundle, hadiscovery.InspectOptions{Ignore: discoveryKeysHomeAssistantIgnores})
	seen := map[string]bool{}
	for _, f := range findings {
		if f.Component == "" || seen[f.Component] {
			continue
		}
		seen[f.Component] = true
		b.incDiscoveryInvalid(centralName)
	}
}

// rememberBundleCentral records which central a node's document belongs to,
// for [Bridge.logContained].
func (b *Bridge) rememberBundleCentral(nodeID, centralName string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.bundleCentral == nil {
		b.bundleCentral = map[string]string{}
	}
	b.bundleCentral[nodeID] = centralName
}

// logContained is the runtime's [hapublisher.Config.OnContained] hook: one
// warning per withheld component, naming the central, the device, the
// component and the finding kinds that withheld it.
//
// It only logs. The counter is raised by [Bridge.countInvalidComponents]
// once per distinct published document; the hook runs on every publish
// call, an unchanged document included, so counting here as well would
// count a withheld component twice and again on every re-render.
func (b *Bridge) logContained(nodeID string, c *hadiscovery.Containment) {
	if c == nil || len(c.Withheld) == 0 {
		return
	}
	b.mu.Lock()
	centralName := b.bundleCentral[nodeID]
	b.mu.Unlock()
	device := ""
	if c.Bundle != nil {
		device = c.Bundle.Device.Name
	}
	for _, key := range c.Findings.FailingComponents() {
		if _, withheld := c.Withheld[key]; !withheld {
			continue
		}
		errs := c.Findings.ForComponent(key).Errors()
		kinds := make([]string, 0, len(errs))
		reasons := make([]string, 0, len(errs))
		for _, f := range errs {
			kinds = append(kinds, string(f.Kind))
			reasons = append(reasons, f.Message)
		}
		b.logger.Warn("mqtt.discovery.component_withheld",
			slog.String("central", centralName),
			slog.String("node_id", nodeID),
			slog.String("device", device),
			slog.String("component", key),
			slog.String("kind", strings.Join(kinds, ",")),
			slog.String("reason", strings.Join(reasons, "; ")))
	}
}
