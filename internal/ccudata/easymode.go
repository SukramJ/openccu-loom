// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package ccudata

import (
	"compress/gzip"
	"encoding/json"
	"errors"
	"fmt"
	"os"
)

// Easymode holds the parsed easymode_extract archive.
//
// The shape mirrors: channel
// metadata keyed by channel type, option presets keyed by preset id,
// and cross-validation rules for conditional visibility.
type Easymode struct {
	ChannelMetadata  map[string]ChannelMetadata `json:"channel_metadata"`
	OptionPresets    map[string]OptionPreset    `json:"option_presets"`
	CrossValidations CrossValidationSet         `json:"cross_validations"`
}

// CrossValidationSet wraps the set of cross-parameter rules. The OCCU
// extractor emits it as a JSON object with a single `rules` field so
// it can carry future top-level metadata (version, generator hash)
// alongside the actual rule list.
type CrossValidationSet struct {
	Rules []CrossValidation `json:"rules"`
}

// ChannelMetadata bundles sender-type-scoped groupings + parameter
// order for one channel type.
type ChannelMetadata struct {
	ChannelType   string                        `json:"channel_type"`
	SenderTypes   map[string]SenderTypeMetadata `json:"sender_types"`
	MasterProfile *MasterProfile                `json:"master_profile,omitempty"`
	Extras        map[string]json.RawMessage    `json:"-"`
}

// SenderTypeMetadata covers the per-sender-type projection of a
// channel: parameter groups + presets + ordering. Field shape mirrors
// the OCCU easymode extractor output.
type SenderTypeMetadata struct {
	ParameterOrder        []string                `json:"parameter_order,omitempty"`
	ParameterGroups       []ParameterGroupDef     `json:"parameter_groups,omitempty"`
	ConditionalVisibility []ConditionalVisibility `json:"conditional_visibility,omitempty"`
	// OptionPresets maps a parameter name onto the id of a preset in
	// [Easymode.OptionPresets]. Some firmwares use inline preset ids
	// prefixed with `_INLINE_...`; those are present in the same
	// OptionPresets map keyed by that inline id.
	OptionPresets  map[string]string `json:"option_presets,omitempty"`
	SubsetGroupIDs map[string]string `json:"subset_group_ids,omitempty"`
	// Subsets are easymode "scene"-style multi-parameter selections — one user
	// choice patches several parameters at once.
	Subsets []SubsetDef `json:"subsets,omitempty"`
	// CrossValidationRuleIDs lists the global rule IDs (from
	// [Easymode.CrossValidations]) that apply to this sender type. When
	// empty, no cross-validation rules are emitted for the schema — only
	// the IDs listed here are relevant for the channel/paramset combination.
	CrossValidationRuleIDs []string `json:"cross_validation_rule_ids,omitempty"`
}

// SubsetDef bundles several parameters into one combined selector.
// Each subset option carries a fixed value per member parameter; the
// SPA picker writes them all in a single patch.
type SubsetDef struct {
	ID           int      `json:"id"`
	NameKey      string   `json:"name_key"`
	MemberParams []string `json:"member_params"`
	// Values is the master option (legacy single-option form). Newer
	// extracts populate Options[] instead.
	Values      map[string]any `json:"values,omitempty"`
	OptionValue any            `json:"option_value,omitempty"`
	Options     []SubsetOption `json:"options,omitempty"`
}

// SubsetOption is one choice in a SubsetDef. `Values` maps each
// member parameter to the value applied when this option is selected.
type SubsetOption struct {
	ID       int            `json:"id"`
	LabelKey string         `json:"label_key"`
	Values   map[string]any `json:"values"`
}

// ConditionalVisibility makes one or more parameters conditionally
// rendered. When Trigger equals TriggerValue, Show-listed parameters
// become visible and Hide-listed parameters become invisible.
// OCCU allows TriggerValue to be a scalar or a list.
type ConditionalVisibility struct {
	Show         []string `json:"show"`
	Hide         []string `json:"hide,omitempty"`
	Trigger      string   `json:"trigger"`
	TriggerValue any      `json:"trigger_value"`
}

// ParameterGroupDef describes one semantic parameter group (e.g.
// "Auto-Mode" on a thermostat).
//
// Label carries an optional inline locale→label map from the embedded
// easymode archive. It is a Stage-2/3 fallback for the group title:
// when LabelKey resolves to nothing via the UILabel translation table,
// Label[locale] (then Label["en"]) is tried before the generic
// "Other Settings" fallback.
type ParameterGroupDef struct {
	ID         string            `json:"id"`
	LabelKey   string            `json:"label_key"`
	Label      map[string]string `json:"label,omitempty"`
	Parameters []string          `json:"parameters"`
}

// OptionPreset is a named, reusable option list.
// AllowCustom mirrors the upstream allow_custom flag: when true the SPA
// renders a free-text input in addition to the preset chips, letting the
// user enter values outside the preset list.
type OptionPreset struct {
	ID          string            `json:"id"`
	Options     []OptionPresetVal `json:"presets"`
	AllowCustom bool              `json:"allow_custom,omitempty"`
}

// OptionPresetVal is one entry of an [OptionPreset].
//
// LabelKey carries the i18n translation key from the easymode archive.
// When non-empty it is resolved via the UILabel lookup chain before
// falling back to the inline Label string (locale → "en" → Label).
type OptionPresetVal struct {
	Value    any    `json:"value"`
	Label    string `json:"label"`
	LabelKey string `json:"label_key,omitempty"`
}

// MasterProfile bundles MASTER-paramset profile definitions for a
// channel.
type MasterProfile struct {
	Profiles []MasterProfileDef `json:"profiles"`
}

// MasterProfileDef is one named MASTER-paramset profile with its
// constraint set.
type MasterProfileDef struct {
	ID          string                   `json:"id"`
	LabelKey    string                   `json:"label_key"`
	Constraints []ProfileParamConstraint `json:"constraints"`
}

// ProfileParamConstraint describes one parameter override inside a
// profile.
type ProfileParamConstraint struct {
	Parameter string `json:"parameter"`
	Value     any    `json:"value"`
}

// CrossValidation is a cross-parameter rule ("parameter A must be
// >= parameter B" etc.). The field set mirrors the OCCU extractor's
// output: a stable rule id, the pair of parameters the rule compares,
// the scope of parameters the rule affects, the comparator, and a
// translation key for the error message.
//
// Param is used for single-parameter rules (e.g. "gte" / "lte" with one
// reference). MinParam and MaxParam are used for "between" rules that
// express a range across two reference parameters.
type CrossValidation struct {
	ID              string   `json:"id"`
	AppliesToParams []string `json:"applies_to_params"`
	ErrorKey        string   `json:"error_key"`
	ParamA          string   `json:"param_a,omitempty"`
	ParamB          string   `json:"param_b,omitempty"`
	Param           string   `json:"param,omitempty"`
	MinParam        string   `json:"min_param,omitempty"`
	MaxParam        string   `json:"max_param,omitempty"`
	Rule            string   `json:"rule"`
}

// ErrNoEasymode is returned when the archive path is empty.
var ErrNoEasymode = errors.New("ccudata: easymode archive path is empty")

// LoadEasymode reads the gzipped JSON archive at path.
//
// loom:reachable:reason="called by ccudata loader during daemon boot to populate easymode registry"
func LoadEasymode(path string) (*Easymode, error) {
	if path == "" {
		return nil, ErrNoEasymode
	}
	f, err := os.Open(path) //nolint:gosec // operator-supplied path; see #20
	if err != nil {
		return nil, fmt.Errorf("ccudata: open %s: %w", path, err)
	}
	defer f.Close() //nolint:errcheck // read-only stream

	gz, err := gzip.NewReader(f)
	if err != nil {
		return nil, fmt.Errorf("ccudata: gzip: %w", err)
	}
	defer gz.Close() //nolint:errcheck // read-only stream

	out := &Easymode{}
	if err := json.NewDecoder(gz).Decode(out); err != nil {
		return nil, fmt.Errorf("ccudata: decode: %w", err)
	}
	materializeSubsetGroupIDs(out)
	return out, nil
}

// materializeSubsetGroupIDs derives SubsetGroupIDs from Subsets for
// every SenderTypeMetadata that carries subset definitions but has no
// pre-computed subset_group_ids in the archive. Each member parameter is
// mapped to "subset_<first member parameter>".
//
// That spelling is not free: it is the id the UI-schema builder gives the
// group it derives from the same SubsetDef, and the field exists so a
// consumer can match a parameter to the group that owns it. An id built
// from anything else — the SubsetDef's own integer identifier, say — names
// no group in the payload it ships with.
func materializeSubsetGroupIDs(e *Easymode) {
	for chKey, chMeta := range e.ChannelMetadata {
		changed := false
		newSTs := make(map[string]SenderTypeMetadata, len(chMeta.SenderTypes))
		for stKey, st := range chMeta.SenderTypes { //nolint:gocritic // value copy is intentional: st is modified and written back to the map
			if len(st.Subsets) > 0 && len(st.SubsetGroupIDs) == 0 {
				ids := make(map[string]string, 4*len(st.Subsets))
				for _, sub := range st.Subsets {
					if len(sub.MemberParams) == 0 {
						continue
					}
					groupID := "subset_" + sub.MemberParams[0]
					for _, param := range sub.MemberParams {
						ids[param] = groupID
					}
				}
				st.SubsetGroupIDs = ids
				changed = true
			}
			newSTs[stKey] = st
		}
		if changed {
			chMeta.SenderTypes = newSTs
			e.ChannelMetadata[chKey] = chMeta
		}
	}
}

// EmptyEasymode returns an Easymode with all fields non-nil.
func EmptyEasymode() *Easymode {
	return &Easymode{
		ChannelMetadata: map[string]ChannelMetadata{},
		OptionPresets:   map[string]OptionPreset{},
	}
}

// Channel returns the metadata for the given channel type; callers
// get a zero-value [ChannelMetadata] on miss so they can keep going
// without nil-checks.
func (e *Easymode) Channel(channelType string) ChannelMetadata {
	return e.ChannelMetadata[channelType]
}

// Preset returns an option preset by id.
func (e *Easymode) Preset(id string) (OptionPreset, bool) {
	p, ok := e.OptionPresets[id]
	return p, ok
}
