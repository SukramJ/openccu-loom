// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package mqtt

import (
	"strings"
	"testing"

	pload "github.com/SukramJ/openccu-loom/internal/payload"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// TestEnumOptionTemplatesRoundTripEveryValue is the guard that makes
// localised options safe to publish: for every VALUE_LIST entry, the
// state template must render the label and the command template must map
// that label back to the CCU's own token.
//
// The round trip is the whole point. Publishing labels as `options` fixes
// what an operator reads, but Home Assistant sends the chosen option
// string back on write — so without the reverse mapping a select would
// hand the CCU a display string it has never heard of, and the write
// would fail with an invalid-value fault. Testing the two templates
// separately would not catch a mismatch between them.
func TestEnumOptionTemplatesRoundTripEveryValue(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		values []string
		labels []string
	}{
		{
			name:   "climate set-point modes",
			values: []string{"AUTO_MODE", "MANU_MODE", "PARTY_MODE", "BOOST_MODE"},
			labels: []string{"Automatik", "Manuell", "Urlaub", "Boost"},
		},
		{
			name:   "label carrying an apostrophe",
			values: []string{"ON", "OFF"},
			labels: []string{"Ein'aus", "Aus"},
		},
		{
			name:   "label carrying a backslash",
			values: []string{"A", "B"},
			labels: []string{`Auf\Zu`, "Zu"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			stateTemplate, commandTemplate := enumOptionTemplates(tc.values, tc.labels)

			for i, value := range tc.values {
				label := tc.labels[i]
				// The state template maps the wire token onto the label…
				if got := renderJinjaMap(t, stateTemplate, value); got != label {
					t.Errorf("state template maps %q to %q, want the label %q", value, got, label)
				}
				// …and the command template maps it back to the token.
				if got := renderJinjaMap(t, commandTemplate, label); got != value {
					t.Errorf("command template maps %q to %q, want the CCU token %q — a write would "+
						"hand the CCU a value it does not accept", label, got, value)
				}
			}
			// An unknown value falls through to itself rather than
			// blanking the entity.
			if got := renderJinjaMap(t, stateTemplate, "UNEXPECTED"); got != "UNEXPECTED" {
				t.Errorf("an unmapped token renders as %q, want it to fall through as itself", got)
			}
		})
	}
}

// TestLocalisedEnumOptionsRejectsUnusableLabelSets pins the fallback: a
// label set that cannot address its values unambiguously must not be
// published, because Home Assistant keys an option by its display string.
// Falling back to raw tokens looks worse and works; publishing an
// ambiguous set looks right and misroutes a write.
func TestLocalisedEnumOptionsRejectsUnusableLabelSets(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name       string
		values     []string
		labels     []string
		wantUsable bool
	}{
		{name: "distinct labels", values: []string{"A", "B"}, labels: []string{"Auf", "Zu"}, wantUsable: true},
		{name: "no labels at all", values: []string{"A", "B"}, labels: nil},
		{name: "fewer labels than values", values: []string{"A", "B"}, labels: []string{"Auf"}},
		{name: "duplicate labels", values: []string{"A", "B"}, labels: []string{"Auf", "Auf"}},
		{name: "empty label", values: []string{"A", "B"}, labels: []string{"Auf", ""}},
		{name: "blank label", values: []string{"A", "B"}, labels: []string{"Auf", "   "}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ev := Event{Descriptor: &pload.GenericConfig{
				Type:        hmenum.ParameterTypeEnum,
				ValueList:   tc.values,
				ValueLabels: tc.labels,
			}}
			got, usable := localisedEnumOptions(ev)
			if usable != tc.wantUsable {
				t.Fatalf("usable=%v, want %v (options=%v)", usable, tc.wantUsable, got)
			}
			if usable && len(got) != len(tc.labels) {
				t.Errorf("got %d options for %d labels", len(got), len(tc.labels))
			}
		})
	}
}

// renderJinjaMap evaluates the `{% set m = {...} %}…m.get(x, x)` shape the
// option templates use, for one input. It is a deliberately narrow
// evaluator: it parses the literal map out of the template and applies
// the documented fallback, which is exactly the contract the real Jinja
// renderer honours for these templates.
func renderJinjaMap(t *testing.T, template, input string) string {
	t.Helper()
	open := strings.Index(template, "{")
	start := strings.Index(template, "{% set m = {")
	if start != 0 || open < 0 {
		t.Fatalf("template does not start with the map assignment: %q", template)
	}
	body := template[len("{% set m = {"):]
	end := strings.Index(body, "} %}")
	if end < 0 {
		t.Fatalf("template map is unterminated: %q", template)
	}
	mapping := parseJinjaPairs(t, body[:end])
	if v, ok := mapping[input]; ok {
		return v
	}
	return input
}

// parseJinjaPairs reads `'k': 'v', 'k2': 'v2'` into a map, honouring the
// backslash escapes jinjaQuote writes.
func parseJinjaPairs(t *testing.T, pairs string) map[string]string {
	t.Helper()
	out := make(map[string]string)
	var (
		current   []string
		literal   strings.Builder
		inLiteral bool
		escaped   bool
	)
	for _, r := range pairs {
		switch {
		case escaped:
			literal.WriteRune(r)
			escaped = false
		case inLiteral && r == '\\':
			escaped = true
		case r == '\'':
			if inLiteral {
				current = append(current, literal.String())
				literal.Reset()
			}
			inLiteral = !inLiteral
		case inLiteral:
			literal.WriteRune(r)
		}
	}
	if inLiteral {
		t.Fatalf("unterminated string literal in %q", pairs)
	}
	if len(current)%2 != 0 {
		t.Fatalf("odd number of literals in %q — the map is malformed", pairs)
	}
	for i := 0; i < len(current); i += 2 {
		out[current[i]] = current[i+1]
	}
	return out
}
