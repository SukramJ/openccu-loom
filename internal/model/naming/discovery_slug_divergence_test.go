// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package naming

import (
	"testing"

	"github.com/SukramJ/go-hamqtt/topic"
)

// TestDiscoverySlugDivergesFromTopicSlug pins every measured disagreement
// between [DiscoverySlug] and the shared `topic.Slug`, in both directions.
//
// The two answer the same question — turn a string into the
// `[A-Za-z0-9_-]` Home Assistant accepts for a node id — and ADR 0070's
// third amendment ends with [DiscoverySlug] being deleted in favour of the
// shared rule. Over a 23-case probe they agree on 15 inputs and disagree on
// eight, in two classes: the shared rule transliterates non-German accented
// Latin where this one drops it, and the shared rule collapses a run of
// separators that includes a literal underscore where this one passes the
// pair through.
//
// The golden fixtures did not cover either class. Of the 68 node ids and 170
// topics they pin, every non-ASCII character sat in a `name`, a unit, or a
// TopicSafe-rendered bridge topic — never in an identifier — so a swap of the
// two functions shipped green while moving a published node id on any
// installation whose CCU names something in French, Spanish, Danish, Swedish
// or Norwegian, and on any installation with a colon-underscore pair in a
// name. "Watchdog:_CCU-Jack" is [DiscoverySlug]'s own documented example of
// a real CCU name.
//
// This is a pin of current behaviour, not an assertion that it is right. The
// shared behaviour IS better. Adopting it moves published object ids and is
// therefore a breaking change under ADR 0068 — which is exactly what this
// test exists to make loud.
func TestDiscoverySlugDivergesFromTopicSlug(t *testing.T) {
	t.Parallel()

	t.Run("class one: non-German accented Latin is dropped, not transliterated", func(t *testing.T) {
		t.Parallel()
		for _, c := range []struct{ in, discovery, shared string }{
			{"Café", "caf", "cafe"},
			{"Señor", "se_or", "senor"},
			{"Garçon", "gar_on", "garcon"},
			{"Ångström", "ngstroem", "angstroem"},
			{"Ærø", "r", "aeroe"},
			{"Søren", "s_ren", "soeren"},
		} {
			assertDivergence(t, c.in, c.discovery, c.shared)
		}
	})

	t.Run("class two: a literal underscore does not collapse", func(t *testing.T) {
		t.Parallel()
		for _, c := range []struct{ in, discovery, shared string }{
			{"a__b", "a__b", "a_b"},
			{"Watchdog:_CCU-Jack", "watchdog__ccu-jack", "watchdog_ccu-jack"},
		} {
			assertDivergence(t, c.in, c.discovery, c.shared)
		}
	})

	t.Run("the German set is where the two already agree", func(t *testing.T) {
		t.Parallel()
		for _, c := range []struct{ in, want string }{
			{"CCU Küche", "ccu_kueche"},
			{"Heizung Büro", "heizung_buero"},
			{"s0_Sensoren_Hülle_EG", "s0_sensoren_huelle_eg"},
			{"Außen Temperatur", "aussen_temperatur"},
		} {
			if got := DiscoverySlug(c.in); got != c.want {
				t.Errorf("DiscoverySlug(%q) = %q, want %q", c.in, got, c.want)
			}
			if got := topic.Slug(c.in); got != c.want {
				t.Errorf("topic.Slug(%q) = %q, want %q — the two no longer agree on the German set", c.in, got, c.want)
			}
		}
	})
}

// TestDiscoverySlugCollidesOnADroppedAccent pins the live consequence of
// class one: two differently named system variables reduce to one node id,
// so their retained discovery configs land on one topic and Home Assistant
// keeps whichever arrived last. The golden fixture
// `sysvar/hazard-accent-twin-{a,b}` is the same fact seen through a rendered
// payload; this is it seen directly.
func TestDiscoverySlugCollidesOnADroppedAccent(t *testing.T) {
	t.Parallel()

	if DiscoverySlug("Café") != DiscoverySlug("Caf") {
		t.Fatalf("the Café/Caf collision is gone — if that was deliberate it moved a published node id")
	}
	if topic.Slug("Café") == topic.Slug("Caf") {
		t.Errorf("topic.Slug collides too, so adopting it would not resolve the collision")
	}
}

// assertDivergence pins one input against both rules.
func assertDivergence(t *testing.T, in, discovery, shared string) {
	t.Helper()
	if got := DiscoverySlug(in); got != discovery {
		t.Errorf("DiscoverySlug(%q) = %q, want %q", in, got, discovery)
	}
	if got := topic.Slug(in); got != shared {
		t.Errorf("topic.Slug(%q) = %q, want %q", in, got, shared)
	}
	if discovery == shared {
		t.Errorf("%q is listed as a divergence but the two agree on %q", in, discovery)
	}
}
