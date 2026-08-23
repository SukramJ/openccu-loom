// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package mqtt

import (
	"encoding/json"
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// sysvarUID builds one sysvar discovery payload and returns its unique_id.
func sysvarUID(t *testing.T, spec HubSysvarSpec) string {
	t.Helper()
	b := NewDefaultDiscoveryBuilder(NewTopicBuilder("openccu-loom"), "ccu-01")
	b.SetHubInfoFor("ccu-01", HubInfo{Serial: "3014F711A0001234"})
	item := b.BuildSysvarDiscovery("ccu-01", spec)
	if !item.OK {
		t.Fatalf("expected an OK discovery item for %q", spec.Name)
	}
	var body map[string]any
	if err := json.Unmarshal(item.Payload, &body); err != nil {
		t.Fatalf("unmarshal payload for %q: %v", spec.Name, err)
	}
	uid, _ := body["unique_id"].(string)
	if uid == "" {
		t.Fatalf("no unique_id in the payload for %q", spec.Name)
	}
	return uid
}

// TestSysvarUniqueIDSurvivesPunctuationOnlyNameDifference pins the sysvar
// identity to the CCU's numeric variable id.
//
// The display name is not an identity. [routingkey.HubSlug] collapses
// punctuation and case, so two system variables whose names differ only there
// slugged to the same string and produced byte-identical unique_ids. Home
// Assistant keeps whichever discovery config arrived first and silently drops
// the other variable's entity; because the payload is retained on the broker,
// the loss outlives the daemon that caused it. Nothing on the daemon side
// noticed — both variables published happily to their own distinct state
// topics, and only the entity registry on the far side was one row short.
func TestSysvarUniqueIDSurvivesPunctuationOnlyNameDifference(t *testing.T) {
	t.Parallel()

	a := sysvarUID(t, HubSysvarSpec{Name: "Alarm: Küche", Vid: 4711, ValueType: hmenum.HubValueTypeFloat})
	b := sysvarUID(t, HubSysvarSpec{Name: "Alarm Küche", Vid: 4712, ValueType: hmenum.HubValueTypeFloat})

	if a == b {
		t.Errorf("two system variables collapsed onto one unique_id %q — Home Assistant\n"+
			"registers only the first and drops the second entity outright", a)
	}
}

// TestSysvarUniqueIDIgnoresARename pins the other half of the same decision:
// keying on the numeric id means an operator renaming a variable in the CCU
// WebUI keeps its Home Assistant entity, its history and every automation
// built on it. Under the old name-derived key a rename re-keyed the entity and
// orphaned all three.
func TestSysvarUniqueIDIgnoresARename(t *testing.T) {
	t.Parallel()

	before := sysvarUID(t, HubSysvarSpec{Name: "Heizung Bad", Vid: 9001, ValueType: hmenum.HubValueTypeFloat})
	after := sysvarUID(t, HubSysvarSpec{Name: "Heizung Badezimmer", Vid: 9001, ValueType: hmenum.HubValueTypeFloat})

	if before != after {
		t.Errorf("renaming a system variable re-keyed its entity: %q -> %q", before, after)
	}
}

// TestSysvarUniqueIDFallsBackToTheSlugWithoutAnID covers the spec built before
// the first hub scan has resolved ids. Keying such a sysvar on the literal 0
// would collide with every other unresolved one, which is strictly worse than
// the name-derived key it replaces, so the builder falls back.
func TestSysvarUniqueIDFallsBackToTheSlugWithoutAnID(t *testing.T) {
	t.Parallel()

	uid := sysvarUID(t, HubSysvarSpec{Name: "Alarm Küche", ValueType: hmenum.HubValueTypeFloat})
	slug := sysvarUID(t, HubSysvarSpec{Name: "Alarm-Küche", ValueType: hmenum.HubValueTypeFloat})

	if uid != slug {
		t.Fatalf("without an id both names should still slug alike: %q vs %q", uid, slug)
	}
	withID := sysvarUID(t, HubSysvarSpec{Name: "Alarm Küche", Vid: 12, ValueType: hmenum.HubValueTypeFloat})
	if withID == uid {
		t.Errorf("a resolved id must produce a different key than the slug fallback, both were %q", uid)
	}
}
