// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package ccudata

import (
	"compress/gzip"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
)

// LocaleDE / LocaleEN are the locales
// from OCCU. Extending this list requires a matching entry in the
// Python extractor.
const (
	LocaleDE = "de"
	LocaleEN = "en"
)

// Translations holds the parsed translation_extract archive.
//
// Every locale-scoped map uses the CCU's raw key form:
// - channel_types_*: CHANNEL_TYPE (e.g. "SHUTTER_TRANSMITTER")
// - device_models_*: type_subtype (e.g. "263_130")
// - parameters_*: parameter name (e.g. "LEVEL")
// - parameter_values_*: "PARAMETER|VALUE" (e.g. "CONTROL_MODE|AUTO")
// - parameter_help_*: parameter name (e.g. "LEVEL")
// - ui_labels_*: ui key (stringtable id)
type Translations struct {
	ChannelTypes    map[string]map[string]string // locale → key → label
	DeviceModels    map[string]map[string]string
	Parameters      map[string]map[string]string
	ParameterValues map[string]map[string]string
	ParameterHelp   map[string]map[string]string
	UILabels        map[string]map[string]string
	DeviceIcons     map[string]string // locale-independent: type_subtype → icon file
	// valueIndices is a lazy-built reverse index: locale → value_lower → shortest label.
	// Used as the last-resort fallback in ParameterValue when no parameter-specific
	// translation is found.
	valueIndices map[string]map[string]string // locale → value_lower → label (shortest)
}

// ErrNoArchive is returned when the translation archive path is empty.
var ErrNoArchive = errors.New("ccudata: archive path is empty")

// LoadTranslations reads the gzipped JSON archive at path.
// Returns [ErrNoArchive] when path is empty; other errors when the
// file is unreadable or the payload mismatches the expected shape.
func LoadTranslations(path string) (*Translations, error) {
	if path == "" {
		return nil, ErrNoArchive
	}
	f, err := os.Open(path) //nolint:gosec // operator-supplied path
	if err != nil {
		return nil, fmt.Errorf("ccudata: open %s: %w", path, err)
	}
	defer f.Close() //nolint:errcheck // read-only file

	gz, err := gzip.NewReader(f)
	if err != nil {
		return nil, fmt.Errorf("ccudata: gzip: %w", err)
	}
	defer gz.Close() //nolint:errcheck // read-only stream

	raw := make(map[string]map[string]string)
	if err := json.NewDecoder(gz).Decode(&raw); err != nil {
		return nil, fmt.Errorf("ccudata: decode: %w", err)
	}

	return translationsFromRaw(raw), nil
}

// Empty returns a Translations struct with all maps non-nil. Used as
// the fallback when no archive is configured so callers don't have
// to nil-check on every lookup.
func Empty() *Translations {
	return &Translations{
		ChannelTypes:    map[string]map[string]string{},
		DeviceModels:    map[string]map[string]string{},
		Parameters:      map[string]map[string]string{},
		ParameterValues: map[string]map[string]string{},
		ParameterHelp:   map[string]map[string]string{},
		UILabels:        map[string]map[string]string{},
		DeviceIcons:     map[string]string{},
	}
}

// ChannelType returns the translated label for a channel type or the
// raw key when no translation exists. Lookups are case-insensitive.
func (t *Translations) ChannelType(locale, channelType string) string {
	if t == nil || channelType == "" {
		return channelType
	}
	table := t.ChannelTypes[locale]
	if table == nil {
		return channelType
	}
	if v, ok := table[strings.ToLower(channelType)]; ok {
		return v
	}
	if v, ok := table[channelType]; ok {
		return v
	}
	return channelType
}

// DeviceModel looks up the description for a CCU `type_subtype` key.
// Accepts either the raw `type_subtype` or the CCU TYPE string
// for TYPE lookups the caller should pre-join the subtype.
func (t *Translations) DeviceModel(locale, typeSubtype string) string {
	if v, ok := t.DeviceModels[locale][typeSubtype]; ok {
		return v
	}
	return typeSubtype
}

// DeviceModelLabel mirrors
// `get_device_model_description`: try the lower-cased full model first
// (e.g. "hmip-swdo"), then fall back to the lower-cased sub-model
// (matches the CCU's SUBTYPE, e.g. "swdo"). Returns an empty string
// when no translation exists so callers can keep the raw model.
//
// Beyond the two stages
// stripped-prefix and stripped-suffix variants of the model so that
// devices whose CCU TYPE includes a vendor prefix (e.g. "HmIP-") or a
// trailing variant tag (e.g. "-2", "-B-2 R4M") still resolve when the
// translation catalogue is keyed by the bare device family (e.g. "trv",
// "psm", "smo"). This fixes 25 SUBTYPE-propagation bugs present when
// the translation catalogue was keyed by the bare device family only.
func (t *Translations) DeviceModelLabel(locale, model, subModel string) string {
	if t == nil {
		return ""
	}
	table := t.DeviceModels[locale]
	if table == nil {
		return ""
	}
	// Stage 1: full lower-cased model.
	if model != "" {
		lo := strings.ToLower(model)
		if v, ok := table[lo]; ok {
			return v
		}
		// Stage 2: stripped vendor prefix (HmIP-, HmIPW-, HM-).
		if stripped, ok := stripVendorPrefix(lo); ok {
			if v, ok := table[stripped]; ok {
				return v
			}
			// Stage 3: stripped prefix + space-tail dropped (handles
			// e.g. "HmIP-eTRV-B-2 R4M" → first "etrv-b-2 r4m", then
			// the head before the space "etrv-b-2").
			head := stripped
			if i := strings.IndexByte(stripped, ' '); i > 0 {
				head = stripped[:i]
				if v, ok := table[head]; ok {
					return v
				}
			}
			// Stage 4: stripped prefix + iteratively drop trailing
			// "-X" tokens until we hit a key in the table. Handles
			// "etrv-b-2" → "etrv-b" → "etrv".
			parts := strings.Split(head, "-")
			for k := len(parts) - 1; k > 0; k-- {
				cand := strings.Join(parts[:k], "-")
				if v, ok := table[cand]; ok {
					return v
				}
			}
		}
	}
	// Stage 5: lower-cased SUBTYPE (CCU-supplied sub-model).
	if subModel != "" {
		lo := strings.ToLower(subModel)
		if v, ok := table[lo]; ok {
			return v
		}
		// Stage 6: SUBTYPE with trailing "-X" tokens dropped.
		// Handles SUBTYPE="TRV-B-2" → "trv-b" → "trv".
		parts := strings.Split(lo, "-")
		for k := len(parts) - 1; k > 0; k-- {
			cand := strings.Join(parts[:k], "-")
			if v, ok := table[cand]; ok {
				return v
			}
		}
	}
	return ""
}

// stripVendorPrefix drops a leading vendor / family prefix from model.
// Returns the stripped form and true on success, or "" and false when
// no known prefix matches. The model is expected to be lower-cased.
func stripVendorPrefix(model string) (string, bool) {
	for _, p := range vendorPrefixes {
		if strings.HasPrefix(model, p) {
			return model[len(p):], true
		}
	}
	return "", false
}

// vendorPrefixes lists the lower-cased vendor / family prefixes that
// precede a CCU TYPE for HomeMatic and HomeMatic IP devices. Order
// matters: longer prefixes must come before shorter ones so the longest
// match wins.
var vendorPrefixes = []string{"hmipw-", "hmip-", "hmw-", "hm-"}

// DeviceModelIcon returns the icon filename for a lowercase model key
// (e.g. "hmip-swdo"). Returns empty when none is known.
func (t *Translations) DeviceModelIcon(model string) string {
	if t == nil || model == "" {
		return ""
	}
	if v, ok := t.DeviceIcons[strings.ToLower(model)]; ok {
		return v
	}
	return ""
}

// ParameterLabel looks up the human-readable parameter name.
//
// 1. `<channel_type>|<parameter>` (both lower-cased)
// 2. bare `<parameter>`
// 3. if the parameter has a SHORT_/LONG_ prefix (LINK paramset),
// strip it and retry (1) + (2), then append a localised
// "(short)/(long)" suffix.
//
// Returns an empty string when nothing matches; callers decide
// whether to fall back to the raw parameter name.
func (t *Translations) ParameterLabel(locale, channelType, parameter string) string {
	label, _ := t.ParameterLabelOk(locale, channelType, parameter)
	return label
}

// ParameterLabelOk is the (label, found) variant of [ParameterLabel].
// The second return value distinguishes a missing entry ("", false)
// from an explicitly-empty translation ("", true).
//
// An explicitly-empty entry signals that the parameter is "primary"
// — north-bound adapters can use it to omit the entity name (so HA
// renders friendly_name + entity_id from the device name alone),
// the same effect HA-native integrations achieve via
// `_attr_translation_key` plus an HA-translation `"name": ""` entry.
func (t *Translations) ParameterLabelOk(locale, channelType, parameter string) (string, bool) {
	if t == nil || parameter == "" {
		return "", false
	}
	table := t.Parameters[locale]
	if table == nil {
		return "", false
	}
	param := strings.ToLower(parameter)
	ct := strings.ToLower(channelType)

	if ct != "" {
		if v, ok := table[ct+"|"+param]; ok {
			return v, true
		}
	}
	if v, ok := table[param]; ok {
		return v, true
	}
	// LINK-paramset SHORT_/LONG_ fallback.
	if base, ok := stripLinkPrefix(parameter); ok {
		baseLower := strings.ToLower(base)
		var (
			label string
			seen  bool
		)
		if ct != "" {
			if v, ok := table[ct+"|"+baseLower]; ok {
				label, seen = v, true
			}
		}
		if !seen {
			if v, ok := table[baseLower]; ok {
				label, seen = v, true
			}
		}
		if seen {
			if label == "" {
				// Stripped LINK base maps to an explicit empty: treat
				// the parameter as found-but-omitted; the LINK suffix
				// is purely decorative without a base label.
				return "", true
			}
			return label + " (" + linkPrefixSuffix(parameter, locale) + ")", true
		}
	}
	return "", false
}

// stripLinkPrefix reports the parameter without its SHORT_/LONG_
// prefix. The prefix matching is case-insensitive; the returned base
// preserves the original casing of the suffix.
func stripLinkPrefix(parameter string) (string, bool) {
	for _, p := range []string{"SHORT_", "LONG_", "short_", "long_"} {
		if strings.HasPrefix(parameter, p) {
			return parameter[len(p):], true
		}
	}
	return "", false
}

// linkPrefixSuffix returns the localised label the CCU WebUI appends
// to a short/long LINK parameter name after stripping the prefix.
func linkPrefixSuffix(parameter, locale string) string {
	lower := strings.ToLower(parameter)
	switch {
	case strings.HasPrefix(lower, "short_"):
		if locale == LocaleDE {
			return "kurz"
		}
		return "short"
	case strings.HasPrefix(lower, "long_"):
		if locale == LocaleDE {
			return "lang"
		}
		return "long"
	}
	return ""
}

// Parameter returns the translated parameter label; wraps
// [ParameterLabel] with an empty channel type and falls back to the
// raw parameter string on miss so legacy callers stay functional.
func (t *Translations) Parameter(locale, parameter string) string {
	if v := t.ParameterLabel(locale, "", parameter); v != "" {
		return v
	}
	return parameter
}

// ParameterValue returns the translated label for a parameter-value
// combination (e.g. CONTROL_MODE=AUTO → "Auto-Modus"). The extractor
// uses `=` as the separator (not `|`), mirroring the CCU stringtable
// convention, and lowercases both segments.
//
// Lookup order mirrors
// (ccu_translations.py:292-343):
// 1. `<channel_type_lower>|<param_lower>=<value_lower>` — channel-specific
// 2. `<param_lower>=<value_lower>` — parameter-specific
// 3. Strip SHORT_/LONG_ prefix and retry (1)+(2)
// 4. Value-only index: shortest label for value_lower across all params
//
// Returns the raw value when no translation is found.
func (t *Translations) ParameterValue(locale, channelType, parameter, value string) string {
	if t == nil {
		return value
	}
	table := t.ParameterValues[locale]
	if table == nil {
		return value
	}
	paramL := strings.ToLower(parameter)
	valueL := strings.ToLower(value)
	ctL := strings.ToLower(channelType)

	// Stage 1 : channel-type + param + value.
	if ctL != "" {
		if v, ok := table[ctL+"|"+paramL+"="+valueL]; ok {
			return v
		}
	}
	// Stage 2: param + value.
	if v, ok := table[paramL+"="+valueL]; ok {
		return v
	}
	// Stage 3: LINK-paramset SHORT_/LONG_ prefix strip, then retry stages 1+2.
	if stripped, ok := stripLinkPrefix(parameter); ok {
		base := strings.ToLower(stripped)
		if ctL != "" {
			if v, ok := table[ctL+"|"+base+"="+valueL]; ok {
				return v
			}
		}
		if v, ok := table[base+"="+valueL]; ok {
			return v
		}
	}
	// Stage 4 : value-only fallback via lazy-built index.
	if v := t.valueIndexLookup(locale, valueL); v != "" {
		return v
	}
	return value
}

// ParameterValueSimple is the old two-argument form kept for callers
// that do not have a channel type. It delegates to [ParameterValue]
// with an empty channel type.
func (t *Translations) ParameterValueSimple(locale, parameter, value string) string {
	return t.ParameterValue(locale, "", parameter, value)
}

// valueIndexLookup returns the shortest label for valueLower from the
// lazy-built value-only index. Returns empty when no label is found.
// Thread-safety: the index is built once at [translationsFromRaw] time
// via [buildValueIndices]; no concurrent writes occur after that.
func (t *Translations) valueIndexLookup(locale, valueLower string) string {
	if t.valueIndices == nil {
		return ""
	}
	if idx := t.valueIndices[locale]; idx != nil {
		return idx[valueLower]
	}
	return ""
}

// buildValueIndices constructs the value-only reverse index for every
// locale's parameter_values table. For each entry "param=value → label" the
// entry keyed only on "value" is kept when it is the shortest label seen for
// that value so far (ties are broken in iteration order).
//
// for k, v in self._data[pv_key].items(): if "=" not in k: continue val =
// k.rsplit("=", maxsplit=1)[1] if val not in value_index or len(v) <
// len(value_index[val]): value_index[val] = v
//
// (ccu_translations.py:157-167)
func buildValueIndices(parameterValues map[string]map[string]string) map[string]map[string]string {
	out := make(map[string]map[string]string, len(parameterValues))
	for locale, table := range parameterValues {
		idx := make(map[string]string)
		for k, label := range table {
			eqIdx := strings.LastIndexByte(k, '=')
			if eqIdx < 0 {
				continue
			}
			val := k[eqIdx+1:]
			if existing, ok := idx[val]; !ok || len(label) < len(existing) {
				idx[val] = label
			}
		}
		out[locale] = idx
	}
	return out
}

// ParameterHelpText returns the translated help text or the empty string when
// none is available.
func (t *Translations) ParameterHelpText(locale, parameter string) string {
	if t == nil || parameter == "" {
		return ""
	}
	table := t.ParameterHelp[locale]
	if table == nil {
		return ""
	}
	param := strings.ToLower(parameter)
	if v, ok := table[param]; ok {
		return v
	}
	if base, ok := stripLinkPrefix(parameter); ok {
		if v, ok := table[strings.ToLower(base)]; ok {
			return v
		}
	}
	return ""
}

// UILabel returns a translated UI string by key. Keys are
// case-insensitive; the extractor lower-cases them during indexing.
func (t *Translations) UILabel(locale, key string) string {
	if t == nil || key == "" {
		return key
	}
	if v, ok := t.UILabels[locale][strings.ToLower(key)]; ok {
		return v
	}
	return key
}

// DeviceIcon returns the icon file name for `type subtype` (note:
// the CCU's DEVDB uses a space, not an underscore, between type and
// subtype).
func (t *Translations) DeviceIcon(typeSubtype string) string {
	if v, ok := t.DeviceIcons[typeSubtype]; ok {
		return v
	}
	// Fall back to the TYPE-only prefix if a full key was supplied.
	if i := strings.IndexByte(typeSubtype, ' '); i > 0 {
		if v, ok := t.DeviceIcons[typeSubtype[:i]]; ok {
			return v
		}
	}
	return ""
}

// Locales returns the locales that carry any data. Useful to
// drive the UI's language picker without hard-coding constants.
func (t *Translations) Locales() []string {
	seen := make(map[string]struct{})
	for _, m := range []map[string]map[string]string{
		t.ChannelTypes, t.DeviceModels, t.Parameters,
		t.ParameterValues, t.ParameterHelp, t.UILabels,
	} {
		for l := range m {
			seen[l] = struct{}{}
		}
	}
	out := make([]string, 0, len(seen))
	for l := range seen {
		out = append(out, l)
	}
	return out
}

// translationsFromRaw splits the flat locale-suffixed map shape
// used by the archive into the per-locale maps [Translations] expects.
// After parsing it builds the value-only index for stage-4 lookups
// in [ParameterValue].
func translationsFromRaw(raw map[string]map[string]string) *Translations {
	out := Empty()
	for key, table := range raw {
		switch {
		case key == "device_icons":
			out.DeviceIcons = table
		case strings.HasPrefix(key, "channel_types_"):
			out.ChannelTypes[localeSuffix(key)] = table
		case strings.HasPrefix(key, "device_models_"):
			out.DeviceModels[localeSuffix(key)] = table
		case strings.HasPrefix(key, "parameters_"):
			out.Parameters[localeSuffix(key)] = table
		case strings.HasPrefix(key, "parameter_values_"):
			out.ParameterValues[localeSuffix(key)] = table
		case strings.HasPrefix(key, "parameter_help_"):
			out.ParameterHelp[localeSuffix(key)] = table
		case strings.HasPrefix(key, "ui_labels_"):
			out.UILabels[localeSuffix(key)] = table
		}
	}
	// Build the value-only reverse index for stage-4 ParameterValue fallback.
	out.valueIndices = buildValueIndices(out.ParameterValues)
	return out
}

// ResolveChannelType resolves the effective channel type for
// translation lookups. When isHmIP is true it appends "_HMIP" to the
// channel type and checks whether any per-locale translation key
// starts with "<CHANNEL_TYPE_HMIP>|". If a match is found the
// remapped name is returned; otherwise the original is returned
// unchanged.
//
// This mirrors
// (ccu_translations.py:356-378), which the CCU WebUI uses to select
// the correct string-table entries for HmIP devices whose parameter
// semantics differ from their BidCoS counterparts
// (e.g. SHUTTER_CONTACT → SHUTTER_CONTACT_HMIP).
//
// Only the parameters sub-table is checked because that is the only
// category where the CCU emits _HMIP-suffixed keys in practice.
func (t *Translations) ResolveChannelType(channelType string, isHmIP bool) string {
	if !isHmIP || channelType == "" || t == nil {
		return channelType
	}
	candidate := strings.ToUpper(channelType) + "_HMIP"
	prefix := strings.ToLower(candidate) + "|"
	for _, table := range t.Parameters {
		for key := range table {
			if strings.HasPrefix(key, prefix) {
				return candidate
			}
		}
	}
	return channelType
}

// ProfileLabel resolves a localized profile name from a ProfileStore
// entry. It is a convenience wrapper around [ProfileStore.ResolvedProfile]
// that returns only the Name string, falling back to a generic label
// when the profile is not found.
//
// Mirrors the name-resolution path in 's
// _resolve_profile (profile_store.py:185-186).
func (t *Translations) ProfileLabel(store *ProfileStore, receiverType string, id int, locale string) string {
	if store == nil {
		return ""
	}
	rp, ok := store.ResolvedProfile(receiverType, id, locale)
	if !ok {
		return ""
	}
	return rp.Name
}

func localeSuffix(key string) string {
	if i := strings.LastIndex(key, "_"); i > 0 {
		return key[i+1:]
	}
	return key
}
