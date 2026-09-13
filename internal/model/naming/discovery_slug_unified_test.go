// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package naming

import (
	"strings"
	"testing"

	"github.com/SukramJ/go-hamqtt/topic"
)

// TestDiscoverySlugIsTheSharedRule asserts the contract the unification
// established: there is ONE discovery slug rule in this daemon, and it is
// `topic.Slug`. [DiscoverySlug] is a delegation, not a second spelling.
//
// This test replaces TestDiscoverySlugDivergesFromTopicSlug, which pinned
// the eight measured disagreements of the old second implementation and
// existed to make this step loud rather than silent. The eight inputs are
// kept verbatim below — they are the whole of what moved — but they are now
// asserted to AGREE, together with the value each one used to produce, so
// the fix stays visible and a regression back to the old rule names itself.
//
// What moved on the wire when these eight started agreeing: the discovery
// `node_id` and `object_id` segments, and the device-block `identifiers`
// (`openccu-loom_central_<slug>` and the central-scoped
// `openccu-loom_<slug>_<address>`). What did NOT move: `unique_id`,
// `default_entity_id`, and every state / command / availability topic —
// those are keyed on the CCU serial, the ISE id and `TopicSafe`
// respectively, none of which this function feeds. See
// `docs/external-clients/ha-unique-id-migration.md`.
func TestDiscoverySlugIsTheSharedRule(t *testing.T) {
	t.Parallel()

	t.Run("class one: non-German accented Latin is transliterated, not dropped", func(t *testing.T) {
		t.Parallel()
		for _, c := range []struct{ in, was, now string }{
			{"Café", "caf", "cafe"},
			{"Señor", "se_or", "senor"},
			{"Garçon", "gar_on", "garcon"},
			{"Ångström", "ngstroem", "angstroem"},
			{"Ærø", "r", "aeroe"},
			{"Søren", "s_ren", "soeren"},
		} {
			assertUnified(t, c.in, c.was, c.now)
		}
	})

	t.Run("class two: a literal underscore run collapses", func(t *testing.T) {
		t.Parallel()
		for _, c := range []struct{ in, was, now string }{
			{"a__b", "a__b", "a_b"},
			{"Watchdog:_CCU-Jack", "watchdog__ccu-jack", "watchdog_ccu-jack"},
		} {
			assertUnified(t, c.in, c.was, c.now)
		}
	})

	t.Run("the German set never moved and still has not", func(t *testing.T) {
		t.Parallel()
		for _, c := range []struct{ in, want string }{
			{"CCU Küche", "ccu_kueche"},
			{"Heizung Büro", "heizung_buero"},
			{"s0_Sensoren_Hülle_EG", "s0_sensoren_huelle_eg"},
			{"Außen Temperatur", "aussen_temperatur"},
		} {
			if got := DiscoverySlug(c.in); got != c.want {
				t.Errorf("DiscoverySlug(%q) = %q, want %q — the German set is what the unification promised NOT to move", c.in, got, c.want)
			}
			if got := topic.Slug(c.in); got != c.want {
				t.Errorf("topic.Slug(%q) = %q, want %q", c.in, got, c.want)
			}
		}
	})

	t.Run("the empty reduction still yields a legal segment", func(t *testing.T) {
		t.Parallel()
		for _, in := range []string{"", ":::", "   "} {
			if got := DiscoverySlug(in); got != "x" {
				t.Errorf("DiscoverySlug(%q) = %q, want %q — a zero-length node id makes HA drop the config silently", in, got, "x")
			}
		}
	})
}

// TestDiscoverySlugNoLongerCollidesOnADroppedAccent pins the first of the two
// defects the unification fixed. Two differently named system variables used
// to reduce to one object id, so their retained discovery configs landed on
// one topic and Home Assistant kept whichever arrived last — one entity where
// the operator had configured two, with no error anywhere.
//
// The golden rows `sysvar/hazard-accent-twin-{a,b}` are the same fact seen
// through a rendered payload: before the unification both pinned the topic
// `homeassistant/binary_sensor/ccu-01_sysvars/caf_terrasse/config`; they now
// pin `cafe_terrasse` and `caf_terrasse`.
func TestDiscoverySlugNoLongerCollidesOnADroppedAccent(t *testing.T) {
	t.Parallel()

	if a, b := DiscoverySlug("Café"), DiscoverySlug("Caf"); a == b {
		t.Fatalf("Café and Caf both slug to %q — the accent collision is back, and two entities share one retained config again", a)
	}
}

// TestDiscoverySlugNoLongerPassesALiteralDoubleUnderscore pins the second
// defect. `Watchdog:_CCU-Jack` is a real CCU name, cited as such by
// [DiscoverySlug]'s own doc comment for as long as the old rule existed: the
// colon collapsed to `_` and then sat next to the literal `_`, so the
// published identifier carried a `__` that nothing else in the daemon could
// produce.
func TestDiscoverySlugNoLongerPassesALiteralDoubleUnderscore(t *testing.T) {
	t.Parallel()

	for _, in := range []string{"a__b", "Watchdog:_CCU-Jack", "a___b", "x:_:_y"} {
		if got := DiscoverySlug(in); strings.Contains(got, "__") {
			t.Errorf("DiscoverySlug(%q) = %q — a separator run reached the wire uncollapsed", in, got)
		}
	}
}

// assertUnified pins one of the eight formerly divergent inputs: the two
// functions agree, they agree on the shared rule's answer, and that answer is
// not the pre-unification one.
func assertUnified(t *testing.T, in, was, now string) {
	t.Helper()
	if got := DiscoverySlug(in); got != now {
		t.Errorf("DiscoverySlug(%q) = %q, want %q", in, got, now)
	}
	if got := topic.Slug(in); got != now {
		t.Errorf("topic.Slug(%q) = %q, want %q", in, got, now)
	}
	if DiscoverySlug(in) != topic.Slug(in) {
		t.Errorf("%q: DiscoverySlug and topic.Slug disagree again — the delegation is gone", in)
	}
	if was == now {
		t.Errorf("%q is listed as one of the eight that moved, but %q is also the old value", in, now)
	}
}
