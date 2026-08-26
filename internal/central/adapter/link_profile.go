// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package adapter

import (
	"encoding/json"
	"math"
	"strconv"
	"strings"
)

// ProfileDoc is the outer shape of an
// archive: a map from sender channel type to the profile set that
// applies when that sender is linked to the receiver. Only the keys
// used by the matcher and the filter are unmarshalled — everything
// else stays as json.RawMessage so the SPA receives the full payload
// verbatim.
type profileDoc map[string]senderProfileSet

type senderProfileSet struct {
	Profiles []profileDef `json:"profiles"`
}

type profileDef struct {
	ID     int                               `json:"id"`
	Name   map[string]string                 `json:"name"`
	Desc   map[string]string                 `json:"description,omitempty"`
	Params map[string]profileParamConstraint `json:"params,omitempty"`
}

// ProfileParamConstraint mirrors the
// `Value` / `Default` / `MinValue` / `MaxValue` are json.RawMessage
// because the archives sometimes ship integers, sometimes floats, and
// the matcher normalises them to float64 itself.
type profileParamConstraint struct {
	ConstraintType string            `json:"constraint_type"`
	Value          json.RawMessage   `json:"value,omitempty"`
	Values         []json.RawMessage `json:"values,omitempty"`
	Default        json.RawMessage   `json:"default,omitempty"`
	MinValue       json.RawMessage   `json:"min_value,omitempty"`
	MaxValue       json.RawMessage   `json:"max_value,omitempty"`
}

// senderTypeAliases maps CCU-reported sender channel types that do
// Not appear verbatim in the
// Semantic equivalent that does. itself has no
// dedicated alias table here — the archives were extracted from the
// pre-HmIP TCL definitions, while modern HmIP devices expose
// "*_VIRTUAL_TRANSCEIVER" channel types plus a few renamed families
// (MOTIONDETECTOR ↔ PRESENCEDETECTOR). Without this mapping the link
// editor shows all sender variants (no filter) and the active-
// profile detector returns 0 for every link originating from a
// HmIP-RF device.
//
// The lookup chain resolveSenderType() walks: exact → alias →
// strip `_VIRTUAL_` token → alias of stripped form.
var senderTypeAliases = map[string]string{
	"MOTIONDETECTOR_TRANSCEIVER":         "PRESENCEDETECTOR_TRANSCEIVER",
	"MOTIONDETECTOR_VIRTUAL_TRANSCEIVER": "PRESENCEDETECTOR_TRANSCEIVER",
	"SHUTTER_CONTACT_TRANSCEIVER":        "SHUTTER_CONTACT",
}

// resolveSenderType picks the first sender-type key that exists in
// `doc` from the lookup chain applied to `raw`. Returns "" when no
// variant resolves — i.e. the archive genuinely has no profile for
// this pairing.
func resolveSenderType(doc profileDoc, raw string) string {
	if raw == "" {
		return ""
	}
	candidates := []string{raw}
	if alias, ok := senderTypeAliases[raw]; ok {
		candidates = append(candidates, alias)
	}
	// Generic _VIRTUAL_ → "" normalisation (KEY_VIRTUAL_TRANSCEIVER
	// → KEY_TRANSCEIVER). HmIP exposes virtual-channel forms that
	// the archives never picked up.
	if stripped := strings.ReplaceAll(raw, "_VIRTUAL_", "_"); stripped != raw {
		candidates = append(candidates, stripped)
		if alias, ok := senderTypeAliases[stripped]; ok {
			candidates = append(candidates, alias)
		}
	}
	for _, c := range candidates {
		if _, ok := doc[c]; ok {
			return c
		}
	}
	return ""
}

// filterProfileDocBySender extracts only the sub-document for the
// given sender type. Returns the narrowed raw JSON, the parsed
// profile list, and the **resolved** sender key (the key that
// actually exists in the archive — may differ from the caller's
// senderType when the resolution chain aliased or normalised it).
// Empty result when no alias can be resolved against the archive.
func filterProfileDocBySender(raw json.RawMessage, senderType string) (json.RawMessage, []profileDef, string, error) {
	if len(raw) == 0 || senderType == "" {
		return nil, nil, "", nil
	}
	var doc profileDoc
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, nil, "", err
	}
	key := resolveSenderType(doc, senderType)
	if key == "" {
		return nil, nil, "", nil
	}
	set := doc[key]
	out, err := json.Marshal(profileDoc{key: set})
	if err != nil {
		return nil, nil, "", err
	}
	return out, set.Profiles, key, nil
}

// matchActiveProfile returns the id of the most specific profile whose
// constraints are satisfied by `current`.
//
// - Profiles with id == 0 or no params are skipped (they are the "Expert"
// fallback). - A profile matches when every parameter present in `current`
// that also appears in the profile satisfies its constraint (fixed / list /
// range). Missing keys in `current` are ignored. - Specificity score =
// fixed_count - loose_count*100; the highest- scoring match wins. Returns 0
// when nothing matches.
func matchActiveProfile(profiles []profileDef, current map[string]any) int {
	bestID := 0
	bestScore := math.Inf(-1)
	for _, p := range profiles {
		if p.ID == 0 || len(p.Params) == 0 {
			continue
		}
		if !profileMatches(p.Params, current) {
			continue
		}
		if score := profileSpecificity(p.Params); score > bestScore {
			bestScore = score
			bestID = p.ID
		}
	}
	return bestID
}

// profileMatches reports whether every constraint in `params` is
// satisfied by the corresponding value in `current`. Missing keys in
// `current` are ignored (no decision either way), mirroring the
// Python reference.
func profileMatches(params map[string]profileParamConstraint, current map[string]any) bool {
	for name := range params {
		c := params[name]
		raw, ok := current[name]
		if !ok {
			continue
		}
		num, ok := toFloat(raw)
		if !ok {
			return false
		}
		switch c.ConstraintType {
		case "fixed":
			v, ok := decodeFloat(c.Value)
			if ok && num != v {
				return false
			}
		case "list":
			if len(c.Values) == 0 {
				continue
			}
			found := false
			for _, entry := range c.Values {
				if v, ok := decodeFloat(entry); ok && v == num {
					found = true
					break
				}
			}
			if !found {
				return false
			}
		case "range":
			lo, hasLo := decodeFloat(c.MinValue)
			hi, hasHi := decodeFloat(c.MaxValue)
			if hasLo && hasHi && (num < lo || num > hi) {
				return false
			}
		}
	}
	return true
}

// profileSpecificity scores a profile: every fixed constraint gains
// one point, every non-fixed (list / range) subtracts 100. All-fixed
// profiles therefore always beat profiles with loose constraints,
// regardless of total parameter count.
func profileSpecificity(params map[string]profileParamConstraint) float64 {
	fixed, loose := 0, 0
	for name := range params {
		if params[name].ConstraintType == "fixed" {
			fixed++
		} else {
			loose++
		}
	}
	return float64(fixed) - float64(loose)*100
}

// toFloat narrows whatever the CCU returned (int / float / string)
// to a float64 for numeric comparison. Returns ok=false when the
// value is not numeric.
func toFloat(v any) (float64, bool) {
	switch t := v.(type) {
	case float64:
		return t, true
	case float32:
		return float64(t), true
	case int:
		return float64(t), true
	case int32:
		return float64(t), true
	case int64:
		return float64(t), true
	case bool:
		if t {
			return 1, true
		}
		return 0, true
	case string:
		if f, err := strconv.ParseFloat(t, 64); err == nil {
			return f, true
		}
	case json.Number:
		if f, err := t.Float64(); err == nil {
			return f, true
		}
	}
	return 0, false
}

// decodeFloat unmarshals a raw JSON number. Returns ok=false on
// empty / non-numeric payloads.
func decodeFloat(raw json.RawMessage) (float64, bool) {
	if len(raw) == 0 {
		return 0, false
	}
	var f float64
	if err := json.Unmarshal(raw, &f); err != nil {
		return 0, false
	}
	return f, true
}

// peerChannelType resolves the sender channel's type for a given
// peer address. Walks the registry so cross-device peers resolve
// too. Returns "" when the peer is unknown.
//
// Unlike the receiver, the sender type is used verbatim as a map
// Key inside the profile archive ( only aliases
// the receiver via _receiver_type_aliases.json, never the sender).
// Applying aliases here would break the filter because the keys
// inside the JSON stay at their raw CCU names.
func (a *UISchemaAdapter) peerChannelType(peerAddress string) string {
	if a.registry == nil || peerAddress == "" {
		return ""
	}
	devAddr := deviceAddressOf(peerAddress)
	for _, u := range a.registry.List() {
		dev, ok := u.ModelRegistry.Get(devAddr)
		if !ok {
			continue
		}
		ch := dev.Channel(peerAddress)
		if ch == nil {
			return ""
		}
		if ch.Type != "" {
			return ch.Type
		}
		// Only fall back to the device model when the channel itself
		// carries no type — this happens for bare placeholder channels
		// created via adapter stubs. Never alias-resolve here.
		return dev.Model
	}
	return ""
}
