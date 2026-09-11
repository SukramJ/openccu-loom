// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package mqtt

import (
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"sort"
	"testing"
)

var updateAddonUpdateGolden = flag.Bool("update-addon-update-golden", false,
	"rewrite the pinned add-on self-update discovery payloads")

var addonUpdateGoldenPath = filepath.Join("testdata", "discovery_golden_addon_update.json")

// addonUpdateGoldenEntry is one discovery publish of the daemon-level
// add-on self-update plane, addressed the way the wire addresses it.
//
// The topic is pinned alongside the body on purpose. This entity's node id
// is the literal "daemon" — not a device identifier, not a central — and
// its object id is a literal too, so a rename on either side moves the
// retained config topic without changing a single key in the body. Home
// Assistant reports nothing: the old config stays retained under the old
// topic, the new one appears beside it, and the operator ends up with two
// update entities of which only one is fed.
//
// The payload is stored decoded rather than as raw bytes so the pin stays
// reviewable — a human has to be able to read a diff and decide whether a
// change is wanted. Comparison is on the canonical re-encoding of both
// sides, which is exact for every key and value; only whitespace, which no
// consumer sees, is out of scope.
type addonUpdateGoldenEntry struct {
	Topic   string         `json:"topic"`
	Payload map[string]any `json:"payload"`
}

// addonUpdateGoldenCase is one configured builder, named so a diff says
// which shape moved rather than only which topic.
type addonUpdateGoldenCase struct {
	name string
	db   *DefaultDiscoveryBuilder
}

// addonUpdateGoldenCases is the fixture matrix for the add-on self-update
// plane.
//
// [DefaultDiscoveryBuilder.BuildAddonUpdateDiscovery] takes no arguments:
// the plane emits exactly one entity per daemon process, so the matrix is
// not one case per component but one case per way that single entity
// resolves differently. Only two inputs reach it — the topic base and the
// builder's locale — and the remaining two cases exist because the
// interesting claim is that an input does NOT reach it.
//
//   - default: the canonical shape, same topic base as the other pins in
//     this package so the four files read against each other.
//   - alternate-base: every declared topic (state, latest-version,
//     command, availability) carries the configurable base, while the
//     discovery config topic and both identity fields must not move with
//     it. An identity that drifted with the base would re-key every
//     entity on any deployment that renamed its base.
//   - second-central: the identity hazard this plane exists to get wrong.
//     Every other builder in this package scopes its unique_id to a
//     central's serial; this one deliberately does not, because the
//     daemon self-updates itself and one process may serve N centrals.
//     Pinned against a builder configured for a different central, with
//     that central's hub metadata registered, so the payload is proven
//     byte-identical to `default` rather than merely assumed to be. If
//     the migration ever made scoping unconditional, one entity per CCU
//     would appear where there must be exactly one.
//   - locale-de: the entity-id seed hazard. The localized name is the
//     only key allowed to move with the locale; `unique_id` and
//     `default_entity_id` must not, because Home Assistant derives the
//     entity_id from `default_entity_id` and keys the registry on
//     `unique_id`. If either started following the name, switching the
//     daemon's language would orphan the existing entity and silently
//     create a second one beside it.
func addonUpdateGoldenCases() []addonUpdateGoldenCase {
	def := NewDefaultDiscoveryBuilder(NewTopicBuilder("gh"), "ccu-01")
	def.SetHubInfoFor("ccu-01", HubInfo{Serial: "3014F711A0001234"})

	altBase := NewDefaultDiscoveryBuilder(NewTopicBuilder("openccu-loom"), "ccu-01")
	altBase.SetHubInfoFor("ccu-01", HubInfo{Serial: "3014F711A0001234"})

	second := NewDefaultDiscoveryBuilder(NewTopicBuilder("gh"), "ccu-02")
	second.SetHubInfoFor("ccu-02", HubInfo{Serial: "3014F711B0005678"})

	german := NewDefaultDiscoveryBuilder(NewTopicBuilder("gh"), "ccu-01")
	german.SetHubInfoFor("ccu-01", HubInfo{Serial: "3014F711A0001234"})
	german.Locale = "de"

	return []addonUpdateGoldenCase{
		{"default", def},
		{"alternate-base", altBase},
		{"second-central", second},
		{"locale-de", german},
	}
}

// TestAddonUpdateDiscoveryPayloadsArePinned is this plane's half of the
// acceptance criterion for moving the daemon's discovery layer onto the
// shared model (ADR 0070), written down before the plane moves.
//
// The move's whole promise is that the bytes do not change. The ways it
// can break here fail SILENTLY in Home Assistant: a changed node id or
// object id moves the retained config topic, a changed `unique_id`
// re-keys the registry entry, a changed `default_entity_id` re-seeds the
// entity id. In each case the old entity is orphaned and a new one
// appears beside it, with nothing in any log. Nothing else in this
// package would have caught that for this plane — the two existing tests
// assert the absence of two keys and the round-trip of three topics, not
// the body.
//
// Scope, plainly. This pins what the builder produces, not what reaches a
// broker: retain flags, QoS, publish ordering and the orphan sweep that
// removes a superseded config are all outside it. It pins the payload's
// keys and values, not Home Assistant's acceptance of them — a key HA
// ignores is pinned exactly as faithfully as one it needs. And it is one
// daemon-level entity: it says nothing about the ~9,996 per-device
// configs a real fleet carries, which cannot be reconstructed in CI. The
// operator-side capture of a real fleet's retained configs before and
// after remains the stronger tier.
//
// A diff here is a regression until someone shows otherwise. Refresh with:
//
//	go test ./internal/north/mqtt/ -run TestAddonUpdateDiscoveryPayloadsArePinned -update-addon-update-golden
func TestAddonUpdateDiscoveryPayloadsArePinned(t *testing.T) {
	// Not parallel: the `origin` block every payload carries reads the
	// package-global origin version, which other tests in this package
	// set and restore.
	got := map[string]addonUpdateGoldenEntry{}
	for _, c := range addonUpdateGoldenCases() {
		item := c.db.BuildAddonUpdateDiscovery()
		if !item.OK {
			t.Fatalf("%s: BuildAddonUpdateDiscovery returned OK=false — the plane no longer produces an entity", c.name)
		}
		// Decoded to a map so the pin is stable against field order in the
		// builder and unstable against a changed key or value — which is
		// exactly the sensitivity wanted.
		var body map[string]any
		if err := json.Unmarshal(item.Payload, &body); err != nil {
			t.Fatalf("%s: unmarshal: %v", c.name, err)
		}
		got[c.name] = addonUpdateGoldenEntry{
			Topic:   c.db.TopicBuilder.DiscoveryConfig(item.Component, item.NodeID, item.ObjectID),
			Payload: body,
		}
	}

	if *updateAddonUpdateGolden {
		writeAddonUpdateGolden(t, got)
		t.Logf("rewrote %s with %d payloads", addonUpdateGoldenPath, len(got))
		return
	}

	want := readAddonUpdateGolden(t)
	names := make([]string, 0, len(got))
	for name := range got {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		w, pinned := want[name]
		if !pinned {
			t.Errorf("%s: not in the pin — a new shape appeared; refresh the pin deliberately", name)
			continue
		}
		if got[name].Topic != w.Topic {
			t.Errorf("%s: topic\n got %s\nwant %s\n(a changed node id or object id orphans the retained config, silently)",
				name, got[name].Topic, w.Topic)
		}
		if g, wantBody := canonical(t, got[name].Payload), canonical(t, w.Payload); g != wantBody {
			t.Errorf("%s: payload\n got %s\nwant %s", name, g, wantBody)
		}
	}
	for name := range want {
		if _, still := got[name]; !still {
			t.Errorf("%s: in the pin but no longer produced — an entity disappeared", name)
		}
	}
}

// TestAddonUpdateDiscoveryIdentityIsDaemonScoped states in one assertion
// what the fixture matrix only implies: the entity's address and identity
// are a property of the daemon, not of the central or the topic base the
// daemon happens to be configured with. Pinning the four payloads would
// catch a drift here too, but only as four diffs a reader has to compare
// by eye.
func TestAddonUpdateDiscoveryIdentityIsDaemonScoped(t *testing.T) {
	type identity struct{ topic, uniqueID, entityID string }
	seen := map[string]identity{}
	for _, c := range addonUpdateGoldenCases() {
		item := c.db.BuildAddonUpdateDiscovery()
		if !item.OK {
			t.Fatalf("%s: BuildAddonUpdateDiscovery returned OK=false", c.name)
		}
		var body map[string]any
		if err := json.Unmarshal(item.Payload, &body); err != nil {
			t.Fatalf("%s: unmarshal: %v", c.name, err)
		}
		uid, _ := body["unique_id"].(string)
		eid, _ := body["default_entity_id"].(string)
		seen[c.name] = identity{
			topic:    c.db.TopicBuilder.DiscoveryConfig(item.Component, item.NodeID, item.ObjectID),
			uniqueID: uid,
			entityID: eid,
		}
	}
	ref := seen["default"]
	if ref.uniqueID == "" || ref.entityID == "" || ref.topic == "" {
		t.Fatalf("default fixture has an empty identity field: %+v", ref)
	}
	for name, id := range seen {
		if id != ref {
			t.Errorf("%s: identity %+v differs from default %+v — this entity exists once per daemon, so its topic, unique_id and default_entity_id must not follow the central or the topic base",
				name, id, ref)
		}
	}
}

func readAddonUpdateGolden(t *testing.T) map[string]addonUpdateGoldenEntry {
	t.Helper()
	raw, err := os.ReadFile(addonUpdateGoldenPath)
	if err != nil {
		t.Fatalf("read %s: %v (create it with -update-addon-update-golden)", addonUpdateGoldenPath, err)
	}
	var out map[string]addonUpdateGoldenEntry
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("parse %s: %v", addonUpdateGoldenPath, err)
	}
	return out
}

func writeAddonUpdateGolden(t *testing.T, entries map[string]addonUpdateGoldenEntry) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(addonUpdateGoldenPath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	raw, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(addonUpdateGoldenPath, append(raw, '\n'), 0o600); err != nil {
		t.Fatalf("write %s: %v", addonUpdateGoldenPath, err)
	}
}
