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
func TestSecurityPlaneTopicsRoundTrip(t *testing.T) {
	t.Parallel()
	const base = "gh"
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
			t.Errorf("declared but never published: %q — a consumer creates this entity and it stays unavailable forever", topic)
		}
	}
	for topic := range published {
		if !declared[topic] && !securityUndeclaredByDesign[topic] {
			t.Logf("published but not declared: %q (no entity is created for it)", topic)
		}
	}
}

// securityUndeclaredByDesign lists topics that carry no entity on
// purpose: availability is referenced from every payload rather than
// declared as one, and the two event topics are addressed by their
// entities without appearing as a state topic.
var securityUndeclaredByDesign = map[string]bool{
	"gh/security/availability": true,
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
