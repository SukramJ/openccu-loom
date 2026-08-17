// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package security

import (
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/SukramJ/openccu-loom/internal/i18n"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
)

// newTestRendererCatalogs loads the real embedded catalogue so a render
// test exercises the actual "security.subject.*"/"security.message.*"
// entries rather than a stand-in template.
func newTestRendererCatalogs(t *testing.T) *i18n.Catalogs {
	t.Helper()
	cat, err := i18n.NewCatalogs()
	if err != nil {
		t.Fatalf("i18n.NewCatalogs: %v", err)
	}
	return cat
}

// TestRendererRenderFillsFields verifies render() populates every facet
// of the Notification from a real catalogue entry: Subject, Message,
// I18nKey, Args, Severity, Link and AtMS.
func TestRendererRenderFillsFields(t *testing.T) {
	r := newRenderer(newTestRendererCatalogs(t), "en", "http://example.test")

	at := time.Date(2026, time.March, 4, 14, 37, 0, 0, time.UTC)
	in := reportInput{
		Class: hmenum.SecurityClassSmoke,
		Verb:  hmenum.SecurityVerbTriggered,
		Sources: []hmevent.SecuritySourceRef{
			{Name: "Kitchen Detector", ChannelAddress: "ABC123:1", Central: "ccu1"},
		},
		At:         at,
		Retainable: true,
	}

	n := r.render(in)

	if want := "Smoke alarm — Kitchen Detector"; n.Subject != want {
		t.Errorf("Subject = %q, want %q", n.Subject, want)
	}
	if want := "Smoke was detected by Kitchen Detector at 14:37 — leave the area immediately."; n.Message != want {
		t.Errorf("Message = %q, want %q", n.Message, want)
	}
	if want := "security.message.smoke.triggered"; n.I18nKey != want {
		t.Errorf("I18nKey = %q, want %q", n.I18nKey, want)
	}
	if n.Args == nil {
		t.Fatal("Args is nil, want the placeholder map")
	}
	if got := n.Args["sensor"]; got != "Kitchen Detector" {
		t.Errorf(`Args["sensor"] = %q, want "Kitchen Detector"`, got)
	}
	if got := n.Args["time"]; got != "14:37" {
		t.Errorf(`Args["time"] = %q, want "14:37"`, got)
	}
	if want := hmenum.SecuritySeverityCritical; n.Severity != want {
		t.Errorf("Severity = %q, want %q", n.Severity, want)
	}
	if want := "http://example.test/app/#/security"; n.Link != want {
		t.Errorf("Link = %q, want %q", n.Link, want)
	}
	if want := at.UnixMilli(); n.AtMS != want {
		t.Errorf("AtMS = %d, want %d", n.AtMS, want)
	}
}

// TestSeverityFor pins the precedence: a cleared condition always folds
// to OK regardless of class, a test notification always folds to Info,
// and everything else defers to hmenum.SeverityForClass.
func TestSeverityFor(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		in   reportInput
		want hmenum.SecuritySeverity
	}{
		{
			name: "cleared smoke is OK despite hazard class",
			in:   reportInput{Class: hmenum.SecurityClassSmoke, Verb: hmenum.SecurityVerbCleared},
			want: hmenum.SecuritySeverityOK,
		},
		{
			name: "cleared intrusion is OK",
			in:   reportInput{Class: hmenum.SecurityClassIntrusion, Verb: hmenum.SecurityVerbCleared},
			want: hmenum.SecuritySeverityOK,
		},
		{
			name: "test smoke is Info, not Critical",
			in:   reportInput{Class: hmenum.SecurityClassSmoke, Verb: hmenum.SecurityVerbTest},
			want: hmenum.SecuritySeverityInfo,
		},
		{
			name: "test tamper is Info",
			in:   reportInput{Class: hmenum.SecurityClassTamper, Verb: hmenum.SecurityVerbTest},
			want: hmenum.SecuritySeverityInfo,
		},
		{
			name: "triggered smoke defers to SeverityForClass (Critical)",
			in:   reportInput{Class: hmenum.SecurityClassSmoke, Verb: hmenum.SecurityVerbTriggered},
			want: hmenum.SecuritySeverityCritical,
		},
		{
			name: "triggered water defers to SeverityForClass (Alarm)",
			in:   reportInput{Class: hmenum.SecurityClassWater, Verb: hmenum.SecurityVerbTriggered},
			want: hmenum.SecuritySeverityAlarm,
		},
		{
			name: "raised tamper defers to SeverityForClass (Warning)",
			in:   reportInput{Class: hmenum.SecurityClassTamper, Verb: hmenum.SecurityVerbRaised},
			want: hmenum.SecuritySeverityWarning,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := severityFor(tc.in); got != tc.want {
				t.Errorf("severityFor(%+v) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// TestClampRunesExactLimitUntouched verifies a string of exactly
// subjectMaxRunes runes is returned unchanged.
func TestClampRunesExactLimitUntouched(t *testing.T) {
	s := strings.Repeat("x", subjectMaxRunes)
	got := clampRunes(s, subjectMaxRunes)
	if got != s {
		t.Errorf("clampRunes returned %q, want the input unchanged", got)
	}
}

// TestClampRunesTruncatesOnRuneBoundary verifies that a subject built
// from multi-byte runes (German umlauts) is truncated on a rune
// boundary rather than a byte boundary: the result must remain valid
// UTF-8 and end with the ellipsis marker.
func TestClampRunesTruncatesOnRuneBoundary(t *testing.T) {
	s := strings.Repeat("ä", 130)
	got := clampRunes(s, subjectMaxRunes)

	if !utf8.ValidString(got) {
		t.Fatalf("clampRunes result is not valid UTF-8: %q", got)
	}
	if !strings.HasSuffix(got, "…") {
		t.Errorf("clampRunes result = %q, want it to end with an ellipsis", got)
	}
	if n := utf8.RuneCountInString(got); n > subjectMaxRunes {
		t.Errorf("clampRunes result has %d runes, want at most %d", n, subjectMaxRunes)
	}
}

// TestJoinNames verifies the switch from a full name list to a
// "first six (+N)" summary happens exactly at the maxNamedSources
// boundary.
func TestJoinNames(t *testing.T) {
	t.Parallel()

	six := []string{"a", "b", "c", "d", "e", "f"}
	if got, want := joinNames(six), "a, b, c, d, e, f"; got != want {
		t.Errorf("joinNames(6 names) = %q, want %q", got, want)
	}

	seven := append(append([]string(nil), six...), "g")
	if got, want := joinNames(seven), "a, b, c, d, e, f (+1)"; got != want {
		t.Errorf("joinNames(7 names) = %q, want %q", got, want)
	}
}

// TestRendererLink verifies the deep-link derivation: an empty base URL
// always suppresses the link, a fault report links to the fault list,
// and a zone-scoped hazard report links to its zone.
func TestRendererLink(t *testing.T) {
	t.Parallel()

	t.Run("empty base URL yields empty link", func(t *testing.T) {
		t.Parallel()
		r := newRenderer(nil, "en", "")
		got := r.link(reportInput{Fault: true, ZoneSlug: "hallway"})
		if got != "" {
			t.Errorf("link = %q, want empty", got)
		}
	})

	t.Run("fault links to the fault list", func(t *testing.T) {
		t.Parallel()
		r := newRenderer(nil, "en", "http://example.test")
		got := r.link(reportInput{Fault: true})
		if want := "http://example.test/app/#/security/faults"; got != want {
			t.Errorf("link = %q, want %q", got, want)
		}
	})

	t.Run("zone-scoped non-fault links to the zone", func(t *testing.T) {
		t.Parallel()
		r := newRenderer(nil, "en", "http://example.test")
		got := r.link(reportInput{ZoneSlug: "hallway"})
		if want := "http://example.test/app/#/security/zones/hallway"; got != want {
			t.Errorf("link = %q, want %q", got, want)
		}
	})

	t.Run("system-level report links to the domain overview", func(t *testing.T) {
		t.Parallel()
		r := newRenderer(nil, "en", "http://example.test")
		got := r.link(reportInput{})
		if want := "http://example.test/app/#/security"; got != want {
			t.Errorf("link = %q, want %q", got, want)
		}
	})
}

// TestRendererArgsAlwaysHasAllKeys verifies that args() populates every
// one of the ten placeholder keys even for a completely empty input —
// that is what keeps a stray literal like {zone} from ever reaching a
// user due to a missing map entry rather than a missing value.
func TestRendererArgsAlwaysHasAllKeys(t *testing.T) {
	r := newRenderer(nil, "en", "")
	args := r.args(reportInput{})

	want := []string{"zone", "mode", "count", "time", "date", "reason", "class", "central", "sensor", "sensors"}
	if len(args) != len(want) {
		t.Errorf("args() has %d keys, want %d: %v", len(args), len(want), args)
	}
	for _, k := range want {
		if _, ok := args[k]; !ok {
			t.Errorf("args() is missing key %q", k)
		}
	}
}

// TestRendererRendersRaisedForEveryHazardClass pins that a hazard class
// (smoke/water/gas/co/intrusion/panic) rendered with [hmenum.
// SecurityVerbRaised] resolves to real catalogue prose in both locales,
// not the raw "security.subject.<class>.raised" / "security.message.
// <class>.raised" key text.
//
// The Raised verb is normally a diagnostic-class-only path (applyFault
// is only reached from subscribe.go for a non-hazard class), but
// index.go's enrollment.SensorType branch can carry a hazard class
// (from the sensor's enrolled type) alongside a diagnostic fault reason
// classified independently from a different parameter on the same
// device — e.g. an enrolled smoke detector's own LOWBAT parameter. That
// combination reaches applyFault via the boot-time fault reconciliation
// pass (index.go rebuild), which raises with the class attached to the
// source, not with the reason's own diagnostic class.
func TestRendererRendersRaisedForEveryHazardClass(t *testing.T) {
	cat := newTestRendererCatalogs(t)

	for _, locale := range []string{"en", "de"} {
		for _, class := range hmenum.SecurityClasses() {
			if !class.Hazard() {
				continue
			}
			t.Run(locale+"/"+string(class), func(t *testing.T) {
				r := newRenderer(cat, locale, "")
				in := reportInput{
					Class:  class,
					Verb:   hmenum.SecurityVerbRaised,
					Reason: hmenum.SecurityFaultReasonLowBattery,
					Sources: []hmevent.SecuritySourceRef{
						{Name: "Detector", ChannelAddress: "ABC123:1", Central: "ccu1"},
					},
					At:         time.Date(2026, time.March, 4, 14, 37, 0, 0, time.UTC),
					Fault:      true,
					Retainable: true,
				}
				n := r.render(in)

				if n.Subject == subjectKey(class, hmenum.SecurityVerbRaised) {
					t.Errorf("Subject fell back to the raw catalogue key %q — no translation for this (class, verb)", n.Subject)
				}
				if n.Message == messageKey(class, hmenum.SecurityVerbRaised) {
					t.Errorf("Message fell back to the raw catalogue key %q — no translation for this (class, verb)", n.Message)
				}
				if strings.Contains(n.Subject, "{") || strings.Contains(n.Message, "{") {
					t.Errorf("unresolved placeholder: Subject=%q Message=%q", n.Subject, n.Message)
				}
			})
		}
	}
}
