// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package mqtt

import (
	"testing"
)

// TestCarriedWithoutDeclarationExemptionsAreAllStillCarried is the anti-rot
// guard on [carriedWithoutDeclaration], and the thing that makes finding
// **F11**'s fix an assertion rather than a second, quieter log line.
//
// F11 was that `planeRoundTrip` reported a carried-but-undeclared topic with
// `t.Logf`. That direction is the one nobody sees from outside: the broker
// accepts the publish, Home Assistant never subscribes because no config
// names the topic, and no log anywhere says a plane is writing into the void.
// It now fails — but an assertion with an exemption list is only as good as
// the list, and an exemption list nothing checks is a blanket exemption with
// extra steps. So every entry here has to be earned on every run: a shape
// that is no longer carried by any plane fails, which forces the entry out.
//
// The check runs the same five plane runners the `*PlaneTopicsRoundTrip`
// guards use, so "carried" means carried by real production code against a
// recording broker — the exemption cannot be justified by a topic helper
// agreeing with itself.
//
// One exemption stands today, `<base>/bridge/health`, and the fact that it is
// one is the measurement that made the assertion possible: before counting,
// nobody knew whether turning the log line into a failure would light up the
// whole suite. Across all five planes, at both base spellings, it lights up
// exactly this.
func TestCarriedWithoutDeclarationExemptionsAreAllStillCarried(t *testing.T) {
	t.Parallel()

	if len(carriedWithoutDeclaration) == 0 {
		t.Skip("no exemptions to check — planeRoundTrip fails on every undeclared publish")
	}

	// Every plane, at the bases their own round-trip guards use.
	carried := map[string]bool{}
	for _, obs := range []*observedPlane{
		runHubPlane(t, "openccu-loom", "ccu-01", "PartyMode", "12459"),
		runDevicePlane(t, "gh", "ccu-01", "HmIP-RF", "0001ABCD"),
		runAlarmPlane(t, "gh"),
		runSecurityPlane(t, "gh"),
		runAddonUpdatePlane(t),
	} {
		for topic := range obs.publishedTopics() {
			carried[topic] = true
		}
	}
	if len(carried) == 0 {
		t.Fatal("no plane carried anything — the exemption check would pass vacuously")
	}

	for tail, reason := range carriedWithoutDeclaration {
		hit := false
		for topic := range carried {
			if r, exempt := carriedWithoutDeclarationReason(topic); exempt && r == reason {
				hit = true
				break
			}
		}
		if !hit {
			t.Errorf("carriedWithoutDeclaration exempts %q (%s) but no plane carries it any more — "+
				"drop the entry, or the list stops meaning what it says and the next genuinely "+
				"undeclared publish hides behind it", tail, reason)
		}
	}
}

// TestUndeclaredPublishIsAFailureNotALogLine is the falsifiability check on
// F11's fix: it drives [planeRoundTrip] directly with a carried topic nothing
// declares and asserts the guard reports a failure.
//
// It exists because the fix is one `t.Logf` → `t.Errorf` edit, and an
// assertion that cannot fail is worse than the log line it replaced — an
// adversarial review of the shared library found five such tests, one of them
// standing exactly where a real defect lived. Driving `planeRoundTrip`
// against a synthetic pair is the only way to observe the new failure without
// breaking a plane on purpose: every real plane passes, which is the whole
// point of the pin.
//
// The exemption path is checked in the same shape, so the two arms cannot
// both be satisfied by a guard that always fails or always passes.
func TestUndeclaredPublishIsAFailureNotALogLine(t *testing.T) {
	t.Parallel()

	declared := map[string]bool{"gh/x/state": true}

	t.Run("undeclared-fails", func(t *testing.T) {
		t.Parallel()
		sub := &testing.T{}
		planeRoundTrip(sub, "probe", declared,
			map[string]bool{"gh/x/state": true, "gh/x/nobody-declares-this": true}, nil, nil)
		if !sub.Failed() {
			t.Fatal("planeRoundTrip passed with a carried topic nothing declares — F11's fix is inert, " +
				"and a plane writing into a topic no entity references is silent again")
		}
	})

	t.Run("exempt-passes", func(t *testing.T) {
		t.Parallel()
		sub := &testing.T{}
		planeRoundTrip(sub, "probe", declared,
			map[string]bool{"gh/x/state": true, "gh/bridge/health": true}, nil, nil)
		if sub.Failed() {
			t.Fatal("planeRoundTrip failed on an exempt shape — the allow-list is not consulted, " +
				"so every plane run would now fail on bridge/health")
		}
	})
}
