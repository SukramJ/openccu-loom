// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package configui

// FormParameter is one configurable parameter as the form-schema generator
// emits it.
type FormParameter struct {
	ID          string `json:"id"`
	Label       string `json:"label"`
	Description string `json:"description,omitempty"`
	Type        string `json:"type"`
	Widget      string `json:"widget"`

	Min     any    `json:"min,omitempty"`
	Max     any    `json:"max,omitempty"`
	Step    any    `json:"step,omitempty"`
	Unit    string `json:"unit,omitempty"`
	Default any    `json:"default,omitempty"`

	CurrentValue any  `json:"current_value,omitempty"`
	Writable     bool `json:"writable"`
	Modified     bool `json:"modified,omitempty"`
	Operations   int  `json:"operations,omitempty"`

	Options      []string          `json:"options,omitempty"`
	OptionLabels map[string]string `json:"option_labels,omitempty"`

	// Link-parameter metadata (LINK paramset only).
	KeypressGroup    string       `json:"keypress_group,omitempty"`
	Category         string       `json:"category,omitempty"`
	DisplayAsPercent bool         `json:"display_as_percent,omitempty"`
	HasLastValue     bool         `json:"has_last_value,omitempty"`
	HiddenByDefault  bool         `json:"hidden_by_default,omitempty"`
	TimePairID       string       `json:"time_pair_id,omitempty"`
	TimeSelectorType string       `json:"time_selector_type,omitempty"`
	TimePresets      []TimePreset `json:"time_presets,omitempty"`

	// Easymode metadata.
	VisibleWhen      map[string]any   `json:"visible_when,omitempty"`
	Presets          []map[string]any `json:"presets,omitempty"`
	AllowCustomValue bool             `json:"allow_custom_value,omitempty"`
	SubsetGroupID    string           `json:"subset_group_id,omitempty"`
}

// TimePreset is one suggestion offered to the operator for a time-
// valued LINK parameter. `Value` carries the raw integer (seconds /
// duration tag); `Label` is the localised display string.
type TimePreset struct {
	Value any    `json:"value"`
	Label string `json:"label"`
}

// FormSection groups parameters into a logical block (e.g.
// "Temperature settings", "Display").
type FormSection struct {
	ID         string          `json:"id"`
	Title      string          `json:"title"`
	Parameters []FormParameter `json:"parameters"`
}

// CrossValidationConstraint expresses a relation between several
// parameters that must hold for the form to be considered valid (e.g.
// "MAX_TEMPERATURE must be ≥ MIN_TEMPERATURE").
type CrossValidationConstraint struct {
	RuleID          string   `json:"rule_id"`
	Rule            string   `json:"rule"`
	AppliesToParams []string `json:"applies_to_params"`
	ErrorKey        string   `json:"error_key"`
	ParamA          string   `json:"param_a,omitempty"`
	ParamB          string   `json:"param_b,omitempty"`
	Param           string   `json:"param,omitempty"`
	MinParam        string   `json:"min_param,omitempty"`
	MaxParam        string   `json:"max_param,omitempty"`
}

// SubsetOption is one choice inside a [SubsetGroup].
type SubsetOption struct {
	ID     int            `json:"id"`
	Label  string         `json:"label"`
	Values map[string]any `json:"values"`
}

// SubsetGroup bundles parameters that the operator manipulates together via a
// single virtual selector.
type SubsetGroup struct {
	ID              string         `json:"id"`
	Label           string         `json:"label"`
	MemberParams    []string       `json:"member_params"`
	Options         []SubsetOption `json:"options"`
	CurrentOptionID *int           `json:"current_option_id,omitempty"`
}

// Schema is the full form schema for one channel paramset.
type Schema struct {
	ChannelAddress     string                      `json:"channel_address"`
	ChannelType        string                      `json:"channel_type"`
	ModelDescription   string                      `json:"model_description,omitempty"`
	ChannelTypeLabel   string                      `json:"channel_type_label,omitempty"`
	DeviceIcon         string                      `json:"device_icon,omitempty"`
	Sections           []FormSection               `json:"sections"`
	TotalParameters    int                         `json:"total_parameters"`
	WritableParameters int                         `json:"writable_parameters"`
	SubsetGroups       []SubsetGroup               `json:"subset_groups,omitempty"`
	CrossValidation    []CrossValidationConstraint `json:"cross_validation,omitempty"`
}
