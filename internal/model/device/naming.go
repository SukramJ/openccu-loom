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

// GenerateUniqueID builds a lower-case unique identifier from an
// address, an optional parameter, and an optional prefix:
//
//	<address with "-" or ":" → "_">                       (no parameter)
//	<address>_<parameter>                                  (with parameter)
//	<prefix>_<address>[_<parameter>]                       (with prefix)
//
// The central_id is prepended for the hub roots (Sysvar / Programs /
// InstallMode), internal addresses (INT000*), and every virtual-remote
// channel — including the VCU* channel range — so identical IDs stay
// disambiguated across CCUs.
//
// This is a daemon-internal generator and is deliberately NOT the
// cross-backend Home Assistant routing key: it carries the central_id
// on VCU channels and capitalises the hub roots, where the routing-key
// contract does neither. It is also distinct from the MQTT-discovery
// unique_id (which is daemon-namespaced with the "openccu-loom" prefix
// and pinned for registry stability). The shared HA routing key lives
// in internal/routingkey (mirrored from the cross-backend contract and
// locked by a golden-fixture test); external clients rebuild the HA
// registry key from address + parameter themselves. See
// docs/parity/by_design.md (BD-Identity-RoutingKeyNamespaces). A future
// consumer that needs the HA routing key must use internal/routingkey,
// not this function.
//
// loom:reachable:reason="daemon-internal id generator exercised by the naming-pipeline golden test; the cross-backend HA routing key lives in internal/routingkey"
func GenerateUniqueID(centralID, address, parameter, prefix string) string {
	uid := strings.ReplaceAll(address, ":", "_")
	uid = strings.ReplaceAll(uid, "-", "_")
	if parameter != "" {
		uid = uid + "_" + parameter
	}
	if prefix != "" {
		uid = prefix + "_" + uid
	}
	switch {
	case address == "BidCoS-RF" || address == "BidCoS-Wir" || address == "HmIP-RCV-1" ||
		address == "Sysvar" || address == "Programs" || address == "InstallMode":
		// Exact virtual-remote root addresses (no channel suffix) are
		// namespaced with central_id to avoid collisions when two CCUs
		// each expose the same logical virtual bus.
		uid = centralID + "_" + uid
	case strings.HasPrefix(address, "INT000"):
		uid = centralID + "_" + uid
	case strings.HasPrefix(address, "BidCoS-RF") || strings.HasPrefix(address, "BidCoS-Wir") ||
		strings.HasPrefix(address, "HmIP-RCV-1") || strings.HasPrefix(address, "VCU"):
		// Channel addresses derived from virtual-remote roots (e.g.
		// "BidCoS-Wir:1", "HmIP-RCV-1:3") also carry the central_id so
		// multi-CCU unique-IDs stay collision-free.
		uid = centralID + "_" + uid
	}
	return strings.ToLower(uid)
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
