// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/SukramJ/openccu-loom/internal/north/rest/problem"
)

// UISchemaService produces a renderable UI schema for one channel /
// paramset pair. The implementation lives in the central/adapter
// layer and composes data points, easymode metadata, translations,
// and the receiver profile catalogue into a single payload the SPA
// can render.
//
// The paramset argument selects between VALUES (runtime state),
// MASTER (channel configuration), and LINK (per-peer direct-link
// configuration). For LINK the peer argument is the peer channel
// address; otherwise it is ignored.
type UISchemaService interface {
	UISchema(ctx context.Context, opts UISchemaRequest) (*UISchema, error)
}

// UISchemaRequest aggregates every option a UI-schema lookup
// understands. Bundling them into a struct keeps the call site
// readable as the parameter list grows (currently: paramset, peer,
// locale, expert).
type UISchemaRequest struct {
	Address  string
	Channel  int
	Paramset string
	Peer     string
	Locale   string
	// Expert disables the easymode filter that hides untranslated
	// MASTER parameters. SPA users opt in via the "Expert"-Toggle in
	// the ChannelPanel; default is the curated, friendly view.
	Expert bool
}

// ErrUISchemaNotFound signals the device or channel cannot be located.
var ErrUISchemaNotFound = errors.New("ui-schema: channel not found")

// --- DTOs ---------------------------------------------------------

// UISchema is the root object returned by the endpoint.
type UISchema struct {
	Channel          UISchemaChannel           `json:"channel"`
	Groups           []UISchemaGroup           `json:"groups,omitempty"`
	ParameterOrder   []string                  `json:"parameter_order,omitempty"`
	Parameters       []UISchemaParameter       `json:"parameters"`
	Visibility       []UISchemaVisibility      `json:"visibility,omitempty"`
	CrossValidations []UISchemaCrossValidation `json:"cross_validations,omitempty"`
	Profile          *UISchemaProfile          `json:"profile,omitempty"`
	// SubsetGroups are easymode "scene"-style multi-parameter choices
	// (e.g. "Light: warm/cool/off") — picking one option patches all
	// member parameters at once. Surfaced separately so the SPA can
	// render the picker above the regular parameter grid.
	SubsetGroups []UISchemaSubsetGroup `json:"subset_groups,omitempty"`
	// ModelDescription is the localised human-readable device model name
	// (e.g. "Funk-Schaltsteckdose"). Empty when the device model cannot
	// be resolved from the translation catalogue.
	ModelDescription string `json:"model_description,omitempty"`
	// DeviceIcon is the icon identifier for the device type. The SPA maps
	// it onto an icon resource. Empty when no icon is registered.
	DeviceIcon string `json:"device_icon,omitempty"`
}

// UISchemaSubsetGroup wraps one [ccudata.SubsetDef] resolved to
// labelled options. The SPA renders it as a labelled dropdown that,
// when the user picks an option, emits a multi-parameter patch.
type UISchemaSubsetGroup struct {
	ID              string              `json:"id"`
	Label           string              `json:"label"`
	MemberParams    []string            `json:"member_params"`
	CurrentOptionID *int                `json:"current_option_id,omitempty"`
	Options         []UISchemaSubsetOpt `json:"options"`
}

// UISchemaSubsetOpt is one labelled value-set inside a subset group.
type UISchemaSubsetOpt struct {
	ID     int            `json:"id"`
	Label  string         `json:"label"`
	Values map[string]any `json:"values"`
}

// UISchemaChannel identifies the channel whose schema is being
// served.
type UISchemaChannel struct {
	Address  string `json:"address"`
	Number   int    `json:"number"`
	Type     string `json:"type"`
	Label    string `json:"label,omitempty"`
	Device   string `json:"device_address"`
	Paramset string `json:"paramset"`
}

// UISchemaGroup is one semantic parameter group (e.g. "timing") with
// its localised label.
type UISchemaGroup struct {
	ID         string   `json:"id"`
	Label      string   `json:"label"`
	Parameters []string `json:"parameters"`
}

// UISchemaParameter is the complete, renderable descriptor for a
// single parameter.
//
// The Link* fields are only populated for LINK-paramset renderings
// (see adapter.buildLinkSchema). They carry the classification
// Derives from the parameter name: keypress
// group (SHORT/LONG/COMMON), functional category, paired time-base
// linking, and preset pickers. The SPA uses them to render SHORT/
// LONG sub-sections, percent sliders, "hidden by default" toggles,
// and TIME_BASE/FACTOR preset dropdowns.
type UISchemaParameter struct {
	Name             string                   `json:"name"`
	Label            string                   `json:"label,omitempty"`
	Help             string                   `json:"help,omitempty"`
	Type             string                   `json:"type"`
	Unit             string                   `json:"unit,omitempty"`
	Min              json.RawMessage          `json:"min,omitempty"`
	Max              json.RawMessage          `json:"max,omitempty"`
	Default          json.RawMessage          `json:"default,omitempty"`
	ValueList        []UISchemaValueListEntry `json:"value_list,omitempty"`
	Operations       UISchemaParameterOps     `json:"operations"`
	Flags            UISchemaParameterFlags   `json:"flags"`
	Control          string                   `json:"control,omitempty"`
	Value            any                      `json:"value,omitempty"`
	Observed         bool                     `json:"observed"`
	ModifiedAt       string                   `json:"modified_at,omitempty"`
	GroupID          string                   `json:"group_id,omitempty"`
	Preset           string                   `json:"preset,omitempty"`
	Category         string                   `json:"category,omitempty"`
	KeypressGroup    string                   `json:"keypress_group,omitempty"`
	DisplayAsPercent bool                     `json:"display_as_percent,omitempty"`
	HasLastValue     bool                     `json:"has_last_value,omitempty"`
	HiddenByDefault  bool                     `json:"hidden_by_default,omitempty"`
	TimePairID       string                   `json:"time_pair_id,omitempty"`
	TimeSelectorType string                   `json:"time_selector_type,omitempty"`
	TimePresets      []UISchemaTimePreset     `json:"time_presets,omitempty"`
	// Presets are EasyMode value chips for ENUM/INTEGER/FLOAT parameters. Each
	// entry carries a localised label and the value to apply when the user
	// clicks the chip.
	Presets []UISchemaPreset `json:"presets,omitempty"`
	// AllowCustomValue is true when this parameter accepts values outside the
	// preset list (UC5: the user can type an arbitrary value in addition to the
	// preset chips). False when the preset list is exhaustive.
	AllowCustomValue bool `json:"allow_custom_value,omitempty"`
	// SubsetGroupID names the subset group (UC6) this parameter belongs to,
	// if any. The SPA hides member parameters behind the group's picker widget
	// and writes the group's selected option instead of the raw value.
	SubsetGroupID string `json:"subset_group_id,omitempty"`
}

// UISchemaPreset is one EasyMode preset chip.
type UISchemaPreset struct {
	Label string          `json:"label"`
	Value json.RawMessage `json:"value"`
}

// UISchemaTimePreset is one preset entry for a TIME_BASE/TIME_FACTOR
// picker. The SPA binds the paired parameters to (Base, Factor).
type UISchemaTimePreset struct {
	Base   int    `json:"base"`
	Factor int    `json:"factor"`
	Label  string `json:"label"`
}

// UISchemaValueListEntry is one entry of an enum-style value list
// with its localised label.
type UISchemaValueListEntry struct {
	Value int    `json:"value"`
	Key   string `json:"key"`
	Label string `json:"label,omitempty"`
}

// UISchemaParameterOps mirrors the OCCU OPERATIONS bitmask.
type UISchemaParameterOps struct {
	Read  bool `json:"read"`
	Write bool `json:"write"`
	Event bool `json:"event"`
}

// UISchemaParameterFlags mirrors the OCCU FLAGS bitmask.
type UISchemaParameterFlags struct {
	Visible  bool `json:"visible"`
	Internal bool `json:"internal"`
	Service  bool `json:"service"`
}

// UISchemaVisibility encodes one conditional-visibility rule.
// When Trigger equals TriggerValue, Show-listed parameters are visible
// and Hide-listed parameters are invisible.
type UISchemaVisibility struct {
	Show         []string `json:"show"`
	Hide         []string `json:"hide,omitempty"`
	Trigger      string   `json:"trigger"`
	TriggerValue any      `json:"trigger_value"`
}

// UISchemaCrossValidation is one cross-parameter rule already
// resolved to a localised error message.
//
// Param is set for single-parameter rules (e.g. "gte" / "lte" with one
// reference). MinParam and MaxParam are set for "between" rules.
// ParamA and ParamB carry the two-parameter form. Only the fields
// relevant to the specific Rule value are non-empty.
type UISchemaCrossValidation struct {
	ID              string   `json:"id"`
	Rule            string   `json:"rule"`
	ParamA          string   `json:"param_a,omitempty"`
	ParamB          string   `json:"param_b,omitempty"`
	Param           string   `json:"param,omitempty"`
	MinParam        string   `json:"min_param,omitempty"`
	MaxParam        string   `json:"max_param,omitempty"`
	AppliesToParams []string `json:"applies_to_params"`
	Error           string   `json:"error,omitempty"`
}

// UISchemaProfile wraps the receiver-profile catalogue for the
// channel's type (after alias resolution). The Raw payload is the
// JSON from the
// structure without a lossy Go mirror.
//
// For LINK paramsets the adapter pre-filters Raw to only the sender-
// channel-type subtree (SenderType) and pre-computes the best-match
// ActiveProfileID from the current values, mirroring
// 's match_active_profile so the SPA can pre-
// select it. Zero means "Expert" / no preset matches.
type UISchemaProfile struct {
	ReceiverType    string          `json:"receiver_type"`
	SenderType      string          `json:"sender_type,omitempty"`
	ActiveProfileID int             `json:"active_profile_id,omitempty"`
	Raw             json.RawMessage `json:"raw,omitempty"`
}

// --- HTTP glue ----------------------------------------------------

// UISchemaHandler serves GET /devices/{addr}/channels/{no}/ui-schema.
func UISchemaHandler(svc UISchemaService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			problem.Write(w, http.StatusServiceUnavailable,
				problem.New(problem.TypeServiceUnready, r, "UI schema service unavailable", ""))
			return
		}
		addr := chi.URLParam(r, "addr")
		noStr := chi.URLParam(r, "no")
		no, err := strconv.Atoi(noStr)
		if err != nil {
			problem.Write(w, http.StatusBadRequest,
				problem.New(problem.TypeBadRequest, r, "Invalid channel number", noStr))
			return
		}
		locale := r.URL.Query().Get("locale")
		if locale == "" {
			locale = "en"
		}
		paramset := r.URL.Query().Get("paramset")
		if paramset == "" {
			paramset = "VALUES"
		}
		peer := r.URL.Query().Get("peer")
		expert := r.URL.Query().Get("expert") == "true" ||
			r.URL.Query().Get("expert") == "1"
		schema, err := svc.UISchema(r.Context(), UISchemaRequest{
			Address:  addr,
			Channel:  no,
			Paramset: paramset,
			Peer:     peer,
			Locale:   locale,
			Expert:   expert,
		})
		if err != nil {
			if errors.Is(err, ErrUISchemaNotFound) {
				problem.Write(w, http.StatusNotFound,
					problem.New(problem.TypeNotFound, r, "Channel not found", addr+"/"+noStr))
				return
			}
			problem.Write(w, http.StatusInternalServerError,
				problem.New(problem.TypeInternal, r, "UI schema failed", err.Error()))
			return
		}
		JSON(w, http.StatusOK, schema)
	}
}
