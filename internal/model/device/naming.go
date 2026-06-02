// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package device

import (
	"strings"
	"unicode"
)

// addressSeparator is the dash the CCU uses between channel and
// parameter slugs.
const addressSeparator = "-"

// CustomDataPointName builds a custom-DP name string for a channel +
// parameter, optionally appending a custom-type postfix (e.g. "color",
// "color_temp", "effect", "button_lock").
func (c *Channel) CustomDataPointName(parameter, postfix string) string {
	if postfix == "" {
		return c.DataPointName(parameter)
	}
	return c.DataPointName(strings.ToUpper(postfix))
}

// CustomDataPointFullName is the full-name (device-prefixed) variant of
// [CustomDataPointName].
func (c *Channel) CustomDataPointFullName(parameter, postfix string) string {
	if postfix == "" {
		return c.DataPointFullName(parameter)
	}
	return c.DataPointFullName(strings.ToUpper(postfix))
}

// GenerateTranslationKey converts a free-form name into the slug
// Mirrors
// `support.py:generate_translation_key` — slugify-then-replace dots
// and dashes with underscores. Punctuation outside [A-Za-z0-9._-] is
// dropped; whitespace is collapsed to "_".
func GenerateTranslationKey(name string) string {
	var b strings.Builder
	prevUnderscore := true
	for _, r := range strings.ToLower(name) {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			b.WriteRune(r)
			prevUnderscore = false
		case r == '_':
			if !prevUnderscore {
				b.WriteRune('_')
				prevUnderscore = true
			}
		default:
			if !prevUnderscore {
				b.WriteRune('_')
				prevUnderscore = true
			}
		}
	}
	out := b.String()
	out = strings.TrimSuffix(out, "_")
	return out
}

// DataPointName returns the data-point-only name segment.
// channel name with the parameter; the device name is intentionally
// left out so callers can reuse it next to it without duplication.
//
// Empty parameters return only the channel-segment so callers can
// build "<device> <channel>" labels for parameter-less rows (the
// device-update synthetic data point, for example).
func (c *Channel) DataPointName(parameter string) string {
	if c == nil {
		return parameter
	}
	deviceName := ""
	if c.device != nil {
		deviceName = c.device.Name
	}
	channelName := normaliseChannelName(deviceName, c.Name)
	if channelName != "" && parameter != "" {
		return strings.TrimSpace(channelName + " " + parameter)
	}
	if channelName != "" {
		return channelName
	}
	return parameter
}

// DataPointFullName returns the device-prefixed full name. Falls back to the
// device name alone when the parameter is empty and to the parameter alone
// when no device name is configured.
func (c *Channel) DataPointFullName(parameter string) string {
	if c == nil {
		return parameter
	}
	deviceName := ""
	if c.device != nil {
		deviceName = c.device.Name
	}
	dpName := c.DataPointName(parameter)
	if deviceName == "" {
		return dpName
	}
	if dpName == "" {
		return deviceName
	}
	return strings.TrimSpace(deviceName + " " + dpName)
}

// normaliseChannelName strips the device-name prefix the CCU sometimes
// duplicates into the channel name. Replicates the channel-name
// Normalisation
func normaliseChannelName(deviceName, channelName string) string {
	channelName = strings.TrimSpace(channelName)
	if deviceName == "" || channelName == "" {
		return channelName
	}
	if !strings.HasPrefix(channelName, deviceName) {
		return channelName
	}
	stripped := strings.TrimSpace(strings.TrimPrefix(channelName, deviceName))
	stripped = strings.TrimPrefix(stripped, addressSeparator)
	return strings.TrimSpace(stripped)
}
