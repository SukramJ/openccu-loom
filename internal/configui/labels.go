// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package configui

import (
	"strings"
	"unicode"
)

// TranslationProvider is the minimal interface a label resolver
// queries for upstream CCU translations. The configui package keeps
// the dependency narrow so production callers can supply
// `internal/ccudata.TranslationLoader`-backed lookups while tests
// inject fixed maps.
//
// Lookup order mirrors the reference config panel's translation resolution:
//
//  1. (parameter, channel_type, locale)  — most specific
//  2. (parameter, "", locale)            — parameter-only
//
// Implementations return the empty string when no translation exists;
// the resolver then falls back to the auto-formatted ID.
type TranslationProvider interface {
	ParameterTranslation(parameter, channelType, locale string) string
}

// ParameterHelpProvider is the optional extension a [TranslationProvider]
// may implement to expose CCU help texts (the OCCU
// `parameter_help_<locale>` extracts). The configui generator probes
// for it via type-assertion so existing providers keep compiling
// without changes — they just won't surface help text.
//
// `internal/ccudata.Translations` happens to satisfy this interface
// out of the box via its `ParameterHelpText(locale, parameter)` method.
type ParameterHelpProvider interface {
	ParameterHelpText(locale, parameter string) string
}

// LabelResolver maps technical parameter IDs to human-readable
// labels. Mirrors the reference config panel's label resolver: queries
// the [TranslationProvider] first and falls back to a human-readable
// formatting of the parameter ID.
type LabelResolver struct {
	locale   string
	provider TranslationProvider
}

// DefaultLocale is the locale the resolver picks when none is given.
const DefaultLocale = "en"

// NewLabelResolver constructs a resolver bound to provider and
// locale. Pass an empty locale to use [DefaultLocale]. Pass nil for
// provider to get a resolver that always falls back to the auto-
// formatted label — useful for the early bring-up phase before the
// translation catalogue is wired.
//
// loom:reachable:reason="instantiated in Config-UI REST handler per request to resolve parameter labels in the operator's locale"
func NewLabelResolver(provider TranslationProvider, locale string) *LabelResolver {
	if locale == "" {
		locale = DefaultLocale
	}
	return &LabelResolver{locale: locale, provider: provider}
}

// Locale returns the resolver's configured locale.
func (r *LabelResolver) Locale() string { return r.locale }

// HasTranslation reports whether the upstream provider has a
// translation for the parameter. Falls through to the parameter-only
// query if no channel-specific entry exists.
func (r *LabelResolver) HasTranslation(parameter, channelType string) bool {
	if r == nil || r.provider == nil {
		return false
	}
	if channelType != "" && r.provider.ParameterTranslation(parameter, channelType, r.locale) != "" {
		return true
	}
	return r.provider.ParameterTranslation(parameter, "", r.locale) != ""
}

// HelpText returns the localised help text for parameter. Empty when
// no help is registered for the active locale. Used by the form-schema
// generator to populate FormParameter.Description so the SPA can show
// an info-icon popover next to each label.
func (r *LabelResolver) HelpText(parameter string) string {
	if r == nil || r.provider == nil {
		return ""
	}
	hp, ok := r.provider.(ParameterHelpProvider)
	if !ok {
		return ""
	}
	return hp.ParameterHelpText(r.locale, parameter)
}

// Resolve returns the human-readable label for parameter. Mirrors the
// reference config panel's LabelResolver.resolve: uses the upstream
// translation when available, otherwise the auto-formatted ID
// (`TEMPERATURE_OFFSET` → `Temperature Offset`).
func (r *LabelResolver) Resolve(parameter, channelType string) string {
	if r != nil && r.provider != nil {
		if channelType != "" {
			if t := r.provider.ParameterTranslation(parameter, channelType, r.locale); t != "" {
				return t
			}
		}
		if t := r.provider.ParameterTranslation(parameter, "", r.locale); t != "" {
			return t
		}
	}
	return Humanize(parameter)
}

// Humanize converts a SCREAMING_SNAKE_CASE parameter id to a Title-
// Cased label. Exposed because consumers without a resolver
// occasionally need the same fallback formatting.
func Humanize(id string) string {
	if id == "" {
		return ""
	}
	parts := strings.Split(id, "_")
	for i, p := range parts {
		parts[i] = titleCase(strings.ToLower(p))
	}
	return strings.Join(parts, " ")
}

func titleCase(s string) string {
	if s == "" {
		return s
	}
	r := []rune(s)
	r[0] = unicode.ToUpper(r[0])
	return string(r)
}
