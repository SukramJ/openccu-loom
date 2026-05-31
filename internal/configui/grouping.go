// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package configui

import (
	"regexp"
	"sort"
)

// MetadataGroup is one semantic parameter group fed in from outside
// — typically derived from `ccudata.SenderTypeMetadata.ParameterGroups`.
// Mirrors the reference config panel's easymode ParameterGroupDef without
// pulling ccudata into this package.
type MetadataGroup struct {
	ID       string
	LabelKey string
	// Label is an optional inline locale→label map used as Stage-2/3
	// fallback when LabelKey is empty or the UILabel table has no entry.
	// When both LabelKey and Label are present, LabelKey wins via
	// resolveMetadataLabel; Label is only consulted when LabelKey
	// resolution returns "".
	Label      map[string]string
	Parameters []string
}

// UILabelTranslator resolves a metadata `label_key` into a localised
// human-readable string. Returns "" when no translation exists; the
// grouper then falls back to the curated section title.
//
// Production callers usually dispatch this to
// `ccudata.TranslationLoader.UILabel`; tests can pass a simple map.
type UILabelTranslator interface {
	UILabel(labelKey, locale string) string
}

// GroupChannelOptions bundles the optional metadata-aware grouping
// inputs accepted by [ParameterGrouper.GroupForChannel]. All fields
// are optional — missing metadata routes the call to the curated
// pattern-based fallback.
type GroupChannelOptions struct {
	// Groups carries the semantic parameter groups for the channel
	// + sender type combination. Order is preserved.
	Groups []MetadataGroup

	// ParameterOrder is the fallback ordering used when Groups is
	// empty but the channel still has a defined display order. The
	// resulting single "all" section preserves this order with any
	// remaining parameters appended in alphabetical order.
	ParameterOrder []string

	// UILabels resolves group label keys. May be nil — the curated
	// English title is then used as fallback.
	UILabels UILabelTranslator

	// Locale overrides the LabelResolver-style locale lookup. Empty
	// falls back to [DefaultLocale].
	Locale string
}

// ParameterGroup is the result of pattern-based parameter grouping.
// Mirrors the reference config panel's ParameterGroup type. The Title is
// pre-localised — callers can substitute their own locale by
// constructing a [ParameterGrouper] with a custom [GroupDefinition]
// list.
type ParameterGroup struct {
	ID         string
	Title      string
	Parameters []string
}

// GroupDefinition declares one classification rule. A parameter
// belongs to the group when at least one of `Patterns` matches its
// ID. Order matters — earlier definitions take precedence.
type GroupDefinition struct {
	ID       string
	Title    string
	Patterns []string
}

// curatedGroupDefinitions mirrors the reference config panel's
// `_GROUP_DEFINITIONS`. Order is significant: each parameter ends up
// in the first matching group. Anything that matches no pattern goes
// into the catch-all "other" group at the end.
var curatedGroupDefinitions = []GroupDefinition{
	{ID: "temperature", Title: "Temperature Settings", Patterns: []string{
		`^TEMPERATURE_.*`, `.*_TEMP_.*`, `^FROST_.*`, `^COMFORT_.*`, `^ECO_.*`,
	}},
	{ID: "timing", Title: "Timing & Duration", Patterns: []string{
		`.*_TIME_.*`, `.*_DURATION_.*`, `.*_DELAY_.*`, `.*_INTERVAL_.*`, `.*_TIMEOUT_.*`,
	}},
	{ID: "display", Title: "Display Settings", Patterns: []string{
		`^SHOW_.*`, `^DISPLAY_.*`, `^BACKLIGHT_.*`, `^LED_.*`,
	}},
	{ID: "transmission", Title: "Transmission & Communication", Patterns: []string{
		`^TRANSMIT_.*`, `^TX_.*`, `^SIGNAL_.*`, `^DUTYCYCLE_.*`, `^COND_TX_.*`,
	}},
	{ID: "powerup", Title: "Power-Up Behavior", Patterns: []string{
		`^POWERUP_.*`,
	}},
	{ID: "boost", Title: "Boost Settings", Patterns: []string{
		`^BOOST_.*`,
	}},
	{ID: "button", Title: "Button Behavior", Patterns: []string{
		`^BUTTON_.*`, `^LOCAL_.*`,
	}},
	{ID: "threshold", Title: "Thresholds & Conditions", Patterns: []string{
		`.*_THRESHOLD_.*`, `.*_DECISION_.*`, `.*_FILTER.*`,
	}},
	{ID: "status", Title: "Status & Reporting", Patterns: []string{
		`^STATUSINFO_.*`, `^STATUS_.*`,
	}},
}

// otherGroupID is the catch-all group for parameters that match no
// curated pattern. Mirrors the reference config panel's "other" sink.
const (
	otherGroupID    = "other"
	otherGroupTitle = "Other Settings"
)

// ParameterGrouper sorts a flat list of parameter IDs into curated
// sections. Mirrors the reference config panel's ParameterGrouper —
// without the easymode-metadata path, which can be layered on
// separately.
//
// Concurrency: a [ParameterGrouper] is safe to share across goroutines
// once constructed; the regexes are precompiled at construction time.
type ParameterGrouper struct {
	defs       []GroupDefinition
	compiled   []groupCompiled
	otherTitle string
}

type groupCompiled struct {
	id       string
	title    string
	patterns []*regexp.Regexp
}

// NewParameterGrouper compiles the curated group definitions (or
// custom ones if provided) into a runnable grouper.
//
// Pass nil for the default English titles. Pass a non-nil slice for
// custom locales / extra rules — the slice is copied so the caller
// may mutate the original safely.
//
// loom:reachable:reason="instantiated in Config-UI REST handler to group MASTER paramset parameters for the operator UI"
func NewParameterGrouper(defs []GroupDefinition) *ParameterGrouper {
	if defs == nil {
		defs = curatedGroupDefinitions
	}
	g := &ParameterGrouper{
		defs:       append([]GroupDefinition(nil), defs...),
		compiled:   make([]groupCompiled, 0, len(defs)),
		otherTitle: otherGroupTitle,
	}
	for _, d := range defs {
		entry := groupCompiled{id: d.ID, title: d.Title, patterns: make([]*regexp.Regexp, 0, len(d.Patterns))}
		for _, p := range d.Patterns {
			if rx, err := regexp.Compile(p); err == nil {
				entry.patterns = append(entry.patterns, rx)
			}
		}
		g.compiled = append(g.compiled, entry)
	}
	return g
}

// SetOtherTitle overrides the catch-all section title. Useful for
// localised UIs that need to render the "Other Settings" header in a
// non-English language without supplying a full custom rule set.
func (g *ParameterGrouper) SetOtherTitle(title string) {
	if title != "" {
		g.otherTitle = title
	}
}

// Group sorts parameters into the configured sections. Returns one
// [ParameterGroup] per non-empty section in definition order, with a
// trailing "other" group for parameters that match no pattern. Empty
// curated sections are omitted so the UI does not render empty
// headers.
//
// Within each group, parameters keep alphabetical order so the output
// is stable and diff-friendly.
func (g *ParameterGrouper) Group(parameters []string) []ParameterGroup {
	if len(parameters) == 0 {
		return nil
	}
	buckets := make(map[string][]string, len(g.compiled)+1)
	other := make([]string, 0)

	for _, name := range parameters {
		matched := false
		for _, c := range g.compiled {
			for _, rx := range c.patterns {
				if rx.MatchString(name) {
					buckets[c.id] = append(buckets[c.id], name)
					matched = true
					break
				}
			}
			if matched {
				break
			}
		}
		if !matched {
			other = append(other, name)
		}
	}

	out := make([]ParameterGroup, 0, len(g.compiled)+1)
	for _, c := range g.compiled {
		bucket := buckets[c.id]
		if len(bucket) == 0 {
			continue
		}
		sort.Strings(bucket)
		out = append(out, ParameterGroup{
			ID:         c.id,
			Title:      c.title,
			Parameters: bucket,
		})
	}
	if len(other) > 0 {
		sort.Strings(other)
		out = append(out, ParameterGroup{
			ID:         otherGroupID,
			Title:      g.otherTitle,
			Parameters: other,
		})
	}
	return out
}

// GroupForChannel sorts parameters with metadata-aware semantic
// grouping when [GroupChannelOptions.Groups] is non-empty, falls back
// to a single "all" section ordered by [GroupChannelOptions.ParameterOrder]
// when only the order is known, and finally falls back to the curated
// pattern-based [Group] when neither is available.
//
// Mirrors the metadata path of the reference config panel's
// ParameterGrouper.group(). Stays free of ccudata imports — callers
// translate `ccudata.SenderTypeMetadata` into [MetadataGroup] /
// [GroupChannelOptions] themselves.
func (g *ParameterGrouper) GroupForChannel(parameters []string, opts GroupChannelOptions) []ParameterGroup {
	if len(parameters) == 0 {
		return nil
	}
	if len(opts.Groups) == 0 && len(opts.ParameterOrder) == 0 {
		return g.Group(parameters)
	}

	available := make(map[string]struct{}, len(parameters))
	for _, p := range parameters {
		available[p] = struct{}{}
	}

	if len(opts.Groups) > 0 {
		out := make([]ParameterGroup, 0, len(opts.Groups)+1)
		assigned := make(map[string]struct{}, len(parameters))
		for _, mg := range opts.Groups {
			members := make([]string, 0, len(mg.Parameters))
			for _, p := range mg.Parameters {
				if _, ok := available[p]; !ok {
					continue
				}
				if _, dup := assigned[p]; dup {
					continue
				}
				assigned[p] = struct{}{}
				members = append(members, p)
			}
			if len(members) == 0 {
				continue
			}
			out = append(out, ParameterGroup{
				ID:         mg.ID,
				Title:      g.resolveMetadataLabel(mg, opts),
				Parameters: members,
			})
		}
		ungrouped := make([]string, 0)
		for _, p := range parameters {
			if _, ok := assigned[p]; !ok {
				ungrouped = append(ungrouped, p)
			}
		}
		if len(ungrouped) > 0 {
			sort.Strings(ungrouped)
			out = append(out, ParameterGroup{
				ID:         otherGroupID,
				Title:      g.otherTitle,
				Parameters: ungrouped,
			})
		}
		if len(out) > 0 {
			return out
		}
	}

	ordered := make([]string, 0, len(opts.ParameterOrder))
	seen := make(map[string]struct{}, len(opts.ParameterOrder))
	for _, p := range opts.ParameterOrder {
		if _, ok := available[p]; !ok {
			continue
		}
		if _, dup := seen[p]; dup {
			continue
		}
		seen[p] = struct{}{}
		ordered = append(ordered, p)
	}
	remaining := make([]string, 0)
	for _, p := range parameters {
		if _, ok := seen[p]; !ok {
			remaining = append(remaining, p)
		}
	}
	sort.Strings(remaining)
	all := make([]string, 0, len(ordered)+len(remaining))
	all = append(all, ordered...)
	all = append(all, remaining...)
	if len(all) == 0 {
		return nil
	}
	return []ParameterGroup{{
		ID:         "all",
		Title:      g.otherTitle,
		Parameters: all,
	}}
}

// resolveMetadataLabel returns the localised group title. Resolution order:
//  1. UILabels.UILabel(label_key, locale)     — translation-table, locale
//  2. UILabels.UILabel(label_key, "en")       — translation-table, English
//  3. mg.Label[locale]                         — inline easymode label, locale
//  4. mg.Label["en"]                           — inline easymode label, English
//  5. the curated otherTitle                   — generic fallback
func (g *ParameterGrouper) resolveMetadataLabel(mg MetadataGroup, opts GroupChannelOptions) string {
	locale := opts.Locale
	if locale == "" {
		locale = DefaultLocale
	}

	// Stages 1 + 2: translation-table lookup via LabelKey.
	if mg.LabelKey != "" && opts.UILabels != nil {
		if t := opts.UILabels.UILabel(mg.LabelKey, locale); t != "" {
			return t
		}
		if locale != DefaultLocale {
			if t := opts.UILabels.UILabel(mg.LabelKey, DefaultLocale); t != "" {
				return t
			}
		}
	}

	// Stages 3 + 4: inline Label map from the easymode archive.
	if len(mg.Label) > 0 {
		if t := mg.Label[locale]; t != "" {
			return t
		}
		if t := mg.Label[DefaultLocale]; t != "" {
			return t
		}
	}

	return g.otherTitle
}
