// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package adapter

import (
	"strconv"

	"github.com/SukramJ/openccu-loom/internal/ccudata"
	"github.com/SukramJ/openccu-loom/internal/north/mqtt"
)

// The labeler the daemon wires into the event bridge
// (cmd/openccu-loom/daemon.go) must carry the value-label half, or the
// discovery builder silently falls back to raw enum tokens — the defect
// this interface exists to fix, in a form no test would notice.
var _ mqtt.ValueListLabeler = (*MqttParameterLabelAdapter)(nil)

// ValueListLabel resolves the display label for one VALUE_LIST entry of an
// ENUM parameter. It is the single implementation of that lookup chain,
// shared by every north-bound surface that shows enum values — the UI
// schema, the REST data-point views, and MQTT discovery.
//
// The chain, in order:
//
//  1. the channel-typed OCCU translation for the value
//     (`<channel-type>|<parameter>=<value>`, then `<parameter>=<value>`,
//     then value-only), via [ccudata.Translations.ParameterValue];
//  2. the index-keyed translation — easymode TCL stores `parameter=N`
//     when the VALUE_LIST strings are not available at extraction time;
//  3. humanisation of the raw token ("HIGH_PRIORITY" → "High Priority"),
//     so the result is always presentable.
//
// It never returns the empty string for a non-empty value, which is what
// lets a caller use the result directly instead of re-deriving a
// fallback. Two derivations of one naming rule is one too many: that is
// how the MQTT entity name drifted from the REST one.
func ValueListLabel(
	t *ccudata.Translations, locale, channelType, parameter, value string, index int,
) string {
	if value == "" {
		return ""
	}
	if t != nil {
		if got := t.ParameterValue(locale, channelType, parameter, value); got != value {
			return got
		}
		idx := strconv.Itoa(index)
		if got := t.ParameterValue(locale, channelType, parameter, idx); got != idx {
			return got
		}
	}
	return humanizeRaw(value)
}

// ValueListLabels maps a whole VALUE_LIST onto its labels, index-aligned
// with the input.
func ValueListLabels(
	t *ccudata.Translations, locale, channelType, parameter string, values []string,
) []string {
	if len(values) == 0 {
		return nil
	}
	out := make([]string, len(values))
	for i, v := range values {
		out[i] = ValueListLabel(t, locale, channelType, parameter, v, i)
	}
	return out
}

// ValueListLabels implements the value-label half of the translation port
// for consumers that hold a [*ParameterLabelAdapter] (the MQTT event
// bridge reaches it through this method). A nil adapter or a nil
// translation archive still yields humanised tokens rather than nothing.
func (a *ParameterLabelAdapter) ValueListLabels(channelType, parameter string, values []string) []string {
	if a == nil {
		return ValueListLabels(nil, "", channelType, parameter, values)
	}
	return ValueListLabels(a.translations, a.locale, channelType, parameter, values)
}

// ValueListLabels implements `mqtt.ValueListLabeler` so the discovery
// builder receives localised enum options.
func (a *MqttParameterLabelAdapter) ValueListLabels(channelType, parameter string, values []string) []string {
	if a == nil || a.inner == nil {
		return ValueListLabels(nil, "", channelType, parameter, values)
	}
	return a.inner.ValueListLabels(channelType, parameter, values)
}
