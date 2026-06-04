// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package configui

import (
	"encoding/json"
	"fmt"
	"maps"
	"sort"

	"github.com/SukramJ/openccu-loom/pkg/hmproto"
)

// ChannelTypeResolver remaps a channel type for HmIP translation
// lookups. Callers supply an implementation backed by
// [ccudata.Translations.ResolveChannelType] when the translations
// archive is available; nil means no remapping.
type ChannelTypeResolver interface {
	ResolveChannelType(channelType string, isHmIP bool) string
}

// OptionValueTranslatorFunc resolves a human-readable label for one
// VALUE_LIST entry of an ENUM parameter. Parameters:
//   - parameter: the parameter ID (e.g. "CONTROL_MODE")
//   - value: one VALUE_LIST entry (e.g. "AUTO")
//   - channelType: the resolved channel type (may be empty)
//   - locale: the UI locale (e.g. "en", "de")
//
// Returns the label, or an empty string when no translation exists.
// The generator falls back to [Humanize](value) on an empty return.
// Mirrors form_schema.py:272-291 (get_parameter_value_translation calls).
type OptionValueTranslatorFunc func(parameter, value, channelType, locale string) string

// GenerateInput bundles the inputs FormSchemaGenerator needs to emit
// a [Schema]. Every field is optional — a missing label resolver
// triggers the auto-formatted fallback, missing current values yield
// an empty CurrentValue.
type GenerateInput struct {
	ChannelAddress   string
	ChannelType      string
	ModelDescription string
	DeviceIcon       string

	// SubModel optionally selects a nested profile sub-variant for a
	// generic model (e.g. an `HmIP-BWTH`-mode within `HmIP-BWTH-24`).
	// Empty means "use the model directly". Sub-model selection
	// currently only affects subset-group resolution through
	// grouper-extension; descriptors stay unchanged.
	SubModel string

	Descriptions  map[string]hmproto.ParameterData
	CurrentValues map[string]any

	LabelResolver *LabelResolver

	// Grouper sorts parameters into curated sections. When nil, the
	// generator falls back to a single "default" section.
	Grouper *ParameterGrouper

	// GroupChannel, when set together with [Grouper], routes the
	// section building through [ParameterGrouper.GroupForChannel] so
	// channel-specific easymode metadata takes precedence over the
	// curated pattern table. Empty/zero falls back to plain
	// [ParameterGrouper.Group] for backwards compatibility.
	GroupChannel GroupChannelOptions

	// Initialised tracks parameters whose CurrentValue has been
	// observed at least once; the resulting `Modified` flag is set
	// when the current value differs from the parameter's DEFAULT.
	// When nil, every parameter present in CurrentValues counts as
	// initialised.
	Initialised map[string]bool

	// IsHmIP, when true, triggers the HmIP channel-type remap for
	// translation lookups (e.g. SHUTTER_CONTACT → SHUTTER_CONTACT_HMIP).
	IsHmIP bool

	// ChannelTypeResolver performs the actual remap when IsHmIP is
	// true. When nil, the channel type is used unchanged even if
	// IsHmIP is set. Callers that have a loaded translations archive
	// should pass [ccudata.Translations] here.
	ChannelTypeResolver ChannelTypeResolver

	// EnrichLinkMetadata, when true, classifies every parameter via
	// [ClassifyLinkParameter] and attaches the resulting metadata
	// (KeypressGroup, Category, time presets, …) to each FormParameter.
	EnrichLinkMetadata bool

	// LinkMetadataLocale is the locale used when building TimePresets
	// labels. Defaults to [DefaultLocale] ("en") when empty.
	LinkMetadataLocale string

	// RequireTranslation, when true, excludes parameters for which no
	// CCU translation is available. This mirrors the CCU WebUI easymode
	// behaviour: only translated parameters appear in the channel-config
	// form. Set to false for LINK paramsets where translations are often
	// absent. When LabelResolver is nil this flag has no effect — all
	// parameters are included regardless.
	RequireTranslation bool

	// OptionValueTranslator, when non-nil, resolves human-readable labels
	// for every VALUE_LIST entry of an ENUM parameter. The resolver is
	// called per-value with (paramID, value, channelType, locale); it
	// returns the label or an empty string when no translation exists.
	// Mirrors form_schema.py:266-292 (option_labels resolution).
	OptionValueTranslator OptionValueTranslatorFunc

	// SubsetDefs, when non-empty, is used to build [SubsetGroup] entries
	// on the returned [Schema]. Mirrors the reference config panel's
	// _build_subset_groups. Translate ccudata.SubsetDef → SubsetDefInput
	// at the call site so this package stays free of ccudata imports.
	SubsetDefs []SubsetDefInput
}

// SubsetDefInput is the configui-internal representation of one
// easymode UC6 subset definition. It mirrors ccudata.SubsetDef but
// lives in this package so [GenerateInput] stays free of ccudata
// imports.
type SubsetDefInput struct {
	ID           int
	NameKey      string
	MemberParams []string
	// Values carries the legacy single-option form.
	Values map[string]any
	// Options carries the multi-option form (newer extracts).
	Options []SubsetOptionInput
}

// SubsetOptionInput is one selectable preset within a [SubsetDefInput].
type SubsetOptionInput struct {
	ID       int
	LabelKey string
	Values   map[string]any
}

// Generate produces a [Schema]. When [GenerateInput.Grouper] is set,
// the parameters are sorted into curated sections; otherwise a single
// "default" section holds them all (alphabetically). Mirrors the
// happy path of the reference config panel's FormSchemaGenerator
// minus the easymode enrichment, which is layered on by callers that
// supply visibility / preset / subset metadata.
//
// Output ordering is alphabetical by parameter ID within each
// section so two consecutive generations produce byte-identical JSON
// for diffing.
func Generate(in GenerateInput) Schema {
	// Resolve channel type for HmIP devices so translation lookups use
	// the correct _HMIP-suffixed key when applicable. Mirrors the reference
	// config panel's FormSchemaGenerator.generate channel-type remap.
	if in.IsHmIP && in.ChannelTypeResolver != nil {
		in.ChannelType = in.ChannelTypeResolver.ResolveChannelType(in.ChannelType, true)
	}

	// Apply require_translation filter: exclude parameters for which no
	// CCU translation is available when RequireTranslation is true and a
	// LabelResolver is present. Mirrors form_schema.py:210-222.
	keys := make([]string, 0, len(in.Descriptions))
	for k := range in.Descriptions {
		if in.RequireTranslation && in.LabelResolver != nil {
			if !in.LabelResolver.HasTranslation(k, in.ChannelType) {
				continue
			}
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)

	built := make(map[string]FormParameter, len(keys))
	writable := 0
	for _, k := range keys {
		param := buildFormParameter(k, in.Descriptions[k], in)
		if param.Writable {
			writable++
		}
		built[k] = param
	}

	var sections []FormSection
	if in.Grouper != nil {
		var groups []ParameterGroup
		if len(in.GroupChannel.Groups) > 0 || len(in.GroupChannel.ParameterOrder) > 0 {
			groups = in.Grouper.GroupForChannel(keys, in.GroupChannel)
		} else {
			groups = in.Grouper.Group(keys)
		}
		sections = make([]FormSection, 0, len(groups))
		for _, g := range groups {
			ps := make([]FormParameter, 0, len(g.Parameters))
			for _, name := range g.Parameters {
				ps = append(ps, built[name])
			}
			sections = append(sections, FormSection{ID: g.ID, Title: g.Title, Parameters: ps})
		}
	} else {
		params := make([]FormParameter, 0, len(keys))
		for _, k := range keys {
			params = append(params, built[k])
		}
		title := "Settings"
		if in.LabelResolver != nil {
			title = in.LabelResolver.Resolve("SETTINGS", in.ChannelType)
		}
		sections = []FormSection{{ID: "default", Title: title, Parameters: params}}
	}

	subsetGroups := buildSubsetGroups(in.SubsetDefs, in.CurrentValues)

	return Schema{
		ChannelAddress:     in.ChannelAddress,
		ChannelType:        in.ChannelType,
		ModelDescription:   in.ModelDescription,
		DeviceIcon:         in.DeviceIcon,
		Sections:           sections,
		TotalParameters:    len(keys),
		WritableParameters: writable,
		SubsetGroups:       subsetGroups,
	}
}

func buildFormParameter(id string, desc hmproto.ParameterData, in GenerateInput) FormParameter {
	var label string
	if in.LabelResolver != nil {
		label = in.LabelResolver.Resolve(id, in.ChannelType)
	} else {
		label = Humanize(id)
	}

	current := in.CurrentValues[id]
	def := decodeRaw(desc.Default)
	modified := false
	if in.Initialised == nil || in.Initialised[id] {
		modified = current != nil && def != nil && current != def
	}

	var help string
	if in.LabelResolver != nil {
		help = in.LabelResolver.HelpText(id)
	}

	// Build option_labels for VALUE_LIST parameters when a translator is
	// available. Mirrors form_schema.py:266-292.
	var optionLabels map[string]string
	if len(desc.ValueList) > 0 && in.OptionValueTranslator != nil {
		locale := DefaultLocale
		if in.LabelResolver != nil {
			locale = in.LabelResolver.Locale()
		}
		resolved := make(map[string]string, len(desc.ValueList))
		for i, value := range desc.ValueList {
			label := in.OptionValueTranslator(id, value, in.ChannelType, locale)
			if label == "" {
				// Index-based fallback: try numeric position as value string.
				label = in.OptionValueTranslator(id, fmt.Sprintf("%d", i), in.ChannelType, locale)
			}
			if label == "" {
				label = Humanize(value)
			}
			resolved[value] = label
		}
		optionLabels = resolved
	}

	p := FormParameter{
		ID:           id,
		Label:        label,
		Description:  help,
		Type:         string(desc.Type),
		Widget:       string(DetermineWidget(desc)),
		Min:          decodeRaw(desc.Min),
		Max:          decodeRaw(desc.Max),
		Step:         ParameterStep(desc),
		Default:      def,
		Unit:         desc.Unit,
		CurrentValue: current,
		Writable:     desc.IsWritable(),
		Modified:     modified,
		Operations:   int(desc.Operations),
		Options:      append([]string(nil), desc.ValueList...),
		OptionLabels: optionLabels,
	}

	if in.EnrichLinkMetadata {
		enrichLinkMetadata(&p, id, desc, in.LinkMetadataLocale)
	}

	return p
}

// enrichLinkMetadata classifies id and attaches the link-parameter
// metadata fields to p. Mirrors form_schema.py:318-335.
func enrichLinkMetadata(p *FormParameter, id string, desc hmproto.ParameterData, locale string) {
	if locale == "" {
		locale = DefaultLocale
	}
	meta := ClassifyLinkParameter(id)
	p.KeypressGroup = string(meta.KeypressGroup)
	p.Category = string(meta.Category)
	p.DisplayAsPercent = meta.DisplayAsPercent
	p.HiddenByDefault = meta.HiddenByDefault
	p.TimePairID = meta.TimePairID
	if meta.TimeSelectorType != TimeSelectorUnknown {
		p.TimeSelectorType = string(meta.TimeSelectorType)
		p.TimePresets = GetTimePresets(meta.TimeSelectorType, locale)
	}
	// LEVEL: has_last_value only when max > 1.0 (matches Python logic).
	if meta.DisplayAsPercent {
		maxRaw := decodeRaw(desc.Max)
		if maxF, ok := toFloat64(maxRaw); ok && maxF > 1.0 {
			p.HasLastValue = true
		} else {
			p.HasLastValue = meta.HasLastValue
		}
	} else {
		p.HasLastValue = meta.HasLastValue
	}
}

// toFloat64 converts an any value to float64 for numeric comparisons.
func toFloat64(v any) (float64, bool) {
	switch x := v.(type) {
	case float64:
		return x, true
	case float32:
		return float64(x), true
	case int:
		return float64(x), true
	case int64:
		return float64(x), true
	case int32:
		return float64(x), true
	}
	return 0, false
}

// buildSubsetGroups translates the flat list of [SubsetDefInput]
// entries into [SubsetGroup] objects, grouping options that share the
// same member_params together. Mirrors the reference config panel's
// FormSchemaGenerator._build_subset_groups.
func buildSubsetGroups(defs []SubsetDefInput, currentValues map[string]any) []SubsetGroup {
	if len(defs) == 0 {
		return nil
	}
	var groups []SubsetGroup

	for _, def := range defs {
		// Collect all options from this def (legacy single-value and
		// multi-option forms).
		var options []SubsetOption
		if len(def.Options) > 0 {
			for _, opt := range def.Options {
				options = append(options, SubsetOption{
					ID:     opt.ID,
					Label:  opt.LabelKey,
					Values: copyAnyMap(opt.Values),
				})
			}
		} else if def.Values != nil {
			options = []SubsetOption{{
				ID:     def.ID,
				Label:  def.NameKey,
				Values: copyAnyMap(def.Values),
			}}
		}
		if len(options) == 0 {
			continue
		}

		// Determine whether current values match this subset.
		var currentOptionID *int
		for _, opt := range options {
			if allMatch(opt.Values, currentValues) {
				id := opt.ID
				currentOptionID = &id
				break
			}
		}

		// Check if a group for these member_params already exists.
		memberSet := toStringSet(def.MemberParams)
		existing := findGroupByMembers(groups, memberSet)
		if existing != nil {
			existing.Options = append(existing.Options, options...)
			if currentOptionID != nil && existing.CurrentOptionID == nil {
				existing.CurrentOptionID = currentOptionID
			}
		} else {
			groups = append(groups, SubsetGroup{
				ID:              "subset_" + firstOrEmpty(def.MemberParams),
				Label:           def.NameKey,
				MemberParams:    append([]string(nil), def.MemberParams...),
				Options:         options,
				CurrentOptionID: currentOptionID,
			})
		}
	}
	if len(groups) == 0 {
		return nil
	}
	return groups
}

func allMatch(want, current map[string]any) bool {
	for k, v := range want {
		got, ok := current[k]
		if !ok {
			return false
		}
		if got != v {
			// Try numeric loose comparison.
			gf, gok := toFloat64(got)
			vf, vok := toFloat64(v)
			if !gok || !vok || gf != vf {
				return false
			}
		}
	}
	return true
}

func toStringSet(ss []string) map[string]struct{} {
	m := make(map[string]struct{}, len(ss))
	for _, s := range ss {
		m[s] = struct{}{}
	}
	return m
}

func findGroupByMembers(groups []SubsetGroup, members map[string]struct{}) *SubsetGroup {
	for i := range groups {
		g := &groups[i]
		if len(g.MemberParams) != len(members) {
			continue
		}
		match := true
		for _, p := range g.MemberParams {
			if _, ok := members[p]; !ok {
				match = false
				break
			}
		}
		if match {
			return g
		}
	}
	return nil
}

func copyAnyMap(in map[string]any) map[string]any {
	if in == nil {
		return nil
	}
	out := make(map[string]any, len(in))
	maps.Copy(out, in)
	return out
}

func firstOrEmpty(ss []string) string {
	if len(ss) == 0 {
		return ""
	}
	return ss[0]
}

// decodeRaw returns the JSON-decoded value of raw, or nil when raw is
// empty or unparseable. Used to project descriptor fields like
// Min/Max/Default into the wire shape the schema expects.
func decodeRaw(raw json.RawMessage) any {
	if len(raw) == 0 {
		return nil
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return nil
	}
	return v
}
