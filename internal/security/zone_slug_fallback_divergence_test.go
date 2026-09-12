// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package security

import (
	"testing"

	"github.com/SukramJ/openccu-loom/internal/routingkey"
)

// TestZoneSlugFallbackDivergesFromTheSharedRule pins a known, deliberately
// unrepaired defect: [zoneSlugFallback] and [routingkey.EffectiveSlug] answer
// the same question — the identity of a zone whose stored slug is blank — and
// give different answers.
//
// The test exists to make the divergence a measured fact rather than a
// comment, and to stop a well-meaning harmonisation from landing quietly. The
// fallback's output reaches `loom_security_zone_<slug>`, which is a published
// unique_id and (on this plane) the seed for `default_entity_id`; Home
// Assistant offers no migration path for either. Read the doc comment on
// [zoneSlugFallback] before changing anything here: making the two agree is
// not the fix on its own.
func TestZoneSlugFallbackDivergesFromTheSharedRule(t *testing.T) {
	t.Parallel()

	const zoneID = "6b1f3a90-2c4d-4f18-9a77-0d5e8c3b1a42"

	cases := []struct {
		what     string
		name     string
		fallback string
		shared   string
	}{
		{
			what:     "a sluggable name: the two agree",
			name:     "Erdgeschoss",
			fallback: "erdgeschoss",
			shared:   "erdgeschoss",
		},
		{
			what:     "a name that slugs to nothing: the two disagree",
			name:     "🏠",
			fallback: "zone-6b1f3a90",
			shared:   routingkey.ZoneSlugStem,
		},
		{
			what:     "an empty name: the two disagree",
			name:     "",
			fallback: "zone-6b1f3a90",
			shared:   routingkey.ZoneSlugStem,
		},
	}

	for _, c := range cases {
		t.Run(c.what, func(t *testing.T) {
			t.Parallel()
			if got := zoneSlugFallback(c.name, zoneID); got != c.fallback {
				t.Errorf("zoneSlugFallback(%q) = %q, want %q", c.name, got, c.fallback)
			}
			got := routingkey.EffectiveSlug("", c.name, routingkey.ZoneSlugStem)
			if got != c.shared {
				t.Errorf("EffectiveSlug(%q) = %q, want %q", c.name, got, c.shared)
			}
		})
	}
}

// TestZoneSlugFallbackTakesNoCollisionSuffix is the second half of the same
// divergence. [routingkey.UniqueSlug] appends "-2" when a sibling already
// answers to the base; the fallback has no view of its siblings at all, so
// two zones sharing a name share an identity — and therefore one MQTT topic
// and one Home Assistant entity — until the store seeds them.
func TestZoneSlugFallbackTakesNoCollisionSuffix(t *testing.T) {
	t.Parallel()

	const name = "Keller"
	taken := map[string]bool{"keller": true}

	if got, want := zoneSlugFallback(name, "aaaaaaaa-0000"), "keller"; got != want {
		t.Errorf("zoneSlugFallback(%q) = %q, want %q", name, got, want)
	}
	if got, want := routingkey.UniqueSlug(name, routingkey.ZoneSlugStem, taken), "keller-2"; got != want {
		t.Errorf("UniqueSlug(%q) = %q, want %q", name, got, want)
	}
}
