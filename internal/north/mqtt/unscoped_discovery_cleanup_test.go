// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package mqtt

import (
	"context"
	"encoding/json"
	"testing"
)

// discoveryPayload renders a retained discovery config the way the
// builder writes one, so the sweep is matched against the real shape
// rather than a hand-picked fragment.
func discoveryPayload(t *testing.T, uniqueID, originName string) []byte {
	t.Helper()
	body := map[string]any{
		"name":      "Taste",
		"unique_id": uniqueID,
		"origin":    map[string]any{"name": originName, "sw_version": "test"},
	}
	buf, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return buf
}

// TestUnscopedDiscoveryCleanupClearsOnlyAmbiguousOwnPayloads pins both
// halves of the sweep's scope.
//
// It has to clear this daemon's configs whose entity id carries an empty
// CCU-serial slot, because two CCUs produce that same id and the
// consumer keys its registry on it — republishing the corrected payload
// on the same topic would leave the stale identity in place beside it.
//
// And it must leave everything else alone: another integration's
// payloads (any id shape is legitimate there), and this daemon's own
// correctly scoped ones.
func TestUnscopedDiscoveryCleanupClearsOnlyAmbiguousOwnPayloads(t *testing.T) {
	t.Parallel()

	const (
		unscopedTopic = "homeassistant/event/ccu_bidcos-rf/10_event/config"
		scopedTopic   = "homeassistant/event/ccu_bidcos-rf/11_event/config"
		foreignTopic  = "homeassistant/sensor/zigbee2mqtt_x/temp/config"
		unrelatedID   = "homeassistant/switch/other/thing/config"
	)

	mc := &mockRetainClient{
		retained: []retainedMsg{
			{topic: unscopedTopic, payload: discoveryPayload(t, "loom__bidcos_rf_10_event", originName)},
			{topic: scopedTopic, payload: discoveryPayload(t, "loom_4993d962_bidcos_rf_11_event", originName)},
			// Another integration whose ids happen to start the same way
			// must not be touched — the origin block is what separates us.
			{topic: foreignTopic, payload: discoveryPayload(t, "loom__something", "zigbee2mqtt")},
			{topic: unrelatedID, payload: []byte(`not json`)},
		},
	}
	b := NewBridge(BridgeConfig{Base: "openccu-loom", HADiscoveryEnabled: true, CentralName: "ccu"}, mc)
	// The stale topic is in `declared` from this boot's publish; the sweep
	// has to drop it there too, or the diff gate suppresses the corrected
	// republish against the payload it just cleared.
	b.mu.Lock()
	b.declared[unscopedTopic] = []byte(`{}`)
	b.mu.Unlock()

	cleared, err := b.RunUnscopedDiscoveryCleanupOnce(context.Background(), 50)
	if err != nil {
		t.Fatalf("RunUnscopedDiscoveryCleanupOnce: %v", err)
	}
	if cleared != 1 {
		t.Errorf("cleared %d configs, want exactly the one with the empty serial slot", cleared)
	}

	cleanedTopics := map[string]bool{}
	mc.mu.Lock()
	for _, p := range mc.published {
		if len(p.payload) == 0 && p.retain {
			cleanedTopics[p.topic] = true
		}
	}
	mc.mu.Unlock()
	if !cleanedTopics[unscopedTopic] {
		t.Errorf("%s was not cleared — the consumer keeps the ambiguous entity id and the corrected "+
			"payload creates a second entity beside it", unscopedTopic)
	}
	for _, keep := range []string{scopedTopic, foreignTopic, unrelatedID} {
		if cleanedTopics[keep] {
			t.Errorf("%s was cleared but must be left alone", keep)
		}
	}

	b.mu.Lock()
	_, stillDeclared := b.declared[unscopedTopic]
	b.mu.Unlock()
	if stillDeclared {
		t.Error("the cleared topic is still in `declared` — the snapshot's diff gate would suppress the " +
			"corrected republish and the entity would never come back")
	}
}

// TestUnscopedDiscoveryCleanupSkipsWhenDiscoveryIsOff pins that a daemon
// not driving HA discovery never touches the discovery namespace.
func TestUnscopedDiscoveryCleanupSkipsWhenDiscoveryIsOff(t *testing.T) {
	t.Parallel()
	mc := &mockRetainClient{
		retained: []retainedMsg{
			{topic: "homeassistant/event/ccu_x/1_event/config", payload: discoveryPayload(t, "loom__x", originName)},
		},
	}
	b := NewBridge(BridgeConfig{Base: "openccu-loom", HADiscoveryEnabled: false, CentralName: "ccu"}, mc)

	cleared, err := b.RunUnscopedDiscoveryCleanupOnce(context.Background(), 50)
	if err != nil {
		t.Fatalf("RunUnscopedDiscoveryCleanupOnce: %v", err)
	}
	if cleared != 0 {
		t.Errorf("cleared %d configs with HA discovery disabled, want 0", cleared)
	}
}
