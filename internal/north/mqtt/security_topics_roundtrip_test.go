// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package mqtt

import (
	"encoding/json"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/security"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
)

// TestSecurityPlaneTopicsRoundTrip asserts that every topic the plane
// declares is a topic the plane writes.
//
// It exists because the two halves disagreed and both passed their own
// tests. Discovery derived the state topic from the entity key, so a
// class entity declared `<base>/security/class_smoke`, while the
// publisher wrote `<base>/security/class/smoke`. Payload-shape tests
// checked the declaration, publish tests checked the write, and nothing
// compared the two sets — so every class and zone entity appeared in a
// consumer and stayed unavailable forever.
//
// The comparison is one-directional on purpose: a declared topic nobody
// writes is the defect. A topic written without a declaration is a
// lesser problem (an operator simply does not get an entity for it) and
// is reported separately rather than failed on.
// The table runs the whole comparison for a plain base and for a base
// that carries the trailing slash an operator is free to configure. The
// second row is what the availability half needs: every payload names
// the bridge-status topic as its first availability source, and the
// bridge publishes that status through its topic builder, which trims
// the base. A source spelled one slash differently never receives a
// payload, and `availability_mode: "all"` then leaves every entity of
// the plane unavailable forever.
func TestSecurityPlaneTopicsRoundTrip(t *testing.T) {
	t.Parallel()
	for _, base := range []string{"gh", "gh/"} {
		snap := roundTripSnapshot()

		declared := map[string]bool{}
		for _, item := range securityDiscoveryItems(base, snap) {
			var body map[string]any
			if err := json.Unmarshal(item.Payload, &body); err != nil {
				t.Fatalf("discovery payload for %q is not JSON: %v", item.ObjectID, err)
			}
			for _, field := range []string{"state_topic", "json_attributes_topic"} {
				if v, ok := body[field].(string); ok && v != "" {
					declared[v] = true
				}
			}
			collectAvailabilityTopics(t, body, declared)
		}
		if len(declared) == 0 {
			t.Fatal("no topics declared; the walk found no discovery payloads and would pass vacuously")
		}

		published := securityPublishedTopics(base, snap)
		if len(published) == 0 {
			t.Fatal("no topics published; the walk found no state writes and would pass vacuously")
		}

		for topic := range declared {
			if !published[topic] {
				t.Errorf("base %q: declared but never published: %q — a consumer creates this entity and it stays unavailable forever", base, topic)
			}
		}
		for topic := range published {
			if !declared[topic] {
				t.Logf("base %q: published but not declared: %q (no entity is created for it)", base, topic)
			}
		}
	}
}

// collectAvailabilityTopics adds every `availability[].topic` of one
// discovery payload to out. An availability source is a declared topic
// like any other — with `availability_mode: "all"` a source nothing
// publishes to is strictly worse than a missing state topic, because it
// takes the whole entity down rather than one value.
func collectAvailabilityTopics(t *testing.T, body map[string]any, out map[string]bool) {
	t.Helper()
	// The payload is decoded generically because it is compared as wire
	// JSON, not as a Go struct.
	list, ok := body["availability"].([]any)
	if !ok {
		return
	}
	for _, entry := range list {
		src, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		if v, ok := src["topic"].(string); ok && v != "" {
			out[v] = true
		}
	}
}

// securityDiscoveryItems builds every discovery payload the plane would
// publish for a snapshot, through the real builders.
func securityDiscoveryItems(base string, snap security.Snapshot) []DiscoveryItem {
	tr := func(_, fallback string) string { return fallback }
	system := securitySystemEntities(tr)
	out := make([]DiscoveryItem, 0, len(system)+len(snap.Classes)+len(snap.Zones))
	for i := range system {
		out = append(out, BuildSecurityDiscovery(base, "Security", "", system[i]))
	}
	for class := range snap.Classes {
		out = append(out, BuildSecurityDiscovery(base, "Security", "", securityClassEntity(base, class, tr)))
	}
	for slug := range snap.Zones {
		out = append(out, BuildSecurityDiscovery(base, "Security", "",
			securityZoneEntity(base, slug, snap.Zones[slug].Name, tr)))
	}
	return out
}

// securityPublishedTopics mirrors the topic set reconcile writes. It is
// deliberately derived from the same builders the publisher uses, so a
// rename on either side keeps both in step and only a genuine
// declaration/publication divergence fails the test.
func securityPublishedTopics(base string, snap security.Snapshot) map[string]bool {
	out := map[string]bool{
		securityAvailabilityTopic(base): true,
		// The bridge writes its retained status through the topic
		// builder, so the published side is spelled the way the bridge
		// spells it — not the way the plane's own helper does.
		NewTopicBuilder(base).BridgeStatus(): true,
	}
	for _, key := range []string{"state", "alarm", "problem", "health", "last_alarm", "last_fault", "event", "fault"} {
		out[securityStateTopic(base, key)] = true
	}
	for class := range snap.Classes {
		out[securityClassTopic(base, class)] = true
	}
	for slug := range snap.Zones {
		out[securityZoneTopic(base, slug)] = true
	}
	return out
}

func roundTripSnapshot() security.Snapshot {
	return security.Snapshot{
		Severity: hmenum.SecuritySeverityWarning,
		Classes: map[hmenum.SecurityClass]security.ClassState{
			hmenum.SecurityClassSmoke: {Class: hmenum.SecurityClassSmoke, Known: 1},
			hmenum.SecurityClassWater: {
				Class: hmenum.SecurityClassWater, Known: 2, Active: true,
				Sources: []hmevent.SecuritySourceRef{{Ref: "c|i|ABC:1|MOISTURE_DETECTED", Name: "Keller"}},
			},
		},
		Zones: map[string]security.ZoneState{
			"erdgeschoss": {ID: "z1", Slug: "erdgeschoss", Name: "Erdgeschoss"},
		},
		EngineHealthy: true,
	}
}
