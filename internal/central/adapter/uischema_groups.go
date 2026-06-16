// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package adapter

import (
	"regexp"

	"github.com/SukramJ/openccu-loom/internal/ccudata"
	"github.com/SukramJ/openccu-loom/pkg/hmapi"
)

// groupPattern bundles one heuristic group the fallback grouper uses
// when easymode has no curated breakdown for a channel type. Order
// matters: earlier entries win, so more specific patterns come first.
type groupPattern struct {
	id         string
	labelEn    string
	labelDe    string
	expression *regexp.Regexp
}

// FallbackGroupPatterns mirrors 's
// `_GROUP_DEFINITIONS` in grouping.py, plus an explicit DST section
// That leaves for the HA frontend to split
// client-side. We split at the backend so every API consumer (and
// our own SPA) sees the same top-level structure.
var fallbackGroupPatterns = []groupPattern{
	{
		id: "dst", labelEn: "Daylight Saving Time", labelDe: "Sommerzeit",
		expression: regexp.MustCompile(`^DST_(START|END)_.*$`),
	},
	{
		id: "temperature", labelEn: "Temperature Settings", labelDe: "Temperatur-Einstellungen",
		expression: regexp.MustCompile(`^(TEMPERATURE_.*|.*_TEMP_.*|FROST_.*|COMFORT_.*|ECO_.*)$`),
	},
	{
		id: "timing", labelEn: "Timing & Duration", labelDe: "Zeit & Dauer",
		expression: regexp.MustCompile(`^(.*_TIME_.*|.*_DURATION_.*|.*_DELAY_.*|.*_INTERVAL_.*|.*_TIMEOUT_.*)$`),
	},
	{
		id: "display", labelEn: "Display Settings", labelDe: "Anzeige-Einstellungen",
		expression: regexp.MustCompile(`^(SHOW_.*|DISPLAY_.*|BACKLIGHT_.*|LED_.*)$`),
	},
	{
		id: "transmission", labelEn: "Transmission & Communication", labelDe: "Übertragung & Kommunikation",
		expression: regexp.MustCompile(`^(TRANSMIT_.*|TX_.*|SIGNAL_.*|DUTYCYCLE_.*|COND_TX_.*)$`),
	},
	{
		id: "powerup", labelEn: "Power-Up Behavior", labelDe: "Einschaltverhalten",
		expression: regexp.MustCompile(`^POWERUP_.*$`),
	},
	{
		id: "boost", labelEn: "Boost Settings", labelDe: "Boost-Einstellungen",
		expression: regexp.MustCompile(`^BOOST_.*$`),
	},
	{
		id: "button", labelEn: "Button Behavior", labelDe: "Tastenverhalten",
		expression: regexp.MustCompile(`^(BUTTON_.*|LOCAL_.*)$`),
	},
	{
		id: "threshold", labelEn: "Thresholds & Conditions", labelDe: "Schwellwerte & Bedingungen",
		expression: regexp.MustCompile(`^(.*_THRESHOLD_.*|.*_DECISION_.*|.*_FILTER.*)$`),
	},
	{
		id: "status", labelEn: "Status & Reporting", labelDe: "Status & Meldungen",
		expression: regexp.MustCompile(`^(STATUSINFO_.*|STATUS_.*)$`),
	},
}

// otherGroupLabels provides the locale-specific title for the
// catch-all "Other Settings" bucket.
var otherGroupLabels = map[string]string{
	"de": "Sonstige Einstellungen",
	"en": "Other Settings",
}

// buildGroups computes the section layout for params.
//
// Tier 1 — easymode semantic groups: use `parameter_groups` directly,
// resolve the label via ui-labels or the inline label dict, put
// parameters that fall outside every group into an "Other" bucket.
//
// Tier 2 — parameter_order only: single "Settings" bucket that
// preserves the extractor's ordering.
//
// Tier 3 — pattern heuristic: walk [fallbackGroupPatterns] and fold
// everything else into "Other".
func (a *UISchemaAdapter) buildGroups(
	locale string,
	meta *ccudata.SenderTypeMetadata,
	params []hmapi.UISchemaParameter,
) []hmapi.UISchemaGroup {
	names := make([]string, 0, len(params))
	for i := range params {
		names = append(names, params[i].Name)
	}
	available := stringSet(names)

	if meta != nil && len(meta.ParameterGroups) > 0 {
		return a.semanticGroups(locale, meta, available)
	}
	if meta != nil && len(meta.ParameterOrder) > 0 {
		return a.orderedSingleGroup(locale, meta, available)
	}
	return a.patternGroups(locale, names)
}

func (a *UISchemaAdapter) semanticGroups(
	locale string,
	meta *ccudata.SenderTypeMetadata,
	available map[string]struct{},
) []hmapi.UISchemaGroup {
	out := make([]hmapi.UISchemaGroup, 0, len(meta.ParameterGroups)+1)
	assigned := make(map[string]struct{})
	for _, g := range meta.ParameterGroups {
		params := filterAvailable(g.Parameters, available)
		if len(params) == 0 {
			continue
		}
		for _, p := range params {
			assigned[p] = struct{}{}
		}
		out = append(out, hmapi.UISchemaGroup{
			ID:         g.ID,
			Label:      a.groupLabelWithFallback(locale, g.LabelKey, g.Label),
			Parameters: params,
		})
	}
	if remaining := difference(available, assigned); len(remaining) > 0 {
		out = append(out, hmapi.UISchemaGroup{
			ID:         "other",
			Label:      otherGroupLabel(locale),
			Parameters: remaining,
		})
	}
	return out
}

func (a *UISchemaAdapter) orderedSingleGroup(
	locale string,
	meta *ccudata.SenderTypeMetadata,
	available map[string]struct{},
) []hmapi.UISchemaGroup {
	ordered := filterAvailable(meta.ParameterOrder, available)
	rest := difference(available, stringSet(ordered))
	all := make([]string, 0, len(ordered)+len(rest))
	all = append(all, ordered...)
	all = append(all, rest...)
	if len(all) == 0 {
		return nil
	}
	label := otherGroupLabel(locale)
	switch locale {
	case "en":
		label = "Settings"
	case "de":
		label = "Einstellungen"
	}
	return []hmapi.UISchemaGroup{{ID: "all", Label: label, Parameters: all}}
}

func (a *UISchemaAdapter) patternGroups(locale string, names []string) []hmapi.UISchemaGroup {
	matched := make(map[string][]string, len(fallbackGroupPatterns))
	used := make(map[string]struct{})
	for _, name := range names {
		for _, g := range fallbackGroupPatterns {
			if g.expression.MatchString(name) {
				matched[g.id] = append(matched[g.id], name)
				used[name] = struct{}{}
				break
			}
		}
	}
	out := make([]hmapi.UISchemaGroup, 0, len(fallbackGroupPatterns)+1)
	for _, g := range fallbackGroupPatterns {
		if len(matched[g.id]) == 0 {
			continue
		}
		out = append(out, hmapi.UISchemaGroup{
			ID:         g.id,
			Label:      groupLabelForLocale(locale, g.labelEn, g.labelDe),
			Parameters: matched[g.id],
		})
	}
	var other []string
	for _, name := range names {
		if _, ok := used[name]; !ok {
			other = append(other, name)
		}
	}
	if len(other) > 0 {
		out = append(out, hmapi.UISchemaGroup{
			ID:         "other",
			Label:      otherGroupLabel(locale),
			Parameters: other,
		})
	}
	return out
}

func groupLabelForLocale(locale, en, de string) string {
	if locale == "de" {
		return de
	}
	return en
}

func otherGroupLabel(locale string) string {
	if v, ok := otherGroupLabels[locale]; ok {
		return v
	}
	return otherGroupLabels["en"]
}

func stringSet(values []string) map[string]struct{} {
	out := make(map[string]struct{}, len(values))
	for _, v := range values {
		out[v] = struct{}{}
	}
	return out
}

func filterAvailable(in []string, available map[string]struct{}) []string {
	out := make([]string, 0, len(in))
	for _, name := range in {
		if _, ok := available[name]; ok {
			out = append(out, name)
		}
	}
	return out
}

func difference(available, used map[string]struct{}) []string {
	out := make([]string, 0, len(available))
	for name := range available {
		if _, ok := used[name]; ok {
			continue
		}
		out = append(out, name)
	}
	// Stable order for tests and diffs.
	sortStrings(out)
	return out
}

func sortStrings(in []string) {
	for i := 1; i < len(in); i++ {
		for j := i; j > 0 && in[j-1] > in[j]; j-- {
			in[j-1], in[j] = in[j], in[j-1]
		}
	}
}
